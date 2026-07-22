package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"studdle/backend/db_sql"
	"studdle/backend/internal/config"
	"studdle/backend/testutil"
)

// testConfig builds a *config.Config wired for integration tests.
// It does not call config.Load() so config validation is bypassed.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Env:         "test",                                 // Env selects the recorder emailer
		Port:        "0",                                    // Port is unused; httptest picks its own
		FrontendURL: "http://test.local",                    // FrontendURL is the CORS allowed origin
		CORSOrigins: []string{"http://test.local"},          // CORSOrigins lists the origins echoed back
		BackendURL:  "http://test.local",                    // BackendURL is used in image URLs
		DatabaseURL: os.Getenv("DATABASE_URL"),              // DatabaseURL points at studbud_test
		JWTSecret:   "test-secret-at-least-32-chars-long!!", // JWTSecret must be ≥32 bytes
		JWTIssuer:   "studbud",                              // JWTIssuer is the "iss" claim value
		JWTTTL:      720 * time.Hour,                        // JWTTTL is the token lifetime
		UploadDir:   t.TempDir(),                            // UploadDir is a per-test temp dir
		SMTPHost:    "",                                     // SMTPHost is empty; falls through to Recorder
		StripeMode:  "test",                                 // StripeMode avoids prod-only validation
	}
}

// httpClient holds a base URL and an optional Bearer token for HTTP helpers.
type httpClient struct {
	base  string // base is the httptest server URL
	token string // token is the current Bearer JWT
}

// mustPostJSON POSTs a JSON-encoded body and returns the response.
func (c *httpClient) mustPostJSON(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// mustGet performs a GET request and returns the response.
func (c *httpClient) mustGet(t *testing.T, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// rawPost posts a JSON body and returns the status code, closing the body.
func (c *httpClient) rawPost(t *testing.T, path string, body any) int {
	t.Helper()
	resp := c.mustPostJSON(t, path, body)
	defer resp.Body.Close()
	return resp.StatusCode
}

// mustPostMultipart POSTs multipart data and returns the response.
func (c *httpClient) mustPostMultipart(t *testing.T, path string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.base+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST multipart %s: %v", path, err)
	}
	return resp
}

// mustJSON decodes a JSON response into dst, asserting the expected status code.
func mustJSON(t *testing.T, resp *http.Response, wantStatus int, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want status %d, got %d: %s", wantStatus, resp.StatusCode, b)
	}
	if dst == nil {
		return
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// pngBytes is a minimal 1×1 red PNG used for upload tests.
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00,
	0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x5B, 0x2F, 0xC0, 0x0F, 0x00, 0x00, 0x00,
	0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

// buildMultipartPNG builds a multipart/form-data body with a single "file" field.
func buildMultipartPNG(t *testing.T) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "pixel.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(pngBytes); err != nil {
		t.Fatalf("write png: %v", err)
	}
	w.Close()
	return &buf, w.FormDataContentType()
}

// e2eState carries all cross-step identifiers for the e2e test flow.
type e2eState struct {
	subjectID int64  // subjectID is the Biology subject's primary key
	imageID   string // imageID is the uploaded PNG's UUID
	chapterID int64  // chapterID is the Cells chapter's primary key
	cardID    int64  // cardID is the created flashcard's primary key
}

// stepRegisterAndVerify registers alice, extracts the verification token from DB,
// verifies email, re-logins, and returns the verified token.
func stepRegisterAndVerify(t *testing.T, cl *httpClient, ctx context.Context, d *deps) string {
	t.Helper()
	mustJSON(t, cl.mustPostJSON(t, "/user-register", map[string]string{
		"username": "alice", "email": "alice@example.com", "password": "password123",
	}), http.StatusOK, nil)

	var verToken string
	if err := d.db.QueryRow(ctx,
		`SELECT token FROM email_verifications ORDER BY id DESC LIMIT 1`,
	).Scan(&verToken); err != nil {
		t.Fatalf("query verification token: %v", err)
	}
	mustJSON(t, cl.mustGet(t, "/verify-email?token="+verToken), http.StatusOK, nil)

	var loginResp struct {
		Token string `json:"token"` // Token is the verified JWT
	}
	mustJSON(t, cl.mustPostJSON(t, "/user-login", map[string]string{
		"identifier": "alice", "password": "password123",
	}), http.StatusOK, &loginResp)

	claims, err := d.signer.Verify(loginResp.Token)
	if err != nil {
		t.Fatalf("verify login token: %v", err)
	}
	if !claims.EmailVerified {
		t.Fatal("expected email_verified=true in relogin token")
	}
	return loginResp.Token
}

// stepCreateSubjectAndImage creates a subject and uploads a PNG, returning their IDs.
func stepCreateSubjectAndImage(t *testing.T, cl *httpClient) (subjectID int64, imageID string) {
	t.Helper()
	var subjResp struct {
		ID int64 `json:"id"` // ID is the created subject primary key
	}
	mustJSON(t, cl.mustPostJSON(t, "/subject-create", map[string]string{
		"name": "Biology", "visibility": "private",
	}), http.StatusCreated, &subjResp)
	if subjResp.ID == 0 {
		t.Fatal("expected non-zero subject id")
	}

	body, ct := buildMultipartPNG(t)
	var imgResp struct {
		ID  string `json:"id"`  // ID is the image UUID string
		URL string `json:"url"` // URL is the serve URL
	}
	mustJSON(t, cl.mustPostMultipart(t, "/upload-image", body, ct), http.StatusOK, &imgResp)
	if imgResp.ID == "" {
		t.Fatal("expected non-empty image id")
	}
	return subjResp.ID, imgResp.ID
}

// stepCreateContent creates a subject, uploads an image, creates a chapter and flashcard.
// It returns an e2eState with the created IDs.
func stepCreateContent(t *testing.T, cl *httpClient) e2eState {
	t.Helper()
	subjectID, imageID := stepCreateSubjectAndImage(t, cl)

	var chapResp struct {
		ID int64 `json:"id"` // ID is the created chapter primary key
	}
	mustJSON(t, cl.mustPostJSON(t, "/chapter-create", map[string]any{
		"subject_id": subjectID, "title": "Cells",
	}), http.StatusCreated, &chapResp)

	var fcResp struct {
		ID int64 `json:"id"` // ID is the created flashcard primary key
	}
	mustJSON(t, cl.mustPostJSON(t, "/flashcard-create", map[string]any{
		"subject_id": subjectID, "chapter_id": chapResp.ID,
		"question": "What is a cell?", "answer": "The basic unit of life.",
		"image_id": imageID,
	}), http.StatusCreated, &fcResp)
	if fcResp.ID == 0 {
		t.Fatal("expected non-zero flashcard id")
	}
	return e2eState{subjectID, imageID, chapResp.ID, fcResp.ID}
}

// sessionResult is the decoded body of POST /training-session-record.
type sessionResult struct {
	Streak struct {
		CurrentStreak int `json:"current_streak"` // CurrentStreak is the updated streak
	} `json:"streak"` // Streak is the streak snapshot after the session
	NewlyAwarded []struct {
		Code string `json:"code"` // Code is the achievement identifier
	} `json:"newly_awarded"` // NewlyAwarded lists achievements unlocked this session
}

// containsCode reports whether codes contains a specific achievement code.
func containsCode(codes []struct {
	Code string `json:"code"` // Code is the achievement identifier
}, want string) bool {
	for _, a := range codes {
		if a.Code == want {
			return true
		}
	}
	return false
}

// stepTrainAndAssert reviews the flashcard, records a session, and asserts results.
func stepTrainAndAssert(t *testing.T, cl *httpClient, st e2eState) {
	t.Helper()
	code := cl.rawPost(t, fmt.Sprintf("/flashcard-review?id=%d", st.cardID), map[string]int{"result": 2})
	if code < 200 || code > 299 {
		t.Fatalf("flashcard-review: want 2xx, got %d", code)
	}
	var res sessionResult
	mustJSON(t, cl.mustPostJSON(t, "/training-session-record", map[string]any{
		"subject_id": st.subjectID, "card_count": 1, "duration_ms": 5000, "score": 100,
	}), http.StatusOK, &res)
	if res.Streak.CurrentStreak != 1 {
		t.Fatalf("want current_streak=1, got %d", res.Streak.CurrentStreak)
	}
	if !containsCode(res.NewlyAwarded, "first_session") {
		t.Fatalf("expected 'first_session' in newly_awarded, got %v", res.NewlyAwarded)
	}
}

// TestE2E_RegisterThroughTraining exercises the full HTTP stack end-to-end.
func TestE2E_RegisterThroughTraining(t *testing.T) {
	testutil.MustTestEnv(t)
	ctx := context.Background()
	cfg := testConfig(t)
	d, cleanup, err := buildDeps(ctx, cfg)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	defer cleanup()
	if err := db_sql.SetupAll(ctx, d.db); err != nil {
		t.Fatalf("SetupAll: %v", err)
	}
	testutil.Reset(t, d.db)
	srv := httptest.NewServer(buildRouter(d))
	defer srv.Close()
	cl := &httpClient{base: srv.URL}
	cl.token = stepRegisterAndVerify(t, cl, ctx, d)
	st := stepCreateContent(t, cl)
	stepTrainAndAssert(t, cl, st)
	if code := cl.rawPost(t, "/user-test-jwt", map[string]string{}); code != http.StatusCreated {
		t.Fatalf("user-test-jwt: want 201, got %d", code)
	}
	if code := cl.rawPost(t, "/ai/flashcards/prompt", map[string]string{}); code != http.StatusBadRequest {
		t.Fatalf("ai/flashcards/prompt: want 400, got %d", code)
	}
}

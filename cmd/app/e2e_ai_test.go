package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/config"
	jwtsigner "studdle/backend/internal/jwt"
	"studdle/backend/testutil"
)

// aiE2ECtx holds the wired server + tokens + identities for one e2e run.
type aiE2ECtx struct {
	pool     *pgxpool.Pool    // pool is the shared DB pool
	srv      *httptest.Server // srv is the wired backend under test
	cfg      *config.Config   // cfg is the test-configured config
	adminTok string           // adminTok is a JWT for the admin user
	userTok  string           // userTok is a JWT for the comp-grant target user
	userID   int64            // userID is the target user id
	subjID   int64            // subjID is the target subject id
}

// TestE2E_AIHappyPath orchestrates: admin grant → SSE generate → commit → quota + DB verify.
func TestE2E_AIHappyPath(t *testing.T) {
	ctx := setupAIE2E(t)
	defer ctx.srv.Close()

	e2eAdminGrant(t, ctx)
	jobID := e2eGenerateAndAssertStream(t, ctx)
	e2eCommit(t, ctx, jobID)
	e2eAssertQuotaAndDB(t, ctx)
}

// setupAIE2E wires deps with a fake AI client and returns a ready e2e context.
func setupAIE2E(t *testing.T) *aiE2ECtx {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	admin := testutil.NewVerifiedUser(t, pool)
	testutil.MakeAdmin(t, pool, admin.ID)
	user := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, user.ID)

	cfg := &config.Config{
		Env: "test", FrontendURL: "http://fe.test", CORSOrigins: []string{"http://fe.test"}, BackendURL: "http://be.test",
		DatabaseURL: "unused", JWTSecret: "a-minimum-32-byte-secret-xxxxxxxxxx",
		JWTIssuer: "studbud-test", JWTTTL: time.Hour,
		SMTPHost: "x", SMTPPort: "1", SMTPFrom: "x@x",
		UploadDir: t.TempDir(), AIModel: "test-model", StripeMode: "test",
	}
	d, _ := mustBuildDepsWithFake(t, pool, cfg, &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"}]}`, Done: true},
		},
	})
	return &aiE2ECtx{
		pool:     pool,
		srv:      httptest.NewServer(buildRouter(d)),
		cfg:      cfg,
		adminTok: mintE2EToken(t, cfg, admin.ID, true, true),
		userTok:  mintE2EToken(t, cfg, user.ID, true, false),
		userID:   user.ID,
		subjID:   subj.ID,
	}
}

// e2eAdminGrant POSTs /admin/grant-ai-access and asserts HTTP 200.
func e2eAdminGrant(t *testing.T, c *aiE2ECtx) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"user_id": c.userID, "active": true})
	if err != nil {
		t.Fatalf("marshal grant: %v", err)
	}
	resp := aiDo(t, c.srv, "POST", "/admin/grant-ai-access", c.adminTok, bytes.NewReader(body), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("grant status = %d", resp.StatusCode)
	}
}

// e2eGenerateAndAssertStream POSTs /ai/flashcards/prompt, asserts SSE events, returns the parsed jobId.
func e2eGenerateAndAssertStream(t *testing.T, c *aiE2ECtx) int64 {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"subject_id": c.subjID, "prompt": "explain X", "style": "standard",
	})
	if err != nil {
		t.Fatalf("marshal gen: %v", err)
	}
	resp := aiDo(t, c.srv, "POST", "/ai/flashcards/prompt", c.userTok, bytes.NewReader(body), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generate status = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	stream := string(raw)
	for _, want := range []string{"event: job", "event: card", "event: done"} {
		if !strings.Contains(stream, want) {
			t.Errorf("missing %q in stream:\n%s", want, stream)
		}
	}
	return parseJobIDFromStream(t, stream)
}

// parseJobIDFromStream extracts jobId from the first `event: job` SSE event.
func parseJobIDFromStream(t *testing.T, stream string) int64 {
	t.Helper()
	lines := strings.Split(stream, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "event: job") || i+1 >= len(lines) {
			continue
		}
		payload := strings.TrimPrefix(lines[i+1], "data: ")
		var env struct {
			JobID int64 `json:"jobId"`
		}
		if err := json.Unmarshal([]byte(payload), &env); err == nil {
			return env.JobID
		}
	}
	t.Fatalf("no jobId in stream:\n%s", stream)
	return 0
}

// e2eCommit POSTs /ai/commit-generation with one loose card and asserts HTTP 200.
func e2eCommit(t *testing.T, c *aiE2ECtx, jobID int64) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"job_id": jobID, "subject_id": c.subjID,
		"chapters": []any{},
		"cards": []any{
			map[string]any{"chapterClientId": "", "title": "t1", "question": "q1", "answer": "a1"},
		},
	})
	if err != nil {
		t.Fatalf("marshal commit: %v", err)
	}
	resp := aiDo(t, c.srv, "POST", "/ai/commit-generation", c.userTok, bytes.NewReader(body), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("commit status = %d", resp.StatusCode)
	}
}

// e2eAssertQuotaAndDB asserts /ai/quota shows aiAccess=true and flashcards row has source='ai'.
func e2eAssertQuotaAndDB(t *testing.T, c *aiE2ECtx) {
	t.Helper()
	resp := aiDo(t, c.srv, "GET", "/ai/quota", c.userTok, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quota status = %d", resp.StatusCode)
	}
	var quota map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&quota); err != nil {
		t.Fatalf("decode quota: %v", err)
	}
	if quota["aiAccess"] != true {
		t.Error("aiAccess = false after grant")
	}
	var count int
	_ = c.pool.QueryRow(context.Background(), `SELECT count(*) FROM flashcards WHERE subject_id=$1 AND source='ai'`, c.subjID).Scan(&count)
	if count != 1 {
		t.Errorf("flashcards source=ai count = %d, want 1", count)
	}
}

// aiDo is a small helper for synthetic HTTP calls in the AI e2e test.
func aiDo(t *testing.T, srv *httptest.Server, method, path, tok string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// mintE2EToken signs a JWT for the e2e test user.
func mintE2EToken(t *testing.T, cfg *config.Config, uid int64, verified, admin bool) string {
	t.Helper()
	signer := jwtsigner.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTTTL)
	tok, err := signer.Sign(jwtsigner.Claims{UID: uid, EmailVerified: verified, IsAdmin: admin})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

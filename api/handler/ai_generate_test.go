package handler_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/api/handler"
	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/http/middleware"
	jwtsigner "studdle/backend/internal/jwt"
	"studdle/backend/pkg/access"
	"studdle/backend/pkg/aipipeline"
	"studdle/backend/testutil"
)

func TestGenerateFromPrompt_StreamsJobThenCardsThenDone(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"}]}`, Done: true},
		},
	}
	srv := newAIGenServer(t, pool, cli)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID, "prompt": "Explain photosynthesis", "style": "standard",
	})
	req := httptest.NewRequest("POST", "/ai/flashcards/prompt", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	stream := w.Body.String()
	for _, want := range []string{"event: job", "event: card", "event: done"} {
		if !strings.Contains(stream, want) {
			t.Errorf("missing %q in stream:\n%s", want, stream)
		}
	}
}

// TestGenerateFromPrompt_CrossUserSubjectForbidden is a regression test for AI-1:
// a caller with no relationship to the target subject must be rejected (403)
// before the provider is ever invoked.
func TestGenerateFromPrompt_CrossUserSubjectForbidden(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, owner.ID)

	stranger := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, stranger.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"}]}`, Done: true},
		},
	}
	srv := newAIGenServer(t, pool, cli)
	tok := mintToken(t, stranger.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID, "prompt": "Explain photosynthesis", "style": "standard",
	})
	req := httptest.NewRequest("POST", "/ai/flashcards/prompt", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	if cli.Calls() != 0 {
		t.Errorf("provider Calls() = %d, want 0 (must reject before calling the provider)", cli.Calls())
	}
}

func newAIGenServer(t *testing.T, pool *pgxpool.Pool, cli aiProvider.Client) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, cli, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/flashcards/prompt", stack(http.HandlerFunc(h.GenerateFromPrompt)))
	return mux
}

func TestGenerateFromPDF_RejectsWithoutFile(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	srv := newAIPDFServer(t, pool, &testutil.FakeAIClient{})
	tok := mintToken(t, u.ID, true, false)

	form := new(bytes.Buffer)
	writer := multipart.NewWriter(form)
	_ = writer.WriteField("subject_id", strconv.FormatInt(subj.ID, 10))
	_ = writer.Close()
	req := httptest.NewRequest("POST", "/ai/flashcards/pdf", form)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

func newAIPDFServer(t *testing.T, pool *pgxpool.Pool, cli aiProvider.Client) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, cli, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/flashcards/pdf", stack(http.HandlerFunc(h.GenerateFromPDF)))
	return mux
}

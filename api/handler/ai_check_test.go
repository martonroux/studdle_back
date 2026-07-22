package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestCheck_ReturnsVerdictJSON(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"verdict":"minor_issues","findings":[{"kind":"style","text":"tighten"}],"suggestion":{"title":"","question":"Q","answer":"A"}}`, Done: true},
		},
	}
	srv := newAICheckServer(t, pool, cli)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{"flashcard_id": fcID})
	req := httptest.NewRequest("POST", "/ai/check", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["verdict"] != "minor_issues" {
		t.Errorf("verdict = %v, want minor_issues", resp["verdict"])
	}
}

// TestCheck_CrossUserFlashcardForbidden is a regression test for AI-1: a caller
// with no relationship to the flashcard's subject must be rejected (403)
// instead of reaching the provider.
func TestCheck_CrossUserFlashcardForbidden(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, owner.ID)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")

	stranger := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, stranger.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"verdict":"ok","findings":[],"suggestion":{"title":"","question":"Q","answer":"A"}}`, Done: true},
		},
	}
	srv := newAICheckServer(t, pool, cli)
	tok := mintToken(t, stranger.ID, true, false)

	body, _ := json.Marshal(map[string]any{"flashcard_id": fcID})
	req := httptest.NewRequest("POST", "/ai/check", bytes.NewReader(body))
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

// TestCheck_NonexistentFlashcardReturns404 is a regression test for AI-4:
// pgx.ErrNoRows from loadFlashcard was previously wrapped as a plain error
// with no sentinel, so it fell through to a 500 instead of 404.
func TestCheck_NonexistentFlashcardReturns404(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)

	srv := newAICheckServer(t, pool, &testutil.FakeAIClient{})
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{"flashcard_id": 999999999})
	req := httptest.NewRequest("POST", "/ai/check", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

func newAICheckServer(t *testing.T, pool *pgxpool.Pool, cli aiProvider.Client) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, cli, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/check", stack(http.HandlerFunc(h.Check)))
	return mux
}

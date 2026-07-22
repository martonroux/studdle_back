package handler_test

import (
	"bytes"
	"context"
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

func TestCommitGeneration_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	srv := newAICommitServer(t, pool)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"job_id":     1,
		"subject_id": subj.ID,
		"chapters":   []any{map[string]any{"clientId": "c1", "title": "Intro"}},
		"cards": []any{
			map[string]any{"chapterClientId": "c1", "title": "t", "question": "q", "answer": "a"},
		},
	})
	req := httptest.NewRequest("POST", "/ai/commit-generation", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

// TestCommitGeneration_CrossUserSubjectForbidden is a regression test for AI-1:
// a caller with no relationship to the target subject must not be able to
// write chapters/cards into it via commit-generation.
func TestCommitGeneration_CrossUserSubjectForbidden(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, owner.ID)

	stranger := testutil.NewVerifiedUser(t, pool)

	srv := newAICommitServer(t, pool)
	tok := mintToken(t, stranger.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID,
		"cards": []any{
			map[string]any{"chapterClientId": "", "title": "IDOR test card", "question": "Injected?", "answer": "Yes if vulnerable"},
		},
	})
	req := httptest.NewRequest("POST", "/ai/commit-generation", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	var cards int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM flashcards WHERE subject_id=$1`, subj.ID).Scan(&cards)
	if cards != 0 {
		t.Errorf("cards inserted = %d, want 0", cards)
	}
}

// TestCommitGeneration_UnknownChapterClientIdReturns400 is a regression test
// for AI-3: an unknown chapterClientId is a client input mistake (the client
// referenced a chapter it never declared), so it must classify as 400, not
// the opaque 500 an unwrapped error previously produced.
func TestCommitGeneration_UnknownChapterClientIdReturns400(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	srv := newAICommitServer(t, pool)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID,
		"cards": []any{
			map[string]any{"chapterClientId": "nonexistent", "title": "t", "question": "q", "answer": "a"},
		},
	})
	req := httptest.NewRequest("POST", "/ai/commit-generation", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

func newAICommitServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, aiProvider.NoopClient{}, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/commit-generation", stack(http.HandlerFunc(h.CommitGeneration)))
	return mux
}

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

	"studbud/backend/api/handler"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/gamification"
	"studbud/backend/testutil"
)

// TestRecordSession_CrossUserSubjectForbidden is a regression test for GAM-1: a caller with
// no ownership or collaborator relationship to the target subject must be rejected (403)
// instead of having the session recorded (and streak/goal/achievements mutated) against it.
func TestRecordSession_CrossUserSubjectForbidden(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, owner.ID)

	outsider := testutil.NewVerifiedUser(t, pool)

	srv := newGamificationServer(t, pool)
	tok := mintToken(t, outsider.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID, "card_count": 999, "duration_ms": 500, "score": 50,
	})
	req := httptest.NewRequest("POST", "/training-session-record", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM training_sessions WHERE subject_id = $1`, subj.ID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no session recorded, got %d", count)
	}
}

// TestRecordSession_OwnerSucceeds sanity-checks that the owner of the subject can still
// record a session, so the GAM-1 access check isn't over-broad.
func TestRecordSession_OwnerSucceeds(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, owner.ID)

	srv := newGamificationServer(t, pool)
	tok := mintToken(t, owner.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID, "card_count": 5, "duration_ms": 500, "score": 50,
	})
	req := httptest.NewRequest("POST", "/training-session-record", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

func newGamificationServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	gam := gamification.NewService(pool, acc)
	h := handler.NewGamificationHandler(gam)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer))
	mux.Handle("POST /training-session-record", stack(http.HandlerFunc(h.RecordSession)))
	return mux
}

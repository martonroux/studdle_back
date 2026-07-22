package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/api/handler"
	"studdle/backend/internal/http/middleware"
	jwtsigner "studdle/backend/internal/jwt"
	"studdle/backend/pkg/friendship"
	"studdle/backend/testutil"
)

// TestFriendshipList_EmptyIsJSONArray is a regression test for SL-5: an empty
// friend list must serialize as `[]`, not `null`, matching the convention
// already used by /collaborators.
func TestFriendshipList_EmptyIsJSONArray(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	srv := newFriendshipServer(t, pool)
	tok := mintToken(t, u.ID, true, false)

	req := httptest.NewRequest("GET", "/friendship-list", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	assertJSONEmptyArray(t, w.Body.String())
}

// TestFriendshipPending_EmptyIsJSONArray is a regression test for SL-5, covering
// the pending-incoming list alongside the friend list.
func TestFriendshipPending_EmptyIsJSONArray(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	srv := newFriendshipServer(t, pool)
	tok := mintToken(t, u.ID, true, false)

	req := httptest.NewRequest("GET", "/friendship-pending", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	assertJSONEmptyArray(t, w.Body.String())
}

// assertJSONEmptyArray fails the test unless body is exactly an empty JSON array.
func assertJSONEmptyArray(t *testing.T, body string) {
	t.Helper()
	got := trimTrailingNewline(body)
	if got != "[]" {
		t.Fatalf("body = %q, want %q", got, "[]")
	}
}

// trimTrailingNewline strips a single trailing newline, as written by json.Encoder.
func trimTrailingNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}

// newFriendshipServer wires Auth → FriendshipHandler for the list/pending routes.
func newFriendshipServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	svc := friendship.NewService(pool)
	h := handler.NewFriendshipHandler(svc)

	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer))
	mux.Handle("GET /friendship-list", stack(http.HandlerFunc(h.ListFriends)))
	mux.Handle("GET /friendship-pending", stack(http.HandlerFunc(h.ListPending)))
	return mux
}

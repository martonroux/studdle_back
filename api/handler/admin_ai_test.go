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
	"studdle/backend/internal/billing"
	"studdle/backend/internal/http/middleware"
	jwtsigner "studdle/backend/internal/jwt"
	"studdle/backend/pkg/access"
	pkgbilling "studdle/backend/pkg/billing"
	"studdle/backend/testutil"
)

func TestGrantAIAccess_AdminPathFlipsUserAccess(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	admin := testutil.NewVerifiedUser(t, pool)
	testutil.MakeAdmin(t, pool, admin.ID)
	target := testutil.NewVerifiedUser(t, pool)

	srv := newAdminAIServer(t, pool)
	tok := mintAdminToken(t, admin.ID)

	body, _ := json.Marshal(map[string]any{"user_id": target.ID, "active": true})
	req := httptest.NewRequest("POST", "/admin/grant-ai-access", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var ok bool
	_ = pool.QueryRow(context.Background(), `SELECT user_has_ai_access($1)`, target.ID).Scan(&ok)
	if !ok {
		t.Fatal("target user has no AI access post-grant")
	}
}

func TestGrantAIAccess_RejectsNonAdmin(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	nonAdmin := testutil.NewVerifiedUser(t, pool)
	target := testutil.NewVerifiedUser(t, pool)

	srv := newAdminAIServer(t, pool)
	tok := mintToken(t, nonAdmin.ID, true, false)

	body, _ := json.Marshal(map[string]any{"user_id": target.ID, "active": true})
	req := httptest.NewRequest("POST", "/admin/grant-ai-access", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

// newAdminAIServer wires Auth → RequireVerified → RequireAdmin → AdminAIHandler.GrantAIAccess.
func newAdminAIServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	billSvc := pkgbilling.NewService(pool, billing.NoopClient{}, pkgbilling.PriceMap{})
	accSvc := access.NewService(pool)
	h := handler.NewAdminAIHandler(billSvc, accSvc)

	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified(), middleware.RequireAdmin())
	mux.Handle("POST /admin/grant-ai-access", stack(http.HandlerFunc(h.GrantAIAccess)))
	return mux
}

func mintAdminToken(t *testing.T, uid int64) string {
	t.Helper()
	return mintToken(t, uid, true, true)
}

func mintToken(t *testing.T, uid int64, verified, admin bool) string {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	tok, err := signer.Sign(jwtsigner.Claims{UID: uid, EmailVerified: verified, IsAdmin: admin})
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}

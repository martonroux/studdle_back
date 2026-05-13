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
	internalbilling "studbud/backend/internal/billing"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestAdminBillingGrant_Returns200AndCompsUser(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	admin := testutil.NewVerifiedUser(t, pool)
	testutil.MakeAdmin(t, pool, admin.ID)
	target := testutil.NewVerifiedUser(t, pool)

	srv := newAdminBillingServer(t, pool)
	tok := mintAdminToken(t, admin.ID)

	body, _ := json.Marshal(map[string]any{"user_id": target.ID, "reason": "beta tester"})
	req := httptest.NewRequest("POST", "/admin/comp-subscription", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["aiAccess"] != true {
		t.Fatalf("aiAccess = %v, want true", resp["aiAccess"])
	}

	var status string
	err := pool.QueryRow(context.Background(),
		`SELECT status FROM user_subscriptions WHERE user_id = $1`, target.ID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "comped" {
		t.Fatalf("status = %q, want comped", status)
	}
}

func TestAdminBillingRevoke_Returns200AndCancels(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	admin := testutil.NewVerifiedUser(t, pool)
	testutil.MakeAdmin(t, pool, admin.ID)
	target := testutil.NewVerifiedUser(t, pool)

	// Pre-grant comp access directly.
	svc := pkgbilling.NewService(pool, internalbilling.NoopClient{}, pkgbilling.PriceMap{})
	_ = svc.GrantCompWithExpiry(context.Background(), pkgbilling.CompGrant{
		UserID: target.ID, Reason: "init", ActorUserID: admin.ID,
	})

	srv := newAdminBillingServer(t, pool)
	tok := mintAdminToken(t, admin.ID)

	body, _ := json.Marshal(map[string]any{"user_id": target.ID, "reason": "support ticket"})
	req := httptest.NewRequest("DELETE", "/admin/comp-subscription", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["aiAccess"] != false {
		t.Fatalf("aiAccess = %v, want false", resp["aiAccess"])
	}
}

func TestAdminBillingGrant_RejectsNonAdmin(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	nonAdmin := testutil.NewVerifiedUser(t, pool)
	target := testutil.NewVerifiedUser(t, pool)

	srv := newAdminBillingServer(t, pool)
	tok := mintToken(t, nonAdmin.ID, true, false)

	body, _ := json.Marshal(map[string]any{"user_id": target.ID, "reason": "test"})
	req := httptest.NewRequest("POST", "/admin/comp-subscription", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

// newAdminBillingServer wires Auth → RequireVerified → RequireAdmin → AdminBillingHandler.
func newAdminBillingServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	billSvc := pkgbilling.NewService(pool, internalbilling.NoopClient{}, pkgbilling.PriceMap{})
	accSvc := access.NewService(pool)
	h := handler.NewAdminBillingHandler(billSvc, accSvc)

	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified(), middleware.RequireAdmin())
	mux.Handle("POST /admin/comp-subscription", stack(http.HandlerFunc(h.Grant)))
	mux.Handle("DELETE /admin/comp-subscription", stack(http.HandlerFunc(h.Revoke)))
	return mux
}

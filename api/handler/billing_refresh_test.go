package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"studbud/backend/api/handler"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	pkgbilling "studbud/backend/pkg/billing"
	pkguser "studbud/backend/pkg/user"
	"studbud/backend/testutil"
)

func TestBillingRefresh_Returns200(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	billSvc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	userSvc := pkguser.NewService(pool, signer)
	h := handler.NewBillingHandler(billSvc, userSvc, &stubProvider{}, "https://app/billing", "https://app/pricing")

	tok, _ := signer.Sign(jwtsigner.Claims{UID: u.ID, EmailVerified: true, IsAdmin: false})
	req := httptest.NewRequest("POST", "/billing/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.Refresh)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestBillingRefresh_RateLimitAfter10(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	billSvc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	userSvc := pkguser.NewService(pool, signer)
	h := handler.NewBillingHandler(billSvc, userSvc, &stubProvider{}, "https://app/billing", "https://app/pricing")

	tok, _ := signer.Sign(jwtsigner.Claims{UID: u.ID, EmailVerified: true, IsAdmin: false})
	// First 10 calls must succeed.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("POST", "/billing/refresh", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		middleware.Auth(signer)(http.HandlerFunc(h.Refresh)).ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("call %d: status %d body %s", i+1, w.Code, w.Body.String())
		}
	}
	// 11th call must be rate-limited.
	req := httptest.NewRequest("POST", "/billing/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.Refresh)).ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("11th call: status %d, want 429", w.Code)
	}

	// The 429 body must be the same JSON error envelope every other
	// endpoint uses, not plain text — a frontend that does res.json()
	// unconditionally on non-200 responses would otherwise throw.
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("11th call: Content-Type = %q, want application/json", ct)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("11th call: body is not valid JSON: %v (body = %s)", err, w.Body.String())
	}
	errBody, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("11th call: response missing 'error' object: %s", w.Body.String())
	}
	if errBody["code"] != "rate_limited" {
		t.Fatalf("11th call: error.code = %v, want rate_limited", errBody["code"])
	}
}

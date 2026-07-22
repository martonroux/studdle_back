package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"studdle/backend/api/handler"
	internalbilling "studdle/backend/internal/billing"
	"studdle/backend/internal/http/middleware"
	jwtsigner "studdle/backend/internal/jwt"
	pkgbilling "studdle/backend/pkg/billing"
	pkguser "studdle/backend/pkg/user"
	"studdle/backend/testutil"
)

func TestGetSubscription_NoRow_ReturnsStatusNone(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	billSvc := pkgbilling.NewService(pool, internalbilling.NoopClient{}, pkgbilling.PriceMap{})
	userSvc := pkguser.NewService(pool, signer)
	h := handler.NewBillingHandler(billSvc, userSvc, &stubProvider{}, "https://app/billing", "https://app/pricing")

	tok, _ := signer.Sign(jwtsigner.Claims{UID: u.ID, EmailVerified: true, IsAdmin: false})
	req := httptest.NewRequest("GET", "/billing/subscription", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.GetSubscription)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "none" {
		t.Fatalf("status = %v, want none", resp["status"])
	}
}

func TestGetSubscription_TrialingRow_ReturnsActiveTrue(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	// Insert a trialing row with a future trial_end.
	trialEnd := time.Now().Add(7 * 24 * time.Hour).UTC()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO user_subscriptions (user_id, plan, status, trial_end)
		VALUES ($1, 'pro_monthly', 'trialing', $2)
	`, u.ID, trialEnd)
	if err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	billSvc := pkgbilling.NewService(pool, internalbilling.NoopClient{}, pkgbilling.PriceMap{})
	userSvc := pkguser.NewService(pool, signer)
	h := handler.NewBillingHandler(billSvc, userSvc, &stubProvider{}, "https://app/billing", "https://app/pricing")

	tok, _ := signer.Sign(jwtsigner.Claims{UID: u.ID, EmailVerified: true, IsAdmin: false})
	req := httptest.NewRequest("GET", "/billing/subscription", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.GetSubscription)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "trialing" {
		t.Fatalf("status = %v, want trialing", resp["status"])
	}
	if resp["isActive"] != true {
		t.Fatalf("isActive = %v, want true", resp["isActive"])
	}
}

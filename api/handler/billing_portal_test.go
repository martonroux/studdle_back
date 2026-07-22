package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"studdle/backend/api/handler"
	"studdle/backend/internal/http/middleware"
	jwtsigner "studdle/backend/internal/jwt"
	pkgbilling "studdle/backend/pkg/billing"
	pkguser "studdle/backend/pkg/user"
	"studdle/backend/testutil"
)

func TestBillingPortal_NoCustomerReturns404(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	userSvc := pkguser.NewService(pool, signer)
	billSvc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	h := handler.NewBillingHandler(billSvc, userSvc, &stubProvider{}, "https://app/billing", "https://app/pricing")

	tok, _ := signer.Sign(jwtsigner.Claims{UID: u.ID, EmailVerified: true, IsAdmin: false})
	req := httptest.NewRequest("POST", "/billing/portal", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.Portal)).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestBillingPortal_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan, stripe_customer_id)
		 VALUES ($1, 'active', 'pro_monthly', 'cus_portal')`, u.ID)

	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	userSvc := pkguser.NewService(pool, signer)
	fake := &testutil.FakeBilling{PortalURL: "https://portal.stripe.test/p"}
	billSvc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	h := handler.NewBillingHandler(billSvc, userSvc, &stubProvider{}, "https://app/billing", "https://app/pricing")

	tok, _ := signer.Sign(jwtsigner.Claims{UID: u.ID, EmailVerified: true, IsAdmin: false})
	req := httptest.NewRequest("POST", "/billing/portal", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.Portal)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.URL != "https://portal.stripe.test/p" {
		t.Fatalf("url=%q", resp.URL)
	}
}

package handler_test

import (
	"bytes"
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

func TestBillingCheckout_ReturnsURL(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	fake := &testutil.FakeBilling{CheckoutURL: "https://co.stripe.test/x", CustomerID: "cus_Z"}
	billSvc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	userSvc := pkguser.NewService(pool, signer)
	h := handler.NewBillingHandler(billSvc, userSvc, &stubProvider{}, "https://app/billing", "https://app/pricing")

	tok, _ := signer.Sign(jwtsigner.Claims{UID: u.ID, EmailVerified: true, IsAdmin: false})
	body, _ := json.Marshal(map[string]string{"plan": "pro_monthly"})
	req := httptest.NewRequest("POST", "/billing/checkout", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.Checkout)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.URL != "https://co.stripe.test/x" {
		t.Fatalf("url=%q", resp.URL)
	}
}

func TestBillingCheckout_AlreadySubscribedReturnsConflict(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	userSvc := pkguser.NewService(pool, signer)
	billSvc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	h := handler.NewBillingHandler(billSvc, userSvc, &stubProvider{}, "https://app/billing", "https://app/pricing")

	tok, _ := signer.Sign(jwtsigner.Claims{UID: u.ID, EmailVerified: true, IsAdmin: false})
	body, _ := json.Marshal(map[string]string{"plan": "pro_monthly"})
	req := httptest.NewRequest("POST", "/billing/checkout", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.Checkout)).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

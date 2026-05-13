package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"studbud/backend/api/handler"
	"studbud/backend/internal/billing"
	jwtsigner "studbud/backend/internal/jwt"
	pkgbilling "studbud/backend/pkg/billing"
	pkguser "studbud/backend/pkg/user"
	"studbud/backend/testutil"
)

// newBillingHandlerForTest constructs a BillingHandler wired to the given service.
func newBillingHandlerForTest(t *testing.T, svc *pkgbilling.Service) *handler.BillingHandler {
	t.Helper()
	pool := testutil.OpenTestDB(t)
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	userSvc := pkguser.NewService(pool, signer)
	return handler.NewBillingHandler(svc, userSvc, &stubProvider{}, "https://app/billing", "https://app/pricing")
}

func TestBillingWebhook_LivemodeMismatchReturns400(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	fake := &testutil.FakeBilling{Event: &billing.WebhookEvent{
		ID: "evt_lm", Type: "customer.subscription.updated", Livemode: true,
		Raw: []byte(`{}`),
	}}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	h := newBillingHandlerForTest(t, svc)
	h.SetStripeLivemode(false)

	req := httptest.NewRequest(http.MethodPost, "/billing/webhook", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Stripe-Signature", "x")
	w := httptest.NewRecorder()
	h.Webhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestBillingWebhook_SuccessReturns200(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	fake := &testutil.FakeBilling{Event: &billing.WebhookEvent{
		ID: "evt_ok", Type: "charge.refunded", Livemode: false,
		Raw: []byte(`{"id":"evt_ok"}`),
	}}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	h := newBillingHandlerForTest(t, svc)
	h.SetStripeLivemode(false)

	req := httptest.NewRequest(http.MethodPost, "/billing/webhook", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Stripe-Signature", "x")
	w := httptest.NewRecorder()
	h.Webhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

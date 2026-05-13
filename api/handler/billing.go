package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/billing"
	"studbud/backend/pkg/user"
)

// BillingHandler exposes Spec C billing endpoints.
type BillingHandler struct {
	svc            *billing.Service // svc is the billing service
	users          *user.Service    // users is used to fetch the caller's email
	billingPageURL string           // billingPageURL is the Stripe checkout success redirect
	pricingPageURL string           // pricingPageURL is the Stripe checkout cancel redirect
	expectLive     bool             // expectLive mirrors STRIPE_MODE=="live"
	limMu          sync.Mutex       // limMu guards lim
	lim            map[int64]*rate.Limiter // lim holds per-user rate limiters
}

// NewBillingHandler constructs a BillingHandler.
func NewBillingHandler(svc *billing.Service, users *user.Service, billingPageURL, pricingPageURL string) *BillingHandler {
	return &BillingHandler{
		svc:            svc,
		users:          users,
		billingPageURL: billingPageURL,
		pricingPageURL: pricingPageURL,
		lim:            map[int64]*rate.Limiter{},
	}
}

// limiterFor returns the rate.Limiter for uid, creating it if absent.
// Allows 10 calls per minute with no additional burst.
func (h *BillingHandler) limiterFor(uid int64) *rate.Limiter {
	h.limMu.Lock()
	defer h.limMu.Unlock()
	l, ok := h.lim[uid]
	if !ok {
		l = rate.NewLimiter(rate.Every(time.Minute/10), 10)
		h.lim[uid] = l
	}
	return l
}

// subscriptionResponse is the JSON shape returned by GET /billing/subscription.
type subscriptionResponse struct {
	Status            string  `json:"status"`
	Plan              *string `json:"plan"`
	CurrentPeriodEnd  *string `json:"currentPeriodEnd"`
	TrialEnd          *string `json:"trialEnd"`
	CancelAtPeriodEnd bool    `json:"cancelAtPeriodEnd"`
	IsActive          bool    `json:"isActive"`
}

// GetSubscription handles GET /billing/subscription.
func (h *BillingHandler) GetSubscription(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	if uid == 0 {
		httpx.WriteError(w, myErrors.ErrUnauthenticated)
		return
	}
	h.writeSubscriptionResponse(r.Context(), w, uid)
}

// writeSubscriptionResponse renders the subscription read shape.
// Shared by GetSubscription and Refresh.
func (h *BillingHandler) writeSubscriptionResponse(ctx context.Context, w http.ResponseWriter, uid int64) {
	sub, err := h.svc.GetSubscription(ctx, uid)
	if errors.Is(err, billing.ErrSubscriptionNotFound) {
		httpx.WriteJSON(w, http.StatusOK, subscriptionResponse{Status: "none"})
		return
	}
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	resp := subscriptionResponse{
		Status:            string(sub.Status),
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
		IsActive:          sub.IsActive(time.Now()),
	}
	if sub.Plan != "" {
		p := string(sub.Plan)
		resp.Plan = &p
	}
	if sub.CurrentPeriodEnd != nil {
		ts := sub.CurrentPeriodEnd.UTC().Format(time.RFC3339)
		resp.CurrentPeriodEnd = &ts
	}
	if sub.TrialEnd != nil {
		ts := sub.TrialEnd.UTC().Format(time.RFC3339)
		resp.TrialEnd = &ts
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// planTile describes one plan for the public pricing UI.
type planTile struct {
	Plan        string  `json:"plan"`
	PriceEur    float64 `json:"priceEur"`
	Interval    string  `json:"interval"`
	DiscountPct *int    `json:"discountPct,omitempty"`
}

// GetPlans handles GET /billing/plans.
// Public: prices are config-driven and safe to expose.
func (h *BillingHandler) GetPlans(w http.ResponseWriter, r *http.Request) {
	discount := 29
	tiles := []planTile{
		{Plan: "pro_monthly", PriceEur: 6.99, Interval: "month"},
		{Plan: "pro_annual", PriceEur: 59.99, Interval: "year", DiscountPct: &discount},
	}
	httpx.WriteJSON(w, http.StatusOK, tiles)
}

// Refresh handles POST /billing/refresh.
func (h *BillingHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	if uid == 0 {
		httpx.WriteError(w, myErrors.ErrUnauthenticated)
		return
	}
	if !h.limiterFor(uid).Allow() {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	if err := h.svc.RefreshFromStripe(r.Context(), uid); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.writeSubscriptionResponse(r.Context(), w, uid)
}

// checkoutInput is the request body for POST /billing/checkout.
type checkoutInput struct {
	Plan string `json:"plan"` // Plan is the desired subscription plan identifier
}

// Checkout handles POST /billing/checkout.
func (h *BillingHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	if uid == 0 {
		httpx.WriteError(w, myErrors.ErrUnauthenticated)
		return
	}
	u, err := h.users.ByID(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, fmt.Errorf("load user:\n%w", err))
		return
	}
	var in checkoutInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	plan := billing.Plan(in.Plan)
	url, err := h.svc.CreateCheckoutSession(r.Context(), uid, u.Email, plan, h.billingPageURL, h.pricingPageURL)
	switch {
	case errors.Is(err, billing.ErrAlreadySubscribed):
		httpx.WriteError(w, &myErrors.AppError{Code: "already_subscribed", Message: "user already has an active subscription", Wrapped: myErrors.ErrConflict})
		return
	case errors.Is(err, billing.ErrUnknownPlan):
		httpx.WriteError(w, &myErrors.AppError{Code: "unknown_plan", Message: "unknown plan", Wrapped: myErrors.ErrValidation, Field: "plan"})
		return
	case err != nil:
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": url})
}

// Portal handles POST /billing/portal.
func (h *BillingHandler) Portal(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	if uid == 0 {
		httpx.WriteError(w, myErrors.ErrUnauthenticated)
		return
	}
	url, err := h.svc.CreatePortalSession(r.Context(), uid, h.billingPageURL)
	switch {
	case errors.Is(err, billing.ErrNoCustomer):
		httpx.WriteError(w, &myErrors.AppError{Code: "no_customer", Message: "no stripe customer for user", Wrapped: myErrors.ErrNotFound})
		return
	case err != nil:
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": url})
}

// SetStripeLivemode sets the expected livemode flag used in webhook validation.
func (h *BillingHandler) SetStripeLivemode(live bool) { h.expectLive = live }

// Webhook handles POST /billing/webhook.
// Public route: the request is authenticated by Stripe-Signature, not by JWT.
func (h *BillingHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "body read failed", http.StatusBadRequest)
		return
	}
	cfg := billing.WebhookConfig{
		ExpectLivemode: h.expectLive,
		Body:           body,
		Signature:      r.Header.Get("Stripe-Signature"),
	}
	if err := h.svc.HandleWebhook(r.Context(), cfg); err != nil {
		if errors.Is(err, billing.ErrLivemodeMismatch) {
			http.Error(w, "livemode mismatch", http.StatusBadRequest)
			return
		}
		http.Error(w, "webhook error: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

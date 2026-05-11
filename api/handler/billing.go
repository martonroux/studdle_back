package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

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
}

// NewBillingHandler constructs a BillingHandler.
func NewBillingHandler(svc *billing.Service, users *user.Service, billingPageURL, pricingPageURL string) *BillingHandler {
	return &BillingHandler{svc: svc, users: users, billingPageURL: billingPageURL, pricingPageURL: pricingPageURL}
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

// Webhook handles POST /billing/webhook (Stripe signed payload).
func (h *BillingHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

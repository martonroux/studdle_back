package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
	"studdle/backend/pkg/billing"
)

// AdminBillingHandler exposes Spec C admin endpoints.
type AdminBillingHandler struct {
	billing *billing.Service
	access  *access.Service
}

// NewAdminBillingHandler constructs an AdminBillingHandler.
func NewAdminBillingHandler(b *billing.Service, a *access.Service) *AdminBillingHandler {
	return &AdminBillingHandler{billing: b, access: a}
}

// grantBody is the request body for POST /admin/comp-subscription.
type grantBody struct {
	UserID    int64   `json:"user_id"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	Reason    string  `json:"reason"`
}

// revokeBody is the request body for DELETE /admin/comp-subscription.
type revokeBody struct {
	UserID int64  `json:"user_id"`
	Reason string `json:"reason"`
}

// Grant handles POST /admin/comp-subscription.
func (h *AdminBillingHandler) Grant(w http.ResponseWriter, r *http.Request) {
	actor := authctx.UID(r.Context())
	var in grantBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "invalid_input", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	if in.UserID <= 0 {
		httpx.WriteError(w, &myErrors.AppError{Code: "validation", Field: "user_id", Wrapped: myErrors.ErrValidation})
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		httpx.WriteError(w, &myErrors.AppError{Code: "validation", Message: "reason is required", Field: "reason", Wrapped: myErrors.ErrValidation})
		return
	}
	var expires *time.Time
	if in.ExpiresAt != nil && *in.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *in.ExpiresAt)
		if err != nil {
			httpx.WriteError(w, &myErrors.AppError{Code: "validation", Field: "expires_at", Wrapped: myErrors.ErrValidation})
			return
		}
		expires = &t
	}
	g := billing.CompGrant{UserID: in.UserID, ExpiresAt: expires, Reason: in.Reason, ActorUserID: actor}
	if err := h.billing.GrantCompWithExpiry(r.Context(), g); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ok, _ := h.access.HasAIAccess(r.Context(), in.UserID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"userId": in.UserID, "aiAccess": ok})
}

// Revoke handles DELETE /admin/comp-subscription.
func (h *AdminBillingHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	actor := authctx.UID(r.Context())
	var in revokeBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "invalid_input", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	if in.UserID <= 0 {
		httpx.WriteError(w, &myErrors.AppError{Code: "validation", Field: "user_id", Wrapped: myErrors.ErrValidation})
		return
	}
	if err := h.billing.RevokeComp(r.Context(), billing.CompRevoke{UserID: in.UserID, Reason: in.Reason, ActorUserID: actor}); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ok, _ := h.access.HasAIAccess(r.Context(), in.UserID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"userId": in.UserID, "aiAccess": ok})
}

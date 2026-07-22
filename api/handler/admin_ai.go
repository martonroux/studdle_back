package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
	"studdle/backend/pkg/billing"
)

// AdminAIHandler exposes admin-only routes for AI entitlement management.
type AdminAIHandler struct {
	billing *billing.Service // billing writes user_subscriptions rows
	access  *access.Service  // access reads user_has_ai_access post-mutation
}

// NewAdminAIHandler constructs an AdminAIHandler.
func NewAdminAIHandler(b *billing.Service, a *access.Service) *AdminAIHandler {
	return &AdminAIHandler{billing: b, access: a}
}

// grantInput is the request body for POST /admin/grant-ai-access.
type grantInput struct {
	UserID int64 `json:"user_id"` // UserID is the target user to flip
	Active bool  `json:"active"`  // Active=true grants, false revokes comp access
}

// grantOutput is the response body for POST /admin/grant-ai-access.
type grantOutput struct {
	UserID   int64 `json:"userId"`   // UserID echoes the target user
	AIAccess bool  `json:"aiAccess"` // AIAccess reflects user_has_ai_access post-mutation
}

// GrantAIAccess flips a comp user_subscriptions row for the target user.
// Response body reports the post-mutation AI-access state.
func (h *AdminAIHandler) GrantAIAccess(w http.ResponseWriter, r *http.Request) {
	in, err := decodeGrantInput(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.billing.GrantComp(r.Context(), in.UserID, in.Active); err != nil {
		httpx.WriteError(w, fmt.Errorf("grant comp:\n%w", err))
		return
	}
	ok, err := h.access.HasAIAccess(r.Context(), in.UserID)
	if err != nil {
		httpx.WriteError(w, fmt.Errorf("check access:\n%w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, grantOutput{UserID: in.UserID, AIAccess: ok})
}

// decodeGrantInput parses and validates the request body.
func decodeGrantInput(r *http.Request) (grantInput, error) {
	var in grantInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return in, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput}
	}
	if in.UserID <= 0 {
		return in, &myErrors.AppError{Code: "validation", Message: "user_id must be positive", Wrapped: myErrors.ErrValidation, Field: "user_id"}
	}
	return in, nil
}

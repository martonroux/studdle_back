package handler

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/emailverification"
	"studdle/backend/pkg/user"
)

// EmailVerificationHandler handles verify + resend routes.
type EmailVerificationHandler struct {
	verifier *emailverification.Service // verifier issues and validates email verification tokens
	users    *user.Service              // users provides user lookups for the resend flow
}

// NewEmailVerificationHandler constructs the handler.
func NewEmailVerificationHandler(v *emailverification.Service, u *user.Service) *EmailVerificationHandler {
	return &EmailVerificationHandler{verifier: v, users: u}
}

// Verify handles GET /verify-email?token=...
func (h *EmailVerificationHandler) Verify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.verifier.Verify(r.Context(), token); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "email verified successfully"})
}

// Resend handles POST /resend-verification (auth only, no RequireVerified).
func (h *EmailVerificationHandler) Resend(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	u, err := h.users.ByID(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if u.EmailVerified {
		httpx.WriteError(w, myErrors.ErrAlreadyVerified)
		return
	}
	if err := h.verifier.Issue(r.Context(), uid, u.Email); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "verification email sent"})
}

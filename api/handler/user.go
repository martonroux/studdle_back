package handler

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/pkg/emailverification"
	"studdle/backend/pkg/user"
)

// UserHandler wires HTTP routes for user-scope operations.
type UserHandler struct {
	svc      *user.Service              // svc is the user domain service
	verifier *emailverification.Service // verifier issues and validates email verification tokens
}

// NewUserHandler constructs the handler.
func NewUserHandler(svc *user.Service, verifier *emailverification.Service) *UserHandler {
	return &UserHandler{svc: svc, verifier: verifier}
}

// Register handles POST /user-register.
func (h *UserHandler) Register(w http.ResponseWriter, r *http.Request) {
	var in user.RegisterInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	tok, uid, err := h.svc.Register(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.verifier.Issue(r.Context(), uid, in.Email); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, user.TokenResponse{Token: tok})
}

// Login handles POST /user-login.
func (h *UserHandler) Login(w http.ResponseWriter, r *http.Request) {
	var in user.LoginInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	tok, err := h.svc.Login(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, user.TokenResponse{Token: tok})
}

// TestJWT handles POST /user-test-jwt (returns 201 on valid token).
func (h *UserHandler) TestJWT(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusCreated)
}

// SetProfilePicture handles POST /set-profile-picture.
func (h *UserHandler) SetProfilePicture(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in struct {
		ImageID string `json:"image_id"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.SetProfilePicture(r.Context(), uid, in.ImageID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "profile picture updated"})
}

// Stats handles GET /get-user-stats.
func (h *UserHandler) Stats(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	out, err := h.svc.Stats(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

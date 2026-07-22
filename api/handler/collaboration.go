package handler

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/collaboration"
)

// CollaborationHandler exposes the subject-collaboration endpoints.
type CollaborationHandler struct {
	svc *collaboration.Service // svc owns the collaboration business logic
}

// NewCollaborationHandler constructs a CollaborationHandler.
func NewCollaborationHandler(svc *collaboration.Service) *CollaborationHandler {
	return &CollaborationHandler{svc: svc}
}

// addInput is the JSON body accepted by AddCollaborator.
type addInput struct {
	SubjectID int64  `json:"subject_id"` // SubjectID is the subject being shared
	UserID    int64  `json:"user_id"`    // UserID is the grantee user id
	Role      string `json:"role"`       // Role is viewer|editor
}

// AddCollaborator handles POST /collaborators (JSON body).
func (h *CollaborationHandler) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in addInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.AddCollaborator(r.Context(), uid, in.SubjectID, in.UserID, in.Role)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

// RemoveCollaborator handles POST /collaborator-remove?subject_id=...&user_id=...
func (h *CollaborationHandler) RemoveCollaborator(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	target, err := httpx.QueryInt64(r, "user_id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.RemoveCollaborator(r.Context(), uid, sid, target); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListCollaborators handles GET /collaborators?subject_id=...
func (h *CollaborationHandler) ListCollaborators(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	list, err := h.svc.ListCollaborators(r.Context(), uid, sid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if list == nil {
		list = []collaboration.Collaborator{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// CreateInvite handles POST /collaboration-invites (JSON body).
func (h *CollaborationHandler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in collaboration.CreateInviteInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	inv, err := h.svc.CreateInvite(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, inv)
}

// RedeemInvite handles POST /collaboration-invite-redeem?token=...
func (h *CollaborationHandler) RedeemInvite(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	token := r.URL.Query().Get("token")
	if token == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	c, err := h.svc.RedeemInvite(r.Context(), uid, token)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

// RevokeInvite handles POST /collaboration-invite-revoke?token=...
func (h *CollaborationHandler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	token := r.URL.Query().Get("token")
	if token == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.RevokeInvite(r.Context(), uid, token); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

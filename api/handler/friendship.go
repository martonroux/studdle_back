package handler

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/pkg/friendship"
)

// FriendshipHandler exposes friendship endpoints.
type FriendshipHandler struct {
	svc *friendship.Service // svc owns the business logic
}

// NewFriendshipHandler constructs a FriendshipHandler.
func NewFriendshipHandler(svc *friendship.Service) *FriendshipHandler {
	return &FriendshipHandler{svc: svc}
}

// Request handles POST /friendship-request.
func (h *FriendshipHandler) Request(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in friendship.RequestInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	f, err := h.svc.Request(r.Context(), uid, in.ReceiverID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, f)
}

// Accept handles POST /friendship-accept?id=...
func (h *FriendshipHandler) Accept(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	f, err := h.svc.Accept(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}

// Decline handles POST /friendship-decline?id=...
func (h *FriendshipHandler) Decline(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	f, err := h.svc.Decline(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}

// Unfriend handles POST /friendship-unfriend?id=...
func (h *FriendshipHandler) Unfriend(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Unfriend(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListFriends handles GET /friendship-list.
func (h *FriendshipHandler) ListFriends(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	list, err := h.svc.ListFriends(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if list == nil {
		list = []friendship.Friendship{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// ListPending handles GET /friendship-pending.
func (h *FriendshipHandler) ListPending(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	list, err := h.svc.ListPendingIncoming(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if list == nil {
		list = []friendship.Friendship{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

package handler

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/pkg/subjectsub"
)

// SubjectSubscriptionHandler exposes subject-subscription endpoints.
type SubjectSubscriptionHandler struct {
	svc *subjectsub.Service // svc owns the subscription business logic
}

// NewSubjectSubscriptionHandler constructs a SubjectSubscriptionHandler.
func NewSubjectSubscriptionHandler(svc *subjectsub.Service) *SubjectSubscriptionHandler {
	return &SubjectSubscriptionHandler{svc: svc}
}

// Subscribe handles POST /subject-subscribe?subject_id=...
func (h *SubjectSubscriptionHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Subscribe(r.Context(), uid, sid); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unsubscribe handles POST /subject-unsubscribe?subject_id=...
func (h *SubjectSubscriptionHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Unsubscribe(r.Context(), uid, sid); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// List handles GET /subject-subscriptions and returns subscribed subject ids.
func (h *SubjectSubscriptionHandler) List(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	ids, err := h.svc.ListSubscribed(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if ids == nil {
		ids = []int64{}
	}
	httpx.WriteJSON(w, http.StatusOK, ids)
}

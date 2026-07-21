package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/pkg/subject"
)

// SubjectHandler exposes subject CRUD endpoints.
type SubjectHandler struct {
	svc *subject.Service // svc owns the business logic
}

// NewSubjectHandler constructs a SubjectHandler.
func NewSubjectHandler(svc *subject.Service) *SubjectHandler {
	return &SubjectHandler{svc: svc}
}

// Create handles POST /subject-create.
func (h *SubjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in subject.CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	sub, err := h.svc.Create(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sub)
}

// List handles GET /subject-list.
func (h *SubjectHandler) List(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	includeArchived := r.URL.Query().Get("archived") == "true"
	subs, err := h.svc.ListOwned(r.Context(), uid, includeArchived)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, subs)
}

// Get handles GET /subject?id=...
func (h *SubjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	sub, err := h.svc.Get(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sub)
}

// Stats handles GET /subject-stats?id=...
func (h *SubjectHandler) Stats(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out, err := h.svc.Stats(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// History handles GET /subject-stats-history?id=...
func (h *SubjectHandler) History(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out, err := h.svc.History(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// MasteryTrend handles GET /subject-stats-mastery-trend?id=...&period=7d|30d|all
func (h *SubjectHandler) MasteryTrend(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	period := r.URL.Query().Get("period")
	out, err := h.svc.MasteryTrend(r.Context(), uid, id, period)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// Update handles POST /subject-update?id=...
func (h *SubjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var in subject.UpdateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	sub, err := h.svc.Update(r.Context(), uid, id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sub)
}

// Delete handles POST /subject-delete?id=...
func (h *SubjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Delete(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

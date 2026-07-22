package handler

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/pkg/chapter"
)

// ChapterHandler exposes chapter CRUD endpoints.
type ChapterHandler struct {
	svc *chapter.Service // svc owns the business logic
}

// NewChapterHandler constructs a ChapterHandler.
func NewChapterHandler(svc *chapter.Service) *ChapterHandler {
	return &ChapterHandler{svc: svc}
}

// Create handles POST /chapter-create.
func (h *ChapterHandler) Create(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in chapter.CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ch, err := h.svc.Create(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ch)
}

// List handles GET /chapter-list?subject_id=...
func (h *ChapterHandler) List(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	list, err := h.svc.List(r.Context(), uid, sid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// Stats handles GET /chapter-stats?id=...
func (h *ChapterHandler) Stats(w http.ResponseWriter, r *http.Request) {
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

// Update handles POST /chapter-update?id=...
func (h *ChapterHandler) Update(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var in chapter.UpdateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ch, err := h.svc.Update(r.Context(), uid, id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ch)
}

// Delete handles POST /chapter-delete?id=...
func (h *ChapterHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

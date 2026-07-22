package handler

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/pkg/flashcard"
)

// FlashcardHandler exposes flashcard CRUD + review endpoints.
type FlashcardHandler struct {
	svc *flashcard.Service // svc owns the business logic
}

// NewFlashcardHandler constructs a FlashcardHandler.
func NewFlashcardHandler(svc *flashcard.Service) *FlashcardHandler {
	return &FlashcardHandler{svc: svc}
}

// Create handles POST /flashcard-create.
func (h *FlashcardHandler) Create(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in flashcard.CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	fc, err := h.svc.Create(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, fc)
}

// ListBySubject handles GET /flashcard-list?subject_id=...
func (h *FlashcardHandler) ListBySubject(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	list, err := h.svc.ListBySubject(r.Context(), uid, sid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// Get handles GET /flashcard?id=...
func (h *FlashcardHandler) Get(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	fc, err := h.svc.Get(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fc)
}

// Update handles POST /flashcard-update?id=...
func (h *FlashcardHandler) Update(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var in flashcard.UpdateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	fc, err := h.svc.Update(r.Context(), uid, id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fc)
}

// Delete handles POST /flashcard-delete?id=...
func (h *FlashcardHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

// Review handles POST /flashcard-review?id=...
func (h *FlashcardHandler) Review(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var in flashcard.ReviewInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	fc, err := h.svc.RecordReview(r.Context(), uid, id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fc)
}

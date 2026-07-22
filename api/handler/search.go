package handler

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/pkg/search"
)

// SearchHandler exposes search endpoints.
type SearchHandler struct {
	svc *search.Service // svc owns the search queries
}

// NewSearchHandler constructs a SearchHandler.
func NewSearchHandler(svc *search.Service) *SearchHandler {
	return &SearchHandler{svc: svc}
}

// SubjectsOwned handles GET /search/subjects/owned?q=...&limit=...&offset=...&include_archived=...
func (h *SearchHandler) SubjectsOwned(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	q := r.URL.Query().Get("q")
	limit := httpx.QueryIntDefault(r, "limit", 20)
	offset := httpx.QueryIntDefault(r, "offset", 0)
	includeArchived := httpx.QueryBoolDefault(r, "include_archived", false)
	hits, err := h.svc.OwnedSubjects(r.Context(), uid, q, includeArchived, limit, offset)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hits)
}

// SubjectsPublic handles GET /search/subjects/public?q=...&limit=...&offset=...
func (h *SearchHandler) SubjectsPublic(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	q := r.URL.Query().Get("q")
	limit := httpx.QueryIntDefault(r, "limit", 20)
	offset := httpx.QueryIntDefault(r, "offset", 0)
	hits, err := h.svc.PublicSubjects(r.Context(), uid, q, limit, offset)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hits)
}

// Users handles GET /search/users?q=...&limit=...&offset=...
func (h *SearchHandler) Users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := httpx.QueryIntDefault(r, "limit", 20)
	offset := httpx.QueryIntDefault(r, "offset", 0)
	hits, err := h.svc.Users(r.Context(), q, limit, offset)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hits)
}

// Flashcards handles GET /search/flashcards?q=...&limit=...&offset=...&include_archived=...
func (h *SearchHandler) Flashcards(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	q := r.URL.Query().Get("q")
	limit := httpx.QueryIntDefault(r, "limit", 20)
	offset := httpx.QueryIntDefault(r, "offset", 0)
	includeArchived := httpx.QueryBoolDefault(r, "include_archived", false)
	hits, err := h.svc.Flashcards(r.Context(), uid, q, includeArchived, limit, offset)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hits)
}

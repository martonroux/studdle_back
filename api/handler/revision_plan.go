package handler

import (
	"encoding/json"
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/plan"
)

// RevisionPlanHandler exposes plan generation, read-back, and progress.
type RevisionPlanHandler struct {
	svc *plan.Service // svc is the plan domain service
}

// NewRevisionPlanHandler constructs the handler.
func NewRevisionPlanHandler(svc *plan.Service) *RevisionPlanHandler {
	return &RevisionPlanHandler{svc: svc}
}

// Generate handles POST /exams/{id}/generate-plan and streams phase events as SSE.
func (h *RevisionPlanHandler) Generate(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := pathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	events, err := h.svc.GenerateForExam(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	streamPlanEvents(w, events)
}

// Get handles GET /exams/{id}/plan.
func (h *RevisionPlanHandler) Get(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := pathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	view, err := h.svc.GetPlan(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

// markDoneBody is the POST /exams/{id}/mark-done request shape.
type markDoneBody struct {
	FlashcardID int64 `json:"flashcardId"` // FlashcardID is the FC the user just completed in today's plan
}

// MarkDone handles POST /exams/{id}/mark-done.
func (h *RevisionPlanHandler) MarkDone(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := pathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var body markDoneBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	if body.FlashcardID <= 0 {
		httpx.WriteError(w, &myErrors.AppError{Code: "validation", Message: "flashcardId is required", Wrapped: myErrors.ErrValidation, Field: "flashcardId"})
		return
	}
	if err := h.svc.MarkDone(r.Context(), uid, id, body.FlashcardID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// streamPlanEvents writes SSE events from the orchestrator's Event channel.
// Each Phase becomes an SSE event named after the phase string.
func streamPlanEvents(w http.ResponseWriter, events <-chan plan.Event) {
	setSSEHeaders(w)
	flusher, _ := w.(http.Flusher)
	for ev := range events {
		writeSSE(w, flusher, string(ev.Phase), ev)
	}
}

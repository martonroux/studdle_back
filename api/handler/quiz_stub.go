package handler

import (
	"net/http"

	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/quiz"
)

// QuizHandler stubs Spec D endpoints.
type QuizHandler struct {
	svc *quiz.Service // svc is the (stub) quiz service
}

// NewQuizHandler constructs a QuizHandler.
func NewQuizHandler(svc *quiz.Service) *QuizHandler { return &QuizHandler{svc: svc} }

// Generate stubs POST /quiz/generate.
func (h *QuizHandler) Generate(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Attempt stubs POST /quiz/attempt?id=...
func (h *QuizHandler) Attempt(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Share stubs POST /quiz/share?id=...
func (h *QuizHandler) Share(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

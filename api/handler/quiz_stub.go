package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/quiz"
)

// QuizHandler exposes the Spec D quiz endpoints.
type QuizHandler struct {
	svc    *quiz.Service   // svc owns all quiz domain operations
	access *access.Service // access answers the AI-entitlement gate
}

// NewQuizHandler constructs a QuizHandler.
func NewQuizHandler(svc *quiz.Service, acc *access.Service) *QuizHandler {
	return &QuizHandler{svc: svc, access: acc}
}

// requireAIAccess is the entitlement check shared by generation endpoints.
// Plan D3 will extend this with the quizDemoUsed demo-path bypass.
func (h *QuizHandler) requireAIAccess(ctx context.Context, uid int64) error {
	ok, err := h.access.HasAIAccess(ctx, uid)
	if err != nil {
		return err
	}
	if !ok {
		return myErrors.ErrNoAIAccess
	}
	return nil
}

// quizIDFromPath parses the {id} path value from the request.
// Handlers registered with stdlib mux's `/quizzes/{id}/...` shape pull it via r.PathValue.
func quizIDFromPath(r *http.Request) (int64, error) {
	raw := r.PathValue("id")
	if raw == "" {
		return 0, myErrors.ErrInvalidInput
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
	}
	return id, nil
}

// attemptIDFromPath parses the {aid} path value from the request.
func attemptIDFromPath(r *http.Request) (int64, error) {
	raw := r.PathValue("aid")
	if raw == "" {
		return 0, myErrors.ErrInvalidInput
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
	}
	return id, nil
}

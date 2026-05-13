package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
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

// generateRequest is the JSON shape for POST /quizzes/generate.
type generateRequest struct {
	SubjectID  int64    `json:"subjectId"`
	ChapterID  *int64   `json:"chapterId,omitempty"`
	Kind       string   `json:"kind"`
	Size       int      `json:"size"`
	Types      []string `json:"types"`
	CardFilter string   `json:"cardFilter,omitempty"`
	// PlanContext is added in Plan D2.
}

// Generate handles POST /quizzes/generate.
func (h *QuizHandler) Generate(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	if err := h.requireAIAccess(r.Context(), uid); err != nil {
		httpx.WriteError(w, err)
		return
	}

	var body generateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err))
		return
	}

	types := make([]quiz.QuestionType, 0, len(body.Types))
	for _, t := range body.Types {
		types = append(types, quiz.QuestionType(t))
	}
	req := quiz.GenerateRequest{
		UserID:     uid,
		SubjectID:  body.SubjectID,
		ChapterID:  body.ChapterID,
		Kind:       quiz.Kind(body.Kind),
		Size:       body.Size,
		Types:      types,
		CardFilter: quiz.CardFilter(body.CardFilter),
	}
	res, err := h.svc.Generate(r.Context(), req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"quizId":        res.QuizID,
		"questionCount": res.QuestionCount,
		"kind":          res.Kind,
	})
}

// Start handles POST /quizzes/{id}/start.
// Returns the existing in-progress attempt (idempotent) or creates a new one.
func (h *QuizHandler) Start(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	qid, err := quizIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	att, next, prog, err := h.svc.Start(r.Context(), uid, qid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"attemptId":    att.ID,
		"nextQuestion": next,
		"progress":     prog,
	})
}

// answerRequest is the JSON shape for POST /quizzes/{id}/attempts/{aid}/answer.
type answerRequest struct {
	QuestionID int64           `json:"questionId"`
	Answer     json.RawMessage `json:"answer"`
}

// Answer handles POST /quizzes/{id}/attempts/{aid}/answer.
func (h *QuizHandler) Answer(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	aid, err := attemptIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var body answerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err))
		return
	}
	res, err := h.svc.Answer(r.Context(), uid, aid, body.QuestionID, body.Answer)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// Abandon handles POST /quizzes/{id}/attempts/{aid}/abandon.
// Returns 204 No Content on success.
func (h *QuizHandler) Abandon(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	aid, err := attemptIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Abandon(r.Context(), uid, aid); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Retake handles POST /quizzes/{id}/retake.
// Returns 409 Conflict if an in-progress attempt already exists.
func (h *QuizHandler) Retake(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	qid, err := quizIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	att, err := h.svc.Retake(r.Context(), uid, qid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"attemptId": att.ID})
}

// Resume handles GET /quizzes/{id}/attempts/{aid}/resume.
// Returns the attempt's current state + next-unanswered question + progress.
func (h *QuizHandler) Resume(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	aid, err := attemptIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	att, err := h.svc.LoadAttemptForUser(r.Context(), uid, aid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	next, prog, err := h.svc.AdvanceForUser(r.Context(), uid, aid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"attemptId":    att.ID,
		"state":        att.State,
		"nextQuestion": next,
		"progress":     prog,
	})
}

// GetAttempt handles GET /quizzes/{id}/attempts/{aid}.
// Returns the full review payload (score + per-question outcome).
func (h *QuizHandler) GetAttempt(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	aid, err := attemptIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	view, err := h.svc.GetAttempt(r.Context(), uid, aid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

// History handles GET /quizzes/{id}/history.
// Returns every attempt the user has made on this quiz, newest first.
func (h *QuizHandler) History(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	qid, err := quizIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	atts, err := h.svc.History(r.Context(), uid, qid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"attempts": atts})
}

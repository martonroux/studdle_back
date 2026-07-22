package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/aipipeline"
)

// AIHandler exposes AI pipeline endpoints.
type AIHandler struct {
	svc *aipipeline.Service // svc is the AI pipeline service
}

// NewAIHandler constructs an AIHandler.
func NewAIHandler(svc *aipipeline.Service) *AIHandler {
	return &AIHandler{svc: svc}
}

// promptGenInput is the POST /ai/flashcards/prompt body.
type promptGenInput struct {
	SubjectID    int64  `json:"subject_id"`    // SubjectID is the target subject
	ChapterID    int64  `json:"chapter_id"`    // ChapterID is optional; when set, auto-chapters is suppressed
	Prompt       string `json:"prompt"`        // Prompt is the user's topic description
	Style        string `json:"style"`         // Style is "short" | "standard" | "detailed"
	Coverage     string `json:"coverage"`      // Coverage is "Core" | "Balanced" | "Comprehensive"
	Focus        string `json:"focus"`         // Focus is an optional narrowing phrase
	AutoChapters bool   `json:"auto_chapters"` // AutoChapters proposes chapters when chapter_id is unset
}

// GenerateFromPrompt is the SSE endpoint for prompt-based flashcard generation.
func (h *AIHandler) GenerateFromPrompt(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := decodePromptGen(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	subject, err := h.svc.LookupSubject(r.Context(), uid, in.SubjectID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rendered, err := renderPromptGenPromptExported(in, subject.Name)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	autoChapters := in.AutoChapters && in.ChapterID == 0
	h.runGeneration(r.Context(), w, aipipeline.AIRequest{
		UserID:       uid,
		Feature:      aipipeline.FeatureGenerateFromPrompt,
		SubjectID:    in.SubjectID,
		Prompt:       rendered,
		DropChapters: !autoChapters,
		Metadata: map[string]any{
			"style": in.Style, "coverage": in.Coverage, "focus": in.Focus,
			"auto_chapters": in.AutoChapters, "chapter_id": in.ChapterID,
		},
	})
}

// decodePromptGen parses + validates the prompt-gen body.
func decodePromptGen(r *http.Request) (promptGenInput, error) {
	var in promptGenInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return in, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput}
	}
	if in.SubjectID <= 0 || in.Prompt == "" || len(in.Prompt) > 8000 {
		return in, &myErrors.AppError{Code: "validation", Message: "subject_id + prompt (<=8000 chars) required", Wrapped: myErrors.ErrValidation}
	}
	if in.Style == "" {
		in.Style = "standard"
	}
	if in.Coverage == "" {
		in.Coverage = "Balanced"
	}
	if !isValidCoverage(in.Coverage) {
		return in, &myErrors.AppError{Code: "validation", Message: "coverage must be Core | Balanced | Comprehensive", Wrapped: myErrors.ErrValidation, Field: "coverage"}
	}
	return in, nil
}

// isValidCoverage returns true when c is one of the accepted coverage values.
func isValidCoverage(c string) bool {
	return c == "Core" || c == "Balanced" || c == "Comprehensive"
}

// renderPromptGenPromptExported is the package-external renderer used by the handler.
func renderPromptGenPromptExported(in promptGenInput, subjectName string) (string, error) {
	return aipipeline.RenderPromptGen(aipipeline.PromptGenValues{
		SubjectName:  subjectName,
		Style:        in.Style,
		Coverage:     in.Coverage,
		Focus:        in.Focus,
		Prompt:       in.Prompt,
		AutoChapters: in.AutoChapters && in.ChapterID == 0,
	})
}

// runGeneration invokes the pipeline and writes SSE events per emitted chunk.
// First event is always job; then card / chapter / progress / terminal done / error.
func (h *AIHandler) runGeneration(ctx context.Context, w http.ResponseWriter, req aipipeline.AIRequest) {
	ch, jobID, err := h.svc.RunStructuredGeneration(ctx, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	setSSEHeaders(w)
	flusher, _ := w.(http.Flusher)
	writeSSE(w, flusher, "job", map[string]any{"jobId": jobID})

	for c := range ch {
		forwardChunkToSSE(w, flusher, c)
	}
}

// setSSEHeaders writes the standard SSE content-type / cache headers.
// X-Accel-Buffering disables proxy buffering (nginx and friends).
func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// writeSSE writes one named SSE event with JSON-serialized data.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	if flusher != nil {
		flusher.Flush()
	}
}

// forwardChunkToSSE maps an AIChunk to a named SSE event.
func forwardChunkToSSE(w http.ResponseWriter, flusher http.Flusher, c aipipeline.AIChunk) {
	switch c.Kind {
	case aipipeline.ChunkItem:
		writeSSE(w, flusher, "card", json.RawMessage(c.Item))
	case aipipeline.ChunkChapter:
		writeSSE(w, flusher, "chapter", json.RawMessage(c.Item))
	case aipipeline.ChunkProgress:
		writeSSE(w, flusher, "progress", c.Progress)
	case aipipeline.ChunkDone:
		writeSSE(w, flusher, "done", map[string]any{"ok": true})
	case aipipeline.ChunkError:
		writeSSE(w, flusher, "error", errorPayload(c.Err))
	}
}

// errorPayload renders the JSON body for an SSE error event.
func errorPayload(err error) map[string]any {
	var ae *myErrors.AppError
	if errors.As(err, &ae) {
		return map[string]any{"code": ae.Code, "message": ae.Message}
	}
	return map[string]any{"code": "internal", "message": err.Error()}
}

// checkInput is the POST /ai/check body.
type checkInput struct {
	FlashcardID   int64  `json:"flashcard_id"`   // FlashcardID is the flashcard to check
	DraftQuestion string `json:"draft_question"` // DraftQuestion overrides the stored question when non-empty
	DraftAnswer   string `json:"draft_answer"`   // DraftAnswer overrides the stored answer when non-empty
}

// Check runs a non-streaming AI check over a flashcard.
func (h *AIHandler) Check(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in checkInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	if in.FlashcardID <= 0 {
		httpx.WriteError(w, &myErrors.AppError{Code: "validation", Message: "flashcard_id required", Wrapped: myErrors.ErrValidation, Field: "flashcard_id"})
		return
	}
	out, err := h.svc.CheckFlashcard(r.Context(), aipipeline.CheckInput{
		UserID:        uid,
		FlashcardID:   in.FlashcardID,
		DraftQuestion: in.DraftQuestion,
		DraftAnswer:   in.DraftAnswer,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// Quota returns the authenticated user's current AI quota snapshot.
func (h *AIHandler) Quota(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	snap, err := h.svc.QuotaSnapshot(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, snap)
}

// commitInput is the POST /ai/commit-generation request body.
type commitInput struct {
	JobID     int64             `json:"job_id"`
	SubjectID int64             `json:"subject_id"`
	Chapters  []commitChapterIn `json:"chapters"`
	Cards     []commitCardIn    `json:"cards"`
}

type commitChapterIn struct {
	ClientID string `json:"clientId"`
	Title    string `json:"title"`
}

type commitCardIn struct {
	ChapterClientID string `json:"chapterClientId"`
	Title           string `json:"title"`
	Question        string `json:"question"`
	Answer          string `json:"answer"`
}

type commitOutput struct {
	SubjectID  int64            `json:"subjectId"`
	ChapterIDs map[string]int64 `json:"chapterIds"`
	CardIDs    []int64          `json:"cardIds"`
}

// CommitGeneration writes the user-edited AI draft atomically.
func (h *AIHandler) CommitGeneration(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := decodeCommit(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out, err := h.svc.CommitGeneration(r.Context(), aipipeline.CommitInput{
		UserID:    uid,
		SubjectID: in.SubjectID,
		Chapters:  convertCommitChapters(in.Chapters),
		Cards:     convertCommitCards(in.Cards),
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, commitOutput{
		SubjectID:  out.SubjectID,
		ChapterIDs: out.ChapterIDs,
		CardIDs:    out.CardIDs,
	})
}

// decodeCommit parses and validates the commit body.
func decodeCommit(r *http.Request) (commitInput, error) {
	var in commitInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return in, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput}
	}
	if in.SubjectID <= 0 || len(in.Cards) == 0 {
		return in, &myErrors.AppError{Code: "validation", Message: "subject_id and at least one card required", Wrapped: myErrors.ErrValidation}
	}
	return in, nil
}

// convertCommitChapters maps the JSON request shape to the service input shape.
func convertCommitChapters(in []commitChapterIn) []aipipeline.CommitChapter {
	out := make([]aipipeline.CommitChapter, len(in))
	for i, c := range in {
		out[i] = aipipeline.CommitChapter{ClientID: c.ClientID, Title: c.Title}
	}
	return out
}

// convertCommitCards maps the JSON request shape to the service input shape.
func convertCommitCards(in []commitCardIn) []aipipeline.CommitCard {
	out := make([]aipipeline.CommitCard, len(in))
	for i, c := range in {
		out[i] = aipipeline.CommitCard{
			ChapterClientID: c.ChapterClientID,
			Title:           c.Title,
			Question:        c.Question,
			Answer:          c.Answer,
		}
	}
	return out
}

package quiz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
)

// allowedSizes is the v1 white-list for quiz size (Spec D §4 Setup).
var allowedSizes = map[int]bool{5: true, 10: true, 15: true, 20: true}

// GenerateProgressKind tags a streamed GenerateProgress event.
type GenerateProgressKind string

const (
	// GenerateProgressItem marks one validated question; Index counts questions
	// validated so far (1-based) out of the requested Size.
	GenerateProgressItem GenerateProgressKind = "item"
	// GenerateProgressDone marks successful completion; Result is set.
	GenerateProgressDone GenerateProgressKind = "done"
	// GenerateProgressError marks a terminal failure; Err is set.
	GenerateProgressError GenerateProgressKind = "error"
)

// GenerateProgress is one streamed event from GenerateStream.
type GenerateProgress struct {
	Kind   GenerateProgressKind
	Index  int             // 1-based count of validated questions so far (Kind == item)
	Size   int             // requested quiz size (Kind == item)
	Result *GenerateResult // set when Kind == done
	Err    error           // set when Kind == error
}

// Generate produces a quiz from a flashcard pool + AI call + persistence.
// Returns GenerateResult with the new quiz id and validated question count.
func (s *Service) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	return s.generate(ctx, req, nil)
}

// GenerateStream behaves like Generate but also emits per-question progress on
// the returned channel as the AI stream validates each item — the same
// AIChunk{Kind: ChunkItem} signal flashcard generation already streams to
// clients (see STU-44). The channel receives exactly one terminal
// GenerateProgressDone or GenerateProgressError event and is then closed.
func (s *Service) GenerateStream(ctx context.Context, req GenerateRequest) <-chan GenerateProgress {
	out := make(chan GenerateProgress, 16)
	go func() {
		defer close(out)
		res, err := s.generate(ctx, req, out)
		if err != nil {
			emitProgress(ctx, out, GenerateProgress{Kind: GenerateProgressError, Err: err})
			return
		}
		emitProgress(ctx, out, GenerateProgress{Kind: GenerateProgressDone, Result: &res})
	}()
	return out
}

// generate is the shared implementation behind Generate and GenerateStream.
// progress may be nil; when set, one GenerateProgressItem event is sent per
// validated question as the AI stream is drained.
func (s *Service) generate(ctx context.Context, req GenerateRequest, progress chan<- GenerateProgress) (GenerateResult, error) {
	if err := validateRequest(req); err != nil {
		return GenerateResult{}, err
	}
	cards, ids, err := s.resolveCardPool(ctx, req)
	if err != nil {
		return GenerateResult{}, err
	}
	if req.Kind == KindSpecific && len(cards) == 0 {
		return GenerateResult{}, myErrors.ErrEmptyCardPool
	}
	subjectName, err := s.lookupSubjectName(ctx, req.SubjectID, req.UserID)
	if err != nil {
		return GenerateResult{}, err
	}
	body, err := aipipeline.RenderGenerateQuiz(aipipeline.QuizGenValues{
		SubjectName: subjectName,
		Kind:        string(req.Kind),
		Size:        req.Size,
		Types:       typeStrings(req.Types),
		Cards:       cards,
	})
	if err != nil {
		return GenerateResult{}, fmt.Errorf("render prompt:\n%w", err)
	}
	out, err := s.ai.GenerateQuiz(ctx, aipipeline.QuizGenerateInput{
		UserID: req.UserID, SubjectID: req.SubjectID, Prompt: body,
		Metadata: map[string]any{
			"kind": string(req.Kind), "size": req.Size, "types": typeStrings(req.Types),
		},
	})
	if err != nil {
		return GenerateResult{}, err
	}
	questions, err := drainQuestions(ctx, out.Chunks, req.Size, progress)
	if err != nil {
		return GenerateResult{}, err
	}
	settings, _ := json.Marshal(map[string]any{
		"size": req.Size, "types": typeStrings(req.Types),
	})
	quizID, err := s.persistQuiz(ctx, PersistInput{
		UserID:     req.UserID,
		SubjectID:  req.SubjectID,
		ChapterID:  req.ChapterID,
		Kind:       req.Kind,
		Source:     SourceUser, // Plan D2 will set SourcePlan when PlanContext != nil
		CardPool:   ids,
		Settings:   settings,
		Model:      "claude-test",
		PromptHash: hashPrompt(body),
		Questions:  questions,
	})
	if err != nil {
		return GenerateResult{}, err
	}
	return GenerateResult{QuizID: quizID, QuestionCount: len(questions), Kind: req.Kind}, nil
}

// emitProgress sends a terminal progress event, aborting instead of blocking
// forever if ctx is canceled and nobody is left reading (e.g. client gone).
func emitProgress(ctx context.Context, out chan<- GenerateProgress, p GenerateProgress) {
	select {
	case out <- p:
	case <-ctx.Done():
	}
}

// validateRequest returns ErrValidation if the request is malformed.
func validateRequest(req GenerateRequest) error {
	if req.Kind != KindSpecific && req.Kind != KindGlobal {
		return fmt.Errorf("%w: kind=%q", myErrors.ErrValidation, req.Kind)
	}
	if !allowedSizes[req.Size] {
		return fmt.Errorf("%w: size=%d (allowed: 5,10,15,20)", myErrors.ErrValidation, req.Size)
	}
	if len(req.Types) == 0 {
		return fmt.Errorf("%w: types must be non-empty", myErrors.ErrValidation)
	}
	for _, t := range req.Types {
		if t != QTypeMultiChoice && t != QTypeTrueFalse && t != QTypeFillBlank {
			return fmt.Errorf("%w: type=%q", myErrors.ErrValidation, t)
		}
	}
	return nil
}

// typeStrings converts a slice of typed QuestionType to plain strings for prompt rendering.
func typeStrings(ts []QuestionType) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, string(t))
	}
	return out
}

// hashPrompt returns a short hex digest of the rendered prompt for audit trails.
func hashPrompt(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:8])
}

// lookupSubjectName fetches the subject's display name, scoped to the owning user.
// Returns ErrNotFound (via the wrapped pgx error) if the row is missing or not owned.
func (s *Service) lookupSubjectName(ctx context.Context, sid, uid int64) (string, error) {
	var name string
	err := s.db.QueryRow(ctx,
		`SELECT name FROM subjects WHERE id = $1 AND owner_id = $2`, sid, uid,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("lookup subject:\n%w", err)
	}
	return name, nil
}

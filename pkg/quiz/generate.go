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

// Generate produces a quiz from a flashcard pool + AI call + persistence.
// Returns GenerateResult with the new quiz id and validated question count.
func (s *Service) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if err := validateRequest(req); err != nil {
		return GenerateResult{}, err
	}
	cards, ids, err := s.resolveCardPool(ctx, req)
	if err != nil {
		return GenerateResult{}, err
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
	questions, err := drainQuestions(out.Chunks, req.Size)
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

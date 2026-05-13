package quiz

import (
	"context"
	"encoding/json"
	"fmt"
)

// RawQuestion is the AI-emitted question shape after validation, ready for insert.
type RawQuestion struct {
	Type            QuestionType    // Type selects the question shape
	Stem            string          // Stem is the user-facing question body
	Options         json.RawMessage // Options is the MCQ option array (nil for non-MCQ)
	Correct         json.RawMessage // Correct payload: {"index":N} | {"value":bool} | {"accepted":[...]}
	Explanation     string          // Explanation is the optional rationale (empty = NULL)
	ReferencedFcIDs []int64         // ReferencedFcIDs is the snapshot of cards this question draws on
}

// PersistInput is the input to persistQuiz.
type PersistInput struct {
	UserID           int64           // UserID owns the quiz
	SubjectID        int64           // SubjectID anchors the quiz
	ChapterID        *int64          // ChapterID is nil for whole-subject quizzes
	Kind             Kind            // Kind is "specific" or "global"
	Source           Source          // Source is the origin (user/plan/shared_copy)
	SourcePlanID     *int64          // SourcePlanID is set for plan-sourced quizzes (Spec D2)
	SourceShareToken *string         // SourceShareToken is set for shared_copy quizzes (Spec D3)
	CardPool         []int64         // CardPool snapshots the candidate flashcard ids at generation
	Settings         json.RawMessage // Settings encodes size/types as a JSONB blob
	Model            string          // Model is the AI model identifier
	PromptHash       string          // PromptHash captures the prompt revision for debugging
	Questions        []RawQuestion   // Questions are the validated AI-emitted questions
}

// persistQuiz writes one quizzes row + N quiz_questions rows in one transaction.
// Returns the new quiz id.
func (s *Service) persistQuiz(ctx context.Context, in PersistInput) (int64, error) {
	cardPoolJSON, err := json.Marshal(in.CardPool)
	if err != nil {
		return 0, fmt.Errorf("marshal card pool:\n%w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var quizID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO quizzes (
			user_id, subject_id, chapter_id, kind, source,
			source_plan_id, source_share_token,
			card_pool_jsonb, settings_jsonb,
			question_count, model, prompt_hash
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id`,
		in.UserID, in.SubjectID, in.ChapterID, string(in.Kind), string(in.Source),
		in.SourcePlanID, in.SourceShareToken,
		cardPoolJSON, in.Settings,
		len(in.Questions), in.Model, in.PromptHash,
	).Scan(&quizID)
	if err != nil {
		return 0, fmt.Errorf("insert quiz:\n%w", err)
	}

	for i, q := range in.Questions {
		fcIDs, err := json.Marshal(q.ReferencedFcIDs)
		if err != nil {
			return 0, fmt.Errorf("marshal fc ids:\n%w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO quiz_questions (
				quiz_id, ordinal, question_type, stem,
				options_jsonb, correct_jsonb, explanation, referenced_fc_ids_jsonb
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			quizID, i+1, string(q.Type), q.Stem,
			q.Options, q.Correct, nullableString(q.Explanation), fcIDs,
		)
		if err != nil {
			return 0, fmt.Errorf("insert question %d:\n%w", i+1, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit tx:\n%w", err)
	}
	return quizID, nil
}

// nullableString returns nil when s is empty (so the column receives NULL),
// otherwise the string itself. Used for optional TEXT columns.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// PersistQuizForTest exposes persistQuiz to tests.
func (s *Service) PersistQuizForTest(ctx context.Context, in PersistInput) (int64, error) {
	return s.persistQuiz(ctx, in)
}

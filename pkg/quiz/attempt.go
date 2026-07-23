package quiz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"studbud/backend/internal/myErrors"
)

// Start returns the user's in-progress attempt for the quiz, creating one if none exists.
// Returns (attempt, nextQuestion, progress).
func (s *Service) Start(ctx context.Context, uid, quizID int64) (Attempt, *PublicQuestion, Progress, error) {
	if err := s.requireQuizOwner(ctx, uid, quizID); err != nil {
		return Attempt{}, nil, Progress{}, err
	}
	att, err := s.findInProgress(ctx, uid, quizID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, nil, Progress{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		att, err = s.createAttempt(ctx, uid, quizID)
		if err != nil {
			return Attempt{}, nil, Progress{}, err
		}
	}
	next, prog, err := s.advance(ctx, att.ID)
	if err != nil {
		return Attempt{}, nil, Progress{}, err
	}
	return att, next, prog, nil
}

// requireQuizOwner returns ErrForbidden if quizID is not owned by uid; ErrNotFound if missing.
func (s *Service) requireQuizOwner(ctx context.Context, uid, quizID int64) error {
	var owner int64
	err := s.db.QueryRow(ctx, `SELECT user_id FROM quizzes WHERE id=$1`, quizID).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return myErrors.ErrNotFound
		}
		return fmt.Errorf("lookup quiz owner:\n%w", err)
	}
	if owner != uid {
		return myErrors.ErrForbidden
	}
	return nil
}

// findInProgress fetches the user's currently-open attempt on quizID, if any.
func (s *Service) findInProgress(ctx context.Context, uid, quizID int64) (Attempt, error) {
	var att Attempt
	err := s.db.QueryRow(ctx, `
		SELECT id, quiz_id, user_id, state, started_at, completed_at,
		       correct_count, total_count, score_pct, plan_id, plan_date
		  FROM quiz_attempts
		 WHERE quiz_id=$1 AND user_id=$2 AND state='in_progress'`,
		quizID, uid,
	).Scan(&att.ID, &att.QuizID, &att.UserID, &att.State, &att.StartedAt, &att.CompletedAt,
		&att.CorrectCount, &att.TotalCount, &att.ScorePct, &att.PlanID, &att.PlanDate)
	return att, err
}

// createAttempt inserts a new in-progress attempt; total_count mirrors quizzes.question_count.
func (s *Service) createAttempt(ctx context.Context, uid, quizID int64) (Attempt, error) {
	var total int
	err := s.db.QueryRow(ctx, `SELECT question_count FROM quizzes WHERE id=$1`, quizID).Scan(&total)
	if err != nil {
		return Attempt{}, fmt.Errorf("lookup question_count:\n%w", err)
	}
	var att Attempt
	err = s.db.QueryRow(ctx, `
		INSERT INTO quiz_attempts (quiz_id, user_id, state, total_count)
		VALUES ($1,$2,'in_progress',$3)
		RETURNING id, quiz_id, user_id, state, started_at, completed_at,
		          correct_count, total_count, score_pct, plan_id, plan_date`,
		quizID, uid, total,
	).Scan(&att.ID, &att.QuizID, &att.UserID, &att.State, &att.StartedAt, &att.CompletedAt,
		&att.CorrectCount, &att.TotalCount, &att.ScorePct, &att.PlanID, &att.PlanDate)
	if err != nil {
		return Attempt{}, fmt.Errorf("insert attempt:\n%w", err)
	}
	return att, nil
}

// advance returns the next unanswered question + the current progress pill.
// Returns (nil, progress, nil) when every question is answered.
func (s *Service) advance(ctx context.Context, attemptID int64) (*PublicQuestion, Progress, error) {
	var prog Progress
	err := s.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM quiz_attempt_answers WHERE attempt_id=$1),
		       (SELECT total_count FROM quiz_attempts WHERE id=$1)`,
		attemptID,
	).Scan(&prog.Answered, &prog.Total)
	if err != nil {
		return nil, prog, fmt.Errorf("progress:\n%w", err)
	}
	if prog.Answered >= prog.Total {
		return nil, prog, nil
	}
	var q PublicQuestion
	var opts []byte
	err = s.db.QueryRow(ctx, `
		SELECT qq.id, qq.ordinal, qq.question_type, qq.stem, qq.options_jsonb
		  FROM quiz_questions qq
		  JOIN quiz_attempts qa ON qa.quiz_id = qq.quiz_id
		 WHERE qa.id = $1
		   AND qq.id NOT IN (SELECT question_id FROM quiz_attempt_answers WHERE attempt_id=$1)
		 ORDER BY qq.ordinal
		 LIMIT 1`, attemptID,
	).Scan(&q.ID, &q.Ordinal, &q.Type, &q.Stem, &opts)
	if err != nil {
		return nil, prog, fmt.Errorf("next question:\n%w", err)
	}
	if opts != nil {
		q.Options = json.RawMessage(opts)
	}
	return &q, prog, nil
}

// AnswerResult is the response payload for /answer.
type AnswerResult struct {
	Correct       bool            `json:"correct"`
	CorrectAnswer json.RawMessage `json:"correctAnswer"`
	Explanation   string          `json:"explanation,omitempty"`
	Next          *PublicQuestion `json:"nextQuestion,omitempty"`
}

// Answer scores the user's submission and advances the attempt.
// Persistence is idempotent on (attempt_id, question_id): repeated submits
// do not insert a duplicate row and do not change persisted state
// (the first submission's answer remains the canonical record). The
// returned AnswerResult reflects the *current* call's score relative to
// the correct payload; on a re-submit this may differ from the persisted
// answer row.
func (s *Service) Answer(ctx context.Context, uid, attemptID, questionID int64, userAns json.RawMessage) (AnswerResult, error) {
	att, q, err := s.loadAnswerContext(ctx, uid, attemptID, questionID)
	if err != nil {
		return AnswerResult{}, err
	}
	correct, err := scoreAnswer(q.Type, q.CorrectJSON, userAns)
	if err != nil {
		return AnswerResult{}, err
	}
	if err := s.commitAnswer(ctx, att, questionID, userAns, correct); err != nil {
		return AnswerResult{}, err
	}
	next, _, err := s.advance(ctx, attemptID)
	if err != nil {
		return AnswerResult{}, err
	}
	return AnswerResult{
		Correct:       correct,
		CorrectAnswer: publicCorrectAnswer(q.Type, q.CorrectJSON),
		Explanation:   q.Explanation,
		Next:          next,
	}, nil
}

// loadAnswerContext validates ownership + state and loads the question.
func (s *Service) loadAnswerContext(ctx context.Context, uid, attemptID, questionID int64) (Attempt, Question, error) {
	att, err := s.loadAttempt(ctx, attemptID)
	if err != nil {
		return Attempt{}, Question{}, err
	}
	if att.UserID != uid {
		return Attempt{}, Question{}, myErrors.ErrForbidden
	}
	if att.State != StateInProgress {
		return Attempt{}, Question{}, fmt.Errorf("%w: attempt not in_progress", myErrors.ErrConflict)
	}
	q, err := s.loadQuestion(ctx, questionID, att.QuizID)
	if err != nil {
		return Attempt{}, Question{}, err
	}
	return att, q, nil
}

// commitAnswer runs the persistence tx: insert answer, bump correct_count,
// flip to completed on the final answer. Idempotent on (attempt_id, question_id).
func (s *Service) commitAnswer(ctx context.Context, att Attempt, questionID int64, userAns json.RawMessage, correct bool) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		INSERT INTO quiz_attempt_answers (attempt_id, question_id, user_answer_jsonb, correct)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (attempt_id, question_id) DO NOTHING`,
		att.ID, questionID, userAns, correct)
	if err != nil {
		return fmt.Errorf("insert answer:\n%w", err)
	}
	inserted := tag.RowsAffected() > 0
	if inserted && correct {
		if _, err := tx.Exec(ctx,
			`UPDATE quiz_attempts SET correct_count = correct_count + 1 WHERE id=$1`,
			att.ID); err != nil {
			return fmt.Errorf("bump correct_count:\n%w", err)
		}
	}
	var answered int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM quiz_attempt_answers WHERE attempt_id=$1`, att.ID,
	).Scan(&answered); err != nil {
		return err
	}
	if answered >= att.TotalCount {
		if err := s.completeAttempt(ctx, tx, att.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// loadAttempt fetches a quiz_attempts row by id; returns ErrNotFound if missing.
func (s *Service) loadAttempt(ctx context.Context, id int64) (Attempt, error) {
	var att Attempt
	err := s.db.QueryRow(ctx, `
		SELECT id, quiz_id, user_id, state, started_at, completed_at,
		       correct_count, total_count, score_pct, plan_id, plan_date
		  FROM quiz_attempts WHERE id=$1`, id,
	).Scan(&att.ID, &att.QuizID, &att.UserID, &att.State, &att.StartedAt, &att.CompletedAt,
		&att.CorrectCount, &att.TotalCount, &att.ScorePct, &att.PlanID, &att.PlanDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return att, myErrors.ErrNotFound
	}
	if err != nil {
		return att, fmt.Errorf("load attempt:\n%w", err)
	}
	return att, nil
}

// loadQuestion fetches a quiz_questions row scoped to quizID, with the server-only
// correct_jsonb payload. Returns ErrNotFound if missing or not in that quiz.
func (s *Service) loadQuestion(ctx context.Context, qid, quizID int64) (Question, error) {
	var q Question
	var opts, fcIDs []byte
	err := s.db.QueryRow(ctx, `
		SELECT id, quiz_id, ordinal, question_type, stem,
		       options_jsonb, correct_jsonb, COALESCE(explanation,''), referenced_fc_ids_jsonb
		  FROM quiz_questions WHERE id=$1 AND quiz_id=$2`,
		qid, quizID,
	).Scan(&q.ID, &q.QuizID, &q.Ordinal, &q.Type, &q.Stem, &opts, &q.CorrectJSON, &q.Explanation, &fcIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, myErrors.ErrNotFound
	}
	if err != nil {
		return q, fmt.Errorf("load question:\n%w", err)
	}
	if opts != nil {
		q.Options = json.RawMessage(opts)
	}
	if len(fcIDs) > 0 {
		_ = json.Unmarshal(fcIDs, &q.ReferencedFcIDs)
	}
	return q, nil
}

// Abandon marks an in-progress attempt as abandoned, freeing the partial-UNIQUE
// in_progress slot. No-op when the attempt is already completed.
func (s *Service) Abandon(ctx context.Context, uid, attemptID int64) error {
	att, err := s.loadAttempt(ctx, attemptID)
	if err != nil {
		return err
	}
	if att.UserID != uid {
		return myErrors.ErrForbidden
	}
	if att.State == StateCompleted {
		return nil
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE quiz_attempts SET state='abandoned' WHERE id=$1 AND state='in_progress'`,
		attemptID); err != nil {
		return fmt.Errorf("abandon:\n%w", err)
	}
	return nil
}

// Retake creates a fresh attempt over the same questions. Returns ErrConflict (409)
// if the user already has an in-progress attempt on this quiz.
func (s *Service) Retake(ctx context.Context, uid, quizID int64) (Attempt, error) {
	if err := s.requireQuizOwner(ctx, uid, quizID); err != nil {
		return Attempt{}, err
	}
	_, err := s.findInProgress(ctx, uid, quizID)
	if err == nil {
		return Attempt{}, fmt.Errorf("%w: in-progress attempt exists", myErrors.ErrConflict)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, err
	}
	return s.createAttempt(ctx, uid, quizID)
}

// LoadAttemptForUser is the public projection of loadAttempt with an ownership check.
// Returns ErrForbidden when the attempt is owned by another user.
func (s *Service) LoadAttemptForUser(ctx context.Context, uid, attemptID int64) (Attempt, error) {
	att, err := s.loadAttempt(ctx, attemptID)
	if err != nil {
		return att, err
	}
	if att.UserID != uid {
		return att, myErrors.ErrForbidden
	}
	return att, nil
}

// AdvanceForUser returns the next-question + progress payload after an ownership check.
func (s *Service) AdvanceForUser(ctx context.Context, uid, attemptID int64) (*PublicQuestion, Progress, error) {
	if _, err := s.LoadAttemptForUser(ctx, uid, attemptID); err != nil {
		return nil, Progress{}, err
	}
	return s.advance(ctx, attemptID)
}

// completeAttempt marks the attempt as completed and computes score_pct.
// Plan D2 will extend this to write revision_plan_progress.
func (s *Service) completeAttempt(ctx context.Context, tx pgx.Tx, attemptID int64) error {
	if _, err := tx.Exec(ctx, `
		UPDATE quiz_attempts
		   SET state='completed',
		       completed_at = now(),
		       score_pct = CASE WHEN total_count > 0
		                        THEN (correct_count * 100) / total_count
		                        ELSE 0 END
		 WHERE id=$1`, attemptID); err != nil {
		return fmt.Errorf("complete attempt:\n%w", err)
	}
	return nil
}

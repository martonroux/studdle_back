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

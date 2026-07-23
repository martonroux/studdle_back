package quiz

import (
	"context"
	"encoding/json"
	"fmt"

	"studbud/backend/internal/myErrors"
)

// QuestionReview is one row in the results-screen payload.
// UserAnswer is null when the user didn't answer that question.
type QuestionReview struct {
	ID            int64           `json:"id"`
	Ordinal       int             `json:"ordinal"`
	Type          QuestionType    `json:"type"`
	Stem          string          `json:"stem"`
	Options       json.RawMessage `json:"options,omitempty"`
	UserAnswer    json.RawMessage `json:"userAnswer,omitempty"`
	CorrectAnswer json.RawMessage `json:"correctAnswer"`
	Explanation   string          `json:"explanation,omitempty"`
	Correct       *bool           `json:"correct,omitempty"`
}

// AttemptView is the GET /quizzes/:id/attempts/:aid response.
type AttemptView struct {
	Attempt   Attempt          `json:"attempt"`
	Questions []QuestionReview `json:"questions"`
}

// GetAttempt returns the full review payload (score, per-question outcome).
// Caller must own the attempt; else returns ErrForbidden.
func (s *Service) GetAttempt(ctx context.Context, uid, attemptID int64) (AttemptView, error) {
	att, err := s.loadAttempt(ctx, attemptID)
	if err != nil {
		return AttemptView{}, err
	}
	if att.UserID != uid {
		return AttemptView{}, myErrors.ErrForbidden
	}
	rows, err := s.db.Query(ctx, `
		SELECT qq.id, qq.ordinal, qq.question_type, qq.stem,
		       qq.options_jsonb, qq.correct_jsonb, COALESCE(qq.explanation,''),
		       qaa.user_answer_jsonb, qaa.correct
		  FROM quiz_questions qq
		  LEFT JOIN quiz_attempt_answers qaa
		    ON qaa.question_id = qq.id AND qaa.attempt_id = $1
		 WHERE qq.quiz_id = $2
		 ORDER BY qq.ordinal`,
		attemptID, att.QuizID,
	)
	if err != nil {
		return AttemptView{}, fmt.Errorf("review query:\n%w", err)
	}
	defer rows.Close()
	var qs []QuestionReview
	for rows.Next() {
		var r QuestionReview
		var opts, ans, correctRaw []byte
		var correct *bool
		if err := rows.Scan(&r.ID, &r.Ordinal, &r.Type, &r.Stem,
			&opts, &correctRaw, &r.Explanation, &ans, &correct); err != nil {
			return AttemptView{}, fmt.Errorf("scan review row:\n%w", err)
		}
		r.CorrectAnswer = publicCorrectAnswer(r.Type, correctRaw)
		if opts != nil {
			r.Options = json.RawMessage(opts)
		}
		if ans != nil {
			r.UserAnswer = json.RawMessage(ans)
		}
		r.Correct = correct
		qs = append(qs, r)
	}
	if err := rows.Err(); err != nil {
		return AttemptView{}, fmt.Errorf("iterate review rows:\n%w", err)
	}
	return AttemptView{Attempt: att, Questions: qs}, nil
}

// History returns every attempt this user has made on this quiz, newest first.
func (s *Service) History(ctx context.Context, uid, quizID int64) ([]Attempt, error) {
	if err := s.requireQuizOwner(ctx, uid, quizID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, quiz_id, user_id, state, started_at, completed_at,
		       correct_count, total_count, score_pct, plan_id, plan_date
		  FROM quiz_attempts
		 WHERE quiz_id=$1 AND user_id=$2
		 ORDER BY started_at DESC`, quizID, uid)
	if err != nil {
		return nil, fmt.Errorf("history query:\n%w", err)
	}
	defer rows.Close()
	var atts []Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.QuizID, &a.UserID, &a.State, &a.StartedAt, &a.CompletedAt,
			&a.CorrectCount, &a.TotalCount, &a.ScorePct, &a.PlanID, &a.PlanDate); err != nil {
			return nil, fmt.Errorf("scan attempt:\n%w", err)
		}
		atts = append(atts, a)
	}
	return atts, rows.Err()
}

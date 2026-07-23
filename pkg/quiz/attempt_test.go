package quiz_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestStart_CreatesAttempt(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 3) // 3 questions

	svc := quiz.NewService(pool, nil)
	att, next, prog, err := svc.Start(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if att.ID == 0 {
		t.Fatalf("attempt id zero")
	}
	if att.State != quiz.StateInProgress {
		t.Fatalf("state = %q, want in_progress", att.State)
	}
	if next == nil || next.Ordinal != 1 {
		t.Fatalf("next.Ordinal = %v, want 1", next)
	}
	if prog.Answered != 0 || prog.Total != 3 {
		t.Fatalf("progress = %+v, want 0/3", prog)
	}
}

func TestStart_IdempotentReturnsExistingInProgress(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	svc := quiz.NewService(pool, nil)
	a1, _, _, err := svc.Start(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	a2, _, _, err := svc.Start(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if a1.ID != a2.ID {
		t.Fatalf("Start returned different attempts: %d vs %d", a1.ID, a2.ID)
	}
}

func TestAnswer_MCQCorrect(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2) // 2 MCQ questions, both correct_index=2

	svc := quiz.NewService(pool, nil)
	att, q1, _, _ := svc.Start(context.Background(), u.ID, qid)

	res, err := svc.Answer(context.Background(), u.ID, att.ID,
		q1.ID, json.RawMessage(`{"index":2}`))
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !res.Correct {
		t.Fatalf("Correct = false, want true")
	}
	if res.Next == nil || res.Next.Ordinal != 2 {
		t.Fatalf("Next.Ordinal = %v, want 2", res.Next)
	}
}

// STU-47: an incorrect fill_blank answer must come back with a usable
// correctAnswer.value the client can display next to "Correct answer".
func TestAnswer_FillBlankIncorrect_ReturnsDisplayableCorrectAnswer(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 0) // quiz shell only, no MCQ questions

	_, err := pool.Exec(context.Background(), `
		INSERT INTO quiz_questions (quiz_id, ordinal, question_type, stem,
		                            correct_jsonb, referenced_fc_ids_jsonb)
		VALUES ($1, 1, 'fill_blank', 'The powerhouse of the cell is the ____.',
		        '{"accepted":["mitochondrion","mitochondria"]}'::jsonb, '[]'::jsonb)`,
		qid)
	if err != nil {
		t.Fatalf("insert fill_blank question: %v", err)
	}
	_, err = pool.Exec(context.Background(),
		`UPDATE quizzes SET question_count = 1 WHERE id=$1`, qid)
	if err != nil {
		t.Fatalf("set question_count: %v", err)
	}

	svc := quiz.NewService(pool, nil)
	att, q1, _, err := svc.Start(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	res, err := svc.Answer(context.Background(), u.ID, att.ID,
		q1.ID, json.RawMessage(`{"value":"wrong guess"}`))
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Correct {
		t.Fatalf("Correct = true, want false")
	}
	var ca struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(res.CorrectAnswer, &ca); err != nil {
		t.Fatalf("unmarshal correctAnswer %s: %v", res.CorrectAnswer, err)
	}
	if ca.Value != "mitochondrion" {
		t.Fatalf("correctAnswer.value = %q, want %q", ca.Value, "mitochondrion")
	}
}

func TestAnswer_LastQuestion_CompletesAttempt(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 1)

	svc := quiz.NewService(pool, nil)
	att, q1, _, _ := svc.Start(context.Background(), u.ID, qid)

	res, err := svc.Answer(context.Background(), u.ID, att.ID,
		q1.ID, json.RawMessage(`{"index":2}`))
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Next != nil {
		t.Fatalf("Next should be nil on final question")
	}
	var state string
	var pct *int
	_ = pool.QueryRow(context.Background(),
		`SELECT state, score_pct FROM quiz_attempts WHERE id=$1`, att.ID,
	).Scan(&state, &pct)
	if state != "completed" || pct == nil || *pct != 100 {
		t.Fatalf("post-answer: state=%q pct=%v, want completed/100", state, pct)
	}
}

func TestAnswer_DoubleSubmit_NoOp(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	svc := quiz.NewService(pool, nil)
	att, q1, _, _ := svc.Start(context.Background(), u.ID, qid)
	_, _ = svc.Answer(context.Background(), u.ID, att.ID, q1.ID,
		json.RawMessage(`{"index":2}`))
	_, err := svc.Answer(context.Background(), u.ID, att.ID, q1.ID,
		json.RawMessage(`{"index":0}`))
	// Idempotent per Spec D §5.7: PK (attempt_id, question_id) ON CONFLICT DO NOTHING.
	if err != nil {
		t.Fatalf("double-submit returned error: %v", err)
	}
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM quiz_attempt_answers WHERE attempt_id=$1`, att.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("got %d answer rows, want 1 (idempotent)", n)
	}
}

func TestAbandon_FreesInProgressSlot(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	svc := quiz.NewService(pool, nil)
	a1, _, _, _ := svc.Start(context.Background(), u.ID, qid)
	if err := svc.Abandon(context.Background(), u.ID, a1.ID); err != nil {
		t.Fatalf("abandon: %v", err)
	}

	// A retake (= new attempt) should now succeed.
	a2, err := svc.Retake(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("retake after abandon: %v", err)
	}
	if a2.ID == a1.ID {
		t.Fatalf("retake should not reuse abandoned attempt")
	}
}

func TestRetake_BlockedWhileInProgress(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	svc := quiz.NewService(pool, nil)
	_, _, _, _ = svc.Start(context.Background(), u.ID, qid)
	_, err := svc.Retake(context.Background(), u.ID, qid)
	if !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

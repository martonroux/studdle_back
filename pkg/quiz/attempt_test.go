package quiz_test

import (
	"context"
	"testing"

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

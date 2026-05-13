package quiz_test

import (
	"context"
	"encoding/json"
	"testing"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestResults_FullReviewPayload(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2) // both MCQ index=2

	svc := quiz.NewService(pool, nil)
	att, q1, _, _ := svc.Start(context.Background(), u.ID, qid)
	r1, err := svc.Answer(context.Background(), u.ID, att.ID, q1.ID,
		json.RawMessage(`{"index":2}`))
	if err != nil {
		t.Fatalf("answer1: %v", err)
	}
	if r1.Next == nil {
		t.Fatalf("no next question after first answer")
	}
	_, err = svc.Answer(context.Background(), u.ID, att.ID, r1.Next.ID,
		json.RawMessage(`{"index":0}`))
	if err != nil {
		t.Fatalf("answer2: %v", err)
	}

	out, err := svc.GetAttempt(context.Background(), u.ID, att.ID)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if out.Attempt.State != quiz.StateCompleted {
		t.Fatalf("state = %q, want completed", out.Attempt.State)
	}
	if len(out.Questions) != 2 {
		t.Fatalf("got %d Q rows, want 2", len(out.Questions))
	}
	if out.Questions[0].Correct == nil || !*out.Questions[0].Correct {
		t.Fatalf("Q1 should be marked correct")
	}
	if out.Questions[1].Correct == nil || *out.Questions[1].Correct {
		t.Fatalf("Q2 (index=0) should be marked incorrect")
	}
}

func TestHistory_ListsAllAttemptsForQuizByUser(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 1)

	svc := quiz.NewService(pool, nil)
	// First attempt + complete.
	att1, q1, _, _ := svc.Start(context.Background(), u.ID, qid)
	_, _ = svc.Answer(context.Background(), u.ID, att1.ID, q1.ID,
		json.RawMessage(`{"index":2}`))
	// Second attempt via Retake.
	_, _ = svc.Retake(context.Background(), u.ID, qid)

	hist, err := svc.History(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("got %d, want 2", len(hist))
	}
}

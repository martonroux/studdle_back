package quiz_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestGenerate_HappyPath_SpecificMultiChoice(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	c1 := testutil.NewChapter(t, pool, sub.ID, "C1")
	fc1 := testutil.NewFlashcard(t, pool, sub.ID, c1, "What is X?", "Mitochondrion")

	item := func(stem string) string {
		return `{"questionType":"multi_choice","stem":"` + stem + `","options":["A","B","C","D"],"correctIndex":2,"referencedFcIds":[` + itoa(fc1) + `]}`
	}
	fake := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[` +
				item("Q1") + `,` +
				item("Q2") + `,` +
				item("Q3") + `,` +
				item("Q4") + `,` +
				item("Q5") + `]}`},
			{Done: true},
		},
	}
	ai := aipipeline.NewService(pool, fake, access.NewService(pool),
		aipipeline.QuotaLimits{QuizCalls: 5}, "claude-test")
	svc := quiz.NewService(pool, ai)

	res, err := svc.Generate(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sub.ID, Kind: quiz.KindSpecific,
		Size: 5, Types: []quiz.QuestionType{quiz.QTypeMultiChoice},
		CardFilter: quiz.FilterAll,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if res.QuestionCount != 5 {
		t.Fatalf("got %d, want 5", res.QuestionCount)
	}

	// Quota debited
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT quiz_calls FROM ai_quota_daily WHERE user_id=$1 AND day=CURRENT_DATE`, u.ID,
	).Scan(&n)
	if n != 1 {
		t.Fatalf("quiz_calls = %d, want 1", n)
	}
}

func TestGenerate_RejectsInvalidSize(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")

	svc := quiz.NewService(pool, nil)
	_, err := svc.Generate(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sub.ID, Kind: quiz.KindGlobal,
		Size: 99, Types: []quiz.QuestionType{quiz.QTypeMultiChoice},
	})
	if !errors.Is(err, myErrors.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

// itoa is a tiny helper used by the JSON fixture above.
func itoa(i int64) string { return fmt.Sprintf("%d", i) }

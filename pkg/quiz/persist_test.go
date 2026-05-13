package quiz_test

import (
	"context"
	"encoding/json"
	"testing"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestPersistQuiz_WritesQuizAndQuestionsTransactionally(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")

	qs := []quiz.RawQuestion{
		{
			Type:            quiz.QTypeMultiChoice,
			Stem:            "What is X?",
			Options:         json.RawMessage(`["A","B","C","D"]`),
			Correct:         json.RawMessage(`{"index":2}`),
			ReferencedFcIDs: []int64{},
		},
		{
			Type:            quiz.QTypeTrueFalse,
			Stem:            "Earth is round",
			Correct:         json.RawMessage(`{"value":true}`),
			ReferencedFcIDs: []int64{},
		},
	}
	svc := quiz.NewService(pool, nil)
	id, err := svc.PersistQuizForTest(context.Background(), quiz.PersistInput{
		UserID:     u.ID,
		SubjectID:  sub.ID,
		Kind:       quiz.KindGlobal,
		Source:     quiz.SourceUser,
		CardPool:   []int64{},
		Settings:   json.RawMessage(`{"size":2}`),
		Model:      "claude-test",
		PromptHash: "h",
		Questions:  qs,
	})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Assert row counts.
	var qc int
	_ = pool.QueryRow(context.Background(),
		`SELECT question_count FROM quizzes WHERE id=$1`, id).Scan(&qc)
	if qc != 2 {
		t.Fatalf("question_count = %d, want 2", qc)
	}
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM quiz_questions WHERE quiz_id=$1`, id).Scan(&n)
	if n != 2 {
		t.Fatalf("quiz_questions rows = %d, want 2", n)
	}

	// Assert ordinal sequence.
	rows, _ := pool.Query(context.Background(),
		`SELECT ordinal FROM quiz_questions WHERE quiz_id=$1 ORDER BY ordinal`, id)
	defer rows.Close()
	want := []int{1, 2}
	var got []int
	for rows.Next() {
		var o int
		_ = rows.Scan(&o)
		got = append(got, o)
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ordinals = %v, want %v", got, want)
	}
}

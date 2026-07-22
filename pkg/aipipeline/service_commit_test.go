package aipipeline_test

import (
	"context"
	"errors"
	"testing"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
	"studdle/backend/pkg/aipipeline"
	"studdle/backend/testutil"
)

func TestCommitGeneration_InsertsChaptersAndCardsInOneTx(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	svc := aipipeline.NewService(pool, nil, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "test-model")
	in := aipipeline.CommitInput{
		UserID:    u.ID,
		SubjectID: subj.ID,
		Chapters: []aipipeline.CommitChapter{
			{ClientID: "c1", Title: "Intro"},
			{ClientID: "c2", Title: "Advanced"},
		},
		Cards: []aipipeline.CommitCard{
			{ChapterClientID: "c1", Title: "a", Question: "q1", Answer: "ans1"},
			{ChapterClientID: "c2", Title: "b", Question: "q2", Answer: "ans2"},
			{ChapterClientID: "", Title: "loose", Question: "q3", Answer: "ans3"},
		},
	}
	out, err := svc.CommitGeneration(context.Background(), in)
	if err != nil {
		t.Fatalf("CommitGeneration: %v", err)
	}
	if len(out.ChapterIDs) != 2 || len(out.CardIDs) != 3 {
		t.Errorf("counts = (%d,%d), want (2,3)", len(out.ChapterIDs), len(out.CardIDs))
	}
	var chapters, cards int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM chapters WHERE subject_id=$1`, subj.ID).Scan(&chapters)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM flashcards WHERE subject_id=$1 AND source='ai'`, subj.ID).Scan(&cards)
	if chapters != 2 || cards != 3 {
		t.Errorf("DB rows = (%d chapters, %d ai cards), want (2, 3)", chapters, cards)
	}
}

func TestCommitGeneration_RollsBackOnFailure(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	svc := aipipeline.NewService(pool, nil, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "test-model")
	in := aipipeline.CommitInput{
		UserID:    u.ID,
		SubjectID: subj.ID,
		Chapters:  []aipipeline.CommitChapter{{ClientID: "c1", Title: "Intro"}},
		Cards: []aipipeline.CommitCard{
			{ChapterClientID: "nonexistent", Title: "bad", Question: "q", Answer: "a"},
		},
	}
	_, err := svc.CommitGeneration(context.Background(), in)
	if err == nil {
		t.Fatal("expected error (unknown chapterClientId)")
	}
	var chapters, cards int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM chapters WHERE subject_id=$1`, subj.ID).Scan(&chapters)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM flashcards WHERE subject_id=$1`, subj.ID).Scan(&cards)
	if chapters != 0 || cards != 0 {
		t.Errorf("after rollback: rows = (%d, %d), want (0, 0)", chapters, cards)
	}
}

// TestCommitGeneration_UnknownChapterClientIdIsValidationError is a regression
// test for AI-3: an unknown chapterClientId is a client input mistake, not a
// server fault, so it must classify as ErrValidation instead of an unwrapped
// error that maps to a 500.
func TestCommitGeneration_UnknownChapterClientIdIsValidationError(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	svc := aipipeline.NewService(pool, nil, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "test-model")
	in := aipipeline.CommitInput{
		UserID:    u.ID,
		SubjectID: subj.ID,
		Cards: []aipipeline.CommitCard{
			{ChapterClientID: "nonexistent", Title: "bad", Question: "q", Answer: "a"},
		},
	}
	_, err := svc.CommitGeneration(context.Background(), in)
	if !errors.Is(err, myErrors.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

// TestCommitGeneration_CrossUserSubjectForbidden is a regression test for AI-1:
// a user with no relationship to the target subject must not be able to write
// chapters/cards into it via commit-generation.
func TestCommitGeneration_CrossUserSubjectForbidden(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, owner.ID)

	stranger := testutil.NewVerifiedUser(t, pool)

	svc := aipipeline.NewService(pool, nil, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "test-model")
	in := aipipeline.CommitInput{
		UserID:    stranger.ID,
		SubjectID: subj.ID,
		Cards: []aipipeline.CommitCard{
			{ChapterClientID: "", Title: "IDOR test card", Question: "Injected?", Answer: "Yes if vulnerable"},
		},
	}
	_, err := svc.CommitGeneration(context.Background(), in)
	if !errors.Is(err, myErrors.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	var cards int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM flashcards WHERE subject_id=$1`, subj.ID).Scan(&cards)
	if cards != 0 {
		t.Errorf("cards inserted = %d, want 0 (rejected commit must not write)", cards)
	}
}

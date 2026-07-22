package aipipeline_test

import (
	"context"
	"errors"
	"testing"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
	"studdle/backend/pkg/aipipeline"
	"studdle/backend/testutil"
)

func TestCheckFlashcard_ReturnsVerdictAndSuggestion(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q1", "A1")

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"verdict":"ok","findings":[],"suggestion":{"title":"","question":"Q1","answer":"A1"}}`, Done: true},
		},
	}
	svc := aipipeline.NewService(pool, cli, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "test-model")

	out, err := svc.CheckFlashcard(context.Background(), aipipeline.CheckInput{
		UserID:      u.ID,
		FlashcardID: fcID,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if out.Verdict != "ok" {
		t.Errorf("Verdict = %q, want ok", out.Verdict)
	}
	if out.Suggestion.Question != "Q1" {
		t.Errorf("Suggestion.Question = %q, want Q1", out.Suggestion.Question)
	}
	if out.JobID <= 0 {
		t.Errorf("JobID = %d, want > 0", out.JobID)
	}
}

// TestCheckFlashcard_CrossUserForbidden is a regression test for AI-1: a user
// with no relationship to the flashcard's subject must be rejected before the
// provider is ever called.
func TestCheckFlashcard_CrossUserForbidden(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, owner.ID)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q1", "A1")

	stranger := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, stranger.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"verdict":"ok","findings":[],"suggestion":{"title":"","question":"Q1","answer":"A1"}}`, Done: true},
		},
	}
	svc := aipipeline.NewService(pool, cli, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "test-model")

	_, err := svc.CheckFlashcard(context.Background(), aipipeline.CheckInput{
		UserID:      stranger.ID,
		FlashcardID: fcID,
	})
	if !errors.Is(err, myErrors.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if cli.Calls() != 0 {
		t.Errorf("provider Calls() = %d, want 0 (must reject before calling the provider)", cli.Calls())
	}
}

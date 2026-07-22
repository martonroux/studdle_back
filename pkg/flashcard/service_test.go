package flashcard_test

import (
	"context"
	"testing"

	"studdle/backend/pkg/access"
	"studdle/backend/pkg/flashcard"
	"studdle/backend/testutil"
)

func TestFlashcardCRUD(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := flashcard.NewService(db, acc, nil)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	fc, err := svc.Create(ctx, owner.ID, flashcard.CreateInput{
		SubjectID: sub.ID, Question: "Q?", Answer: "A.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if fc.Source != "manual" || fc.LastResult != -1 {
		t.Fatalf("unexpected defaults: %+v", fc)
	}

	list, err := svc.ListBySubject(ctx, owner.ID, sub.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	reviewed, err := svc.RecordReview(ctx, owner.ID, fc.ID, flashcard.ReviewInput{Result: 2})
	if err != nil || reviewed.LastResult != 2 || reviewed.DueAt == nil {
		t.Fatalf("review: %v %+v", err, reviewed)
	}

	if err := svc.Delete(ctx, owner.ID, fc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestFlashcard_RejectBadResult(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := flashcard.NewService(db, acc, nil)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)
	fc, _ := svc.Create(ctx, owner.ID, flashcard.CreateInput{
		SubjectID: sub.ID, Question: "Q", Answer: "A",
	})

	if _, err := svc.RecordReview(ctx, owner.ID, fc.ID, flashcard.ReviewInput{Result: 7}); err == nil {
		t.Fatal("expected invalid input for out-of-range result")
	}
}

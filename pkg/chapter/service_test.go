package chapter_test

import (
	"context"
	"testing"

	"studdle/backend/pkg/access"
	"studdle/backend/pkg/chapter"
	"studdle/backend/testutil"
)

func TestChapterCRUD(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := chapter.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	ch, err := svc.Create(ctx, owner.ID, chapter.CreateInput{SubjectID: sub.ID, Title: "Cells"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ch.Position != 0 {
		t.Fatalf("expected position=0, got %d", ch.Position)
	}

	list, err := svc.List(ctx, owner.ID, sub.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	newTitle := "Cell Biology"
	u, err := svc.Update(ctx, owner.ID, ch.ID, chapter.UpdateInput{Title: &newTitle})
	if err != nil || u.Title != newTitle {
		t.Fatalf("update: %v %+v", err, u)
	}

	if err := svc.Delete(ctx, owner.ID, ch.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestChapter_ForbiddenForStranger(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := chapter.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	stranger := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	if _, err := svc.Create(ctx, stranger.ID, chapter.CreateInput{SubjectID: sub.ID, Title: "x"}); err == nil {
		t.Fatal("expected forbidden")
	}
}

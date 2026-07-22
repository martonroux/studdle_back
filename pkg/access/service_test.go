package access

import (
	"context"
	"testing"

	"studdle/backend/testutil"
)

func TestHasAIAccessFalseByDefault(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := NewService(pool)
	ok, err := svc.HasAIAccess(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("HasAIAccess: %v", err)
	}
	if ok {
		t.Fatal("expected no AI access for fresh user")
	}
}

func TestHasAIAccessTrueAfterSubscription(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)

	svc := NewService(pool)
	ok, err := svc.HasAIAccess(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("HasAIAccess: %v", err)
	}
	if !ok {
		t.Fatal("expected AI access after giving subscription")
	}
}

func TestSubjectAccessOwnerCanManage(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubject(t, pool, owner.ID)

	svc := NewService(pool)
	lvl, err := svc.SubjectLevel(context.Background(), owner.ID, sub.ID)
	if err != nil {
		t.Fatalf("SubjectLevel: %v", err)
	}
	if lvl != LevelOwner {
		t.Fatalf("level = %v, want Owner", lvl)
	}
}

func TestSubjectAccessStrangerIsNone(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	stranger := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubject(t, pool, owner.ID)

	svc := NewService(pool)
	lvl, err := svc.SubjectLevel(context.Background(), stranger.ID, sub.ID)
	if err != nil {
		t.Fatalf("SubjectLevel: %v", err)
	}
	if lvl != LevelNone {
		t.Fatalf("level = %v, want None", lvl)
	}
}

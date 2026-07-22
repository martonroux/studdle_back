package subjectsub_test

import (
	"context"
	"errors"
	"testing"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
	"studdle/backend/pkg/subjectsub"
	"studdle/backend/testutil"
)

// TestSubscribeAndList verifies a user can subscribe to a public subject, list
// their subscriptions, check membership, and unsubscribe.
func TestSubscribeAndList(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := subjectsub.NewService(db, access.NewService(db))

	bob := testutil.NewVerifiedUser(t, db)
	alice := testutil.NewVerifiedUser(t, db)
	subj := testutil.NewSubjectNamed(t, db, bob.ID, "Public Subj", "public")

	if err := svc.Subscribe(ctx, alice.ID, subj.ID); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ok, err := svc.IsSubscribed(ctx, alice.ID, subj.ID)
	if err != nil || !ok {
		t.Fatalf("expected subscribed, got ok=%v err=%v", ok, err)
	}
	ids, err := svc.ListSubscribed(ctx, alice.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 1 || ids[0] != subj.ID {
		t.Fatalf("expected [%d], got %v", subj.ID, ids)
	}
	if err := svc.Unsubscribe(ctx, alice.ID, subj.ID); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	ok, err = svc.IsSubscribed(ctx, alice.ID, subj.ID)
	if err != nil || ok {
		t.Fatalf("expected unsubscribed, got ok=%v err=%v", ok, err)
	}
}

// TestSubscribe_ForbiddenOnPrivate verifies a stranger cannot subscribe to a
// private subject they have no access to.
func TestSubscribe_ForbiddenOnPrivate(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := subjectsub.NewService(db, access.NewService(db))

	bob := testutil.NewVerifiedUser(t, db)
	alice := testutil.NewVerifiedUser(t, db)
	subj := testutil.NewSubjectNamed(t, db, bob.ID, "Private Subj", "private")

	err := svc.Subscribe(ctx, alice.ID, subj.ID)
	if !errors.Is(err, myErrors.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

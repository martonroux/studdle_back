package friendship_test

import (
	"context"
	"errors"
	"testing"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/friendship"
	"studdle/backend/testutil"
)

// TestFriendshipFlow exercises the happy path: request, receiver accepts, list, unfriend.
func TestFriendshipFlow(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := friendship.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	f, err := svc.Request(ctx, alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if f.Status != "pending" {
		t.Fatalf("expected pending, got %s", f.Status)
	}

	if _, err := svc.Accept(ctx, alice.ID, f.ID); err == nil {
		t.Fatal("sender should not be able to accept")
	}

	acc, err := svc.Accept(ctx, bob.ID, f.ID)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if acc.Status != "accepted" {
		t.Fatalf("expected accepted, got %s", acc.Status)
	}

	friends, err := svc.ListFriends(ctx, alice.ID)
	if err != nil {
		t.Fatalf("list friends: %v", err)
	}
	if len(friends) != 1 {
		t.Fatalf("expected 1 friend, got %d", len(friends))
	}

	if err := svc.Unfriend(ctx, alice.ID, f.ID); err != nil {
		t.Fatalf("unfriend: %v", err)
	}
}

// TestFriendshipRequest_DuplicateRejected verifies unique-violation maps to ErrConflict.
func TestFriendshipRequest_DuplicateRejected(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := friendship.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	if _, err := svc.Request(ctx, alice.ID, bob.ID); err != nil {
		t.Fatalf("first request: %v", err)
	}
	_, err := svc.Request(ctx, alice.ID, bob.ID)
	if !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

// TestFriendshipRequest_ReverseRejectedWhenAlreadyFriends is a regression test for SL-1:
// once two users are friends, a request in the *reverse* direction must be
// rejected instead of silently creating a second pending request.
func TestFriendshipRequest_ReverseRejectedWhenAlreadyFriends(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := friendship.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	f, err := svc.Request(ctx, alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := svc.Accept(ctx, bob.ID, f.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}

	_, err = svc.Request(ctx, bob.ID, alice.ID)
	if !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("expected ErrConflict for reverse request between friends, got %v", err)
	}
}

// TestFriendshipRequest_ReopensAfterDecline is a regression test for SL-3: after a
// decline, the original sender must be able to send a fresh request to the same
// receiver instead of being permanently blocked by the unique index.
func TestFriendshipRequest_ReopensAfterDecline(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := friendship.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	first, err := svc.Request(ctx, alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if _, err := svc.Decline(ctx, bob.ID, first.ID); err != nil {
		t.Fatalf("decline: %v", err)
	}

	second, err := svc.Request(ctx, alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("re-request after decline: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected the declined row to be reopened (same id %d), got id %d", first.ID, second.ID)
	}
	if second.Status != "pending" {
		t.Fatalf("expected reopened request to be pending, got %s", second.Status)
	}

	pending, err := svc.ListPendingIncoming(ctx, bob.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request after reopen, got %d", len(pending))
	}
}

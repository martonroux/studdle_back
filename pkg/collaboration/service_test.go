package collaboration_test

import (
	"context"
	"errors"
	"testing"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
	"studdle/backend/pkg/collaboration"
	"studdle/backend/testutil"
)

// TestInviteFlow covers the happy path: owner creates an invite, invitee redeems,
// and the collaborator list reports the new member.
func TestInviteFlow(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := collaboration.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	invitee := testutil.NewVerifiedUser(t, db)
	subj := testutil.NewSubject(t, db, owner.ID)

	inv, err := svc.CreateInvite(ctx, owner.ID, collaboration.CreateInviteInput{
		SubjectID: subj.ID,
		Role:      "editor",
		TTLHours:  24,
	})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	if _, err := svc.RedeemInvite(ctx, invitee.ID, inv.Token); err != nil {
		t.Fatalf("redeem invite: %v", err)
	}

	list, err := svc.ListCollaborators(ctx, owner.ID, subj.ID)
	if err != nil {
		t.Fatalf("list collaborators: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 collaborator, got %d", len(list))
	}
}

// TestInvite_NonOwnerForbidden verifies a non-owner cannot mint an invite link.
func TestInvite_NonOwnerForbidden(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := collaboration.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)
	subj := testutil.NewSubject(t, db, owner.ID)

	_, err := svc.CreateInvite(ctx, other.ID, collaboration.CreateInviteInput{
		SubjectID: subj.ID,
		Role:      "viewer",
		TTLHours:  1,
	})
	if err == nil {
		t.Fatal("expected non-owner CreateInvite to fail")
	}
}

// TestRevokeInvite_BlocksSubsequentRedeem is a regression test for SL-4: once the
// owner revokes an invite link, redeeming it must fail instead of silently
// succeeding (revoked_at was previously written by nobody and read by nobody).
func TestRevokeInvite_BlocksSubsequentRedeem(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := collaboration.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	invitee := testutil.NewVerifiedUser(t, db)
	subj := testutil.NewSubject(t, db, owner.ID)

	inv, err := svc.CreateInvite(ctx, owner.ID, collaboration.CreateInviteInput{
		SubjectID: subj.ID,
		Role:      "viewer",
	})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	if err := svc.RevokeInvite(ctx, owner.ID, inv.Token); err != nil {
		t.Fatalf("revoke invite: %v", err)
	}

	_, err = svc.RedeemInvite(ctx, invitee.ID, inv.Token)
	if !errors.Is(err, myErrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound redeeming a revoked invite, got %v", err)
	}
}

// TestRevokeInvite_NonOwnerForbidden verifies only the subject owner may revoke.
func TestRevokeInvite_NonOwnerForbidden(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := collaboration.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)
	subj := testutil.NewSubject(t, db, owner.ID)

	inv, err := svc.CreateInvite(ctx, owner.ID, collaboration.CreateInviteInput{
		SubjectID: subj.ID,
		Role:      "viewer",
	})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	if err := svc.RevokeInvite(ctx, other.ID, inv.Token); err == nil {
		t.Fatal("expected non-owner RevokeInvite to fail")
	}
}

// TestRedeemInvite_OwnerSelfRedeemNoop is a regression test for SL-7: the subject
// owner redeeming their own invite link must not create a spurious collaborator
// row for someone who already has full access.
func TestRedeemInvite_OwnerSelfRedeemNoop(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := collaboration.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	subj := testutil.NewSubject(t, db, owner.ID)

	inv, err := svc.CreateInvite(ctx, owner.ID, collaboration.CreateInviteInput{
		SubjectID: subj.ID,
		Role:      "editor",
	})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	if _, err := svc.RedeemInvite(ctx, owner.ID, inv.Token); err == nil {
		t.Fatal("expected owner self-redeem to be rejected")
	}

	list, err := svc.ListCollaborators(ctx, owner.ID, subj.ID)
	if err != nil {
		t.Fatalf("list collaborators: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected no collaborator row from owner self-redeem, got %d", len(list))
	}
}

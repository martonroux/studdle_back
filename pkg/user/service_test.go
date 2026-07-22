package user

import (
	"context"
	"errors"
	"testing"
	"time"

	jwtsigner "studdle/backend/internal/jwt"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/gamification"
	"studdle/backend/testutil"
)

func TestRegisterAndLogin(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour)
	svc := NewService(pool, signer)

	tok, uid, err := svc.Register(context.Background(), RegisterInput{
		Username: "alice", Email: "alice@example.com", Password: "password123",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if tok == "" || uid == 0 {
		t.Fatal("empty token or uid")
	}

	tok2, err := svc.Login(context.Background(), LoginInput{Identifier: "alice", Password: "password123"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok2 == "" {
		t.Fatal("empty login token")
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	svc := NewService(pool, jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour))

	_, _, err := svc.Register(context.Background(), RegisterInput{Username: "a", Email: "a@x.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Register(context.Background(), RegisterInput{Username: "a", Email: "b@x.com", Password: "password123"})
	if !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	svc := NewService(pool, jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour))
	_, _, _ = svc.Register(context.Background(), RegisterInput{Username: "alice", Email: "alice@x.com", Password: "password123"})

	_, err := svc.Login(context.Background(), LoginInput{Identifier: "alice", Password: "wrongpass"})
	if !errors.Is(err, myErrors.ErrUnauthenticated) {
		t.Fatalf("want ErrUnauthenticated, got %v", err)
	}
}

// TestStatsBadgesTotalMatchesCatalog is a regression test for GAM-2: badgesTotal must be
// derived from the real achievement catalog, not a hardcoded literal that can drift.
func TestStatsBadgesTotalMatchesCatalog(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	svc := NewService(pool, jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour))

	_, uid, err := svc.Register(context.Background(), RegisterInput{
		Username: "carol", Email: "carol@x.com", Password: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}

	stats, err := svc.Stats(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	want := len(gamification.AllAchievements())
	if stats.BadgesTotal != want {
		t.Fatalf("BadgesTotal = %d, want %d (catalog size)", stats.BadgesTotal, want)
	}
}

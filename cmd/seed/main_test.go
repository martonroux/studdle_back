package main

import (
	"context"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"studdle/backend/testutil"
)

func TestUpsertTestUser_CreatesVerifiedUser(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	f := seedFlags{Username: "e2e_test_user", Email: "e2e-test@studdle.dev", Password: "E2eTestPass123!"}
	uid, err := upsertTestUser(context.Background(), pool, f)
	if err != nil {
		t.Fatalf("upsertTestUser: %v", err)
	}

	var username, email, hash string
	var verified bool
	err = pool.QueryRow(context.Background(),
		`SELECT username, email, password_hash, email_verified FROM users WHERE id = $1`, uid,
	).Scan(&username, &email, &hash, &verified)
	if err != nil {
		t.Fatalf("load user: %v", err)
	}
	if username != f.Username || email != f.Email || !verified {
		t.Fatalf("got username=%q email=%q verified=%v", username, email, verified)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(f.Password)) != nil {
		t.Fatal("stored hash does not match seed password")
	}
}

func TestUpsertTestUser_IdempotentOnRerun(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	f := seedFlags{Username: "e2e_test_user", Email: "e2e-test@studdle.dev", Password: "FirstPass123!"}
	firstID, err := upsertTestUser(context.Background(), pool, f)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	f.Password = "SecondPass456!"
	secondID, err := upsertTestUser(context.Background(), pool, f)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("re-running changed the user id: %d -> %d", firstID, secondID)
	}

	var hash string
	err = pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE id = $1`, secondID).Scan(&hash)
	if err != nil {
		t.Fatalf("load user: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(f.Password)) != nil {
		t.Fatal("password was not reset to the second run's value")
	}
}

func TestGrantAIAccess_GrantsIndefiniteCompedAccess(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	if err := grantAIAccess(context.Background(), pool, u.ID); err != nil {
		t.Fatalf("grantAIAccess: %v", err)
	}

	var status, plan string
	var periodEnd *string
	err := pool.QueryRow(context.Background(),
		`SELECT status, plan, current_period_end FROM user_subscriptions WHERE user_id = $1`, u.ID,
	).Scan(&status, &plan, &periodEnd)
	if err != nil {
		t.Fatalf("load subscription: %v", err)
	}
	if status != "comped" || plan != "comp" || periodEnd != nil {
		t.Fatalf("got status=%q plan=%q periodEnd=%v, want comped/comp/nil", status, plan, periodEnd)
	}

	var hasAccess bool
	if err := pool.QueryRow(context.Background(),
		`SELECT user_has_ai_access($1)`, u.ID).Scan(&hasAccess); err != nil {
		t.Fatalf("check ai access: %v", err)
	}
	if !hasAccess {
		t.Fatal("user_has_ai_access = false after grantAIAccess")
	}
}

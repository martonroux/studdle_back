package billing_test

import (
	"context"
	"testing"

	"studdle/backend/internal/billing"
	pkgbilling "studdle/backend/pkg/billing"
	"studdle/backend/testutil"
)

func TestGrantComp_InsertsActiveCompRow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{}, pkgbilling.PriceMap{})

	if err := svc.GrantComp(context.Background(), u.ID, true); err != nil {
		t.Fatalf("GrantComp(true): %v", err)
	}

	var ok bool
	_ = pool.QueryRow(context.Background(), `SELECT user_has_ai_access($1)`, u.ID).Scan(&ok)
	if !ok {
		t.Fatal("user_has_ai_access = false after GrantComp(true)")
	}
}

func TestGrantComp_RevokesByMarkingCanceled(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{}, pkgbilling.PriceMap{})

	if err := svc.GrantComp(context.Background(), u.ID, true); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := svc.GrantComp(context.Background(), u.ID, false); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	var ok bool
	_ = pool.QueryRow(context.Background(), `SELECT user_has_ai_access($1)`, u.ID).Scan(&ok)
	if ok {
		t.Fatal("user_has_ai_access = true after revoke")
	}
}

func TestGrantComp_IdempotentOnDoubleGrant(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{}, pkgbilling.PriceMap{})

	if err := svc.GrantComp(context.Background(), u.ID, true); err != nil {
		t.Fatalf("grant1: %v", err)
	}
	if err := svc.GrantComp(context.Background(), u.ID, true); err != nil {
		t.Fatalf("grant2: %v", err)
	}

	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM user_subscriptions WHERE user_id = $1 AND plan = 'comp'`, u.ID).Scan(&n)
	if n != 1 {
		t.Errorf("comp-row count = %d, want 1", n)
	}
}

// TestGrantComp_WritesStatusComped verifies the post-schema literal change.
func TestGrantComp_WritesStatusComped(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := pkgbilling.NewService(pool, billing.NoopClient{}, pkgbilling.PriceMap{})
	if err := svc.GrantComp(context.Background(), u.ID, true); err != nil {
		t.Fatalf("grant: %v", err)
	}

	var status, plan string
	err := pool.QueryRow(context.Background(),
		`SELECT status, plan FROM user_subscriptions WHERE user_id = $1`, u.ID,
	).Scan(&status, &plan)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "comped" {
		t.Fatalf("status = %q, want comped", status)
	}
	if plan != "comp" {
		t.Fatalf("plan = %q, want comp", plan)
	}
}

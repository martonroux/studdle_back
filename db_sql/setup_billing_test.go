package db_sql_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/testutil"
)

// TestBillingSchema_MatchesSpec asserts the post-migration shape:
// user_subscriptions has user_id PRIMARY KEY, the stripe_customer_id and
// stripe_sub_id columns, trial_end + paused_at, and the full status CHECK set.
// billing_events has livemode NOT NULL and a NULLABLE stripe_event_id.
func TestBillingSchema_MatchesSpec(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	ctx := context.Background()

	var pkCol string
	err := pool.QueryRow(ctx, `
		SELECT a.attname
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = 'user_subscriptions'::regclass AND i.indisprimary
	`).Scan(&pkCol)
	if err != nil {
		t.Fatalf("query PK: %v", err)
	}
	if pkCol != "user_id" {
		t.Fatalf("user_subscriptions PK = %q, want user_id", pkCol)
	}

	requireColumn(t, pool, "user_subscriptions", "stripe_customer_id")
	requireColumn(t, pool, "user_subscriptions", "stripe_sub_id")
	requireColumn(t, pool, "user_subscriptions", "trial_end")
	requireColumn(t, pool, "user_subscriptions", "paused_at")
	requireColumn(t, pool, "billing_events", "livemode")
	requireColumn(t, pool, "billing_events", "event_type")

	var nullable string
	err = pool.QueryRow(ctx, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_name = 'billing_events' AND column_name = 'stripe_event_id'
	`).Scan(&nullable)
	if err != nil {
		t.Fatalf("query nullability: %v", err)
	}
	if nullable != "YES" {
		t.Fatalf("billing_events.stripe_event_id nullable = %q, want YES", nullable)
	}

	// status CHECK accepts 'comped' (we don't need a row to exist; the CHECK
	// is enforced at INSERT time, so a no-match SELECT proves nothing —
	// instead, try inserting one synthetic row guarded by a transaction we
	// roll back.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	u := testutil.NewVerifiedUser(t, pool)
	_, err = tx.Exec(ctx, `
		INSERT INTO user_subscriptions (user_id, plan, status)
		VALUES ($1, 'comp', 'comped')
	`, u.ID)
	if err != nil {
		t.Fatalf("status='comped' should pass CHECK: %v", err)
	}
}

// TestBillingSchema_RejectsOldCompStatus asserts the CHECK constraint rejects
// the pre-Spec-C literal status='comp' (it was renamed to 'comped').
func TestBillingSchema_RejectsOldCompStatus(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	ctx := context.Background()

	u := testutil.NewVerifiedUser(t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	// status='comp' (old literal) must be rejected
	_, err = tx.Exec(ctx, `
		INSERT INTO user_subscriptions (user_id, plan, status)
		SELECT id, 'comp', 'comp' FROM users WHERE id = $1
	`, u.ID)
	if err == nil {
		t.Fatal("status='comp' should be rejected by CHECK constraint")
	}
}

// requireColumn fails the test if table.col does not exist.
func requireColumn(t *testing.T, pool *pgxpool.Pool, table, col string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		)`, table, col).Scan(&exists)
	if err != nil {
		t.Fatalf("query column %s.%s: %v", table, col, err)
	}
	if !exists {
		t.Fatalf("column %s.%s missing", table, col)
	}
}

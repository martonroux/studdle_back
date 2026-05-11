# Spec C — Subscription Billing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Spec A's admin-flip stub with real Stripe-backed subscription billing on the web. A free user can click "Start 30-day trial," check out via Stripe Checkout, and have AI access within seconds; cancellations, payment failures, and admin comps all flow through `user_subscriptions` + `billing_events`.

**Architecture:** Single backend package `pkg/billing` owns all writes to `user_subscriptions` and `billing_events`; webhook handler, refresh endpoint, and reconciliation cron all funnel through one `applyStripeState` write path. Stripe SDK calls live in `internal/billing` behind the existing `Client` interface (real `StripeClient` replaces `NoopClient` in prod). Frontend gets a `/pricing` page and an authed `/billing` page; `PaywallCard.vue` becomes the inline checkout entry point.

**Tech Stack:** Go 1.25 + pgx + `github.com/stripe/stripe-go/v76`; Vue 3 + Pinia on the frontend; Postgres for state; Stripe Checkout + Customer Portal (hosted UIs).

**Spec reference:** `docs/superpowers/specs/2026-04-21-subscription-billing-design.md` (revised 2026-05-11).

**Prerequisite reading for the implementer:**
- Spec C design doc (above)
- `internal/billing/client.go` — existing `Client` interface + `NoopClient`
- `pkg/billing/service.go` — existing stub Service with `GrantComp` (status `'comp'`)
- `db_sql/setup_billing.go` — existing scaffold schema (drift documented in spec §4.4)
- `api/handler/admin_ai.go` + `_test.go` — pattern for admin-gated handlers
- `pkg/aipipeline/` — pattern for splitting a single package across many focused files

---

## Phase 1 — Schema reconciliation

The scaffold from Spec A's foundation work mostly aligns with the spec but has 6 deltas to resolve. This phase replaces `db_sql/setup_billing.go` so the table shape matches §4.1–4.3 exactly.

### Task 1: Schema test — desired post-migration shape

**Files:**
- Create: `db_sql/setup_billing_test.go`

- [ ] **Step 1: Write the failing test**

```go
package db_sql

import (
	"context"
	"testing"

	"studbud/backend/testutil"
)

// TestBillingSchema_MatchesSpec asserts the post-migration shape:
// user_subscriptions has user_id PRIMARY KEY, the stripe_customer_id and
// stripe_sub_id columns, trial_end + paused_at, and the full status CHECK set.
// billing_events has livemode NOT NULL and a NULLABLE stripe_event_id.
func TestBillingSchema_MatchesSpec(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	ctx := context.Background()

	// PK on user_id (not on id)
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

	// required columns exist
	requireColumn(t, pool, "user_subscriptions", "stripe_customer_id")
	requireColumn(t, pool, "user_subscriptions", "stripe_sub_id")
	requireColumn(t, pool, "user_subscriptions", "trial_end")
	requireColumn(t, pool, "user_subscriptions", "paused_at")
	requireColumn(t, pool, "billing_events", "livemode")

	// billing_events.stripe_event_id is nullable
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

	// status CHECK accepts 'comped' and rejects 'comp'
	_, err = pool.Exec(ctx, `
		INSERT INTO user_subscriptions (user_id, plan, status)
		SELECT id, 'comp', 'comped' FROM users LIMIT 1
	`)
	// no users yet → INSERT no-ops; we only care that the CHECK accepted 'comped'.
	if err != nil {
		t.Fatalf("status='comped' should pass CHECK: %v", err)
	}
}

func requireColumn(t *testing.T, pool interface {
	QueryRow(ctx context.Context, sql string, args ...any) interface{ Scan(...any) error }
}, table, col string) {
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
```

Note: the `pool` parameter type in `requireColumn` should be `*pgxpool.Pool`. Use that explicitly; the inline interface above is shorthand. Replace with:

```go
func requireColumn(t *testing.T, pool *pgxpool.Pool, table, col string) {
```

and add the import `"github.com/jackc/pgx/v5/pgxpool"`.

- [ ] **Step 2: Run test to verify it fails**

```
go test ./db_sql/ -run TestBillingSchema_MatchesSpec -v
```

Expected: FAIL — current scaffold has `id BIGSERIAL` PK, missing columns, non-nullable `stripe_event_id`.

- [ ] **Step 3: Rewrite `db_sql/setup_billing.go` to the spec shape**

Replace the contents of `db_sql/setup_billing.go`:

```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const billingSchema = `
CREATE TABLE IF NOT EXISTS user_subscriptions (
    user_id              BIGINT       PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    stripe_customer_id   TEXT         UNIQUE,
    stripe_sub_id        TEXT         UNIQUE,
    status               TEXT         NOT NULL CHECK (status IN (
                                         'trialing','active','past_due','paused',
                                         'canceled','incomplete','incomplete_expired',
                                         'comped'
                                       )),
    plan                 TEXT         NOT NULL CHECK (plan IN ('pro_monthly','pro_annual','comp')),
    current_period_end   TIMESTAMPTZ,
    trial_end            TIMESTAMPTZ,
    cancel_at_period_end BOOLEAN      NOT NULL DEFAULT FALSE,
    paused_at            TIMESTAMPTZ,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_subs_status ON user_subscriptions (status);
CREATE INDEX IF NOT EXISTS idx_user_subs_period_end ON user_subscriptions (current_period_end)
    WHERE status IN ('active','trialing','past_due');

CREATE TABLE IF NOT EXISTS billing_events (
    id                BIGSERIAL PRIMARY KEY,
    stripe_event_id   TEXT,
    user_id           BIGINT REFERENCES users(id) ON DELETE SET NULL,
    event_type        TEXT NOT NULL,
    livemode          BOOLEAN NOT NULL,
    payload           JSONB NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_billing_events_stripe_event_id
    ON billing_events (stripe_event_id) WHERE stripe_event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_billing_events_user ON billing_events (user_id, received_at DESC);

CREATE OR REPLACE FUNCTION user_has_ai_access(uid BIGINT) RETURNS BOOLEAN
LANGUAGE SQL STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM user_subscriptions
        WHERE user_id = uid
          AND status IN ('active','trialing','comped')
          AND (current_period_end IS NULL OR current_period_end > NOW())
    );
$$;
`

// setupBilling creates user_subscriptions, billing_events, and user_has_ai_access.
// Idempotent: re-running on an existing schema is a no-op.
func setupBilling(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, billingSchema); err != nil {
		return fmt.Errorf("exec billing schema:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./db_sql/ -run TestBillingSchema_MatchesSpec -v
```

Expected: PASS. (You may need to drop the test DB once so the new schema is created fresh; running `make test` from a clean DB also works.)

- [ ] **Step 5: Commit**

```bash
git add db_sql/setup_billing.go db_sql/setup_billing_test.go
git commit -m "$(cat <<'EOF'
Spec C: reconcile billing schema to spec

[&] user_subscriptions PK switched to user_id (was id BIGSERIAL)
[+] stripe_customer_id, stripe_sub_id, trial_end, paused_at columns
[+] status CHECK expanded to full 8-value set (paused, incomplete, incomplete_expired, comped)
[+] billing_events.livemode column
[&] billing_events.stripe_event_id now nullable + partial unique index
[+] db_sql/setup_billing_test.go schema assertion
EOF
)"
```

### Task 2: Migrate existing `GrantComp` to write `status='comped'`

The existing `pkg/billing/service.go` writes `'comp'`. The new CHECK constraint rejects that. Fix the SQL.

**Files:**
- Modify: `pkg/billing/service.go:53-95` (the three helpers)
- Test: `pkg/billing/service_test.go` (new file)

- [ ] **Step 1: Write the failing test**

Create `pkg/billing/service_test.go`:

```go
package billing_test

import (
	"context"
	"testing"

	"studbud/backend/internal/billing"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestGrantComp_WritesStatusComped(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := pkgbilling.NewService(pool, billing.NoopClient{})
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
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run TestGrantComp_WritesStatusComped -v
```

Expected: FAIL — existing code writes `'comp'` for status.

- [ ] **Step 3: Update SQL constants in `pkg/billing/service.go`**

In `pkg/billing/service.go`, replace every literal `'comp'` that refers to the **status** column with `'comped'`. The plan column stays `'comp'` (it is a separate enum). The three helpers become:

```go
func (s *Service) upsertComp(ctx context.Context, uid int64) error {
	const upd = `
        UPDATE user_subscriptions
        SET status = 'comped', updated_at = now()
        WHERE user_id = $1 AND plan = 'comp'
    `
	tag, err := s.db.Exec(ctx, upd, uid)
	if err != nil {
		return fmt.Errorf("activate comp:\n%w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	return s.insertComp(ctx, uid)
}

func (s *Service) insertComp(ctx context.Context, uid int64) error {
	const ins = `
        INSERT INTO user_subscriptions (user_id, plan, status)
        SELECT $1, 'comp', 'comped'
        WHERE NOT EXISTS (
            SELECT 1 FROM user_subscriptions WHERE user_id = $1 AND plan = 'comp'
        )
    `
	if _, err := s.db.Exec(ctx, ins, uid); err != nil {
		return fmt.Errorf("insert comp:\n%w", err)
	}
	return nil
}

func (s *Service) cancelComp(ctx context.Context, uid int64) error {
	const q = `
        UPDATE user_subscriptions
        SET status = 'canceled', updated_at = now()
        WHERE user_id = $1 AND plan = 'comp'
    `
	if _, err := s.db.Exec(ctx, q, uid); err != nil {
		return fmt.Errorf("cancel comp:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./pkg/billing/ -run TestGrantComp_WritesStatusComped -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/billing/service.go pkg/billing/service_test.go
git commit -m "$(cat <<'EOF'
Spec C: GrantComp writes status='comped'

[&] upsertComp/insertComp/cancelComp use 'comped' instead of 'comp'
[+] service_test.go covers post-grant status assertion
EOF
)"
```

### Task 3: Ensure existing admin-grant + access tests still pass

The status change affects `user_has_ai_access`, which `access.HasAIAccess` calls. Run the full suite to catch any regressions.

**Files:** (no edits — verification only)

- [ ] **Step 1: Run the admin-AI handler test**

```
go test ./api/handler/ -run TestGrantAIAccess -v
```

Expected: PASS (the SQL function reads `'comped'` from the table now, but its predicate also includes `'comped'`).

- [ ] **Step 2: Run the access package tests**

```
go test ./pkg/access/... -v
```

Expected: PASS.

- [ ] **Step 3: Run the full backend test suite**

```
make test
```

Expected: PASS for all existing packages. Any failure here means a leftover reference to `status='comp'`; grep for it: `grep -rn "status = 'comp'\|status='comp'" --include='*.go' .` and update.

- [ ] **Step 4: Commit (only if Step 3 surfaced any fixes)**

```bash
git add -p
git commit -m "$(cat <<'EOF'
Spec C: fix remaining status='comp' references

[!] sweep any caller still expecting the old status literal
EOF
)"
```

---

## Phase 2 — Stripe SDK + config hardening

### Task 4: Add `stripe-go` to `go.mod`

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

```
go get github.com/stripe/stripe-go/v76
go mod tidy
```

- [ ] **Step 2: Verify build still passes**

```
go build ./...
```

Expected: clean compile.

- [ ] **Step 3: Verify no `replace` directive present**

```
grep -n "^replace" go.mod
```

Expected: no output. CLAUDE.md forbids `replace` in commits.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "$(cat <<'EOF'
Spec C: add stripe-go dependency

[+] github.com/stripe/stripe-go/v76 for Checkout, Portal, webhooks
EOF
)"
```

### Task 5: Key-prefix assertion in `validateStripeMode`

Spec §9 requires the secret key prefix to match `STRIPE_MODE` so a deploy with a swapped key fails at boot rather than at first Stripe call.

**Files:**
- Modify: `internal/config/config.go:187-196` (the existing `validateStripeMode`)
- Test: `internal/config/config_test.go` (extend existing file if present; otherwise create)

- [ ] **Step 1: Write the failing test**

Add to (or create) `internal/config/config_test.go`:

```go
package config

import (
	"strings"
	"testing"
)

// TestValidateStripeMode_KeyPrefixMustMatch covers the four prefix/mode combos.
func TestValidateStripeMode_KeyPrefixMustMatch(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		key     string
		env     string
		wantErr string
	}{
		{"test mode + sk_test passes", "test", "sk_test_abc", "dev", ""},
		{"test mode + sk_live fails", "test", "sk_live_abc", "dev", "STRIPE_SECRET_KEY prefix"},
		{"live mode + sk_live passes", "live", "sk_live_abc", "prod", ""},
		{"live mode + sk_test fails", "live", "sk_test_abc", "prod", "STRIPE_SECRET_KEY prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{
				StripeMode:      tc.mode,
				StripeSecretKey: tc.key,
				Env:             tc.env,
			}
			err := validateStripeMode(c)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./internal/config/ -run TestValidateStripeMode_KeyPrefixMustMatch -v
```

Expected: FAIL — current `validateStripeMode` only checks mode, not key prefix.

- [ ] **Step 3: Update `validateStripeMode`**

Replace the function body:

```go
// validateStripeMode rejects unknown modes, live-mode outside prod, and
// secret keys whose prefix does not match the configured mode.
func validateStripeMode(c *Config) error {
	if c.StripeMode != "test" && c.StripeMode != "live" {
		return fmt.Errorf("STRIPE_MODE must be 'test' or 'live' (got %q)", c.StripeMode)
	}
	if c.StripeMode == "live" && c.Env != "prod" {
		return fmt.Errorf("STRIPE_MODE=live is not allowed when ENV=%q", c.Env)
	}
	if c.StripeSecretKey == "" {
		return nil
	}
	wantPrefix := "sk_test_"
	if c.StripeMode == "live" {
		wantPrefix = "sk_live_"
	}
	if !strings.HasPrefix(c.StripeSecretKey, wantPrefix) {
		return fmt.Errorf("STRIPE_SECRET_KEY prefix does not match STRIPE_MODE=%s (want %s*)", c.StripeMode, wantPrefix)
	}
	return nil
}
```

(`strings` is already imported in the file.)

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./internal/config/ -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "$(cat <<'EOF'
Spec C: assert Stripe key prefix matches STRIPE_MODE at boot

[+] validateStripeMode rejects sk_live in test mode and sk_test in live mode
[+] table-driven test covers the 4 mode/prefix combinations
EOF
)"
```

### Task 6: Add `AppURL` to config (Checkout success_url / portal return_url)

The spec's Checkout flow needs `APP_URL` for redirects. The existing `FrontendURL` could serve, but the spec uses `APP_URL` explicitly — and there are deployments where the user-facing app URL differs from the email-link URL. Add a dedicated field that falls back to `FrontendURL` when unset.

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add the field**

In `Config`:

```go
	AppURL string // AppURL is the user-facing app origin for Stripe redirects (defaults to FrontendURL)
```

In `loadFromEnv`:

```go
		AppURL: getEnvDefault("APP_URL", ""),
```

After load, in `Load()` (before `validate`):

```go
	if cfg.AppURL == "" {
		cfg.AppURL = cfg.FrontendURL
	}
```

- [ ] **Step 2: Verify build**

```
go build ./...
```

Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "$(cat <<'EOF'
Spec C: add APP_URL config for Stripe redirects

[+] Config.AppURL with FrontendURL fallback
EOF
)"
```

---

## Phase 3 — Billing service core

This phase adds the data types and the central write path (`applyStripeState`) used by webhook, refresh, and cron. No Stripe HTTP yet.

### Task 7: Define billing model types

**Files:**
- Create: `pkg/billing/model.go`

- [ ] **Step 1: Write the file**

```go
package billing

import "time"

// Status is the local subscription status. Mirrors the schema CHECK set.
type Status string

const (
	StatusTrialing          Status = "trialing"
	StatusActive            Status = "active"
	StatusPastDue           Status = "past_due"
	StatusPaused            Status = "paused"
	StatusCanceled          Status = "canceled"
	StatusIncomplete        Status = "incomplete"
	StatusIncompleteExpired Status = "incomplete_expired"
	StatusComped            Status = "comped"
)

// Plan identifies which Stripe price (or 'comp') the row is anchored to.
type Plan string

const (
	PlanProMonthly Plan = "pro_monthly"
	PlanProAnnual  Plan = "pro_annual"
	PlanComp       Plan = "comp"
)

// Subscription is the read-side projection returned by GetSubscription.
type Subscription struct {
	Status             Status     // Status is the current local status
	Plan               Plan       // Plan is the row's plan column
	CurrentPeriodEnd   *time.Time // CurrentPeriodEnd is the renewal/expiry boundary
	TrialEnd           *time.Time // TrialEnd is the trial expiry (nil after conversion)
	CancelAtPeriodEnd  bool       // CancelAtPeriodEnd is true after a user cancels mid-period
	StripeCustomerID   string     // StripeCustomerID is the Stripe Customer (empty for comped)
	StripeSubID        string     // StripeSubID is the Stripe Subscription id (empty for comped)
}

// IsActive returns true when the subscription grants AI access.
// Mirrors user_has_ai_access() exactly.
func (s Subscription) IsActive(now time.Time) bool {
	switch s.Status {
	case StatusActive, StatusTrialing, StatusComped:
	default:
		return false
	}
	if s.CurrentPeriodEnd == nil {
		return true
	}
	return s.CurrentPeriodEnd.After(now)
}

// StateUpdate is the payload applyStripeState writes.
// Source-agnostic — populated by webhook, refresh, and cron alike.
type StateUpdate struct {
	UserID             int64      // UserID identifies the target row
	StripeCustomerID   string     // StripeCustomerID is the Stripe Customer id
	StripeSubID        string     // StripeSubID is the Stripe Subscription id
	Status             Status     // Status is the new local status
	Plan               Plan       // Plan is the resolved plan from price ID
	CurrentPeriodEnd   *time.Time // CurrentPeriodEnd from Stripe
	TrialEnd           *time.Time // TrialEnd from Stripe (nil after conversion)
	CancelAtPeriodEnd  bool       // CancelAtPeriodEnd from Stripe
	PausedAt           *time.Time // PausedAt is set when Status=paused, NULL otherwise
}
```

- [ ] **Step 2: Verify build**

```
go build ./pkg/billing/
```

Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add pkg/billing/model.go
git commit -m "$(cat <<'EOF'
Spec C: billing model types

[+] Status, Plan enums mirroring schema CHECK sets
[+] Subscription read projection
[+] StateUpdate payload for applyStripeState
EOF
)"
```

### Task 8: SQL constants file

Keep queries out of the handler files. The pattern matches `pkg/aipipeline/queries.go`.

**Files:**
- Create: `pkg/billing/queries.go`

- [ ] **Step 1: Write the file**

```go
package billing

// sqlUpsertSubscription writes the full row from a StateUpdate.
// All comp-related rows skip this path (comp.go owns those).
const sqlUpsertSubscription = `
INSERT INTO user_subscriptions (
    user_id, stripe_customer_id, stripe_sub_id, status, plan,
    current_period_end, trial_end, cancel_at_period_end, paused_at,
    created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())
ON CONFLICT (user_id) DO UPDATE SET
    stripe_customer_id   = EXCLUDED.stripe_customer_id,
    stripe_sub_id        = EXCLUDED.stripe_sub_id,
    status               = EXCLUDED.status,
    plan                 = EXCLUDED.plan,
    current_period_end   = EXCLUDED.current_period_end,
    trial_end            = EXCLUDED.trial_end,
    cancel_at_period_end = EXCLUDED.cancel_at_period_end,
    paused_at            = EXCLUDED.paused_at,
    updated_at           = now()
`

// sqlSelectSubscription returns the columns GetSubscription needs.
const sqlSelectSubscription = `
SELECT status, plan, current_period_end, trial_end, cancel_at_period_end,
       COALESCE(stripe_customer_id, ''), COALESCE(stripe_sub_id, '')
FROM user_subscriptions
WHERE user_id = $1
`

// sqlGetCustomerID returns the Stripe customer id for a user (empty when missing).
const sqlGetCustomerID = `
SELECT COALESCE(stripe_customer_id, '')
FROM user_subscriptions
WHERE user_id = $1
`

// sqlSetCustomerID upserts a row that only knows the customer (pre-checkout).
// Status is 'incomplete' until the first webhook arrives.
const sqlSetCustomerID = `
INSERT INTO user_subscriptions (user_id, stripe_customer_id, status, plan)
VALUES ($1, $2, 'incomplete', 'pro_monthly')
ON CONFLICT (user_id) DO UPDATE SET
    stripe_customer_id = COALESCE(user_subscriptions.stripe_customer_id, EXCLUDED.stripe_customer_id),
    updated_at = now()
`

// sqlInsertEvent records one entry in the audit log.
// stripe_event_id is nullable; pass empty string and we treat it as NULL.
const sqlInsertEvent = `
INSERT INTO billing_events (stripe_event_id, user_id, event_type, livemode, payload)
VALUES (NULLIF($1, ''), $2, $3, $4, $5)
`

// sqlListActiveStripeSubs returns every row with a non-NULL stripe_sub_id.
// Used by the reconciliation cron.
const sqlListActiveStripeSubs = `
SELECT user_id, stripe_sub_id
FROM user_subscriptions
WHERE stripe_sub_id IS NOT NULL
`
```

- [ ] **Step 2: Verify build**

```
go build ./pkg/billing/
```

Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add pkg/billing/queries.go
git commit -m "$(cat <<'EOF'
Spec C: billing SQL constants

[+] sqlUpsertSubscription (applyStripeState write)
[+] sqlSelectSubscription (read projection)
[+] sqlGetCustomerID, sqlSetCustomerID, sqlInsertEvent, sqlListActiveStripeSubs
EOF
)"
```

### Task 9: `applyStripeState` write path

**Files:**
- Create: `pkg/billing/state.go`
- Test: `pkg/billing/state_test.go`

- [ ] **Step 1: Write the failing test**

```go
package billing_test

import (
	"context"
	"testing"
	"time"

	"studbud/backend/internal/billing"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestApplyStripeState_InsertsRow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{})

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	trial := end
	upd := pkgbilling.StateUpdate{
		UserID:           u.ID,
		StripeCustomerID: "cus_test1",
		StripeSubID:      "sub_test1",
		Status:           pkgbilling.StatusTrialing,
		Plan:             pkgbilling.PlanProMonthly,
		CurrentPeriodEnd: &end,
		TrialEnd:         &trial,
	}
	if err := svc.ApplyStripeState(context.Background(), upd); err != nil {
		t.Fatalf("apply: %v", err)
	}

	sub, err := svc.GetSubscription(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sub.Status != pkgbilling.StatusTrialing || sub.Plan != pkgbilling.PlanProMonthly {
		t.Fatalf("got %+v", sub)
	}
	if sub.StripeCustomerID != "cus_test1" || sub.StripeSubID != "sub_test1" {
		t.Fatalf("ids = %q / %q", sub.StripeCustomerID, sub.StripeSubID)
	}
	if !sub.IsActive(time.Now()) {
		t.Fatalf("expected IsActive=true")
	}
}

func TestApplyStripeState_TransitionsActiveToPastDue(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{})

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	ctx := context.Background()
	_ = svc.ApplyStripeState(ctx, pkgbilling.StateUpdate{
		UserID: u.ID, StripeCustomerID: "cus_t", StripeSubID: "sub_t",
		Status: pkgbilling.StatusActive, Plan: pkgbilling.PlanProMonthly,
		CurrentPeriodEnd: &end,
	})

	_ = svc.ApplyStripeState(ctx, pkgbilling.StateUpdate{
		UserID: u.ID, StripeCustomerID: "cus_t", StripeSubID: "sub_t",
		Status: pkgbilling.StatusPastDue, Plan: pkgbilling.PlanProMonthly,
		CurrentPeriodEnd: &end,
	})

	sub, _ := svc.GetSubscription(ctx, u.ID)
	if sub.Status != pkgbilling.StatusPastDue {
		t.Fatalf("status = %q, want past_due", sub.Status)
	}
	if sub.IsActive(time.Now()) {
		t.Fatalf("expected IsActive=false for past_due")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run TestApplyStripeState -v
```

Expected: FAIL — `ApplyStripeState` and `GetSubscription` don't exist.

- [ ] **Step 3: Implement `ApplyStripeState` + `GetSubscription`**

Create `pkg/billing/state.go`:

```go
package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrSubscriptionNotFound is returned when no user_subscriptions row exists.
var ErrSubscriptionNotFound = errors.New("subscription not found")

// ApplyStripeState upserts the row described by upd. Single write path used by
// webhook, refresh, and cron. Status='paused' should set PausedAt; everything
// else sets it nil.
func (s *Service) ApplyStripeState(ctx context.Context, upd StateUpdate) error {
	_, err := s.db.Exec(ctx, sqlUpsertSubscription,
		upd.UserID,
		nullable(upd.StripeCustomerID),
		nullable(upd.StripeSubID),
		string(upd.Status),
		string(upd.Plan),
		upd.CurrentPeriodEnd,
		upd.TrialEnd,
		upd.CancelAtPeriodEnd,
		upd.PausedAt,
	)
	if err != nil {
		return fmt.Errorf("apply stripe state:\n%w", err)
	}
	return nil
}

// GetSubscription returns the row for uid or ErrSubscriptionNotFound.
func (s *Service) GetSubscription(ctx context.Context, uid int64) (Subscription, error) {
	var sub Subscription
	var status, plan string
	err := s.db.QueryRow(ctx, sqlSelectSubscription, uid).Scan(
		&status, &plan,
		&sub.CurrentPeriodEnd, &sub.TrialEnd, &sub.CancelAtPeriodEnd,
		&sub.StripeCustomerID, &sub.StripeSubID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, ErrSubscriptionNotFound
	}
	if err != nil {
		return Subscription{}, fmt.Errorf("get subscription:\n%w", err)
	}
	sub.Status = Status(status)
	sub.Plan = Plan(plan)
	return sub, nil
}

// nullable returns nil for empty strings so the COALESCE/NULLIF in SQL
// produces a real NULL instead of the empty string.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/billing/ -run TestApplyStripeState -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/billing/state.go pkg/billing/state_test.go
git commit -m "$(cat <<'EOF'
Spec C: applyStripeState single write path

[+] Service.ApplyStripeState upserts user_subscriptions row
[+] Service.GetSubscription read projection
[+] state_test.go covers insert + active→past_due transition
EOF
)"
```

### Task 10: Plan resolution helper

The webhook receives Stripe `price.id` values; we map back to our `Plan` enum via config.

**Files:**
- Create: `pkg/billing/plan_map.go`
- Test: `pkg/billing/plan_map_test.go`

- [ ] **Step 1: Write the failing test**

```go
package billing_test

import (
	"testing"

	pkgbilling "studbud/backend/pkg/billing"
)

func TestPlanFromPriceID(t *testing.T) {
	m := pkgbilling.PriceMap{
		Monthly: "price_M1",
		Annual:  "price_A1",
	}
	if p, ok := m.PlanFromPriceID("price_M1"); !ok || p != pkgbilling.PlanProMonthly {
		t.Fatalf("monthly mismatch: %v ok=%v", p, ok)
	}
	if p, ok := m.PlanFromPriceID("price_A1"); !ok || p != pkgbilling.PlanProAnnual {
		t.Fatalf("annual mismatch: %v ok=%v", p, ok)
	}
	if _, ok := m.PlanFromPriceID("price_unknown"); ok {
		t.Fatalf("unknown price should return ok=false")
	}
}

func TestPriceIDFromPlan(t *testing.T) {
	m := pkgbilling.PriceMap{Monthly: "price_M1", Annual: "price_A1"}
	if got, _ := m.PriceIDFromPlan(pkgbilling.PlanProMonthly); got != "price_M1" {
		t.Fatalf("got %q", got)
	}
	if got, _ := m.PriceIDFromPlan(pkgbilling.PlanProAnnual); got != "price_A1" {
		t.Fatalf("got %q", got)
	}
	if _, ok := m.PriceIDFromPlan(pkgbilling.PlanComp); ok {
		t.Fatalf("comp plan has no price id")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run "TestPlanFromPriceID|TestPriceIDFromPlan" -v
```

Expected: FAIL.

- [ ] **Step 3: Implement**

Create `pkg/billing/plan_map.go`:

```go
package billing

// PriceMap is the two-way lookup between local Plan names and Stripe price IDs.
// Constructed from config at boot.
type PriceMap struct {
	Monthly string // Monthly is the Stripe price id for pro_monthly
	Annual  string // Annual is the Stripe price id for pro_annual
}

// PlanFromPriceID returns the local Plan that corresponds to priceID, or false.
func (m PriceMap) PlanFromPriceID(priceID string) (Plan, bool) {
	switch priceID {
	case m.Monthly:
		return PlanProMonthly, true
	case m.Annual:
		return PlanProAnnual, true
	default:
		return "", false
	}
}

// PriceIDFromPlan returns the Stripe price id for plan, or false for PlanComp.
func (m PriceMap) PriceIDFromPlan(plan Plan) (string, bool) {
	switch plan {
	case PlanProMonthly:
		return m.Monthly, true
	case PlanProAnnual:
		return m.Annual, true
	default:
		return "", false
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/billing/ -run "TestPlanFromPriceID|TestPriceIDFromPlan" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/billing/plan_map.go pkg/billing/plan_map_test.go
git commit -m "$(cat <<'EOF'
Spec C: PriceMap two-way price-id / plan lookup

[+] PriceMap.PlanFromPriceID + PriceIDFromPlan
EOF
)"
```

### Task 11: Wire `PriceMap` into the Service

The Service needs the map to resolve incoming webhook prices.

**Files:**
- Modify: `pkg/billing/service.go`
- Modify: `cmd/app/deps.go` (constructor call)

- [ ] **Step 1: Update `pkg/billing.NewService`**

Replace the existing `NewService`:

```go
// Service owns all writes to user_subscriptions and billing_events.
type Service struct {
	db       *pgxpool.Pool         // db is the shared pool
	provider billingadapter.Client // provider is the underlying Stripe (or fake) client
	prices   PriceMap              // prices maps Stripe price ids ↔ local Plan
}

// NewService constructs a Service with a PriceMap.
func NewService(db *pgxpool.Pool, provider billingadapter.Client, prices PriceMap) *Service {
	return &Service{db: db, provider: provider, prices: prices}
}

// Prices exposes the PriceMap to callers (handlers) that need to read it.
func (s *Service) Prices() PriceMap { return s.prices }
```

- [ ] **Step 2: Update the construction site in `cmd/app/deps.go`**

In `buildStubServices`, change:

```go
		billing: pkgbilling.NewService(pool, inf.billing),
```

to:

```go
		billing: pkgbilling.NewService(pool, inf.billing, pkgbilling.PriceMap{
			Monthly: cfg.StripePriceProMonth,
			Annual:  cfg.StripePriceProAnnual,
		}),
```

- [ ] **Step 3: Verify build + run existing billing tests**

```
go build ./...
go test ./pkg/billing/ -v
go test ./api/handler/ -run TestGrantAIAccess -v
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add pkg/billing/service.go cmd/app/deps.go
git commit -m "$(cat <<'EOF'
Spec C: inject PriceMap into billing.Service

[&] NewService takes PriceMap, exposes Prices() reader
[&] deps.go constructs map from STRIPE_PRICE_PRO_MONTHLY/ANNUAL
EOF
)"
```

---

## Phase 4 — Real Stripe client wrapper

Replaces `internal/billing.NoopClient` with a real Stripe-backed implementation. Keeps the existing `Client` interface untouched but extends it where the Service needs new calls (retrieve subscription, create customer, list subscriptions).

### Task 12: Extend the `Client` interface

The current interface has 3 methods. Spec C needs 4 more: create-customer, retrieve-subscription, list-subscriptions-by-customer, and a webhook-construct (sig verify + JSON decode in one shot).

**Files:**
- Modify: `internal/billing/client.go`

- [ ] **Step 1: Update the interface and NoopClient**

```go
package billing

import (
	"context"
	"time"

	"studbud/backend/internal/myErrors"
)

// CheckoutSession is what the frontend redirects a user to.
type CheckoutSession struct {
	URL string
	ID  string
}

// Subscription is the provider-agnostic snapshot returned by RetrieveSubscription.
type Subscription struct {
	ID                 string     // ID is the Stripe Subscription id
	CustomerID         string     // CustomerID is the Stripe Customer id
	Status             string     // Status mirrors Stripe's subscription.status (raw string)
	PriceID            string     // PriceID is the active price's id (first item)
	CurrentPeriodEnd   *time.Time // CurrentPeriodEnd is the current period boundary
	TrialEnd           *time.Time // TrialEnd is the trial boundary (nil after conversion)
	CancelAtPeriodEnd  bool       // CancelAtPeriodEnd is Stripe's cancel_at_period_end
	PausedAt           *time.Time // PausedAt is set when Stripe paused the subscription
	Livemode           bool       // Livemode is the Stripe livemode flag
}

// CheckoutInput packs the args CreateCheckout needs.
type CheckoutInput struct {
	UserID           int64  // UserID is our user id, written to metadata
	CustomerID       string // CustomerID is the existing Stripe Customer id
	PriceID          string // PriceID is the Stripe price id
	TrialDays        int    // TrialDays is the free-trial length
	SuccessURL       string // SuccessURL is the post-checkout redirect
	CancelURL        string // CancelURL is the cancel-from-checkout redirect
}

// WebhookEvent is the provider-agnostic webhook payload.
type WebhookEvent struct {
	ID       string // ID is the Stripe event id (unique)
	Type     string // Type is the Stripe event type ("customer.subscription.updated", ...)
	Livemode bool   // Livemode is the event's livemode flag
	Raw      []byte // Raw is the original JSON payload (kept for billing_events.payload)
}

// Client is the billing-provider interface.
type Client interface {
	CreateCustomer(ctx context.Context, email string, userID int64) (string, error)
	CreateCheckout(ctx context.Context, in CheckoutInput) (*CheckoutSession, error)
	CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error)
	RetrieveSubscription(ctx context.Context, subID string) (*Subscription, error)
	ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]Subscription, error)
	ConstructWebhookEvent(payload []byte, signature string) (*WebhookEvent, error)
}

// NoopClient returns ErrNotImplemented for every call.
type NoopClient struct{}

func (NoopClient) CreateCustomer(ctx context.Context, email string, userID int64) (string, error) {
	return "", myErrors.ErrNotImplemented
}
func (NoopClient) CreateCheckout(ctx context.Context, in CheckoutInput) (*CheckoutSession, error) {
	return nil, myErrors.ErrNotImplemented
}
func (NoopClient) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	return "", myErrors.ErrNotImplemented
}
func (NoopClient) RetrieveSubscription(ctx context.Context, subID string) (*billing.Subscription, error) {
	return nil, myErrors.ErrNotImplemented
}
func (NoopClient) ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]billing.Subscription, error) {
	return nil, myErrors.ErrNotImplemented
}
func (NoopClient) ConstructWebhookEvent(payload []byte, signature string) (*WebhookEvent, error) {
	return nil, myErrors.ErrNotImplemented
}
```

Note: the `RetrieveSubscription` / `ListSubscriptionsByCustomer` return types reference `*Subscription` defined in this same package — fix the receivers to drop the `billing.` qualifier since they live in package `billing` themselves:

```go
func (NoopClient) RetrieveSubscription(ctx context.Context, subID string) (*Subscription, error) {
	return nil, myErrors.ErrNotImplemented
}
func (NoopClient) ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]Subscription, error) {
	return nil, myErrors.ErrNotImplemented
}
```

- [ ] **Step 2: Update `testutil/stripe.go` to match the new interface**

```go
package testutil

import (
	"context"

	"studbud/backend/internal/billing"
)

// FakeBilling is a test double for billing.Client.
type FakeBilling struct {
	CheckoutURL string
	PortalURL   string
	CustomerID  string
	Subscription *billing.Subscription
	Subscriptions []billing.Subscription
	Event       *billing.WebhookEvent
	WebhookErr  error
}

func (f *FakeBilling) CreateCustomer(ctx context.Context, email string, userID int64) (string, error) {
	if f.CustomerID == "" {
		return "cus_test_fake", nil
	}
	return f.CustomerID, nil
}
func (f *FakeBilling) CreateCheckout(ctx context.Context, in billing.CheckoutInput) (*billing.CheckoutSession, error) {
	return &billing.CheckoutSession{URL: f.CheckoutURL, ID: "cs_test"}, nil
}
func (f *FakeBilling) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	return f.PortalURL, nil
}
func (f *FakeBilling) RetrieveSubscription(ctx context.Context, subID string) (*billing.Subscription, error) {
	return f.Subscription, nil
}
func (f *FakeBilling) ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]billing.Subscription, error) {
	return f.Subscriptions, nil
}
func (f *FakeBilling) ConstructWebhookEvent(payload []byte, signature string) (*billing.WebhookEvent, error) {
	return f.Event, f.WebhookErr
}
```

- [ ] **Step 3: Find and update any other implementers**

```
grep -rn "billing.Client\|VerifyWebhook" --include='*.go' . | grep -v _test.go
```

If anything implements the old method names, fix it. The handler `billing_stub.go` doesn't talk to `Client` directly, so should be unaffected.

- [ ] **Step 4: Verify build**

```
go build ./...
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/billing/client.go testutil/stripe.go
git commit -m "$(cat <<'EOF'
Spec C: extend Client interface for full Stripe surface

[+] Client.CreateCustomer, RetrieveSubscription, ListSubscriptionsByCustomer
[&] CreateCheckout takes CheckoutInput (was 2 args)
[&] VerifyWebhook → ConstructWebhookEvent (returns parsed event)
[+] Subscription + WebhookEvent + CheckoutInput types
[&] testutil.FakeBilling tracks the new surface
EOF
)"
```

### Task 13: Real `StripeClient` implementation — customer + checkout

**Files:**
- Create: `internal/billing/stripe_client.go`
- Create: `internal/billing/stripe_checkout.go`

- [ ] **Step 1: Write the client constructor**

`internal/billing/stripe_client.go`:

```go
package billing

import (
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/client"
)

// StripeClient is the production billing.Client.
type StripeClient struct {
	api           *client.API // api is the underlying stripe-go client
	webhookSecret string      // webhookSecret signs incoming webhook payloads
}

// NewStripeClient builds a StripeClient. secretKey must already be prefix-matched
// to mode by the config layer.
func NewStripeClient(secretKey, webhookSecret string) *StripeClient {
	sc := &client.API{}
	sc.Init(secretKey, nil)
	return &StripeClient{api: sc, webhookSecret: webhookSecret}
}

// CreateCustomer creates a Stripe Customer keyed to email and writes the
// internal user id into metadata so webhooks can find the user without a lookup.
func (c *StripeClient) CreateCustomer(ctx context.Context, email string, userID int64) (string, error) {
	params := &stripe.CustomerParams{
		Email: stripe.String(email),
		Metadata: map[string]string{
			"userId": fmt.Sprintf("%d", userID),
		},
	}
	params.Context = ctx
	cus, err := c.api.Customers.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe create customer:\n%w", err)
	}
	return cus.ID, nil
}
```

Add the imports `"context"`, `"fmt"` at the top.

- [ ] **Step 2: Write the checkout call**

`internal/billing/stripe_checkout.go`:

```go
package billing

import (
	"context"
	"fmt"

	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/billingportal/session"
)

// CreateCheckout creates a hosted Stripe Checkout session matching Spec C §5.1:
// subscription mode, 30-day trial, automatic tax, tax-id collection, single price.
func (c *StripeClient) CreateCheckout(ctx context.Context, in CheckoutInput) (*CheckoutSession, error) {
	params := &stripe.CheckoutSessionParams{
		Mode:                stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		Customer:            stripe.String(in.CustomerID),
		ClientReferenceID:   stripe.String(fmt.Sprintf("%d", in.UserID)),
		SuccessURL:          stripe.String(in.SuccessURL),
		CancelURL:           stripe.String(in.CancelURL),
		PaymentMethodCollection: stripe.String("always"),
		AutomaticTax:        &stripe.CheckoutSessionAutomaticTaxParams{Enabled: stripe.Bool(true)},
		TaxIDCollection:     &stripe.CheckoutSessionTaxIDCollectionParams{Enabled: stripe.Bool(true)},
		LineItems: []*stripe.CheckoutSessionLineItemParams{{
			Price:    stripe.String(in.PriceID),
			Quantity: stripe.Int64(1),
		}},
		SubscriptionData: &stripe.CheckoutSessionSubscriptionDataParams{
			TrialPeriodDays: stripe.Int64(int64(in.TrialDays)),
			Metadata: map[string]string{
				"userId": fmt.Sprintf("%d", in.UserID),
			},
		},
		Metadata: map[string]string{
			"userId": fmt.Sprintf("%d", in.UserID),
		},
	}
	params.Context = ctx
	sess, err := session.New(params)
	if err != nil {
		return nil, fmt.Errorf("stripe create checkout:\n%w", err)
	}
	return &CheckoutSession{URL: sess.URL, ID: sess.ID}, nil
}

// CreatePortal creates a Customer Portal session for stripeCustomerID.
func (c *StripeClient) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(stripeCustomerID),
		ReturnURL: stripe.String(returnURL),
	}
	params.Context = ctx
	sess, err := portalsession.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe create portal:\n%w", err)
	}
	return sess.URL, nil
}
```

Resolve the import alias for the two `session` packages — Go won't accept two imports with the same identifier. Use:

```go
import (
	checkoutsession "github.com/stripe/stripe-go/v76/checkout/session"
	portalsession   "github.com/stripe/stripe-go/v76/billingportal/session"
)
```

and update the call sites: `checkoutsession.New(params)` / `portalsession.New(params)`.

- [ ] **Step 3: Verify build**

```
go build ./...
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/billing/stripe_client.go internal/billing/stripe_checkout.go
git commit -m "$(cat <<'EOF'
Spec C: StripeClient — customer + checkout + portal

[+] NewStripeClient constructor
[+] CreateCustomer with userId metadata
[+] CreateCheckout (subscription mode, 30-day trial, automatic tax)
[+] CreatePortal
EOF
)"
```

### Task 14: Real `StripeClient` — retrieve / list / webhook

**Files:**
- Create: `internal/billing/stripe_subscription.go`
- Create: `internal/billing/stripe_webhook.go`

- [ ] **Step 1: Write retrieve + list**

`internal/billing/stripe_subscription.go`:

```go
package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/subscription"
)

// RetrieveSubscription fetches one subscription by id and projects it to the
// provider-agnostic Subscription struct.
func (c *StripeClient) RetrieveSubscription(ctx context.Context, subID string) (*Subscription, error) {
	params := &stripe.SubscriptionParams{}
	params.Context = ctx
	sub, err := subscription.Get(subID, params)
	if err != nil {
		return nil, fmt.Errorf("stripe retrieve subscription:\n%w", err)
	}
	return project(sub), nil
}

// ListSubscriptionsByCustomer returns up to `limit` subscriptions for a
// customer, status='all'. Used by /billing/refresh.
func (c *StripeClient) ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]Subscription, error) {
	params := &stripe.SubscriptionListParams{
		Customer: stripe.String(customerID),
		Status:   stripe.String("all"),
	}
	params.Limit = stripe.Int64(int64(limit))
	params.Context = ctx
	iter := subscription.List(params)
	out := make([]Subscription, 0, limit)
	for iter.Next() {
		out = append(out, *project(iter.Subscription()))
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("stripe list subscriptions:\n%w", err)
	}
	return out, nil
}

// project converts a stripe.Subscription to the local Subscription type.
func project(sub *stripe.Subscription) *Subscription {
	out := &Subscription{
		ID:                sub.ID,
		CustomerID:        sub.Customer.ID,
		Status:            string(sub.Status),
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
		Livemode:          sub.Livemode,
	}
	if sub.CurrentPeriodEnd > 0 {
		t := time.Unix(sub.CurrentPeriodEnd, 0).UTC()
		out.CurrentPeriodEnd = &t
	}
	if sub.TrialEnd > 0 {
		t := time.Unix(sub.TrialEnd, 0).UTC()
		out.TrialEnd = &t
	}
	if sub.PauseCollection != nil && sub.Status == stripe.SubscriptionStatusPaused {
		now := time.Now().UTC()
		out.PausedAt = &now
	}
	if len(sub.Items.Data) > 0 && sub.Items.Data[0].Price != nil {
		out.PriceID = sub.Items.Data[0].Price.ID
	}
	return out
}
```

- [ ] **Step 2: Write the webhook construct**

`internal/billing/stripe_webhook.go`:

```go
package billing

import (
	"encoding/json"
	"fmt"

	stripewebhook "github.com/stripe/stripe-go/v76/webhook"
)

// ConstructWebhookEvent verifies the signature and decodes the event header.
// Raw payload is preserved for audit storage.
func (c *StripeClient) ConstructWebhookEvent(payload []byte, signature string) (*WebhookEvent, error) {
	event, err := stripewebhook.ConstructEvent(payload, signature, c.webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("stripe webhook construct:\n%w", err)
	}
	// stripe-go already decoded the envelope; we keep the raw payload to write
	// into billing_events.payload as JSONB.
	if !json.Valid(payload) {
		return nil, fmt.Errorf("webhook payload is not valid JSON")
	}
	return &WebhookEvent{
		ID:       event.ID,
		Type:     string(event.Type),
		Livemode: event.Livemode,
		Raw:      payload,
	}, nil
}
```

- [ ] **Step 3: Verify build**

```
go build ./...
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/billing/stripe_subscription.go internal/billing/stripe_webhook.go
git commit -m "$(cat <<'EOF'
Spec C: StripeClient — retrieve, list, webhook construct

[+] RetrieveSubscription + project() to local Subscription
[+] ListSubscriptionsByCustomer for refresh path
[+] ConstructWebhookEvent verifies signature + preserves raw payload
EOF
)"
```

### Task 15: Wire `StripeClient` into `buildInfra`

**Files:**
- Modify: `cmd/app/deps.go` (`buildInfra` + add `selectBillingClient`)

- [ ] **Step 1: Add the selector**

Below `selectAIClient`, add:

```go
// selectBillingClient returns the real Stripe client when in prod (or when keys
// are configured in dev); otherwise the NoopClient.
func selectBillingClient(cfg *config.Config) billingadapter.Client {
	if cfg.Env == "test" || cfg.StripeSecretKey == "" {
		return billingadapter.NoopClient{}
	}
	return billingadapter.NewStripeClient(cfg.StripeSecretKey, cfg.StripeWebhookSecret)
}
```

- [ ] **Step 2: Use it in `buildInfra`**

Replace:

```go
		billing:   billingadapter.NoopClient{},
```

with:

```go
		billing:   selectBillingClient(cfg),
```

- [ ] **Step 3: Verify build + tests still pass with NoopClient**

```
go build ./...
go test ./pkg/billing/ ./api/handler/ -v
```

Expected: PASS — tests run in `ENV=test` so they still get the noop.

- [ ] **Step 4: Commit**

```bash
git add cmd/app/deps.go
git commit -m "$(cat <<'EOF'
Spec C: wire real StripeClient in buildInfra

[+] selectBillingClient mirrors selectAIClient pattern
[&] buildInfra uses real client when STRIPE_SECRET_KEY is set
EOF
)"
```

---

## Phase 5 — Checkout + Portal flows

### Task 16: `CreateCheckoutSession` service method

**Files:**
- Create: `pkg/billing/checkout.go`
- Test: `pkg/billing/checkout_test.go`

- [ ] **Step 1: Write the failing test**

```go
package billing_test

import (
	"context"
	"testing"

	"studbud/backend/internal/billing"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestCreateCheckoutSession_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	fake := &testutil.FakeBilling{
		CheckoutURL: "https://checkout.stripe.test/cs_test",
		CustomerID:  "cus_X",
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{
		Monthly: "price_M", Annual: "price_A",
	})

	url, err := svc.CreateCheckoutSession(context.Background(),
		u.ID, u.Email, pkgbilling.PlanProMonthly, "https://app/billing", "https://app/pricing")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if url != "https://checkout.stripe.test/cs_test" {
		t.Fatalf("got url %q", url)
	}

	// stripe_customer_id was persisted
	var cust string
	_ = pool.QueryRow(context.Background(),
		`SELECT COALESCE(stripe_customer_id,'') FROM user_subscriptions WHERE user_id=$1`,
		u.ID,
	).Scan(&cust)
	if cust != "cus_X" {
		t.Fatalf("customer not persisted, got %q", cust)
	}
}

func TestCreateCheckoutSession_RefusesAlreadyActive(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})

	// pre-existing active sub
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan) VALUES ($1, 'active', 'pro_monthly')`, u.ID)

	_, err := svc.CreateCheckoutSession(context.Background(),
		u.ID, u.Email, pkgbilling.PlanProMonthly, "https://app/billing", "https://app/pricing")
	if err == nil || err != pkgbilling.ErrAlreadySubscribed {
		t.Fatalf("err = %v, want ErrAlreadySubscribed", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run TestCreateCheckoutSession -v
```

Expected: FAIL — method doesn't exist.

- [ ] **Step 3: Implement**

`pkg/billing/checkout.go`:

```go
package billing

import (
	"context"
	"errors"
	"fmt"

	billingadapter "studbud/backend/internal/billing"
)

// ErrAlreadySubscribed is returned when a user with an active/trialing row tries to check out again.
var ErrAlreadySubscribed = errors.New("user already has an active subscription")

// ErrUnknownPlan is returned for plan values that have no Stripe price configured.
var ErrUnknownPlan = errors.New("unknown plan")

// TrialDays is the free-trial length applied to every new checkout session.
const TrialDays = 30

// CreateCheckoutSession returns the URL the user must visit to pay.
// Refuses ErrAlreadySubscribed for users in 'trialing' or 'active'.
func (s *Service) CreateCheckoutSession(
	ctx context.Context,
	uid int64, email string,
	plan Plan, billingPageURL, pricingPageURL string,
) (string, error) {
	if err := s.guardAlreadySubscribed(ctx, uid); err != nil {
		return "", err
	}
	priceID, ok := s.prices.PriceIDFromPlan(plan)
	if !ok {
		return "", ErrUnknownPlan
	}
	custID, err := s.getOrCreateCustomer(ctx, uid, email)
	if err != nil {
		return "", err
	}
	sess, err := s.provider.CreateCheckout(ctx, billingadapter.CheckoutInput{
		UserID:     uid,
		CustomerID: custID,
		PriceID:    priceID,
		TrialDays:  TrialDays,
		SuccessURL: billingPageURL + "?status=success&session_id={CHECKOUT_SESSION_ID}",
		CancelURL:  pricingPageURL + "?status=cancelled",
	})
	if err != nil {
		return "", fmt.Errorf("create checkout:\n%w", err)
	}
	return sess.URL, nil
}

// guardAlreadySubscribed returns ErrAlreadySubscribed when uid is trialing/active.
func (s *Service) guardAlreadySubscribed(ctx context.Context, uid int64) error {
	sub, err := s.GetSubscription(ctx, uid)
	if errors.Is(err, ErrSubscriptionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if sub.Status == StatusTrialing || sub.Status == StatusActive {
		return ErrAlreadySubscribed
	}
	return nil
}

// getOrCreateCustomer returns the user's stripe_customer_id, creating one
// (and persisting an 'incomplete' user_subscriptions row) when absent.
func (s *Service) getOrCreateCustomer(ctx context.Context, uid int64, email string) (string, error) {
	var cust string
	if err := s.db.QueryRow(ctx, sqlGetCustomerID, uid).Scan(&cust); err == nil && cust != "" {
		return cust, nil
	}
	id, err := s.provider.CreateCustomer(ctx, email, uid)
	if err != nil {
		return "", fmt.Errorf("create stripe customer:\n%w", err)
	}
	if _, err := s.db.Exec(ctx, sqlSetCustomerID, uid, id); err != nil {
		return "", fmt.Errorf("persist customer id:\n%w", err)
	}
	return id, nil
}
```

- [ ] **Step 4: Run tests**

```
go test ./pkg/billing/ -run TestCreateCheckoutSession -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/billing/checkout.go pkg/billing/checkout_test.go
git commit -m "$(cat <<'EOF'
Spec C: CreateCheckoutSession service method

[+] guardAlreadySubscribed, getOrCreateCustomer helpers
[+] ErrAlreadySubscribed, ErrUnknownPlan sentinels
[+] TrialDays = 30 constant
EOF
)"
```

### Task 17: `CreatePortalSession` service method

**Files:**
- Create: `pkg/billing/portal.go`
- Test: `pkg/billing/portal_test.go`

- [ ] **Step 1: Write the failing test**

```go
package billing_test

import (
	"context"
	"errors"
	"testing"

	"studbud/backend/internal/billing"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestCreatePortalSession_NoCustomerReturnsErr(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})

	_, err := svc.CreatePortalSession(context.Background(), u.ID, "https://app/billing")
	if !errors.Is(err, pkgbilling.ErrNoCustomer) {
		t.Fatalf("err = %v, want ErrNoCustomer", err)
	}
}

func TestCreatePortalSession_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan, stripe_customer_id)
		 VALUES ($1, 'active', 'pro_monthly', 'cus_Y')`, u.ID)

	fake := &testutil.FakeBilling{PortalURL: "https://portal.stripe.test/x"}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	url, err := svc.CreatePortalSession(context.Background(), u.ID, "https://app/billing")
	if err != nil {
		t.Fatalf("portal: %v", err)
	}
	if url != "https://portal.stripe.test/x" {
		t.Fatalf("got %q", url)
	}
	_ = billing.NoopClient{} // keep import alive
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run TestCreatePortalSession -v
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`pkg/billing/portal.go`:

```go
package billing

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoCustomer is returned when the user has no stripe_customer_id (never checked out).
var ErrNoCustomer = errors.New("user has no stripe customer")

// CreatePortalSession returns the URL of a Stripe Customer Portal session.
// Returns ErrNoCustomer if the user has never checked out.
func (s *Service) CreatePortalSession(ctx context.Context, uid int64, returnURL string) (string, error) {
	var cust string
	if err := s.db.QueryRow(ctx, sqlGetCustomerID, uid).Scan(&cust); err != nil {
		return "", fmt.Errorf("lookup customer:\n%w", err)
	}
	if cust == "" {
		return "", ErrNoCustomer
	}
	url, err := s.provider.CreatePortal(ctx, cust, returnURL)
	if err != nil {
		return "", fmt.Errorf("create portal:\n%w", err)
	}
	return url, nil
}
```

- [ ] **Step 4: Run tests**

```
go test ./pkg/billing/ -run TestCreatePortalSession -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/billing/portal.go pkg/billing/portal_test.go
git commit -m "$(cat <<'EOF'
Spec C: CreatePortalSession service method

[+] ErrNoCustomer sentinel for users who never checked out
EOF
)"
```

### Task 18: `BillingHandler.Checkout` HTTP wiring

**Files:**
- Create: `api/handler/billing.go` (will replace `billing_stub.go` later)
- Test: `api/handler/billing_checkout_test.go`

- [ ] **Step 1: Write the failing test**

```go
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"studbud/backend/api/handler"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestBillingCheckout_ReturnsURL(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	fake := &testutil.FakeBilling{CheckoutURL: "https://co.stripe.test/x", CustomerID: "cus_Z"}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	h := handler.NewBillingHandler(svc, "https://app.example", "https://app.example")

	signer := jwtsigner.NewSigner([]byte("0123456789abcdef0123456789abcdef"), "studbud", 0)
	tok, _ := signer.Sign(u.ID)
	body, _ := json.Marshal(map[string]string{"plan": "pro_monthly"})
	req := httptest.NewRequest("POST", "/billing/checkout", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.Checkout)).ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp struct{ URL string `json:"url"` }
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.URL != "https://co.stripe.test/x" {
		t.Fatalf("url=%q", resp.URL)
	}
}
```

(`signer.Sign` and the auth middleware shape match existing handler tests — model on `admin_ai_test.go` if signatures differ.)

- [ ] **Step 2: Run test to verify it fails**

```
go test ./api/handler/ -run TestBillingCheckout -v
```

Expected: FAIL — handler signature changed.

- [ ] **Step 3: Replace `billing_stub.go` with the real handler**

Delete `api/handler/billing_stub.go`. Create `api/handler/billing.go`:

```go
package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"studbud/backend/internal/http/middleware"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/billing"
)

// BillingHandler exposes Spec C billing endpoints.
type BillingHandler struct {
	svc            *billing.Service // svc owns user_subscriptions writes
	billingPageURL string           // billingPageURL is AppURL + "/billing"
	pricingPageURL string           // pricingPageURL is AppURL + "/pricing"
}

// NewBillingHandler constructs a BillingHandler.
// appURL is the user-facing app origin (from Config.AppURL).
func NewBillingHandler(svc *billing.Service, billingPageURL, pricingPageURL string) *BillingHandler {
	return &BillingHandler{svc: svc, billingPageURL: billingPageURL, pricingPageURL: pricingPageURL}
}

// checkoutInput is the request body for POST /billing/checkout.
type checkoutInput struct {
	Plan string `json:"plan"` // Plan is "pro_monthly" or "pro_annual"
}

// Checkout handles POST /billing/checkout.
func (h *BillingHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	uid, ok := middleware.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, myErrors.ErrUnauthorized)
		return
	}
	email, _ := middleware.UserEmail(r.Context())
	var in checkoutInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	plan := billing.Plan(in.Plan)
	url, err := h.svc.CreateCheckoutSession(r.Context(), uid, email, plan, h.billingPageURL, h.pricingPageURL)
	switch {
	case errors.Is(err, billing.ErrAlreadySubscribed):
		httpx.WriteError(w, &myErrors.AppError{Code: "already_subscribed", Message: "user already has an active subscription", Wrapped: myErrors.ErrConflict})
		return
	case errors.Is(err, billing.ErrUnknownPlan):
		httpx.WriteError(w, &myErrors.AppError{Code: "unknown_plan", Message: "unknown plan", Wrapped: myErrors.ErrValidation, Field: "plan"})
		return
	case err != nil:
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": url})
}
```

If `middleware.UserEmail` doesn't exist, look up the existing helper to access the authenticated user's email — there may be a different accessor. If the only thing the middleware exposes is the user id, fetch email from the DB inside the handler using `pkg/user`.

- [ ] **Step 4: Update `cmd/app/routes.go` BillingHandler construction**

Where `handler.NewBillingHandler(d.billing)` is called (two places — public + auth-social):

```go
billH := handler.NewBillingHandler(d.billing, d.cfg.AppURL+"/billing", d.cfg.AppURL+"/pricing")
```

- [ ] **Step 5: Run test + verify build**

```
go build ./...
go test ./api/handler/ -run TestBillingCheckout -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/handler/billing.go cmd/app/routes.go
git rm api/handler/billing_stub.go
git commit -m "$(cat <<'EOF'
Spec C: POST /billing/checkout returns Stripe URL

[+] BillingHandler with billing/pricing URLs injected from config
[+] checkout input decode + plan validation + already_subscribed mapping
[-] api/handler/billing_stub.go (replaced)
EOF
)"
```

### Task 19: `BillingHandler.Portal` HTTP wiring

**Files:**
- Modify: `api/handler/billing.go`
- Test: `api/handler/billing_portal_test.go`

- [ ] **Step 1: Write the failing test**

```go
package handler_test

// ... imports as in billing_checkout_test.go ...

func TestBillingPortal_NoCustomer404(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	h := handler.NewBillingHandler(svc, "https://app/billing", "https://app/pricing")

	signer := jwtsigner.NewSigner([]byte("0123456789abcdef0123456789abcdef"), "studbud", 0)
	tok, _ := signer.Sign(u.ID)
	req := httptest.NewRequest("POST", "/billing/portal", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.Portal)).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./api/handler/ -run TestBillingPortal -v
```

Expected: FAIL — Portal handler stubbed.

- [ ] **Step 3: Add `Portal` method to BillingHandler**

```go
// Portal handles POST /billing/portal.
func (h *BillingHandler) Portal(w http.ResponseWriter, r *http.Request) {
	uid, ok := middleware.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, myErrors.ErrUnauthorized)
		return
	}
	url, err := h.svc.CreatePortalSession(r.Context(), uid, h.billingPageURL)
	switch {
	case errors.Is(err, billing.ErrNoCustomer):
		httpx.WriteError(w, &myErrors.AppError{Code: "no_customer", Message: "no stripe customer for user", Wrapped: myErrors.ErrNotFound})
		return
	case err != nil:
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": url})
}
```

If `myErrors.ErrNotFound` doesn't exist with a matching HTTP-status mapping, look up the convention in `internal/myErrors/errors.go` — there's likely an existing `ErrNotFound` or you can use `httpx.WriteJSONWithCode(w, http.StatusNotFound, ...)`.

- [ ] **Step 4: Run test**

```
go test ./api/handler/ -run TestBillingPortal -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/handler/billing.go api/handler/billing_portal_test.go
git commit -m "$(cat <<'EOF'
Spec C: POST /billing/portal returns Stripe portal URL

[+] BillingHandler.Portal with no_customer 404 mapping
EOF
)"
```

---

## Phase 6 — Webhook handler + dispatch

The webhook is the heart of Spec C. Six tasks: preamble (sig/livemode/idempotency), then dispatch per event type.

### Task 20: Webhook preamble — signature, livemode, idempotency

**Files:**
- Create: `pkg/billing/webhook.go`
- Test: `pkg/billing/webhook_test.go`

- [ ] **Step 1: Write the failing test**

```go
package billing_test

import (
	"context"
	"testing"

	"studbud/backend/internal/billing"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestHandleWebhook_LivemodeMismatch(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	fake := &testutil.FakeBilling{Event: &billing.WebhookEvent{
		ID: "evt_1", Type: "customer.subscription.updated", Livemode: true,
		Raw: []byte(`{"id":"evt_1"}`),
	}}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	err := svc.HandleWebhook(context.Background(), pkgbilling.WebhookConfig{
		ExpectLivemode: false, Body: []byte(`{}`), Signature: "any",
	})
	if err != pkgbilling.ErrLivemodeMismatch {
		t.Fatalf("err = %v, want ErrLivemodeMismatch", err)
	}
}

func TestHandleWebhook_DuplicateEventIdempotent(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	fake := &testutil.FakeBilling{Event: &billing.WebhookEvent{
		ID: "evt_dup", Type: "charge.refunded", Livemode: false,
		Raw: []byte(`{"id":"evt_dup"}`),
	}}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	cfg := pkgbilling.WebhookConfig{ExpectLivemode: false, Body: []byte(`{}`), Signature: "x"}

	if err := svc.HandleWebhook(context.Background(), cfg); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := svc.HandleWebhook(context.Background(), cfg); err != nil {
		t.Fatalf("dup: %v", err)
	}

	var count int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM billing_events WHERE stripe_event_id = $1`, "evt_dup",
	).Scan(&count)
	if count != 1 {
		t.Fatalf("billing_events count = %d, want 1", count)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run TestHandleWebhook -v
```

Expected: FAIL.

- [ ] **Step 3: Implement preamble**

`pkg/billing/webhook.go`:

```go
package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	billingadapter "studbud/backend/internal/billing"
)

// ErrLivemodeMismatch is returned when an incoming event's livemode flag
// differs from the configured STRIPE_MODE.
var ErrLivemodeMismatch = errors.New("webhook livemode mismatch")

// WebhookConfig packs the per-call inputs HandleWebhook needs.
type WebhookConfig struct {
	ExpectLivemode bool   // ExpectLivemode is true when STRIPE_MODE=live
	Body           []byte // Body is the raw request body
	Signature      string // Signature is the Stripe-Signature header value
}

// HandleWebhook is the single entry point for Stripe webhook deliveries.
// Verifies the signature, enforces livemode, writes billing_events
// idempotently, and dispatches by event type.
func (s *Service) HandleWebhook(ctx context.Context, cfg WebhookConfig) error {
	event, err := s.provider.ConstructWebhookEvent(cfg.Body, cfg.Signature)
	if err != nil {
		return fmt.Errorf("verify webhook:\n%w", err)
	}
	if event.Livemode != cfg.ExpectLivemode {
		return ErrLivemodeMismatch
	}
	inserted, err := s.recordEvent(ctx, event)
	if err != nil {
		return err
	}
	if !inserted {
		// Duplicate delivery — silently succeed.
		return nil
	}
	return s.dispatch(ctx, event)
}

// recordEvent inserts the audit row. Returns inserted=false when the unique
// index on stripe_event_id already has the row (idempotent re-delivery).
func (s *Service) recordEvent(ctx context.Context, event *billingadapter.WebhookEvent) (bool, error) {
	_, err := s.db.Exec(ctx, sqlInsertEvent,
		event.ID,
		nil, // user_id resolved later inside dispatch via metadata
		event.Type,
		event.Livemode,
		event.Raw,
	)
	if err == nil {
		return true, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return false, nil
	}
	return false, fmt.Errorf("record event:\n%w", err)
}
```

- [ ] **Step 4: Run tests**

```
go test ./pkg/billing/ -run TestHandleWebhook -v
```

Expected: livemode test PASSES; duplicate test will FAIL on the `dispatch` call (unimplemented). That's fine for now — we'll implement dispatch in Task 21. To keep TDD green, replace `dispatch` with a stub that returns nil for unrecognized types:

Add this temporary stub at the bottom of `webhook.go`:

```go
// dispatch is the per-event-type router. Implemented incrementally; unknown
// or unmapped events are logged and ignored.
func (s *Service) dispatch(ctx context.Context, event *billingadapter.WebhookEvent) error {
	// TODO: implemented in Task 21+
	return nil
}
```

- [ ] **Step 5: Run tests again — should PASS**

```
go test ./pkg/billing/ -run TestHandleWebhook -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/billing/webhook.go pkg/billing/webhook_test.go
git commit -m "$(cat <<'EOF'
Spec C: webhook preamble — sig verify, livemode, idempotency

[+] HandleWebhook + WebhookConfig
[+] ErrLivemodeMismatch sentinel
[+] recordEvent dedupes by unique stripe_event_id
[+] dispatch() stub for incremental fill-in
EOF
)"
```

### Task 21: Dispatch `customer.subscription.updated` (the core lifecycle event)

`subscription.updated` covers trial→active, active→past_due, cancel_at_period_end flips, plan swaps. Implementing it first means most webhook scenarios work.

**Files:**
- Create: `pkg/billing/webhook_dispatch.go`
- Modify: `pkg/billing/webhook.go` (remove dispatch stub)
- Test: `pkg/billing/webhook_dispatch_test.go`

- [ ] **Step 1: Write the failing test**

```go
package billing_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"studbud/backend/internal/billing"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestDispatch_SubscriptionUpdated_TrialingToActive(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)

	fake := &testutil.FakeBilling{
		Event: &billing.WebhookEvent{
			ID: "evt_upd", Type: "customer.subscription.updated", Livemode: false,
			Raw: []byte(`{"id":"evt_upd","data":{"object":{"id":"sub_1","metadata":{"userId":"` + strconv.FormatInt(u.ID, 10) + `"}}}}`),
		},
		Subscription: &billing.Subscription{
			ID: "sub_1", CustomerID: "cus_1",
			Status: "active", PriceID: "price_M",
			CurrentPeriodEnd: &end,
		},
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	err := svc.HandleWebhook(context.Background(), pkgbilling.WebhookConfig{
		ExpectLivemode: false,
		Body:           fake.Event.Raw,
		Signature:      "any",
	})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusActive {
		t.Fatalf("status %q want active", sub.Status)
	}
	if sub.Plan != pkgbilling.PlanProMonthly {
		t.Fatalf("plan %q want pro_monthly", sub.Plan)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run TestDispatch_SubscriptionUpdated -v
```

Expected: FAIL.

- [ ] **Step 3: Replace the dispatch stub**

In `pkg/billing/webhook.go`, delete the `dispatch` stub. Create `pkg/billing/webhook_dispatch.go`:

```go
package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	billingadapter "studbud/backend/internal/billing"
)

// dispatch routes the event by Type. Unknown events are no-ops (the audit
// row was already written by recordEvent).
func (s *Service) dispatch(ctx context.Context, event *billingadapter.WebhookEvent) error {
	switch event.Type {
	case "customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted",
		"customer.subscription.paused",
		"customer.subscription.resumed",
		"checkout.session.completed":
		return s.handleSubscriptionEvent(ctx, event)
	case "invoice.payment_failed", "invoice.payment_succeeded", "charge.refunded":
		// Log-only events. Audit row already written.
		return nil
	default:
		return nil
	}
}

// handleSubscriptionEvent extracts the Stripe Subscription id from the event
// payload, retrieves the authoritative subscription, and calls applyStripeState.
func (s *Service) handleSubscriptionEvent(ctx context.Context, event *billingadapter.WebhookEvent) error {
	subID, userID, err := extractSubAndUser(event.Raw)
	if err != nil {
		return err
	}
	sub, err := s.provider.RetrieveSubscription(ctx, subID)
	if err != nil {
		return fmt.Errorf("retrieve sub for %s:\n%w", event.Type, err)
	}
	upd, err := s.stateUpdateFromStripe(userID, sub)
	if err != nil {
		return err
	}
	return s.ApplyStripeState(ctx, upd)
}

// extractSubAndUser parses {data:{object:{id, metadata:{userId}}}} or
// {data:{object:{subscription, customer, metadata:{userId}}}} from a webhook
// payload. Returns subscription id and user id.
func extractSubAndUser(raw []byte) (string, int64, error) {
	var env struct {
		Data struct {
			Object struct {
				ID           string            `json:"id"`
				Subscription string            `json:"subscription"`
				Metadata     map[string]string `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", 0, fmt.Errorf("decode webhook envelope:\n%w", err)
	}
	subID := env.Data.Object.Subscription
	if subID == "" {
		subID = env.Data.Object.ID
	}
	uidStr := env.Data.Object.Metadata["userId"]
	if uidStr == "" {
		return subID, 0, fmt.Errorf("webhook event missing userId metadata")
	}
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		return subID, 0, fmt.Errorf("parse userId %q:\n%w", uidStr, err)
	}
	return subID, uid, nil
}

// stateUpdateFromStripe projects a billing.Subscription onto a StateUpdate.
func (s *Service) stateUpdateFromStripe(userID int64, sub *billingadapter.Subscription) (StateUpdate, error) {
	plan, ok := s.prices.PlanFromPriceID(sub.PriceID)
	if !ok {
		return StateUpdate{}, fmt.Errorf("unknown stripe price id %q (userId=%d, subId=%s)", sub.PriceID, userID, sub.ID)
	}
	return StateUpdate{
		UserID:            userID,
		StripeCustomerID:  sub.CustomerID,
		StripeSubID:       sub.ID,
		Status:            Status(sub.Status),
		Plan:              plan,
		CurrentPeriodEnd:  sub.CurrentPeriodEnd,
		TrialEnd:          sub.TrialEnd,
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
		PausedAt:          sub.PausedAt,
	}, nil
}
```

- [ ] **Step 4: Run tests**

```
go test ./pkg/billing/ -run TestDispatch_SubscriptionUpdated -v
go test ./pkg/billing/ -run TestHandleWebhook -v
```

Expected: PASS for both.

- [ ] **Step 5: Commit**

```bash
git add pkg/billing/webhook.go pkg/billing/webhook_dispatch.go pkg/billing/webhook_dispatch_test.go
git commit -m "$(cat <<'EOF'
Spec C: dispatch core subscription webhooks

[+] handleSubscriptionEvent retrieves sub + applies state
[+] extractSubAndUser parses Stripe envelope for sub id + userId metadata
[+] stateUpdateFromStripe maps Stripe → local StateUpdate
[-] dispatch stub from Task 20
EOF
)"
```

### Task 22: Dispatch `customer.subscription.deleted` produces `canceled`

The previous task accepts deleted events into the same handler, but `deleted` events may omit `current_period_end`. Test specifically for the final state.

**Files:**
- Modify: `pkg/billing/webhook_dispatch_test.go`

- [ ] **Step 1: Add failing test**

```go
func TestDispatch_SubscriptionDeleted(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	fake := &testutil.FakeBilling{
		Event: &billing.WebhookEvent{
			ID: "evt_del", Type: "customer.subscription.deleted", Livemode: false,
			Raw: []byte(`{"id":"evt_del","data":{"object":{"id":"sub_X","metadata":{"userId":"` + strconv.FormatInt(u.ID, 10) + `"}}}}`),
		},
		Subscription: &billing.Subscription{
			ID: "sub_X", CustomerID: "cus_X",
			Status: "canceled", PriceID: "price_M",
		},
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	if err := svc.HandleWebhook(context.Background(), pkgbilling.WebhookConfig{
		ExpectLivemode: false, Body: fake.Event.Raw, Signature: "x",
	}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusCanceled {
		t.Fatalf("status %q want canceled", sub.Status)
	}
	if sub.IsActive(time.Now()) {
		t.Fatalf("canceled should be inactive")
	}
}
```

- [ ] **Step 2: Run test**

```
go test ./pkg/billing/ -run TestDispatch_SubscriptionDeleted -v
```

Expected: PASS — same code path handles it.

- [ ] **Step 3: Commit (test-only)**

```bash
git add pkg/billing/webhook_dispatch_test.go
git commit -m "$(cat <<'EOF'
Spec C: webhook test — subscription.deleted produces canceled

[+] TestDispatch_SubscriptionDeleted
EOF
)"
```

### Task 23: Webhook HTTP handler

**Files:**
- Modify: `api/handler/billing.go`
- Test: `api/handler/billing_webhook_test.go`

- [ ] **Step 1: Write the failing test**

```go
package handler_test

// imports: bytes, net/http, net/http/httptest, testing,
//   "studbud/backend/api/handler", "studbud/backend/internal/billing",
//   pkgbilling "studbud/backend/pkg/billing", "studbud/backend/testutil"

func TestBillingWebhook_LivemodeMismatchReturns400(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	fake := &testutil.FakeBilling{Event: &billing.WebhookEvent{
		ID: "evt_lm", Type: "customer.subscription.updated", Livemode: true,
		Raw: []byte(`{}`),
	}}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	h := handler.NewBillingHandler(svc, "https://app/billing", "https://app/pricing")
	h.SetStripeLivemode(false)

	req := httptest.NewRequest("POST", "/billing/webhook", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Stripe-Signature", "x")
	w := httptest.NewRecorder()
	h.Webhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./api/handler/ -run TestBillingWebhook_Livemode -v
```

Expected: FAIL — `SetStripeLivemode` and full webhook handler don't exist.

- [ ] **Step 3: Update `BillingHandler`**

Modify `api/handler/billing.go`:

Add field + setter:

```go
type BillingHandler struct {
	svc            *billing.Service
	billingPageURL string
	pricingPageURL string
	expectLive     bool // expectLive mirrors STRIPE_MODE=="live"
}

// SetStripeLivemode sets the expected livemode flag used in webhook validation.
func (h *BillingHandler) SetStripeLivemode(live bool) { h.expectLive = live }
```

Add `Webhook` method:

```go
// Webhook handles POST /billing/webhook.
// Public route: the request is authenticated by Stripe-Signature, not by JWT.
func (h *BillingHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "body_read_failed", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	cfg := billing.WebhookConfig{
		ExpectLivemode: h.expectLive,
		Body:           body,
		Signature:      r.Header.Get("Stripe-Signature"),
	}
	if err := h.svc.HandleWebhook(r.Context(), cfg); err != nil {
		if errors.Is(err, billing.ErrLivemodeMismatch) {
			http.Error(w, "livemode mismatch", http.StatusBadRequest)
			return
		}
		// Signature failure or downstream error.
		http.Error(w, "webhook error: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}
```

Add `io` to imports.

- [ ] **Step 4: Wire `SetStripeLivemode` in `routes.go`**

`routes.go` currently constructs `billH` twice (in `registerPublicRoutes` and `registerAuthSocialRoutes`). Update **both** call sites identically:

```go
billH := handler.NewBillingHandler(d.billing, d.cfg.AppURL+"/billing", d.cfg.AppURL+"/pricing")
billH.SetStripeLivemode(d.cfg.StripeMode == "live")
```

(A future refactor could stash a single handler on `deps` and reuse it; out of scope for Spec C.)

- [ ] **Step 5: Run test**

```
go test ./api/handler/ -run TestBillingWebhook -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/handler/billing.go api/handler/billing_webhook_test.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec C: POST /billing/webhook

[+] BillingHandler.Webhook reads body, verifies via service, 400 on livemode mismatch
[+] SetStripeLivemode setter wired from STRIPE_MODE
EOF
)"
```

---

## Phase 7 — Refresh endpoint + reconciliation cron

### Task 24: `RefreshFromStripe` service method

**Files:**
- Create: `pkg/billing/refresh.go`
- Test: `pkg/billing/refresh_test.go`

- [ ] **Step 1: Write the failing test**

```go
package billing_test

import (
	"context"
	"testing"
	"time"

	"studbud/backend/internal/billing"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestRefreshFromStripe_AppliesAuthoritativeState(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan, stripe_customer_id)
		 VALUES ($1, 'past_due', 'pro_monthly', 'cus_R')`, u.ID)

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	fake := &testutil.FakeBilling{
		Subscriptions: []billing.Subscription{{
			ID: "sub_R", CustomerID: "cus_R",
			Status: "active", PriceID: "price_M",
			CurrentPeriodEnd: &end,
		}},
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	if err := svc.RefreshFromStripe(context.Background(), u.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusActive {
		t.Fatalf("status = %q, want active", sub.Status)
	}
}

func TestRefreshFromStripe_NoCustomerNoOps(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	if err := svc.RefreshFromStripe(context.Background(), u.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run TestRefreshFromStripe -v
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`pkg/billing/refresh.go`:

```go
package billing

import (
	"context"
	"errors"
	"fmt"
)

// RefreshFromStripe pulls the user's authoritative subscription state from
// Stripe and applies it locally. No-ops if the user has no stripe_customer_id.
func (s *Service) RefreshFromStripe(ctx context.Context, uid int64) error {
	var cust string
	if err := s.db.QueryRow(ctx, sqlGetCustomerID, uid).Scan(&cust); err != nil {
		// No row at all → nothing to refresh.
		if errors.Is(err, ErrSubscriptionNotFound) {
			return nil
		}
	}
	if cust == "" {
		return nil
	}
	subs, err := s.provider.ListSubscriptionsByCustomer(ctx, cust, 1)
	if err != nil {
		return fmt.Errorf("list subs:\n%w", err)
	}
	if len(subs) == 0 {
		return nil
	}
	upd, err := s.stateUpdateFromStripe(uid, &subs[0])
	if err != nil {
		return err
	}
	return s.ApplyStripeState(ctx, upd)
}
```

- [ ] **Step 4: Run tests**

```
go test ./pkg/billing/ -run TestRefreshFromStripe -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/billing/refresh.go pkg/billing/refresh_test.go
git commit -m "$(cat <<'EOF'
Spec C: RefreshFromStripe re-reads authoritative state

[+] Service.RefreshFromStripe lists by customer + applies first sub
[+] no-ops when stripe_customer_id is empty
EOF
)"
```

### Task 25: `POST /billing/refresh` handler with rate limit

**Files:**
- Modify: `api/handler/billing.go`
- Test: `api/handler/billing_refresh_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBillingRefresh_RateLimitedAfter10PerMinute(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	h := handler.NewBillingHandler(svc, "https://app/billing", "https://app/pricing")

	signer := jwtsigner.NewSigner([]byte("0123456789abcdef0123456789abcdef"), "studbud", 0)
	tok, _ := signer.Sign(u.ID)

	chain := middleware.Auth(signer)(http.HandlerFunc(h.Refresh))
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("POST", "/billing/refresh", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		chain.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("call %d: status %d", i, w.Code)
		}
	}
	// 11th must 429
	req := httptest.NewRequest("POST", "/billing/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("11th: status %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./api/handler/ -run TestBillingRefresh -v
```

Expected: FAIL.

- [ ] **Step 3: Add per-user limiter + handler method**

In `api/handler/billing.go`:

```go
import (
	// ... existing imports ...
	"sync"
	"time"
	"golang.org/x/time/rate"
)

type BillingHandler struct {
	svc            *billing.Service
	billingPageURL string
	pricingPageURL string
	expectLive     bool
	limMu          sync.Mutex                  // limMu guards lim
	lim            map[int64]*rate.Limiter     // lim is per-user refresh limiter (10/min)
}

func NewBillingHandler(svc *billing.Service, billingPageURL, pricingPageURL string) *BillingHandler {
	return &BillingHandler{
		svc:            svc,
		billingPageURL: billingPageURL,
		pricingPageURL: pricingPageURL,
		lim:            map[int64]*rate.Limiter{},
	}
}

// limiterFor returns the rate.Limiter for uid, creating it if absent.
// 10 calls per minute, no burst beyond that.
func (h *BillingHandler) limiterFor(uid int64) *rate.Limiter {
	h.limMu.Lock()
	defer h.limMu.Unlock()
	l, ok := h.lim[uid]
	if !ok {
		l = rate.NewLimiter(rate.Every(time.Minute/10), 10)
		h.lim[uid] = l
	}
	return l
}

// Refresh handles POST /billing/refresh.
func (h *BillingHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	uid, ok := middleware.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, myErrors.ErrUnauthorized)
		return
	}
	if !h.limiterFor(uid).Allow() {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	if err := h.svc.RefreshFromStripe(r.Context(), uid); err != nil {
		httpx.WriteError(w, err)
		return
	}
	// Return the same payload as GET /billing/subscription (Task 27).
	// Until that exists, return a minimal OK.
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})
}
```

If `golang.org/x/time/rate` isn't already in `go.mod` (the keyword worker uses it per earlier audit, so it should be), `go mod tidy` after.

- [ ] **Step 4: Run test + verify build**

```
go build ./...
go test ./api/handler/ -run TestBillingRefresh -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/handler/billing.go api/handler/billing_refresh_test.go
git commit -m "$(cat <<'EOF'
Spec C: POST /billing/refresh with per-user 10/min limiter

[+] BillingHandler.Refresh + limiterFor
[+] returns 429 after 10 calls in a minute
EOF
)"
```

### Task 26: Reconciliation cron job

**Files:**
- Create: `pkg/billing/reconcile.go`
- Test: `pkg/billing/reconcile_test.go`
- Modify: `cmd/app/main.go` (or wherever `cron.Scheduler` jobs are registered)

- [ ] **Step 1: Write the failing test**

```go
package billing_test

import (
	"context"
	"testing"
	"time"

	"studbud/backend/internal/billing"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestReconcileOnce_CorrectsDriftedRow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan, stripe_customer_id, stripe_sub_id)
		 VALUES ($1, 'past_due', 'pro_monthly', 'cus_S', 'sub_S')`, u.ID)

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	fake := &testutil.FakeBilling{
		Subscription: &billing.Subscription{
			ID: "sub_S", CustomerID: "cus_S",
			Status: "active", PriceID: "price_M",
			CurrentPeriodEnd: &end,
		},
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})

	corrected, err := svc.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if corrected != 1 {
		t.Fatalf("corrected = %d, want 1", corrected)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusActive {
		t.Fatalf("status = %q, want active", sub.Status)
	}
}
```

The test asserts the count of corrected drifts. The existing fake `FakeBilling.RetrieveSubscription` always returns `f.Subscription` regardless of subID — that's fine for a single-row test. Update `testutil/stripe.go` if you want a per-subID lookup; for now this works.

Note: `ReconcileOnce` needs a way to associate `stripe_sub_id` with `user_id` since we look up by subID. The query `sqlListActiveStripeSubs` already returns both. The implementation uses each row's `user_id` directly.

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run TestReconcileOnce -v
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`pkg/billing/reconcile.go`:

```go
package billing

import (
	"context"
	"fmt"
)

// ReconcileOnce iterates every row with a non-nil stripe_sub_id, retrieves
// the authoritative state from Stripe, and applies any drift. Returns the
// number of rows corrected.
func (s *Service) ReconcileOnce(ctx context.Context) (int, error) {
	rows, err := s.db.Query(ctx, sqlListActiveStripeSubs)
	if err != nil {
		return 0, fmt.Errorf("list active subs:\n%w", err)
	}
	defer rows.Close()

	type pair struct{ uid int64; subID string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.uid, &p.subID); err != nil {
			return 0, fmt.Errorf("scan pair:\n%w", err)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("rows iterate:\n%w", err)
	}

	corrected := 0
	for _, p := range pairs {
		sub, err := s.provider.RetrieveSubscription(ctx, p.subID)
		if err != nil {
			// Skip individual failures; cron retries on next tick.
			continue
		}
		upd, err := s.stateUpdateFromStripe(p.uid, sub)
		if err != nil {
			continue
		}
		if err := s.ApplyStripeState(ctx, upd); err != nil {
			continue
		}
		corrected++
	}
	return corrected, nil
}
```

- [ ] **Step 4: Wire into the scheduler**

Look at how the keyword worker is started in `cmd/app/main.go` — it likely uses `inf.scheduler.Add(name, interval, func)` or similar. Add a registration:

```go
deps.scheduler.Add("billing-reconcile", 24*time.Hour, func(ctx context.Context) {
	if _, err := deps.billing.ReconcileOnce(ctx); err != nil {
		log.Printf("billing reconcile: %v", err)
	}
})
```

If the scheduler API differs, follow the existing keyword-worker scheduling pattern verbatim.

- [ ] **Step 5: Run tests + build**

```
go build ./...
go test ./pkg/billing/ -run TestReconcileOnce -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/billing/reconcile.go pkg/billing/reconcile_test.go cmd/app/main.go
git commit -m "$(cat <<'EOF'
Spec C: nightly billing reconciliation cron

[+] Service.ReconcileOnce iterates subs + applies drift
[+] cmd/app schedules billing-reconcile every 24h
EOF
)"
```

---

## Phase 8 — Read endpoints

### Task 27: `GET /billing/subscription`

**Files:**
- Modify: `api/handler/billing.go`
- Test: `api/handler/billing_subscription_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBillingGetSubscription_ReturnsNoneForNewUser(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	h := handler.NewBillingHandler(svc, "https://app/billing", "https://app/pricing")

	signer := jwtsigner.NewSigner([]byte("0123456789abcdef0123456789abcdef"), "studbud", 0)
	tok, _ := signer.Sign(u.ID)
	req := httptest.NewRequest("GET", "/billing/subscription", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.GetSubscription)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Status   string `json:"status"`
		IsActive bool   `json:"isActive"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Status != "none" || resp.IsActive {
		t.Fatalf("got %+v", resp)
	}
}

func TestBillingGetSubscription_TrialingActive(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	end := time.Now().Add(30 * 24 * time.Hour).UTC()
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan, current_period_end, trial_end)
		 VALUES ($1, 'trialing', 'pro_monthly', $2, $2)`, u.ID, end)

	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	h := handler.NewBillingHandler(svc, "https://app/billing", "https://app/pricing")
	signer := jwtsigner.NewSigner([]byte("0123456789abcdef0123456789abcdef"), "studbud", 0)
	tok, _ := signer.Sign(u.ID)
	req := httptest.NewRequest("GET", "/billing/subscription", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	middleware.Auth(signer)(http.HandlerFunc(h.GetSubscription)).ServeHTTP(w, req)

	var resp struct {
		Status            string `json:"status"`
		Plan              string `json:"plan"`
		IsActive          bool   `json:"isActive"`
		CancelAtPeriodEnd bool   `json:"cancelAtPeriodEnd"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Status != "trialing" || resp.Plan != "pro_monthly" || !resp.IsActive {
		t.Fatalf("got %+v", resp)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./api/handler/ -run TestBillingGetSubscription -v
```

Expected: FAIL.

- [ ] **Step 3: Implement**

In `api/handler/billing.go`:

```go
// subscriptionResponse is the JSON shape returned by GET /billing/subscription.
type subscriptionResponse struct {
	Status            string  `json:"status"`
	Plan              *string `json:"plan"`
	CurrentPeriodEnd  *string `json:"currentPeriodEnd"`
	TrialEnd          *string `json:"trialEnd"`
	CancelAtPeriodEnd bool    `json:"cancelAtPeriodEnd"`
	IsActive          bool    `json:"isActive"`
}

// GetSubscription handles GET /billing/subscription.
func (h *BillingHandler) GetSubscription(w http.ResponseWriter, r *http.Request) {
	uid, ok := middleware.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, myErrors.ErrUnauthorized)
		return
	}
	sub, err := h.svc.GetSubscription(r.Context(), uid)
	if errors.Is(err, billing.ErrSubscriptionNotFound) {
		httpx.WriteJSON(w, http.StatusOK, subscriptionResponse{Status: "none"})
		return
	}
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	resp := subscriptionResponse{
		Status:            string(sub.Status),
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
		IsActive:          sub.IsActive(time.Now()),
	}
	if sub.Plan != "" {
		p := string(sub.Plan)
		resp.Plan = &p
	}
	if sub.CurrentPeriodEnd != nil {
		ts := sub.CurrentPeriodEnd.UTC().Format(time.RFC3339)
		resp.CurrentPeriodEnd = &ts
	}
	if sub.TrialEnd != nil {
		ts := sub.TrialEnd.UTC().Format(time.RFC3339)
		resp.TrialEnd = &ts
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}
```

- [ ] **Step 4: Update Refresh to return the same shape**

Replace the `httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})` line in `Refresh` with a call back into the same response builder. Refactor:

```go
func (h *BillingHandler) writeSubscriptionResponse(ctx context.Context, w http.ResponseWriter, uid int64) {
	sub, err := h.svc.GetSubscription(ctx, uid)
	// ... same body as GetSubscription's success path ...
}
```

Use `writeSubscriptionResponse` from both `GetSubscription` and `Refresh`.

- [ ] **Step 5: Run test**

```
go test ./api/handler/ -run TestBillingGetSubscription -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/handler/billing.go api/handler/billing_subscription_test.go
git commit -m "$(cat <<'EOF'
Spec C: GET /billing/subscription

[+] BillingHandler.GetSubscription + subscriptionResponse shape
[+] 'none' status for users with no row
[&] Refresh returns the same shape
EOF
)"
```

### Task 28: `GET /billing/plans` (public)

**Files:**
- Modify: `api/handler/billing.go`
- Test: `api/handler/billing_plans_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBillingPlans_Public(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	h := handler.NewBillingHandler(svc, "https://app/billing", "https://app/pricing")

	req := httptest.NewRequest("GET", "/billing/plans", nil)
	w := httptest.NewRecorder()
	h.GetPlans(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"pro_monthly", "pro_annual", "6.99", "59.99"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./api/handler/ -run TestBillingPlans -v
```

Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// planTile describes one plan for the public pricing UI.
type planTile struct {
	Plan        string  `json:"plan"`
	PriceEur    float64 `json:"priceEur"`
	Interval    string  `json:"interval"`
	DiscountPct *int    `json:"discountPct,omitempty"`
}

// GetPlans handles GET /billing/plans.
// Public: prices are config-driven and safe to expose.
func (h *BillingHandler) GetPlans(w http.ResponseWriter, r *http.Request) {
	discount := 29
	tiles := []planTile{
		{Plan: "pro_monthly", PriceEur: 6.99, Interval: "month"},
		{Plan: "pro_annual", PriceEur: 59.99, Interval: "year", DiscountPct: &discount},
	}
	httpx.WriteJSON(w, http.StatusOK, tiles)
}
```

- [ ] **Step 4: Wire route in `routes.go` (public)**

Add to `registerPublicRoutes`:

```go
mux.HandleFunc("GET /billing/plans", billH.GetPlans)
```

- [ ] **Step 5: Run test**

```
go test ./api/handler/ -run TestBillingPlans -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/handler/billing.go api/handler/billing_plans_test.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec C: GET /billing/plans (public)

[+] BillingHandler.GetPlans returns pro_monthly + pro_annual tiles
[+] route registered as public
EOF
)"
```

### Task 29: Wire all the new routes

**Files:**
- Modify: `cmd/app/routes.go`

- [ ] **Step 1: Add the new authed routes**

In `registerAuthSocialRoutes` (where existing checkout/portal are), add:

```go
mux.Handle("GET /billing/subscription", auth(billH.GetSubscription))
mux.Handle("POST /billing/refresh", auth(billH.Refresh))
```

- [ ] **Step 2: Verify build**

```
go build ./...
```

Expected: clean.

- [ ] **Step 3: Run the full handler test suite**

```
go test ./api/handler/ -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec C: register GET /billing/subscription + POST /billing/refresh
EOF
)"
```

---

## Phase 9 — Admin endpoints

### Task 30: `GrantCompWithExpiry` service method

The existing `GrantComp(uid, active)` is a simple boolean toggle. Spec C adds an admin endpoint with `expiresAt` + `reason`. Add a new service method that handles the structured comp case and writes an audit row.

**Files:**
- Modify: `pkg/billing/service.go`
- Test: `pkg/billing/comp_test.go`

- [ ] **Step 1: Write the failing test**

```go
package billing_test

import (
	"context"
	"testing"
	"time"

	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestGrantCompWithExpiry_PersistsExpiry(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})

	expires := time.Now().Add(60 * 24 * time.Hour).UTC().Truncate(time.Second)
	if err := svc.GrantCompWithExpiry(context.Background(), pkgbilling.CompGrant{
		UserID: u.ID, ExpiresAt: &expires, Reason: "Beta tester", ActorUserID: 1,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusComped || sub.Plan != pkgbilling.PlanComp {
		t.Fatalf("got %+v", sub)
	}
	if sub.CurrentPeriodEnd == nil || !sub.CurrentPeriodEnd.Equal(expires) {
		t.Fatalf("expiry mismatch: got %v want %v", sub.CurrentPeriodEnd, expires)
	}
	// audit row written
	var count int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM billing_events WHERE event_type='admin_comp_granted' AND user_id=$1`, u.ID,
	).Scan(&count)
	if count != 1 {
		t.Fatalf("audit rows = %d, want 1", count)
	}
}

func TestRevokeComp_SetsCanceled(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	_ = svc.GrantCompWithExpiry(context.Background(), pkgbilling.CompGrant{
		UserID: u.ID, Reason: "init", ActorUserID: 1,
	})

	if err := svc.RevokeComp(context.Background(), pkgbilling.CompRevoke{
		UserID: u.ID, Reason: "support ticket", ActorUserID: 1,
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusCanceled {
		t.Fatalf("got %q", sub.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/billing/ -run "TestGrantCompWithExpiry|TestRevokeComp" -v
```

Expected: FAIL.

- [ ] **Step 3: Implement**

Add to `pkg/billing/service.go` (or create `pkg/billing/comp.go` for the new methods):

```go
// CompGrant captures the admin form values for /admin/comp-subscription POST.
type CompGrant struct {
	UserID      int64      // UserID is the recipient
	ExpiresAt   *time.Time // ExpiresAt is the comp expiry (nil = indefinite)
	Reason      string     // Reason is the operator-supplied note
	ActorUserID int64      // ActorUserID is the admin who granted the comp
}

// CompRevoke captures the admin form values for DELETE /admin/comp-subscription.
type CompRevoke struct {
	UserID      int64  // UserID is the target user
	Reason      string // Reason is the operator-supplied note
	ActorUserID int64  // ActorUserID is the admin who revoked
}

// GrantCompWithExpiry upserts a comp row with an optional expiry and writes
// an admin_comp_granted audit row. This is the structured Spec C admin path.
func (s *Service) GrantCompWithExpiry(ctx context.Context, g CompGrant) error {
	const upsert = `
        INSERT INTO user_subscriptions (user_id, plan, status, current_period_end, created_at, updated_at)
        VALUES ($1, 'comp', 'comped', $2, now(), now())
        ON CONFLICT (user_id) DO UPDATE SET
            plan = 'comp',
            status = 'comped',
            current_period_end = EXCLUDED.current_period_end,
            updated_at = now()
    `
	if _, err := s.db.Exec(ctx, upsert, g.UserID, g.ExpiresAt); err != nil {
		return fmt.Errorf("upsert comp:\n%w", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"reason":    g.Reason,
		"actor":     g.ActorUserID,
		"expiresAt": g.ExpiresAt,
	})
	if _, err := s.db.Exec(ctx, sqlInsertEvent,
		"", g.UserID, "admin_comp_granted", false, payload,
	); err != nil {
		return fmt.Errorf("insert audit:\n%w", err)
	}
	return nil
}

// RevokeComp sets the user's row to status='canceled' and writes an
// admin_comp_revoked audit row.
func (s *Service) RevokeComp(ctx context.Context, r CompRevoke) error {
	const upd = `
        UPDATE user_subscriptions
        SET status = 'canceled', updated_at = now()
        WHERE user_id = $1 AND plan = 'comp'
    `
	if _, err := s.db.Exec(ctx, upd, r.UserID); err != nil {
		return fmt.Errorf("revoke comp:\n%w", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"reason": r.Reason,
		"actor":  r.ActorUserID,
	})
	if _, err := s.db.Exec(ctx, sqlInsertEvent,
		"", r.UserID, "admin_comp_revoked", false, payload,
	); err != nil {
		return fmt.Errorf("insert audit:\n%w", err)
	}
	return nil
}
```

Add imports `"encoding/json"` and `"time"` to the file.

- [ ] **Step 4: Run tests**

```
go test ./pkg/billing/ -run "TestGrantCompWithExpiry|TestRevokeComp" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/billing/service.go pkg/billing/comp_test.go
git commit -m "$(cat <<'EOF'
Spec C: structured comp grant + revoke with audit row

[+] CompGrant, CompRevoke service inputs
[+] Service.GrantCompWithExpiry, Service.RevokeComp
[+] audit row pattern (admin_comp_granted / admin_comp_revoked)
EOF
)"
```

### Task 31: Admin handler `POST /admin/comp-subscription`

**Files:**
- Modify: `api/handler/admin_ai.go` (or create `api/handler/admin_billing.go` for clarity)
- Test: extend `api/handler/admin_ai_test.go` (or create `admin_billing_test.go`)

For clarity, create a dedicated file: `api/handler/admin_billing.go`.

- [ ] **Step 1: Write the failing test**

```go
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"studbud/backend/api/handler"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestAdminCompSubscription_Grant(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	admin := testutil.NewVerifiedUser(t, pool)
	testutil.MakeAdmin(t, pool, admin.ID)
	target := testutil.NewVerifiedUser(t, pool)

	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	acc := access.NewService(pool)
	h := handler.NewAdminBillingHandler(svc, acc)
	signer := jwtsigner.NewSigner([]byte("0123456789abcdef0123456789abcdef"), "studbud", 0)
	tok, _ := signer.Sign(admin.ID)

	body, _ := json.Marshal(map[string]any{
		"user_id":    target.ID,
		"reason":     "beta",
		"expires_at": time.Now().Add(60 * 24 * time.Hour).UTC().Format(time.RFC3339),
	})
	req := httptest.NewRequest("POST", "/admin/comp-subscription", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	middleware.Auth(signer)(middleware.RequireAdmin()(http.HandlerFunc(h.Grant))).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./api/handler/ -run TestAdminCompSubscription -v
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`api/handler/admin_billing.go`:

```go
package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"studbud/backend/internal/http/middleware"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/billing"
)

// AdminBillingHandler exposes Spec C admin endpoints.
type AdminBillingHandler struct {
	billing *billing.Service // billing owns the writes
	access  *access.Service  // access reads post-mutation state
}

// NewAdminBillingHandler constructs an AdminBillingHandler.
func NewAdminBillingHandler(b *billing.Service, a *access.Service) *AdminBillingHandler {
	return &AdminBillingHandler{billing: b, access: a}
}

// grantBody is the request body for POST /admin/comp-subscription.
type grantBody struct {
	UserID    int64   `json:"user_id"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	Reason    string  `json:"reason"`
}

// revokeBody is the request body for DELETE /admin/comp-subscription.
type revokeBody struct {
	UserID int64  `json:"user_id"`
	Reason string `json:"reason"`
}

// Grant handles POST /admin/comp-subscription.
func (h *AdminBillingHandler) Grant(w http.ResponseWriter, r *http.Request) {
	actor, _ := middleware.UserID(r.Context())
	var in grantBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "invalid_input", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	if in.UserID <= 0 {
		httpx.WriteError(w, &myErrors.AppError{Code: "validation", Field: "user_id", Wrapped: myErrors.ErrValidation})
		return
	}
	var expires *time.Time
	if in.ExpiresAt != nil && *in.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *in.ExpiresAt)
		if err != nil {
			httpx.WriteError(w, &myErrors.AppError{Code: "validation", Field: "expires_at", Wrapped: myErrors.ErrValidation})
			return
		}
		expires = &t
	}
	g := billing.CompGrant{UserID: in.UserID, ExpiresAt: expires, Reason: in.Reason, ActorUserID: actor}
	if err := h.billing.GrantCompWithExpiry(r.Context(), g); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ok, _ := h.access.HasAIAccess(r.Context(), in.UserID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"userId": in.UserID, "aiAccess": ok})
}

// Revoke handles DELETE /admin/comp-subscription.
func (h *AdminBillingHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	actor, _ := middleware.UserID(r.Context())
	var in revokeBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "invalid_input", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	if in.UserID <= 0 {
		httpx.WriteError(w, &myErrors.AppError{Code: "validation", Field: "user_id", Wrapped: myErrors.ErrValidation})
		return
	}
	if err := h.billing.RevokeComp(r.Context(), billing.CompRevoke{UserID: in.UserID, Reason: in.Reason, ActorUserID: actor}); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ok, _ := h.access.HasAIAccess(r.Context(), in.UserID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"userId": in.UserID, "aiAccess": ok})
}
```

- [ ] **Step 4: Register routes**

In `registerAdminRoutes` in `routes.go`:

```go
adminBillH := handler.NewAdminBillingHandler(d.billing, d.access)
mux.Handle("POST /admin/comp-subscription", adm(adminBillH.Grant))
mux.Handle("DELETE /admin/comp-subscription", adm(adminBillH.Revoke))
```

- [ ] **Step 5: Run tests + build**

```
go build ./...
go test ./api/handler/ -run TestAdminCompSubscription -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/handler/admin_billing.go api/handler/admin_billing_test.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec C: POST/DELETE /admin/comp-subscription

[+] AdminBillingHandler.Grant + Revoke
[+] routes registered under admin gate (auth + verified + admin)
EOF
)"
```

---

## Phase 10 — Frontend integration (Vue 3)

The frontend lives in `/Users/martonroux/Documents/WEB/studbud_3/studbud/`. Same TDD discipline — Testing Library + MSW for component tests.

### Task 32: Pinia store `stores/billing.ts`

**Files:**
- Create: `studbud/src/stores/billing.ts`
- Test: `studbud/src/stores/__tests__/billing.spec.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { setActivePinia, createPinia } from 'pinia';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { useBillingStore } from '../billing';

beforeEach(() => setActivePinia(createPinia()));

describe('billing store', () => {
  it('fetch() populates subscription', async () => {
    const fetchMock = vi.spyOn(global, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      status: 'trialing', plan: 'pro_monthly', isActive: true,
      currentPeriodEnd: '2026-06-10T00:00:00Z', trialEnd: '2026-06-10T00:00:00Z',
      cancelAtPeriodEnd: false,
    }), { status: 200 }));
    const s = useBillingStore();
    await s.fetch();
    expect(s.subscription?.status).toBe('trialing');
    expect(s.isActive).toBe(true);
    fetchMock.mockRestore();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
cd ../studbud && npm run test -- billing.spec
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`studbud/src/stores/billing.ts`:

```ts
import { defineStore } from 'pinia';
import { ref, computed } from 'vue';

export type Plan = 'pro_monthly' | 'pro_annual' | 'comp';
export type Status = 'none' | 'trialing' | 'active' | 'past_due' | 'paused' | 'canceled' | 'comped';

export interface Subscription {
  status: Status;
  plan: Plan | null;
  currentPeriodEnd: string | null;
  trialEnd: string | null;
  cancelAtPeriodEnd: boolean;
  isActive: boolean;
}

export interface PlanTile {
  plan: Plan;
  priceEur: number;
  interval: 'month' | 'year';
  discountPct?: number;
}

export const useBillingStore = defineStore('billing', () => {
  const subscription = ref<Subscription | null>(null);
  const plans = ref<PlanTile[] | null>(null);
  const loading = ref(false);
  const error = ref<string | null>(null);

  const isActive = computed(() => subscription.value?.isActive ?? false);

  async function fetch() {
    loading.value = true;
    error.value = null;
    try {
      const res = await window.fetch('/billing/subscription', { credentials: 'include' });
      if (!res.ok) throw new Error(`status ${res.status}`);
      subscription.value = await res.json();
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e);
    } finally {
      loading.value = false;
    }
  }

  async function refresh() {
    await window.fetch('/billing/refresh', { method: 'POST', credentials: 'include' });
    await fetch();
  }

  async function checkout(plan: Plan) {
    const res = await window.fetch('/billing/checkout', {
      method: 'POST', credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ plan }),
    });
    if (!res.ok) throw new Error(`status ${res.status}`);
    const { url } = await res.json();
    window.location.href = url;
  }

  async function portal() {
    const res = await window.fetch('/billing/portal', { method: 'POST', credentials: 'include' });
    if (!res.ok) throw new Error(`status ${res.status}`);
    const { url } = await res.json();
    window.location.href = url;
  }

  async function fetchPlans() {
    const res = await window.fetch('/billing/plans');
    plans.value = await res.json();
  }

  return { subscription, plans, loading, error, isActive, fetch, refresh, checkout, portal, fetchPlans };
});
```

- [ ] **Step 4: Run test**

```
cd ../studbud && npm run test -- billing.spec
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ../studbud/src/stores/billing.ts ../studbud/src/stores/__tests__/billing.spec.ts
git commit -m "$(cat <<'EOF'
Spec C: Pinia store stores/billing.ts

[+] Subscription, Plan, Status type aliases
[+] fetch, refresh, checkout, portal, fetchPlans actions
[+] isActive computed
EOF
)"
```

### Task 33: Update `PaywallCard.vue` for monthly/annual toggle + 30-day trial

**Files:**
- Modify: `studbud/src/components/ai/PaywallCard.vue`
- Test: `studbud/src/components/ai/__tests__/PaywallCard.spec.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/vue';
import { createPinia, setActivePinia } from 'pinia';
import PaywallCard from '../PaywallCard.vue';
import { useBillingStore } from '../../../stores/billing';

describe('PaywallCard', () => {
  it('clicking CTA calls billing.checkout with selected plan', async () => {
    setActivePinia(createPinia());
    const billing = useBillingStore();
    const spy = vi.spyOn(billing, 'checkout').mockResolvedValue();
    render(PaywallCard);
    await fireEvent.click(screen.getByRole('radio', { name: /annual/i }));
    await fireEvent.click(screen.getByRole('button', { name: /start 30-day trial/i }));
    expect(spy).toHaveBeenCalledWith('pro_annual');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
cd ../studbud && npm run test -- PaywallCard
```

Expected: FAIL.

- [ ] **Step 3: Implement**

Replace the contents of `PaywallCard.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue';
import { useBillingStore, type Plan } from '../../stores/billing';

const plan = ref<Plan>('pro_monthly');
const billing = useBillingStore();

async function start() {
  await billing.checkout(plan.value);
}
</script>

<template>
  <section class="paywall">
    <h3>Unlock AI features</h3>
    <div role="radiogroup" aria-label="Plan">
      <label>
        <input type="radio" name="plan" value="pro_monthly" v-model="plan" />
        <span>Monthly — €6.99/mo</span>
      </label>
      <label>
        <input type="radio" name="plan" value="pro_annual" v-model="plan" />
        <span>Annual — €59.99/yr (save 29%)</span>
      </label>
    </div>
    <button type="button" @click="start">Start 30-day trial</button>
  </section>
</template>
```

- [ ] **Step 4: Run test**

```
cd ../studbud && npm run test -- PaywallCard
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ../studbud/src/components/ai/PaywallCard.vue ../studbud/src/components/ai/__tests__/PaywallCard.spec.ts
git commit -m "$(cat <<'EOF'
Spec C: PaywallCard supports plan toggle + 30-day trial CTA

[+] monthly / annual radio group
[+] CTA wired to billingStore.checkout
EOF
)"
```

### Task 34: `PricingPage.vue` + route

**Files:**
- Create: `studbud/src/pages/PricingPage.vue`
- Modify: `studbud/src/router/index.ts` (or wherever routes are defined)
- Test: `studbud/src/pages/__tests__/PricingPage.spec.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/vue';
import { createPinia, setActivePinia } from 'pinia';
import PricingPage from '../PricingPage.vue';
import { useBillingStore } from '../../stores/billing';

describe('PricingPage', () => {
  it('fetches plans and renders monthly + annual tiles', async () => {
    setActivePinia(createPinia());
    const b = useBillingStore();
    vi.spyOn(b, 'fetchPlans').mockImplementation(async () => {
      b.plans = [
        { plan: 'pro_monthly', priceEur: 6.99, interval: 'month' },
        { plan: 'pro_annual',  priceEur: 59.99, interval: 'year', discountPct: 29 },
      ];
    });
    const spy = vi.spyOn(b, 'checkout').mockResolvedValue();
    render(PricingPage);
    await waitFor(() => screen.getByText(/€6\.99/));
    await fireEvent.click(screen.getAllByRole('button', { name: /start 30-day trial/i })[0]);
    expect(spy).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
cd ../studbud && npm run test -- PricingPage
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`studbud/src/pages/PricingPage.vue`:

```vue
<script setup lang="ts">
import { onMounted } from 'vue';
import { useBillingStore, type Plan } from '../stores/billing';

const billing = useBillingStore();

onMounted(() => billing.fetchPlans());

async function start(plan: Plan) {
  await billing.checkout(plan);
}
</script>

<template>
  <main class="pricing">
    <h1>Choose your plan</h1>
    <ul v-if="billing.plans">
      <li v-for="tile in billing.plans" :key="tile.plan">
        <h2>{{ tile.plan === 'pro_monthly' ? 'Monthly' : 'Annual' }}</h2>
        <p>€{{ tile.priceEur }} / {{ tile.interval }}</p>
        <p v-if="tile.discountPct">Save {{ tile.discountPct }}%</p>
        <button type="button" @click="start(tile.plan)">Start 30-day trial</button>
      </li>
    </ul>
  </main>
</template>
```

- [ ] **Step 4: Register the route**

In the Vue router (e.g. `studbud/src/router/index.ts`):

```ts
{ path: '/pricing', component: () => import('../pages/PricingPage.vue'), meta: { public: true } },
```

- [ ] **Step 5: Run test**

```
cd ../studbud && npm run test -- PricingPage
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ../studbud/src/pages/PricingPage.vue ../studbud/src/router ../studbud/src/pages/__tests__/PricingPage.spec.ts
git commit -m "$(cat <<'EOF'
Spec C: /pricing public page

[+] PricingPage renders plan tiles from /billing/plans
[+] CTA → billingStore.checkout
[+] route registered as public
EOF
)"
```

### Task 35: `BillingPage.vue` + banners + route

**Files:**
- Create: `studbud/src/pages/BillingPage.vue`
- Modify: `studbud/src/router/index.ts`
- Test: `studbud/src/pages/__tests__/BillingPage.spec.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/vue';
import { createPinia, setActivePinia } from 'pinia';
import BillingPage from '../BillingPage.vue';
import { useBillingStore } from '../../stores/billing';

describe('BillingPage', () => {
  it('renders past_due banner when status is past_due', async () => {
    setActivePinia(createPinia());
    const b = useBillingStore();
    vi.spyOn(b, 'fetch').mockImplementation(async () => {
      b.subscription = {
        status: 'past_due', plan: 'pro_monthly',
        currentPeriodEnd: '2026-06-01T00:00:00Z', trialEnd: null,
        cancelAtPeriodEnd: false, isActive: false,
      };
    });
    render(BillingPage);
    await waitFor(() => screen.getByText(/payment failed/i));
  });

  it('renders trialing banner with days remaining', async () => {
    setActivePinia(createPinia());
    const b = useBillingStore();
    const end = new Date(Date.now() + 30 * 86400000).toISOString();
    vi.spyOn(b, 'fetch').mockImplementation(async () => {
      b.subscription = {
        status: 'trialing', plan: 'pro_monthly',
        currentPeriodEnd: end, trialEnd: end,
        cancelAtPeriodEnd: false, isActive: true,
      };
    });
    render(BillingPage);
    await waitFor(() => screen.getByText(/free trial/i));
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
cd ../studbud && npm run test -- BillingPage
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`studbud/src/pages/BillingPage.vue`:

```vue
<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { useBillingStore } from '../stores/billing';
import { useRoute } from 'vue-router';

const billing = useBillingStore();
const route = useRoute();

onMounted(async () => {
  await billing.fetch();
  if (route.query.status === 'success') await billing.refresh();
});

const banner = computed(() => {
  const s = billing.subscription;
  if (!s || s.status === 'none') return { kind: 'free', tone: 'gray' };
  if (s.status === 'past_due') return { kind: 'past_due', tone: 'red' };
  if (s.status === 'paused') return { kind: 'paused', tone: 'red' };
  if (s.cancelAtPeriodEnd) return { kind: 'cancel_at_period_end', tone: 'orange' };
  if (s.status === 'comped') return { kind: 'comped', tone: 'neutral' };
  if (s.status === 'trialing') return { kind: 'trialing', tone: 'blue' };
  if (s.status === 'active') return { kind: 'active', tone: 'green' };
  return { kind: 'free', tone: 'gray' };
});

const daysUntil = (iso: string | null): number => {
  if (!iso) return 0;
  return Math.max(0, Math.ceil((new Date(iso).getTime() - Date.now()) / 86400000));
};
</script>

<template>
  <main class="billing">
    <h1>Subscription</h1>
    <div :class="['banner', `tone-${banner.tone}`]">
      <p v-if="banner.kind === 'past_due'">Payment failed. Stripe is retrying — update your card to restore AI access sooner.</p>
      <p v-else-if="banner.kind === 'paused'">Subscription paused. Contact support to resume.</p>
      <p v-else-if="banner.kind === 'cancel_at_period_end'">Your Pro access ends on {{ billing.subscription?.currentPeriodEnd?.slice(0, 10) }}.</p>
      <p v-else-if="banner.kind === 'comped'">Complimentary access.</p>
      <p v-else-if="banner.kind === 'trialing'">Free trial — {{ daysUntil(billing.subscription?.trialEnd ?? null) }} days remaining.</p>
      <p v-else-if="banner.kind === 'active'">Pro — renews on {{ billing.subscription?.currentPeriodEnd?.slice(0, 10) }}.</p>
      <p v-else>You're on the free plan.</p>
    </div>
    <button v-if="billing.subscription?.status !== 'none'" type="button" @click="billing.portal()">Manage subscription</button>
    <button v-else type="button" @click="$router.push('/pricing')">See pricing</button>
    <button type="button" @click="billing.refresh()">Refresh status</button>
  </main>
</template>
```

- [ ] **Step 4: Register route**

```ts
{ path: '/billing', component: () => import('../pages/BillingPage.vue'), meta: { auth: true } },
```

- [ ] **Step 5: Run test**

```
cd ../studbud && npm run test -- BillingPage
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ../studbud/src/pages/BillingPage.vue ../studbud/src/router ../studbud/src/pages/__tests__/BillingPage.spec.ts
git commit -m "$(cat <<'EOF'
Spec C: /billing authed page with status banners

[+] BillingPage with past_due, paused, cancel_at_period_end, comped, trialing, active banners
[+] Manage / Refresh buttons
[+] post-checkout success triggers refresh()
EOF
)"
```

### Task 36: Navigation integration (Profile + QuotaBadge)

**Files:**
- Modify: `studbud/src/pages/ProfilePage.vue` (or wherever profile lives)
- Modify: `studbud/src/components/ai/QuotaBadge.vue`

- [ ] **Step 1: Profile — add "Billing" row**

Add a row in `ProfilePage.vue` that links to `/billing` when `billingStore.subscription?.status !== 'none'`, otherwise "Upgrade to Pro" links to `/pricing`.

```vue
<router-link v-if="billingStore.subscription?.status !== 'none'" to="/billing">Billing</router-link>
<router-link v-else to="/pricing">Upgrade to Pro</router-link>
```

(Adapt to the existing Profile layout — model on the existing rows.)

- [ ] **Step 2: QuotaBadge — tap-through**

In `QuotaBadge.vue`, make the badge clickable:

```vue
<button type="button" @click="onClick">{{ /* existing display */ }}</button>
```

```ts
import { useBillingStore } from '../../stores/billing';
import { useRouter } from 'vue-router';
const billing = useBillingStore();
const router = useRouter();
function onClick() {
  router.push(billing.isActive ? '/billing' : '/pricing');
}
```

- [ ] **Step 3: Verify build**

```
cd ../studbud && npm run build
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add ../studbud/src/pages/ProfilePage.vue ../studbud/src/components/ai/QuotaBadge.vue
git commit -m "$(cat <<'EOF'
Spec C: navigation entry points

[+] Profile shows Billing or Upgrade-to-Pro depending on status
[+] QuotaBadge tap routes to /billing or /pricing
EOF
)"
```

---

## Phase 11 — Documentation + final pass

### Task 37: OpenAPI updates

**Files:**
- Modify: `api/handler/docs_openapi.yaml`

- [ ] **Step 1: Add Spec C paths**

Add (or update existing stub entries for):

- `POST /billing/checkout` — body `{ plan: pro_monthly | pro_annual }`, returns `{ url }`. 409 `already_subscribed`. 422 `unknown_plan`.
- `POST /billing/portal` — empty body, returns `{ url }`. 404 `no_customer`.
- `POST /billing/webhook` — public, Stripe-Signature header, returns 200.
- `POST /billing/refresh` — empty body, returns the same shape as `GET /billing/subscription`. 429 when rate-limited.
- `GET /billing/subscription` — returns the `Subscription` shape (see store types in §8.5 of spec).
- `GET /billing/plans` — public, returns `[{ plan, priceEur, interval, discountPct? }]`.
- `POST /admin/comp-subscription` — admin-only, body `{ user_id, expires_at?, reason }`, returns `{ userId, aiAccess }`.
- `DELETE /admin/comp-subscription` — admin-only, body `{ user_id, reason }`, returns `{ userId, aiAccess }`.

Schema definitions: add `Subscription`, `PlanTile`, `CompGrantInput`, `CompRevokeInput` under `components.schemas`. Mirror the JSON tags from the Go types.

- [ ] **Step 2: Verify the docs handler still serves**

```
go test ./api/handler/ -run TestDocs -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add api/handler/docs_openapi.yaml
git commit -m "$(cat <<'EOF'
Spec C: OpenAPI updates for billing surface

[+] /billing/checkout, /billing/portal, /billing/webhook
[+] /billing/refresh, /billing/subscription, /billing/plans
[+] /admin/comp-subscription POST + DELETE
[+] Subscription, PlanTile, CompGrantInput, CompRevokeInput schemas
EOF
)"
```

### Task 38: `.env.example` additions

**Files:**
- Modify: `.env.example` (or wherever sample env is — look for existing `STRIPE_PRICE_*` entries; if none, create the file)

- [ ] **Step 1: Add Spec C env vars**

```
# --- Stripe (Spec C) ---
# Mode: "test" (dev/staging) or "live" (prod only)
STRIPE_MODE=test
# Secret key must match mode (sk_test_* in test, sk_live_* in live)
STRIPE_SECRET_KEY=
# Webhook signing secret (separate per mode)
STRIPE_WEBHOOK_SECRET=
# Price IDs created in the Stripe dashboard
STRIPE_PRICE_PRO_MONTHLY=
STRIPE_PRICE_PRO_ANNUAL=
# App URL used for Stripe redirects (falls back to FRONTEND_URL)
APP_URL=
```

- [ ] **Step 2: Commit**

```bash
git add .env.example
git commit -m "$(cat <<'EOF'
Spec C: .env.example documents Stripe vars

[+] STRIPE_MODE, STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET
[+] STRIPE_PRICE_PRO_MONTHLY, STRIPE_PRICE_PRO_ANNUAL, APP_URL
EOF
)"
```

### Task 39: Full-suite verification + lint

**Files:** none (verification only)

- [ ] **Step 1: Run the backend test suite**

```
make test
```

Expected: all PASS.

- [ ] **Step 2: Build the whole module**

```
go build ./...
go vet ./...
```

Expected: clean.

- [ ] **Step 3: Confirm `go.mod` has no `replace` lines**

```
grep -n "^replace" go.mod
```

Expected: no output.

- [ ] **Step 4: Run the frontend test suite**

```
cd ../studbud && npm run test
```

Expected: all PASS.

- [ ] **Step 5: Run the frontend build**

```
cd ../studbud && npm run build
```

Expected: clean.

- [ ] **Step 6: Manual QA (Stripe test mode)**

Walk through these manually with Stripe CLI + test keys:

1. **New trial signup:** click "Start 30-day trial" → check out via `4242 4242 4242 4242` → verify `/billing` shows trialing banner with "30 days remaining" → confirm AI generation works.
2. **Trial → paid auto-conversion:** Stripe CLI `trigger customer.subscription.updated` with `status=active` → verify banner flips to Active.
3. **Payment failure / Smart Retries:** `stripe trigger invoice.payment_failed` → companion `customer.subscription.updated` with `status=past_due` → verify red past_due banner + AI access blocked → simulate retry success → verify access restored.
4. **Mid-trial cancel:** portal → cancel → verify `cancel_at_period_end` banner (orange) + access retained until period end.
5. **Admin comp:** `POST /admin/comp-subscription` with admin token → verify target user gets comped status + `user_has_ai_access` returns true.
6. **Livemode guard:** point a test webhook payload at a `STRIPE_MODE=live` deploy → verify 400 + structured log + no state change.

No commit needed for this task — it's verification only. Note any issues in a follow-up ticket.

### Task 40: Update the project CLAUDE.md with Spec C completion note

**Files:**
- Modify: `docs/CLAUDE.md` (only if it has a feature-status section; otherwise skip)

- [ ] **Step 1: Check if CLAUDE.md mentions Spec C status**

```
grep -n "Spec C\|spec-c\|billing" docs/CLAUDE.md
```

If there's no Spec C reference, skip to Step 3 (no commit needed).

- [ ] **Step 2: Update the relevant line**

If a status line exists, mark Spec C as shipped with the date.

- [ ] **Step 3: Final commit (if Step 2 modified the file)**

```bash
git add docs/CLAUDE.md
git commit -m "$(cat <<'EOF'
Spec C: mark subscription billing as shipped
EOF
)"
```

---

## Implementer notes

**Order:** Tasks are written to run sequentially. Each task is independent enough that you can stop at any boundary and re-pick later; the test you just wrote will be the next checkpoint.

**Frontend split:** Tasks 32–36 modify the Vue codebase under `studbud/`, not `backend/`. The path is sibling: `/Users/martonroux/Documents/WEB/studbud_3/studbud/`. Commits for those tasks live in the **frontend** git repository, not the backend one. Switch directories appropriately.

**Test infra:** Backend tests use real Postgres via `testutil.OpenTestDB`. The first invocation runs every `setup*.go` migration. After Task 1's `setup_billing.go` rewrite, drop the test DB once so the new schema is created from scratch: `dropdb studbud_test && createdb studbud_test`.

**`middleware.UserID` vs `middleware.UserEmail`:** the existing middleware exposes `UserID(ctx) (int64, bool)`. If it does not expose `UserEmail`, fetch the user's email inside the Checkout handler via `pkg/user.Service.GetByID` (or whichever read helper exists). Adjust Task 18's handler accordingly.

**Stripe SDK gotchas:**
- The Go SDK's `subscription.List` is a paged iterator; iterate via `iter.Next()` / `iter.Subscription()`.
- The `client.API.Init` call signature in v76 takes `(secretKey, backends)`; passing `nil` for backends is fine.
- `webhook.ConstructEvent` returns `stripe.Event`; we only use `ID`, `Type`, `Livemode`. The body must be the raw bytes before any middleware decoded them — `MaxBytesReader` is fine, but don't `json.Decode` the body before signature verification.

**Rate limiter cleanup:** Task 25's per-user limiter map grows unbounded over time. Acceptable for v1 (10/min × N users is small). If it becomes a concern, evict entries idle for >1h in a separate task.

**Why no scaffold `users.ai_subscription_active` drop:** the Spec A audit confirmed this column never made it past the scaffold phase; the AI pipeline already reads via `user_has_ai_access()`. Task 1's schema rewrite leaves nothing to drop.

**Manual QA depends on real Stripe:** Tasks 1–36 are fully testable offline (real Postgres, fake Stripe client). Task 39's Step 6 needs a Stripe test account + `stripe-cli`. If the implementer doesn't have credentials, capture the manual-QA scenarios in a follow-up ticket and ship Tasks 1–38; this is acceptable.

**Observability (Spec §11) — deferred:** The spec lists six `billing_*_total` counters and a structured-log shape for state transitions. The repo doesn't have a metrics framework today (only `middleware.Logger()` for HTTP logs), and the SQL probes in §11 already cover the v1 ops needs. Ship Spec C without the counters; track adding them as a follow-up alongside the first prod incident that demands them. If you want a low-effort win during implementation, add a `log.Printf` in `ApplyStripeState` that prints `from_status → to_status` for every transition — useful for incident retros and trivially `grep`-able in `journalctl`.

**One source of truth, two access endpoints:** Spec A's existing `POST /admin/grant-ai-access` (boolean toggle) and Spec C's new `POST /admin/comp-subscription` (structured) both write `comped` rows. Keep both — the boolean endpoint stays the dev/QA quick-flip; the structured one is the auditable admin path. Tasks 1–3 cover the status literal migration; Tasks 30–31 add the new endpoint.

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
// Idempotent for fresh databases: re-running on an existing schema is a no-op.
//
// NOTE (Spec C §4.4): this function uses CREATE TABLE IF NOT EXISTS, which does
// NOT alter pre-existing tables. Production deployments that previously ran the
// Spec A scaffold of these tables must drop both tables (or run a manual ALTER
// script) before bringing up this schema. Pre-launch, the project drops+recreates
// the DB on each release, so no migration is required.
func setupBilling(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, billingSchema); err != nil {
		return fmt.Errorf("exec billing schema:\n%w", err)
	}
	return nil
}

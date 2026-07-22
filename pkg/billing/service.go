package billing

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	billingadapter "studdle/backend/internal/billing"
)

// Service wraps the billing provider (Stripe in prod, fake in tests).
// Spec C fills in the real flows.
type Service struct {
	db       *pgxpool.Pool         // db is the shared pool
	provider billingadapter.Client // provider is the underlying billing adapter
	prices   PriceMap              // prices maps Stripe price ids ↔ local Plan
}

// NewService constructs a Service with a PriceMap.
func NewService(db *pgxpool.Pool, provider billingadapter.Client, prices PriceMap) *Service {
	return &Service{db: db, provider: provider, prices: prices}
}

// Prices exposes the PriceMap to callers (handlers) that need to read it.
func (s *Service) Prices() PriceMap { return s.prices }

// GrantComp inserts or revokes a complimentary (comp) AI subscription for uid.
// active=true upserts a row with plan='comp', status='comped'.
// active=false marks any existing comp row as status='canceled'.
// Leaves Stripe-originated rows (plan='pro_monthly' / 'pro_annual') untouched.
func (s *Service) GrantComp(ctx context.Context, uid int64, active bool) error {
	if active {
		return s.upsertComp(ctx, uid)
	}
	return s.cancelComp(ctx, uid)
}

// upsertComp inserts a comp row if absent, or resets an existing comp row to status='comped'.
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

// insertComp inserts a fresh comp row for uid (called only when no comp row exists).
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

// cancelComp marks any existing comp row for uid as status='canceled'.
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

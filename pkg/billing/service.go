package billing

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	billingadapter "studbud/backend/internal/billing"
	"studbud/backend/internal/myErrors"
)

// Service wraps the billing provider (Stripe in prod, fake in tests).
// Spec C fills in the real flows.
type Service struct {
	db       *pgxpool.Pool         // db is the shared pool
	provider billingadapter.Client // provider is the underlying billing adapter
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, provider billingadapter.Client) *Service {
	return &Service{db: db, provider: provider}
}

// CreateCheckoutSession returns a URL the user must visit to pay.
// Stub: not implemented until Spec C.
func (s *Service) CreateCheckoutSession(ctx context.Context, uid int64, tier string) (string, error) {
	return "", myErrors.ErrNotImplemented
}

// CreatePortalSession returns a URL for the Stripe customer portal.
func (s *Service) CreatePortalSession(ctx context.Context, uid int64) (string, error) {
	return "", myErrors.ErrNotImplemented
}

// HandleWebhook processes a Stripe webhook payload.
func (s *Service) HandleWebhook(ctx context.Context, signature string, body []byte) error {
	return myErrors.ErrNotImplemented
}

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

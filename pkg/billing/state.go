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

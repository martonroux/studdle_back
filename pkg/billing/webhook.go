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
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return false, nil
	}
	return false, fmt.Errorf("record event:\n%w", err)
}


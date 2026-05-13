package billing

import (
	"context"
	"fmt"
)

// RefreshFromStripe pulls the user's authoritative subscription state from
// Stripe and applies it locally. No-ops if the user has no stripe_customer_id.
func (s *Service) RefreshFromStripe(ctx context.Context, uid int64) error {
	var cust string
	err := s.db.QueryRow(ctx, sqlGetCustomerID, uid).Scan(&cust)
	if err != nil {
		// pgx.ErrNoRows or anything else: treat as "no customer to refresh".
		return nil
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

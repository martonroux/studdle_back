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

	type pair struct {
		uid   int64
		subID string
	}
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

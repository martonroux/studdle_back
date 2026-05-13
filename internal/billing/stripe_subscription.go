package billing

import (
	"context"
	"fmt"
	"time"

	stripe "github.com/stripe/stripe-go/v76"
	stripesubscription "github.com/stripe/stripe-go/v76/subscription"
)

// RetrieveSubscription fetches a single Stripe subscription by ID and
// returns a provider-agnostic Subscription snapshot.
func (c *StripeClient) RetrieveSubscription(ctx context.Context, subID string) (*Subscription, error) {
	s, err := stripesubscription.Get(subID, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe retrieve subscription:\n%w", err)
	}
	return projectSubscription(s), nil
}

// ListSubscriptionsByCustomer returns up to limit active subscriptions for
// the given Stripe customer ID, most recently created first.
func (c *StripeClient) ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]Subscription, error) {
	params := &stripe.SubscriptionListParams{
		Customer: stripe.String(customerID),
	}
	params.Limit = stripe.Int64(int64(limit))

	iter := stripesubscription.List(params)
	var subs []Subscription
	for iter.Next() {
		subs = append(subs, *projectSubscription(iter.Subscription()))
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("stripe list subscriptions:\n%w", err)
	}
	return subs, nil
}

// projectSubscription converts a stripe.Subscription to the local Subscription type.
func projectSubscription(s *stripe.Subscription) *Subscription {
	sub := &Subscription{
		ID:                s.ID,
		Status:            string(s.Status),
		CancelAtPeriodEnd: s.CancelAtPeriodEnd,
		Livemode:          s.Livemode,
	}

	if s.Customer != nil {
		sub.CustomerID = s.Customer.ID
	}

	if s.Items != nil && len(s.Items.Data) > 0 && s.Items.Data[0].Price != nil {
		sub.PriceID = s.Items.Data[0].Price.ID
	}

	if s.CurrentPeriodEnd != 0 {
		t := time.Unix(s.CurrentPeriodEnd, 0)
		sub.CurrentPeriodEnd = &t
	}

	if s.TrialEnd != 0 {
		t := time.Unix(s.TrialEnd, 0)
		sub.TrialEnd = &t
	}

	if s.PauseCollection != nil {
		t := time.Unix(s.Created, 0)
		sub.PausedAt = &t
	}

	return sub
}

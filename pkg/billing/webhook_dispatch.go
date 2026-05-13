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
		return nil
	default:
		return nil
	}
}

// handleSubscriptionEvent extracts the Stripe Subscription id from the event
// payload, retrieves the authoritative subscription, and calls ApplyStripeState.
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

// extractSubAndUser parses {data:{object:{id, metadata:{user_id}}}} or
// {data:{object:{subscription, metadata:{user_id}}}} from a webhook payload.
// Returns subscription id and user id.
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
	uidStr := env.Data.Object.Metadata["user_id"]
	if uidStr == "" {
		return subID, 0, fmt.Errorf("webhook event missing user_id metadata")
	}
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		return subID, 0, fmt.Errorf("parse user_id %q:\n%w", uidStr, err)
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

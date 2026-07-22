package billing

import (
	"context"
	"errors"
	"fmt"

	billingadapter "studdle/backend/internal/billing"
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

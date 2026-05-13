package billing

import (
	"context"
	"fmt"
	"strconv"

	stripe "github.com/stripe/stripe-go/v76"
	portalsession "github.com/stripe/stripe-go/v76/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v76/checkout/session"
)

// CreateCheckout opens a Stripe Checkout session for the given input.
// It attaches the existing customer, pre-fills the price, and optionally
// applies a trial period when in.TrialDays > 0.
func (c *StripeClient) CreateCheckout(ctx context.Context, in CheckoutInput) (*CheckoutSession, error) {
	subData := &stripe.CheckoutSessionSubscriptionDataParams{}
	if in.TrialDays > 0 {
		subData.TrialPeriodDays = stripe.Int64(int64(in.TrialDays))
	}

	params := &stripe.CheckoutSessionParams{
		Customer: stripe.String(in.CustomerID),
		Mode:     stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(in.PriceID), Quantity: stripe.Int64(1)},
		},
		SuccessURL:        stripe.String(in.SuccessURL),
		CancelURL:         stripe.String(in.CancelURL),
		ClientReferenceID: stripe.String(strconv.FormatInt(in.UserID, 10)),
		SubscriptionData:  subData,
	}

	s, err := checkoutsession.New(params)
	if err != nil {
		return nil, fmt.Errorf("stripe checkout session:\n%w", err)
	}
	return &CheckoutSession{URL: s.URL, ID: s.ID}, nil
}

// CreatePortal opens a Stripe Customer Portal session for the given customer.
func (c *StripeClient) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(stripeCustomerID),
		ReturnURL: stripe.String(returnURL),
	}
	s, err := portalsession.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe portal session:\n%w", err)
	}
	return s.URL, nil
}

package billing

import (
	"context"
	"fmt"

	stripe "github.com/stripe/stripe-go/v76"
	portalsession "github.com/stripe/stripe-go/v76/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v76/checkout/session"
	stripewebhook "github.com/stripe/stripe-go/v76/webhook"
)

// StripeClient implements Client using the Stripe API.
type StripeClient struct {
	secretKey      string
	webhookSecret  string
	priceProMonth  string
	priceProAnnual string
	successURL     string
	cancelURL      string
}

// NewStripeClient initialises a StripeClient and sets the global Stripe key.
func NewStripeClient(secretKey, webhookSecret, priceProMonth, priceProAnnual, appURL string) *StripeClient {
	stripe.Key = secretKey
	return &StripeClient{
		secretKey:      secretKey,
		webhookSecret:  webhookSecret,
		priceProMonth:  priceProMonth,
		priceProAnnual: priceProAnnual,
		successURL:     appURL + "/billing/success",
		cancelURL:      appURL + "/billing/cancel",
	}
}

// CreateCheckout opens a Stripe Checkout session for the given user and price.
func (c *StripeClient) CreateCheckout(ctx context.Context, uid int64, priceID string) (*CheckoutSession, error) {
	params := &stripe.CheckoutSessionParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
		SuccessURL:        stripe.String(c.successURL),
		CancelURL:         stripe.String(c.cancelURL),
		ClientReferenceID: stripe.String(fmt.Sprintf("%d", uid)),
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

// VerifyWebhook validates the Stripe-Signature header and returns the event payload.
func (c *StripeClient) VerifyWebhook(payload []byte, signature string) error {
	_, err := stripewebhook.ConstructEvent(payload, signature, c.webhookSecret)
	if err != nil {
		return fmt.Errorf("stripe webhook signature:\n%w", err)
	}
	return nil
}

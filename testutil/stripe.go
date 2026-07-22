package testutil

import (
	"context"

	"studdle/backend/internal/billing"
)

// FakeBilling is a test double for billing.Client.
type FakeBilling struct {
	CheckoutURL   string
	PortalURL     string
	CustomerID    string
	Subscription  *billing.Subscription
	Subscriptions []billing.Subscription
	Event         *billing.WebhookEvent
	WebhookErr    error
	Price         billing.PriceData
}

// CreateCustomer returns the configured CustomerID or a default test value.
func (f *FakeBilling) CreateCustomer(ctx context.Context, email string, userID int64) (string, error) {
	if f.CustomerID == "" {
		return "cus_test_fake", nil
	}
	return f.CustomerID, nil
}

// CreateCheckout returns a canned CheckoutSession.
func (f *FakeBilling) CreateCheckout(ctx context.Context, in billing.CheckoutInput) (*billing.CheckoutSession, error) {
	return &billing.CheckoutSession{URL: f.CheckoutURL, ID: "cs_test"}, nil
}

// CreatePortal returns the configured portal URL.
func (f *FakeBilling) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	return f.PortalURL, nil
}

// RetrieveSubscription returns the configured Subscription.
func (f *FakeBilling) RetrieveSubscription(ctx context.Context, subID string) (*billing.Subscription, error) {
	return f.Subscription, nil
}

// ListSubscriptionsByCustomer returns the configured Subscriptions slice.
func (f *FakeBilling) ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]billing.Subscription, error) {
	return f.Subscriptions, nil
}

// ConstructWebhookEvent returns the configured Event and WebhookErr.
func (f *FakeBilling) ConstructWebhookEvent(payload []byte, signature string) (*billing.WebhookEvent, error) {
	return f.Event, f.WebhookErr
}

// GetPrice returns the configured PriceData regardless of priceID.
func (f *FakeBilling) GetPrice(ctx context.Context, priceID string) (billing.PriceData, error) {
	return f.Price, nil
}

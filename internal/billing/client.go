package billing

import (
	"context"
	"time"

	"studdle/backend/internal/myErrors"
)

// CheckoutSession is what the frontend redirects a user to.
type CheckoutSession struct {
	URL string
	ID  string
}

// Subscription is the provider-agnostic snapshot returned by RetrieveSubscription.
type Subscription struct {
	ID                string     // ID is the Stripe Subscription id
	CustomerID        string     // CustomerID is the Stripe Customer id
	Status            string     // Status mirrors Stripe's subscription.status (raw string)
	PriceID           string     // PriceID is the active price's id (first item)
	CurrentPeriodEnd  *time.Time // CurrentPeriodEnd is the current period boundary
	TrialEnd          *time.Time // TrialEnd is the trial boundary (nil after conversion)
	CancelAtPeriodEnd bool       // CancelAtPeriodEnd is Stripe's cancel_at_period_end
	PausedAt          *time.Time // PausedAt is set when Stripe paused the subscription
	Livemode          bool       // Livemode is the Stripe livemode flag
}

// CheckoutInput packs the args CreateCheckout needs.
type CheckoutInput struct {
	UserID     int64
	CustomerID string
	PriceID    string
	TrialDays  int
	SuccessURL string
	CancelURL  string
}

// WebhookEvent is the provider-agnostic webhook payload.
type WebhookEvent struct {
	ID       string
	Type     string
	Livemode bool
	Raw      []byte
}

// PriceData is a Stripe Price flattened to the fields the pricing UI needs.
type PriceData struct {
	Amount   int64  // Amount is the price in the smallest currency unit (e.g. cents).
	Currency string // Currency is the ISO 4217 lowercase code (e.g. "eur").
	Interval string // Interval is "month" or "year" for recurring prices, "" otherwise.
}

// Client is the billing-provider interface.
type Client interface {
	CreateCustomer(ctx context.Context, email string, userID int64) (string, error)
	CreateCheckout(ctx context.Context, in CheckoutInput) (*CheckoutSession, error)
	CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error)
	RetrieveSubscription(ctx context.Context, subID string) (*Subscription, error)
	ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]Subscription, error)
	ConstructWebhookEvent(payload []byte, signature string) (*WebhookEvent, error)
	GetPrice(ctx context.Context, priceID string) (PriceData, error)
}

// NoopClient returns ErrNotImplemented for every call.
type NoopClient struct{}

// CreateCustomer returns ErrNotImplemented.
func (NoopClient) CreateCustomer(ctx context.Context, email string, userID int64) (string, error) {
	return "", myErrors.ErrNotImplemented
}

// CreateCheckout returns ErrNotImplemented.
func (NoopClient) CreateCheckout(ctx context.Context, in CheckoutInput) (*CheckoutSession, error) {
	return nil, myErrors.ErrNotImplemented
}

// CreatePortal returns ErrNotImplemented.
func (NoopClient) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	return "", myErrors.ErrNotImplemented
}

// RetrieveSubscription returns ErrNotImplemented.
func (NoopClient) RetrieveSubscription(ctx context.Context, subID string) (*Subscription, error) {
	return nil, myErrors.ErrNotImplemented
}

// ListSubscriptionsByCustomer returns ErrNotImplemented.
func (NoopClient) ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]Subscription, error) {
	return nil, myErrors.ErrNotImplemented
}

// ConstructWebhookEvent returns ErrNotImplemented.
func (NoopClient) ConstructWebhookEvent(payload []byte, signature string) (*WebhookEvent, error) {
	return nil, myErrors.ErrNotImplemented
}

// GetPrice returns ErrNotImplemented.
func (NoopClient) GetPrice(ctx context.Context, priceID string) (PriceData, error) {
	return PriceData{}, myErrors.ErrNotImplemented
}

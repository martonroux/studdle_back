package billing

import (
	"context"
	"fmt"
	"strconv"

	stripe "github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/customer"
)

// StripeClient implements Client using the real Stripe API.
type StripeClient struct {
	webhookSecret string
}

// NewStripeClient initialises a StripeClient and sets the global Stripe secret key.
func NewStripeClient(secretKey, webhookSecret string) *StripeClient {
	stripe.Key = secretKey
	return &StripeClient{webhookSecret: webhookSecret}
}

// CreateCustomer creates a Stripe customer for the given email and user ID.
// The user ID is stored in the customer's metadata under the "user_id" key.
func (c *StripeClient) CreateCustomer(ctx context.Context, email string, userID int64) (string, error) {
	params := &stripe.CustomerParams{
		Email: stripe.String(email),
		Metadata: map[string]string{
			"user_id": strconv.FormatInt(userID, 10),
		},
	}
	cus, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe create customer:\n%w", err)
	}
	return cus.ID, nil
}

package billing

import (
	"fmt"

	stripewebhook "github.com/stripe/stripe-go/v76/webhook"
)

// ConstructWebhookEvent validates the Stripe-Signature header, parses the
// payload, and returns a provider-agnostic WebhookEvent.
func (c *StripeClient) ConstructWebhookEvent(payload []byte, signature string) (*WebhookEvent, error) {
	ev, err := stripewebhook.ConstructEvent(payload, signature, c.webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("stripe webhook signature:\n%w", err)
	}
	return &WebhookEvent{
		ID:       ev.ID,
		Type:     string(ev.Type),
		Livemode: ev.Livemode,
		Raw:      payload,
	}, nil
}

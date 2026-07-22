package billing

import (
	"encoding/json"
	"strconv"
	"testing"

	stripe "github.com/stripe/stripe-go/v76"
)

// unmarshalStripeSubscription simulates what the stripe-go Call plumbing does
// for a real API response: unmarshal the body into the typed struct, then
// stash the raw bytes on LastResponse so raw-JSON fallbacks can re-read
// fields the typed struct doesn't model.
func unmarshalStripeSubscription(t *testing.T, raw []byte) *stripe.Subscription {
	t.Helper()
	var s stripe.Subscription
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	s.LastResponse = &stripe.APIResponse{RawJSON: raw}
	return &s
}

// STU-37 regression: for some Stripe account billing configurations, the API
// returns current_period_end as null at the top level and populated only on
// the first subscription item. stripe-go v76's typed SubscriptionItem has no
// field for it, so projectSubscription must fall back to a raw-JSON read.
func TestProjectSubscription_FallsBackToItemLevelCurrentPeriodEnd(t *testing.T) {
	const itemPeriodEnd = int64(1785000000)
	raw := []byte(`{
		"id": "sub_1TbNJLJdg1NnW31Sf4di4eh4",
		"object": "subscription",
		"customer": "cus_test123",
		"status": "active",
		"current_period_end": null,
		"cancel_at_period_end": false,
		"livemode": false,
		"trial_end": null,
		"items": {
			"object": "list",
			"data": [
				{
					"id": "si_test123",
					"object": "subscription_item",
					"current_period_end": ` + jsonInt(itemPeriodEnd) + `,
					"price": {"id": "price_M", "object": "price"}
				}
			]
		}
	}`)

	sub := projectSubscription(unmarshalStripeSubscription(t, raw))

	if sub.CurrentPeriodEnd == nil {
		t.Fatal("CurrentPeriodEnd = nil, want populated from item-level fallback")
	}
	if got, want := sub.CurrentPeriodEnd.Unix(), itemPeriodEnd; got != want {
		t.Fatalf("CurrentPeriodEnd = %d, want %d", got, want)
	}
	if sub.PriceID != "price_M" {
		t.Fatalf("PriceID = %q, want price_M", sub.PriceID)
	}
}

// When the top-level field is populated, it must still win over the item
// level (existing behavior, unchanged by the fallback).
func TestProjectSubscription_PrefersTopLevelCurrentPeriodEndWhenPresent(t *testing.T) {
	const topPeriodEnd = int64(1700000000)
	const itemPeriodEnd = int64(1785000000)
	raw := []byte(`{
		"id": "sub_test",
		"object": "subscription",
		"customer": "cus_test123",
		"status": "active",
		"current_period_end": ` + jsonInt(topPeriodEnd) + `,
		"cancel_at_period_end": false,
		"livemode": false,
		"items": {
			"object": "list",
			"data": [
				{
					"id": "si_test123",
					"object": "subscription_item",
					"current_period_end": ` + jsonInt(itemPeriodEnd) + `,
					"price": {"id": "price_M", "object": "price"}
				}
			]
		}
	}`)

	sub := projectSubscription(unmarshalStripeSubscription(t, raw))

	if sub.CurrentPeriodEnd == nil {
		t.Fatal("CurrentPeriodEnd = nil, want populated from top-level field")
	}
	if got, want := sub.CurrentPeriodEnd.Unix(), topPeriodEnd; got != want {
		t.Fatalf("CurrentPeriodEnd = %d, want %d (top-level, not item-level)", got, want)
	}
}

// Absent at both levels (and no LastResponse at all, e.g. a bare struct built
// outside the SDK's Call path) must not panic and must leave the field nil.
func TestProjectSubscription_NoCurrentPeriodEndAnywhere(t *testing.T) {
	s := &stripe.Subscription{ID: "sub_bare", Status: stripe.SubscriptionStatusActive}

	sub := projectSubscription(s)

	if sub.CurrentPeriodEnd != nil {
		t.Fatalf("CurrentPeriodEnd = %v, want nil", sub.CurrentPeriodEnd)
	}
}

func jsonInt(v int64) string {
	return strconv.FormatInt(v, 10)
}

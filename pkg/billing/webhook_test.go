package billing_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"studdle/backend/internal/billing"
	pkgbilling "studdle/backend/pkg/billing"
	"studdle/backend/testutil"
)

func TestHandleWebhook_LivemodeMismatch(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	fake := &testutil.FakeBilling{Event: &billing.WebhookEvent{
		ID: "evt_1", Type: "customer.subscription.updated", Livemode: true,
		Raw: []byte(`{"id":"evt_1"}`),
	}}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	err := svc.HandleWebhook(context.Background(), pkgbilling.WebhookConfig{
		ExpectLivemode: false, Body: []byte(`{}`), Signature: "any",
	})
	if err != pkgbilling.ErrLivemodeMismatch {
		t.Fatalf("err = %v, want ErrLivemodeMismatch", err)
	}
}

func TestHandleWebhook_DuplicateEventIdempotent(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	fake := &testutil.FakeBilling{Event: &billing.WebhookEvent{
		ID: "evt_dup", Type: "charge.refunded", Livemode: false,
		Raw: []byte(`{"id":"evt_dup"}`),
	}}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	cfg := pkgbilling.WebhookConfig{ExpectLivemode: false, Body: []byte(`{}`), Signature: "x"}
	if err := svc.HandleWebhook(context.Background(), cfg); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := svc.HandleWebhook(context.Background(), cfg); err != nil {
		t.Fatalf("dup: %v", err)
	}
	var count int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM billing_events WHERE stripe_event_id = $1`, "evt_dup").Scan(&count)
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestDispatch_SubscriptionUpdated_TrialingToActive(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)

	raw := []byte(`{"id":"evt_upd","data":{"object":{"id":"sub_1","metadata":{"user_id":"` + strconv.FormatInt(u.ID, 10) + `"}}}}`)
	fake := &testutil.FakeBilling{
		Event: &billing.WebhookEvent{ID: "evt_upd", Type: "customer.subscription.updated", Livemode: false, Raw: raw},
		Subscription: &billing.Subscription{
			ID: "sub_1", CustomerID: "cus_1",
			Status: "active", PriceID: "price_M",
			CurrentPeriodEnd: &end,
		},
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	err := svc.HandleWebhook(context.Background(), pkgbilling.WebhookConfig{
		ExpectLivemode: false, Body: raw, Signature: "any",
	})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusActive {
		t.Fatalf("status %q want active", sub.Status)
	}
	if sub.Plan != pkgbilling.PlanProMonthly {
		t.Fatalf("plan %q want pro_monthly", sub.Plan)
	}
}

func TestDispatch_SubscriptionDeleted(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	raw := []byte(`{"id":"evt_del","data":{"object":{"id":"sub_X","metadata":{"user_id":"` + strconv.FormatInt(u.ID, 10) + `"}}}}`)
	fake := &testutil.FakeBilling{
		Event: &billing.WebhookEvent{ID: "evt_del", Type: "customer.subscription.deleted", Livemode: false, Raw: raw},
		Subscription: &billing.Subscription{
			ID: "sub_X", CustomerID: "cus_X",
			Status: "canceled", PriceID: "price_M",
		},
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	if err := svc.HandleWebhook(context.Background(), pkgbilling.WebhookConfig{
		ExpectLivemode: false, Body: raw, Signature: "x",
	}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusCanceled {
		t.Fatalf("status %q want canceled", sub.Status)
	}
}

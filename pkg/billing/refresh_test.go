package billing_test

import (
	"context"
	"testing"
	"time"

	"studdle/backend/internal/billing"
	pkgbilling "studdle/backend/pkg/billing"
	"studdle/backend/testutil"
)

func TestRefreshFromStripe_AppliesAuthoritativeState(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan, stripe_customer_id)
		 VALUES ($1, 'past_due', 'pro_monthly', 'cus_R')`, u.ID)

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	fake := &testutil.FakeBilling{
		Subscriptions: []billing.Subscription{{
			ID: "sub_R", CustomerID: "cus_R",
			Status: "active", PriceID: "price_M",
			CurrentPeriodEnd: &end,
		}},
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	if err := svc.RefreshFromStripe(context.Background(), u.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusActive {
		t.Fatalf("status = %q, want active", sub.Status)
	}
}

func TestRefreshFromStripe_NoCustomerNoOps(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	if err := svc.RefreshFromStripe(context.Background(), u.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

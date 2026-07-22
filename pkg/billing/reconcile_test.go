package billing_test

import (
	"context"
	"testing"
	"time"

	"studdle/backend/internal/billing"
	pkgbilling "studdle/backend/pkg/billing"
	"studdle/backend/testutil"
)

func TestReconcileOnce_CorrectsDriftedRow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan, stripe_customer_id, stripe_sub_id)
		 VALUES ($1, 'past_due', 'pro_monthly', 'cus_S', 'sub_S')`, u.ID)

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	fake := &testutil.FakeBilling{
		Subscription: &billing.Subscription{
			ID: "sub_S", CustomerID: "cus_S",
			Status: "active", PriceID: "price_M",
			CurrentPeriodEnd: &end,
		},
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})
	corrected, err := svc.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if corrected != 1 {
		t.Fatalf("corrected = %d, want 1", corrected)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusActive {
		t.Fatalf("status = %q, want active", sub.Status)
	}
}

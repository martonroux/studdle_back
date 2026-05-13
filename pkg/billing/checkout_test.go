package billing_test

import (
	"context"
	"testing"

	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestCreateCheckoutSession_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	fake := &testutil.FakeBilling{
		CheckoutURL: "https://checkout.stripe.test/cs_test",
		CustomerID:  "cus_X",
	}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{
		Monthly: "price_M", Annual: "price_A",
	})

	url, err := svc.CreateCheckoutSession(context.Background(),
		u.ID, u.Email, pkgbilling.PlanProMonthly, "https://app/billing", "https://app/pricing")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if url != "https://checkout.stripe.test/cs_test" {
		t.Fatalf("got url %q", url)
	}

	var cust string
	_ = pool.QueryRow(context.Background(),
		`SELECT COALESCE(stripe_customer_id,'') FROM user_subscriptions WHERE user_id=$1`,
		u.ID,
	).Scan(&cust)
	if cust != "cus_X" {
		t.Fatalf("customer not persisted, got %q", cust)
	}
}

func TestCreateCheckoutSession_RefusesAlreadyActive(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{Monthly: "price_M", Annual: "price_A"})

	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan) VALUES ($1, 'active', 'pro_monthly')`, u.ID)

	_, err := svc.CreateCheckoutSession(context.Background(),
		u.ID, u.Email, pkgbilling.PlanProMonthly, "https://app/billing", "https://app/pricing")
	if err != pkgbilling.ErrAlreadySubscribed {
		t.Fatalf("err = %v, want ErrAlreadySubscribed", err)
	}
}

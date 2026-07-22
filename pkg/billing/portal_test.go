package billing_test

import (
	"context"
	"errors"
	"testing"

	pkgbilling "studdle/backend/pkg/billing"
	"studdle/backend/testutil"
)

func TestCreatePortalSession_NoCustomerReturnsErr(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})

	_, err := svc.CreatePortalSession(context.Background(), u.ID, "https://app/billing")
	if !errors.Is(err, pkgbilling.ErrNoCustomer) {
		t.Fatalf("err = %v, want ErrNoCustomer", err)
	}
}

func TestCreatePortalSession_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO user_subscriptions (user_id, status, plan, stripe_customer_id)
		 VALUES ($1, 'active', 'pro_monthly', 'cus_Y')`, u.ID)

	fake := &testutil.FakeBilling{PortalURL: "https://portal.stripe.test/x"}
	svc := pkgbilling.NewService(pool, fake, pkgbilling.PriceMap{})
	url, err := svc.CreatePortalSession(context.Background(), u.ID, "https://app/billing")
	if err != nil {
		t.Fatalf("portal: %v", err)
	}
	if url != "https://portal.stripe.test/x" {
		t.Fatalf("got %q", url)
	}
}

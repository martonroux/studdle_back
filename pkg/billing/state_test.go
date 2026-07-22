package billing_test

import (
	"context"
	"testing"
	"time"

	"studdle/backend/internal/billing"
	pkgbilling "studdle/backend/pkg/billing"
	"studdle/backend/testutil"
)

func TestApplyStripeState_InsertsRow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{}, pkgbilling.PriceMap{})

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	trial := end
	upd := pkgbilling.StateUpdate{
		UserID:           u.ID,
		StripeCustomerID: "cus_test1",
		StripeSubID:      "sub_test1",
		Status:           pkgbilling.StatusTrialing,
		Plan:             pkgbilling.PlanProMonthly,
		CurrentPeriodEnd: &end,
		TrialEnd:         &trial,
	}
	if err := svc.ApplyStripeState(context.Background(), upd); err != nil {
		t.Fatalf("apply: %v", err)
	}

	sub, err := svc.GetSubscription(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sub.Status != pkgbilling.StatusTrialing || sub.Plan != pkgbilling.PlanProMonthly {
		t.Fatalf("got %+v", sub)
	}
	if sub.StripeCustomerID != "cus_test1" || sub.StripeSubID != "sub_test1" {
		t.Fatalf("ids = %q / %q", sub.StripeCustomerID, sub.StripeSubID)
	}
	if !sub.IsActive(time.Now()) {
		t.Fatalf("expected IsActive=true")
	}
}

func TestApplyStripeState_TransitionsActiveToPastDue(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{}, pkgbilling.PriceMap{})

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	ctx := context.Background()
	_ = svc.ApplyStripeState(ctx, pkgbilling.StateUpdate{
		UserID: u.ID, StripeCustomerID: "cus_t", StripeSubID: "sub_t",
		Status: pkgbilling.StatusActive, Plan: pkgbilling.PlanProMonthly,
		CurrentPeriodEnd: &end,
	})

	_ = svc.ApplyStripeState(ctx, pkgbilling.StateUpdate{
		UserID: u.ID, StripeCustomerID: "cus_t", StripeSubID: "sub_t",
		Status: pkgbilling.StatusPastDue, Plan: pkgbilling.PlanProMonthly,
		CurrentPeriodEnd: &end,
	})

	sub, _ := svc.GetSubscription(ctx, u.ID)
	if sub.Status != pkgbilling.StatusPastDue {
		t.Fatalf("status = %q, want past_due", sub.Status)
	}
	if sub.IsActive(time.Now()) {
		t.Fatalf("expected IsActive=false for past_due")
	}
}

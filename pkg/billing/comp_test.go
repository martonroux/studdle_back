package billing_test

import (
	"context"
	"testing"
	"time"

	pkgbilling "studdle/backend/pkg/billing"
	"studdle/backend/testutil"
)

func TestGrantCompWithExpiry_PersistsExpiry(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})

	expires := time.Now().Add(60 * 24 * time.Hour).UTC().Truncate(time.Second)
	if err := svc.GrantCompWithExpiry(context.Background(), pkgbilling.CompGrant{
		UserID: u.ID, ExpiresAt: &expires, Reason: "Beta tester", ActorUserID: 1,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusComped || sub.Plan != pkgbilling.PlanComp {
		t.Fatalf("got %+v", sub)
	}
	if sub.CurrentPeriodEnd == nil || !sub.CurrentPeriodEnd.Equal(expires) {
		t.Fatalf("expiry mismatch: got %v want %v", sub.CurrentPeriodEnd, expires)
	}
	var count int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM billing_events WHERE event_type='admin_comp_granted' AND user_id=$1`, u.ID,
	).Scan(&count)
	if count != 1 {
		t.Fatalf("audit rows = %d, want 1", count)
	}
}

func TestRevokeComp_SetsCanceled(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, &testutil.FakeBilling{}, pkgbilling.PriceMap{})
	_ = svc.GrantCompWithExpiry(context.Background(), pkgbilling.CompGrant{
		UserID: u.ID, Reason: "init", ActorUserID: 1,
	})

	if err := svc.RevokeComp(context.Background(), pkgbilling.CompRevoke{
		UserID: u.ID, Reason: "support ticket", ActorUserID: 1,
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	sub, _ := svc.GetSubscription(context.Background(), u.ID)
	if sub.Status != pkgbilling.StatusCanceled {
		t.Fatalf("got %q", sub.Status)
	}
}

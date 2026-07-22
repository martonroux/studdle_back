package preferences_test

import (
	"context"
	"testing"

	"studdle/backend/pkg/preferences"
	"studdle/backend/testutil"
)

// TestPreferencesGetCreatesDefault verifies Get auto-creates a row with AIPlanningEnabled=true.
func TestPreferencesGetCreatesDefault(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := preferences.NewService(db)
	u := testutil.NewVerifiedUser(t, db)

	p, err := svc.Get(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != u.ID || !p.AIPlanningEnabled { // default is on
		t.Fatalf("unexpected defaults: %+v", p)
	}
}

// TestPreferencesUpdate verifies Update patches both fields and returns the refreshed row.
func TestPreferencesUpdate(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := preferences.NewService(db)
	u := testutil.NewVerifiedUser(t, db)

	goal := 25
	off := false
	p, err := svc.Update(ctx, u.ID, preferences.UpdateInput{
		AIPlanningEnabled: &off, DailyGoalTarget: &goal,
	})
	if err != nil || p.DailyGoalTarget != 25 || p.AIPlanningEnabled {
		t.Fatalf("update: %v %+v", err, p)
	}
}

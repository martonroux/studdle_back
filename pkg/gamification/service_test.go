package gamification_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/gamification"
	"studbud/backend/testutil"
)

// TestRecordSessionBumpsStreakAndGoal verifies the first session sets streak=1, fills the
// daily goal, and unlocks the first_session achievement.
func TestRecordSessionBumpsStreakAndGoal(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := gamification.NewService(db, access.NewService(db))

	user := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, user.ID)

	res, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, CardCount: 5, DurationMs: 120000, Score: 80,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Streak.CurrentStreak != 1 {
		t.Fatalf("expected streak=1, got %d", res.Streak.CurrentStreak)
	}
	if res.DailyGoal.DoneToday != 5 {
		t.Fatalf("expected done_today=5, got %d", res.DailyGoal.DoneToday)
	}
	gotFirst := false
	for _, a := range res.NewlyAwarded {
		if a.Code == "first_session" {
			gotFirst = true
		}
	}
	if !gotFirst {
		t.Fatal("expected first_session achievement")
	}
}

// TestStreakResetsAfterGap verifies that a 3-day gap between sessions resets the streak to 1.
func TestStreakResetsAfterGap(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := gamification.NewService(db, access.NewService(db))

	user := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, user.ID)

	past := time.Now().UTC().Add(-72 * time.Hour)
	svc.SetClock(func() time.Time { return past })
	if _, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, CardCount: 1, DurationMs: 1000, Score: 10,
	}); err != nil {
		t.Fatal(err)
	}
	svc.SetClock(time.Now)
	res, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, CardCount: 1, DurationMs: 1000, Score: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Streak.CurrentStreak != 1 {
		t.Fatalf("expected streak reset to 1, got %d", res.Streak.CurrentStreak)
	}
}

// TestListAchievementsShowsFullCatalog verifies a brand new user sees the entire catalog
// even when nothing has been unlocked.
func TestListAchievementsShowsFullCatalog(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := gamification.NewService(db, access.NewService(db))

	user := testutil.NewVerifiedUser(t, db)
	list, err := svc.ListAchievements(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 6 {
		t.Fatalf("expected full catalog (>=6), got %d", len(list))
	}
}

// TestRecordSessionForbiddenForOutsider is a regression test for GAM-1: a caller with no
// ownership or collaborator relationship to the subject must not be able to record a
// session against it.
func TestRecordSessionForbiddenForOutsider(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := gamification.NewService(db, access.NewService(db))

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)
	stranger := testutil.NewVerifiedUser(t, db)

	_, err := svc.RecordSession(ctx, stranger.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, CardCount: 999, DurationMs: 500, Score: 50,
	})
	if !errors.Is(err, myErrors.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}

	var count int
	if err := db.QueryRow(ctx,
		`SELECT count(*) FROM training_sessions WHERE subject_id = $1`, sub.ID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no session recorded, got %d", count)
	}
}

// TestRecordSessionNewlyAwardedIsEmptySliceNotNil is a regression test for GAM-3: once every
// currently-reachable achievement is already unlocked, newly_awarded must serialize as []
// rather than null.
func TestRecordSessionNewlyAwardedIsEmptySliceNotNil(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := gamification.NewService(db, access.NewService(db))

	user := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, user.ID)

	if _, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, CardCount: 1, DurationMs: 1000, Score: 10,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, CardCount: 1, DurationMs: 1000, Score: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.NewlyAwarded == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(res.NewlyAwarded) != 0 {
		t.Fatalf("expected no new achievements on second same-day session, got %v", res.NewlyAwarded)
	}
	b, err := json.Marshal(res.NewlyAwarded)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("expected JSON [], got %s", b)
	}
}

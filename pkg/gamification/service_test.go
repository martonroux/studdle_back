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

// TestRecordSessionPerChapterSplitPreservesFullDuration verifies that recording two
// chapter-scoped sessions (the shape the frontend now sends after splitting a
// multi-chapter study run by (subject_id, chapter_id) instead of subject_id alone)
// produces two distinct training_sessions rows, each carrying its own chapter_id and
// total_cards. Duration is NOT apportioned between the split rows — each one gets the
// full duration passed to RecordSession. That is an accepted pre-existing
// simplification (the multi-subject split already behaves the same way today), not a
// new limitation introduced by chapter attribution.
func TestRecordSessionPerChapterSplitPreservesFullDuration(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := gamification.NewService(db, access.NewService(db))

	user := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, user.ID)
	ch1 := testutil.NewChapter(t, db, sub.ID, "Alkenes")
	ch2 := testutil.NewChapter(t, db, sub.ID, "Alkynes")

	if _, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, ChapterID: &ch1, CardCount: 3, DurationMs: 90000, Score: 6,
	}); err != nil {
		t.Fatalf("record chapter 1 session: %v", err)
	}
	if _, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, ChapterID: &ch2, CardCount: 5, DurationMs: 150000, Score: 8,
	}); err != nil {
		t.Fatalf("record chapter 2 session: %v", err)
	}

	rows, err := db.Query(ctx, `
		SELECT chapter_id, total_cards, duration_ms
		FROM training_sessions
		WHERE subject_id = $1
		ORDER BY chapter_id
	`, sub.ID)
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	defer rows.Close()

	type row struct {
		ChapterID  int64
		TotalCards int
		DurationMs int64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ChapterID, &r.TotalCards, &r.DurationMs); err != nil {
			t.Fatalf("scan session row: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 training_sessions rows, got %d: %+v", len(got), got)
	}
	if got[0].ChapterID != ch1 || got[0].TotalCards != 3 || got[0].DurationMs != 90000 {
		t.Fatalf("unexpected chapter 1 row: %+v", got[0])
	}
	if got[1].ChapterID != ch2 || got[1].TotalCards != 5 || got[1].DurationMs != 150000 {
		t.Fatalf("unexpected chapter 2 row: %+v", got[1])
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

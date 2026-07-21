package subject_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/subject"
	"studbud/backend/testutil"
)

func TestSubjectCRUD(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)
	owner := testutil.NewVerifiedUser(t, db)

	created, err := svc.Create(ctx, owner.ID, subject.CreateInput{Name: "Biology", Color: "#0a0"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.OwnerID != owner.ID || created.Name != "Biology" || created.Visibility != "private" {
		t.Fatalf("unexpected created subject: %+v", created)
	}

	got, err := svc.Get(ctx, owner.ID, created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("get: %v %+v", err, got)
	}

	newName := "Biology 101"
	updated, err := svc.Update(ctx, owner.ID, created.ID, subject.UpdateInput{Name: &newName})
	if err != nil || updated.Name != newName {
		t.Fatalf("update: %v %+v", err, updated)
	}

	list, err := svc.ListOwned(ctx, owner.ID, false)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	if err := svc.Delete(ctx, owner.ID, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestSubjectGet_ForbiddenForPrivate(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)

	sub, err := svc.Create(ctx, owner.ID, subject.CreateInput{Name: "Secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, other.ID, sub.ID); err == nil {
		t.Fatal("expected forbidden for other user on private subject")
	}
}

// TestSubjectGet_NotFoundDoesNotLeakExistence is a regression test for SL-6: a
// private subject the caller has no relationship to must return the exact same
// error (ErrNotFound / 404) as a subject ID that does not exist at all, so the
// status code can't be used to probe for the existence of private subject IDs.
func TestSubjectGet_NotFoundDoesNotLeakExistence(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)

	sub, err := svc.Create(ctx, owner.ID, subject.CreateInput{Name: "Secret"})
	if err != nil {
		t.Fatal(err)
	}

	_, errPrivate := svc.Get(ctx, other.ID, sub.ID)
	if !errors.Is(errPrivate, myErrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for private subject caller can't see, got %v", errPrivate)
	}

	const nonexistentID = 987654321
	_, errMissing := svc.Get(ctx, other.ID, nonexistentID)
	if !errors.Is(errMissing, myErrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for nonexistent subject, got %v", errMissing)
	}
}

// insertTrainingSession writes one training_sessions row directly, bypassing
// gamification.Service, so tests can control completed_at precisely.
func insertTrainingSession(t *testing.T, db *pgxpool.Pool, userID, subjectID int64, chapterID *int64, cards int, durationMs int64, score int, completedAt time.Time) {
	t.Helper()
	_, err := db.Exec(context.Background(), `
		INSERT INTO training_sessions (user_id, subject_id, chapter_id, total_cards, duration_ms, score, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, userID, subjectID, chapterID, cards, durationMs, score, completedAt)
	if err != nil {
		t.Fatalf("insert training session: %v", err)
	}
}

// insertMasteryDay writes one subject_mastery_daily row at CURRENT_DATE+dayOffset
// (dayOffset may be negative for past days). total_cards/good/ok/bad/new are fixed
// filler values — only mastery_percent matters to the tests using this helper.
func insertMasteryDay(t *testing.T, db *pgxpool.Pool, userID, subjectID int64, dayOffset int, masteryPercent float64) {
	t.Helper()
	_, err := db.Exec(context.Background(), `
		INSERT INTO subject_mastery_daily
			(user_id, subject_id, day, total_cards, good_count, ok_count, bad_count, new_count, mastery_percent)
		VALUES ($1, $2, CURRENT_DATE + $3::int, 10, 5, 2, 1, 2, $4)
	`, userID, subjectID, dayOffset, masteryPercent)
	if err != nil {
		t.Fatalf("insert mastery day: %v", err)
	}
}

func TestHistoryForbiddenForNonMemberOnPrivateSubject(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	if _, err := svc.History(ctx, other.ID, sub.ID); !errors.Is(err, myErrors.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestHistorySessionsOrderingAndLimit(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	// Postgres timestamptz only stores microsecond precision; truncate here so the
	// in-memory comparison below matches what round-trips through the DB exactly,
	// instead of flaking on hosts where time.Now() has true nanosecond jitter.
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-24 * time.Hour)
	for i := 0; i < 25; i++ {
		insertTrainingSession(t, db, owner.ID, sub.ID, nil, 1, 1000, 2, base.Add(time.Duration(i)*time.Minute))
	}

	out, err := svc.History(ctx, owner.ID, sub.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(out.Sessions) != 20 {
		t.Fatalf("expected 20 sessions (LIMIT 20), got %d", len(out.Sessions))
	}
	want := base.Add(24 * time.Minute) // the 25th insert (i=24) is the newest
	if !out.Sessions[0].CompletedAt.Equal(want) {
		t.Fatalf("expected newest session first (%v), got %v", want, out.Sessions[0].CompletedAt)
	}
	for i := 1; i < len(out.Sessions); i++ {
		if out.Sessions[i-1].CompletedAt.Before(out.Sessions[i].CompletedAt) {
			t.Fatalf("sessions not ordered newest-first at index %d", i)
		}
	}
}

func TestHistoryHeatmapZeroFillOver56Days(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	today := time.Now().UTC()
	insertTrainingSession(t, db, owner.ID, sub.ID, nil, 4, 1000, 2, today)
	insertTrainingSession(t, db, owner.ID, sub.ID, nil, 3, 1000, 2, today.AddDate(0, 0, -10))
	// Outside the 8-week window — must not leak into the heatmap at all.
	insertTrainingSession(t, db, owner.ID, sub.ID, nil, 99, 1000, 2, today.AddDate(0, 0, -100))

	out, err := svc.History(ctx, owner.ID, sub.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(out.Heatmap) != 56 {
		t.Fatalf("expected 56 heatmap days, got %d", len(out.Heatmap))
	}
	last := out.Heatmap[len(out.Heatmap)-1]
	if last.Day != today.Format("2006-01-02") || last.Cards != 4 {
		t.Fatalf("expected today's entry {%s, 4}, got %+v", today.Format("2006-01-02"), last)
	}
	nonZeroDays := 0
	for _, d := range out.Heatmap {
		if d.Cards != 0 {
			nonZeroDays++
		}
	}
	if nonZeroDays != 2 {
		t.Fatalf("expected exactly 2 non-zero heatmap days (today + 10 days ago), got %d", nonZeroDays)
	}
}

func TestHistoryChapterAggregationIncludesZeroSessionChapters(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)
	chA := testutil.NewChapter(t, db, sub.ID, "Chapter A")
	chB := testutil.NewChapter(t, db, sub.ID, "Chapter B")

	fcA1 := testutil.NewFlashcard(t, db, sub.ID, chA, "qa1", "aa1")
	testutil.NewFlashcard(t, db, sub.ID, chA, "qa2", "aa2") // left at last_result=-1 (new)
	if _, err := db.Exec(ctx, `UPDATE flashcards SET last_result = 2 WHERE id = $1`, fcA1); err != nil {
		t.Fatalf("seed chapter A last_result: %v", err)
	}
	testutil.NewFlashcard(t, db, sub.ID, chB, "qb1", "ab1") // chapter B: flashcard exists, never trained

	insertTrainingSession(t, db, owner.ID, sub.ID, &chA, 6, 120000, 10, time.Now().UTC())

	out, err := svc.History(ctx, owner.ID, sub.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(out.Chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d: %+v", len(out.Chapters), out.Chapters)
	}
	byID := map[int64]subject.ChapterEntry{}
	for _, c := range out.Chapters {
		byID[c.ChapterID] = c
	}
	a, ok := byID[chA]
	if !ok {
		t.Fatalf("chapter A missing from result: %+v", out.Chapters)
	}
	if a.Cards != 6 || a.MinutesTrained != 2 {
		t.Fatalf("unexpected chapter A aggregation: %+v", a)
	}
	if diff := a.MasteryPercent - 0.5; diff > 1e-9 || diff < -1e-9 { // 1 good of 2 cards
		t.Fatalf("chapter A mastery = %v, want 0.5", a.MasteryPercent)
	}
	b, ok := byID[chB]
	if !ok {
		t.Fatalf("chapter B (zero sessions) missing from result: %+v", out.Chapters)
	}
	if b.Cards != 0 || b.MinutesTrained != 0 {
		t.Fatalf("expected chapter B cards=0/minutesTrained=0, got %+v", b)
	}
	if b.MasteryPercent != 0 {
		t.Fatalf("expected chapter B mastery=0 (all-new card), got %v", b.MasteryPercent)
	}
}

func TestMasteryTrendForbiddenForNonMemberOnPrivateSubject(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	if _, err := svc.MasteryTrend(ctx, other.ID, sub.ID, "7d"); !errors.Is(err, myErrors.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestMasteryTrendRejectsUnknownPeriod(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	if _, err := svc.MasteryTrend(ctx, owner.ID, sub.ID, "90d"); !errors.Is(err, myErrors.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for unrecognized period, got %v", err)
	}
}

func TestMasteryTrendPeriodDayCountMapping(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	// A snapshot 20 days old: inside the 30d window, outside the 7d window.
	insertMasteryDay(t, db, owner.ID, sub.ID, -20, 0.10)

	out7, err := svc.MasteryTrend(ctx, owner.ID, sub.ID, "7d")
	if err != nil {
		t.Fatalf("7d: %v", err)
	}
	if len(out7.Series) != 0 {
		t.Fatalf("7d window should not reach a 20-day-old snapshot, got %v", out7.Series)
	}

	out30, err := svc.MasteryTrend(ctx, owner.ID, sub.ID, "30d")
	if err != nil {
		t.Fatalf("30d: %v", err)
	}
	if len(out30.Series) == 0 || out30.Series[0] != 0.10 {
		t.Fatalf("30d window should include the 20-day-old snapshot, got %v", out30.Series)
	}
}

func TestMasteryTrendForwardFillsGapsAndComputesDelta(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	// 7d window is [today-6 .. today]. Seed only the two endpoints; days -5..-1
	// have no snapshot and must forward-fill from the last known value (0.20).
	insertMasteryDay(t, db, owner.ID, sub.ID, -6, 0.20)
	insertMasteryDay(t, db, owner.ID, sub.ID, 0, 0.50)

	out, err := svc.MasteryTrend(ctx, owner.ID, sub.ID, "7d")
	if err != nil {
		t.Fatalf("mastery trend: %v", err)
	}
	if len(out.Series) != 7 {
		t.Fatalf("expected 7 points for the 7d period, got %d: %v", len(out.Series), out.Series)
	}
	if out.Series[0] != 0.20 {
		t.Fatalf("expected first point 0.20, got %v", out.Series[0])
	}
	for i := 1; i < 6; i++ {
		if out.Series[i] != 0.20 {
			t.Fatalf("expected forward-filled 0.20 at index %d, got %v", i, out.Series[i])
		}
	}
	if out.Series[6] != 0.50 {
		t.Fatalf("expected last point 0.50, got %v", out.Series[6])
	}
	wantDelta := 0.30
	if diff := out.Delta - wantDelta; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("delta = %v, want %v", out.Delta, wantDelta)
	}
}

func TestMasteryTrendDeltaZeroWithFewerThanTwoPoints(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	// No snapshots at all yet: zero points, delta must be 0, not error.
	out, err := svc.MasteryTrend(ctx, owner.ID, sub.ID, "7d")
	if err != nil {
		t.Fatalf("mastery trend (no data): %v", err)
	}
	if len(out.Series) != 0 || out.Delta != 0 {
		t.Fatalf("expected empty series/zero delta with no snapshots, got series=%v delta=%v", out.Series, out.Delta)
	}

	// A single snapshot today: one point, delta must still be 0.
	insertMasteryDay(t, db, owner.ID, sub.ID, 0, 0.33)
	out, err = svc.MasteryTrend(ctx, owner.ID, sub.ID, "7d")
	if err != nil {
		t.Fatalf("mastery trend (one point): %v", err)
	}
	if len(out.Series) != 1 || out.Delta != 0 {
		t.Fatalf("expected 1 point/zero delta with a single snapshot, got series=%v delta=%v", out.Series, out.Delta)
	}
}

func TestSnapshotMasteryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)
	ch := testutil.NewChapter(t, db, sub.ID, "Ch")
	testutil.NewFlashcard(t, db, sub.ID, ch, "q1", "a1") // stays new (-1)
	fc2 := testutil.NewFlashcard(t, db, sub.ID, ch, "q2", "a2")
	if _, err := db.Exec(ctx, `UPDATE flashcards SET last_result = 2 WHERE id = $1`, fc2); err != nil {
		t.Fatalf("seed last_result: %v", err)
	}

	if err := svc.SnapshotMastery(ctx); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if err := svc.SnapshotMastery(ctx); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM subject_mastery_daily WHERE subject_id = $1`, sub.ID).Scan(&count); err != nil {
		t.Fatalf("count snapshot rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row after running twice the same day, got %d", count)
	}

	var totalCards int
	var masteryPercent float64
	if err := db.QueryRow(ctx, `SELECT total_cards, mastery_percent FROM subject_mastery_daily WHERE subject_id = $1`, sub.ID).
		Scan(&totalCards, &masteryPercent); err != nil {
		t.Fatalf("read snapshot row: %v", err)
	}
	if totalCards != 2 {
		t.Fatalf("expected total_cards=2, got %d", totalCards)
	}
	if diff := masteryPercent - 0.5; diff > 1e-9 || diff < -1e-9 { // 1 good of 2 cards
		t.Fatalf("mastery_percent = %v, want 0.5", masteryPercent)
	}
}

func TestSnapshotMasterySkipsZeroFlashcardSubjects(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID) // no flashcards at all

	if err := svc.SnapshotMastery(ctx); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM subject_mastery_daily WHERE subject_id = $1`, sub.ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no snapshot row for a zero-flashcard subject, got %d", count)
	}
}

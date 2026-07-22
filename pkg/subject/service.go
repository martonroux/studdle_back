package subject

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
)

// Service owns subject CRUD and listing.
type Service struct {
	db     *pgxpool.Pool   // db is the shared connection pool
	access *access.Service // access resolves visibility and membership
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Create inserts a new subject owned by uid.
func (s *Service) Create(ctx context.Context, uid int64, in CreateInput) (*Subject, error) {
	if in.Name == "" {
		return nil, myErrors.ErrInvalidInput
	}
	vis := in.Visibility
	if vis == "" {
		vis = "private"
	}
	if vis != "private" && vis != "friends" && vis != "public" {
		return nil, myErrors.ErrInvalidInput
	}
	var sub Subject
	err := s.db.QueryRow(ctx, `
		INSERT INTO subjects (owner_id, name, color, icon, tags, visibility, description)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, owner_id, name, color, icon, tags, visibility, archived,
		          description, last_used, created_at, updated_at
	`, uid, in.Name, in.Color, in.Icon, in.Tags, vis, in.Description).Scan(
		&sub.ID, &sub.OwnerID, &sub.Name, &sub.Color, &sub.Icon, &sub.Tags,
		&sub.Visibility, &sub.Archived, &sub.Description, &sub.LastUsed,
		&sub.CreatedAt, &sub.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert subject:\n%w", err)
	}
	return &sub, nil
}

// Get fetches a subject if the user can read it.
// Both "subject does not exist" and "subject exists but is not visible to
// uid" return ErrNotFound — never ErrForbidden — so an unrelated caller
// cannot use the status code to learn whether a private subject ID exists.
func (s *Service) Get(ctx context.Context, uid, subjectID int64) (*Subject, error) {
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrNotFound
	}
	return sub, nil
}

// Stats returns the per-difficulty card distribution for a subject the caller can read.
func (s *Service) Stats(ctx context.Context, uid, subjectID int64) (*StatsResponse, error) {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	out := &StatsResponse{}
	err = s.db.QueryRow(ctx, `
		SELECT
			COUNT(*)                                   AS total,
			COUNT(*) FILTER (WHERE last_result = 2)    AS good,
			COUNT(*) FILTER (WHERE last_result = 1)    AS ok,
			COUNT(*) FILTER (WHERE last_result = 0)    AS bad,
			COUNT(*) FILTER (WHERE last_result = -1)   AS new_count
		FROM flashcards
		WHERE subject_id = $1
	`, subjectID).Scan(&out.TotalCards, &out.GoodCount, &out.OkCount, &out.BadCount, &out.NewCount)
	if err != nil {
		return nil, fmt.Errorf("subject stats:\n%w", err)
	}
	out.CardsStudied = out.TotalCards - out.NewCount
	if out.TotalCards > 0 {
		out.MasteryPercent = (float64(out.GoodCount) + float64(out.OkCount)*0.5) / float64(out.TotalCards)
	}
	return out, nil
}

// History returns the session list, activity heatmap, and per-chapter aggregation for a
// subject the caller can read. Bundles all three SubjectStatsView widgets in one call.
func (s *Service) History(ctx context.Context, uid, subjectID int64) (*HistoryResponse, error) {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	sessions, err := s.loadSessions(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	heatmap, err := s.loadHeatmap(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	chapters, err := s.loadChapterEntries(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	return &HistoryResponse{Sessions: sessions, Heatmap: heatmap, Chapters: chapters}, nil
}

// loadSessions returns the caller's most recent 20 sessions for subjectID, newest first.
func (s *Service) loadSessions(ctx context.Context, uid, subjectID int64) ([]SessionEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ts.completed_at, ts.chapter_id, c.title, ts.total_cards, ts.duration_ms, ts.score
		FROM training_sessions ts
		LEFT JOIN chapters c ON c.id = ts.chapter_id
		WHERE ts.subject_id = $1 AND ts.user_id = $2
		ORDER BY ts.completed_at DESC
		LIMIT 20
	`, subjectID, uid)
	if err != nil {
		return nil, fmt.Errorf("list sessions:\n%w", err)
	}
	defer rows.Close()

	var out []SessionEntry
	for rows.Next() {
		var e SessionEntry
		var score int
		if err := rows.Scan(&e.CompletedAt, &e.ChapterID, &e.ChapterName, &e.Cards, &e.DurationMs, &score); err != nil {
			return nil, fmt.Errorf("scan session:\n%w", err)
		}
		if e.Cards > 0 {
			e.Accuracy = float64(score) / (2 * float64(e.Cards))
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// loadHeatmap returns the caller's training volume for subjectID over the last 8 full
// weeks (56 days), oldest first, zero-filled in Go for days with no recorded session.
func (s *Service) loadHeatmap(ctx context.Context, uid, subjectID int64) ([]DayIntensity, error) {
	rows, err := s.db.Query(ctx, `
		SELECT completed_at::date AS day, COALESCE(sum(total_cards), 0)
		FROM training_sessions
		WHERE subject_id = $1 AND user_id = $2 AND completed_at >= now() - interval '8 weeks'
		GROUP BY day
	`, subjectID, uid)
	if err != nil {
		return nil, fmt.Errorf("heatmap query:\n%w", err)
	}
	defer rows.Close()

	byDay := map[string]int{}
	for rows.Next() {
		var day time.Time
		var cards int
		if err := rows.Scan(&day, &cards); err != nil {
			return nil, fmt.Errorf("scan heatmap row:\n%w", err)
		}
		byDay[day.Format("2006-01-02")] = cards
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	today := todayUTC()
	out := make([]DayIntensity, 0, 56)
	for i := 55; i >= 0; i-- {
		key := today.AddDate(0, 0, -i).Format("2006-01-02")
		out = append(out, DayIntensity{Day: key, Cards: byDay[key]})
	}
	return out, nil
}

// loadChapterEntries aggregates per-chapter mastery (live, from flashcards.last_result)
// and per-chapter cards/minutes trained (from the caller's training_sessions rows) into
// one list, ordered by chapter_id. Chapters with flashcards but no recorded sessions
// still appear, with cards=0 / minutesTrained=0.
func (s *Service) loadChapterEntries(ctx context.Context, uid, subjectID int64) ([]ChapterEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT f.chapter_id, c.title,
			COUNT(*) FILTER (WHERE f.last_result = 2) AS good,
			COUNT(*) FILTER (WHERE f.last_result = 1) AS ok,
			COUNT(*) AS total
		FROM flashcards f
		JOIN chapters c ON c.id = f.chapter_id
		WHERE f.subject_id = $1 AND f.chapter_id IS NOT NULL
		GROUP BY f.chapter_id, c.title
	`, subjectID)
	if err != nil {
		return nil, fmt.Errorf("chapter mastery query:\n%w", err)
	}
	defer rows.Close()

	entries := map[int64]*ChapterEntry{}
	for rows.Next() {
		var chapterID int64
		var name string
		var good, ok, total int
		if err := rows.Scan(&chapterID, &name, &good, &ok, &total); err != nil {
			return nil, fmt.Errorf("scan chapter mastery:\n%w", err)
		}
		e := &ChapterEntry{ChapterID: chapterID, ChapterName: name}
		if total > 0 {
			e.MasteryPercent = (float64(good) + float64(ok)*0.5) / float64(total)
		}
		entries[chapterID] = e
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	trainRows, err := s.db.Query(ctx, `
		SELECT chapter_id, COALESCE(sum(total_cards), 0), COALESCE(sum(duration_ms), 0)::bigint
		FROM training_sessions
		WHERE subject_id = $1 AND user_id = $2 AND chapter_id IS NOT NULL
		GROUP BY chapter_id
	`, subjectID, uid)
	if err != nil {
		return nil, fmt.Errorf("chapter training query:\n%w", err)
	}
	defer trainRows.Close()
	for trainRows.Next() {
		var chapterID int64
		var cards, durationMs int64
		if err := trainRows.Scan(&chapterID, &cards, &durationMs); err != nil {
			return nil, fmt.Errorf("scan chapter training:\n%w", err)
		}
		if e, ok := entries[chapterID]; ok {
			e.Cards = int(cards)
			e.MinutesTrained = int(durationMs / 60000)
		}
		// Sessions recorded against a chapter that no longer has any flashcards
		// (all deleted) are dropped: ChapterEntry has no mastery source outside
		// flashcards, and there is no consumer for a chapter with cards=0 and no
		// mastery number to show alongside it.
	}
	if err := trainRows.Err(); err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]ChapterEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, *entries[id])
	}
	return out, nil
}

// validMasteryTrendPeriods allow-lists the period query values accepted by MasteryTrend.
var validMasteryTrendPeriods = map[string]bool{"7d": true, "30d": true, "all": true}

// MasteryTrend returns the subject's daily mastery-percent trend over the requested
// period, sourced from the subject_mastery_daily snapshot table.
func (s *Service) MasteryTrend(ctx context.Context, uid, subjectID int64, period string) (*MasteryTrendResponse, error) {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	if !validMasteryTrendPeriods[period] {
		return nil, myErrors.ErrInvalidInput
	}
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return nil, err
	}

	today := todayUTC()
	since := periodStart(period, sub.CreatedAt, today)

	// subject_mastery_daily rows are keyed by the subject's owner, not the viewing
	// caller — SnapshotMastery derives user_id from subjects.owner_id (mastery is a
	// subject-level aggregate over flashcards, not a per-viewer one), so a
	// non-owner viewer with read access must still be able to look up the owner's
	// snapshot rows. Filtering by uid here (mirroring the sessions/heatmap queries,
	// which really are per-recording-user) would return nothing for shared subjects.
	rows, err := s.db.Query(ctx, `
		SELECT day, mastery_percent FROM subject_mastery_daily
		WHERE subject_id = $1 AND user_id = $2 AND day >= $3
		ORDER BY day
	`, subjectID, sub.OwnerID, since)
	if err != nil {
		return nil, fmt.Errorf("mastery trend query:\n%w", err)
	}
	defer rows.Close()

	byDay := map[string]float64{}
	for rows.Next() {
		var day time.Time
		var pct float64
		if err := rows.Scan(&day, &pct); err != nil {
			return nil, fmt.Errorf("scan mastery trend row:\n%w", err)
		}
		byDay[day.Format("2006-01-02")] = pct
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	series := forwardFillSeries(since, today, byDay)
	var delta float64
	if len(series) >= 2 {
		delta = series[len(series)-1] - series[0]
	}
	return &MasteryTrendResponse{Period: period, Series: series, Delta: delta}, nil
}

// periodStart maps a MasteryTrend period string to the first day included in the range.
func periodStart(period string, createdAt, today time.Time) time.Time {
	switch period {
	case "7d":
		return today.AddDate(0, 0, -6)
	case "30d":
		return today.AddDate(0, 0, -29)
	default: // "all"
		c := createdAt.UTC()
		start := time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, time.UTC)
		if start.After(today) {
			return today
		}
		return start
	}
}

// forwardFillSeries builds one point per day from since through today (inclusive),
// forward-filling gaps from the last known value. Days before the first known value
// are omitted rather than backfilled with a placeholder, so the series naturally
// starts at the first available snapshot instead of a misleading flat/zero line.
func forwardFillSeries(since, today time.Time, byDay map[string]float64) []float64 {
	var series []float64
	var last float64
	haveLast := false
	for d := since; !d.After(today); d = d.AddDate(0, 0, 1) {
		if v, ok := byDay[d.Format("2006-01-02")]; ok {
			last, haveLast = v, true
			series = append(series, v)
		} else if haveLast {
			series = append(series, last)
		}
	}
	return series
}

// todayUTC returns the current calendar day at UTC midnight.
func todayUTC() time.Time {
	t := time.Now().UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// SnapshotMastery writes today's mastery snapshot for every subject with at least one
// flashcard. Invoked by the masterySnapshot cron job (24h interval); idempotent —
// re-running it the same day overwrites that day's row rather than duplicating it.
func (s *Service) SnapshotMastery(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO subject_mastery_daily
			(user_id, subject_id, day, total_cards, good_count, ok_count, bad_count, new_count, mastery_percent)
		SELECT
			sub.owner_id,
			f.subject_id,
			CURRENT_DATE,
			COUNT(*),
			COUNT(*) FILTER (WHERE f.last_result = 2),
			COUNT(*) FILTER (WHERE f.last_result = 1),
			COUNT(*) FILTER (WHERE f.last_result = 0),
			COUNT(*) FILTER (WHERE f.last_result = -1),
			CASE WHEN COUNT(*) > 0
				THEN (COUNT(*) FILTER (WHERE f.last_result = 2)::numeric
					+ COUNT(*) FILTER (WHERE f.last_result = 1)::numeric * 0.5) / COUNT(*)
				ELSE 0
			END
		FROM flashcards f
		JOIN subjects sub ON sub.id = f.subject_id
		GROUP BY sub.owner_id, f.subject_id
		ON CONFLICT (user_id, subject_id, day) DO UPDATE SET
			total_cards = EXCLUDED.total_cards,
			good_count = EXCLUDED.good_count,
			ok_count = EXCLUDED.ok_count,
			bad_count = EXCLUDED.bad_count,
			new_count = EXCLUDED.new_count,
			mastery_percent = EXCLUDED.mastery_percent
	`)
	if err != nil {
		return fmt.Errorf("snapshot mastery:\n%w", err)
	}
	return nil
}

// ListOwned returns all subjects owned by uid, excluding archived when includeArchived=false.
func (s *Service) ListOwned(ctx context.Context, uid int64, includeArchived bool) ([]Subject, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, owner_id, name, color, icon, tags, visibility, archived,
		       description, last_used, created_at, updated_at
		FROM subjects
		WHERE owner_id = $1 AND ($2 OR archived = false)
		ORDER BY last_used DESC NULLS LAST, id DESC
	`, uid, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("list subjects:\n%w", err)
	}
	defer rows.Close()
	return scanSubjects(rows)
}

// Update patches a subject; requires the caller to be owner.
func (s *Service) Update(ctx context.Context, uid, subjectID int64, in UpdateInput) (*Subject, error) {
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	if sub.OwnerID != uid {
		return nil, myErrors.ErrForbidden
	}
	name, color, icon, tags, vis, desc, archived, err := applySubjectPatch(sub, in)
	if err != nil {
		return nil, err
	}
	var out Subject
	err = s.db.QueryRow(ctx, `
		UPDATE subjects
		SET name=$1, color=$2, icon=$3, tags=$4, visibility=$5,
		    description=$6, archived=$7, updated_at=now()
		WHERE id=$8
		RETURNING id, owner_id, name, color, icon, tags, visibility, archived,
		          description, last_used, created_at, updated_at
	`, name, color, icon, tags, vis, desc, archived, subjectID).Scan(
		&out.ID, &out.OwnerID, &out.Name, &out.Color, &out.Icon, &out.Tags,
		&out.Visibility, &out.Archived, &out.Description, &out.LastUsed,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update subject:\n%w", err)
	}
	return &out, nil
}

// applySubjectPatch merges UpdateInput fields onto the existing Subject values.
// Returns the patched field values or ErrInvalidInput for constraint violations.
func applySubjectPatch(sub *Subject, in UpdateInput) (name, color, icon, tags, vis, desc string, archived bool, err error) {
	name, color, icon = sub.Name, sub.Color, sub.Icon
	tags, vis, desc, archived = sub.Tags, sub.Visibility, sub.Description, sub.Archived
	if in.Name != nil {
		if *in.Name == "" {
			return "", "", "", "", "", "", false, myErrors.ErrInvalidInput
		}
		name = *in.Name
	}
	if in.Color != nil {
		color = *in.Color
	}
	if in.Icon != nil {
		icon = *in.Icon
	}
	if in.Tags != nil {
		tags = *in.Tags
	}
	if in.Visibility != nil {
		v := *in.Visibility
		if v != "private" && v != "friends" && v != "public" {
			return "", "", "", "", "", "", false, myErrors.ErrInvalidInput
		}
		vis = v
	}
	if in.Description != nil {
		desc = *in.Description
	}
	if in.Archived != nil {
		archived = *in.Archived
	}
	return name, color, icon, tags, vis, desc, archived, nil
}

// Delete removes a subject; requires the caller to be owner.
func (s *Service) Delete(ctx context.Context, uid, subjectID int64) error {
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return err
	}
	if sub.OwnerID != uid {
		return myErrors.ErrForbidden
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM subjects WHERE id=$1`, subjectID); err != nil {
		return fmt.Errorf("delete subject:\n%w", err)
	}
	return nil
}

// TouchLastUsed sets last_used=now() on the subject (called by training/quiz flows later).
func (s *Service) TouchLastUsed(ctx context.Context, subjectID int64) error {
	if _, err := s.db.Exec(ctx, `UPDATE subjects SET last_used = now() WHERE id = $1`, subjectID); err != nil {
		return fmt.Errorf("touch subject last_used:\n%w", err)
	}
	return nil
}

func (s *Service) load(ctx context.Context, id int64) (*Subject, error) {
	var sub Subject
	err := s.db.QueryRow(ctx, `
		SELECT id, owner_id, name, color, icon, tags, visibility, archived,
		       description, last_used, created_at, updated_at
		FROM subjects WHERE id=$1
	`, id).Scan(
		&sub.ID, &sub.OwnerID, &sub.Name, &sub.Color, &sub.Icon, &sub.Tags,
		&sub.Visibility, &sub.Archived, &sub.Description, &sub.LastUsed,
		&sub.CreatedAt, &sub.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load subject:\n%w", err)
	}
	return &sub, nil
}

func scanSubjects(rows pgx.Rows) ([]Subject, error) {
	var out []Subject
	for rows.Next() {
		var sub Subject
		if err := rows.Scan(
			&sub.ID, &sub.OwnerID, &sub.Name, &sub.Color, &sub.Icon, &sub.Tags,
			&sub.Visibility, &sub.Archived, &sub.Description, &sub.LastUsed,
			&sub.CreatedAt, &sub.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan subject:\n%w", err)
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

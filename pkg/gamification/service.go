package gamification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
)

// Service owns gamification state: streaks, daily goals, sessions, achievements.
type Service struct {
	db     *pgxpool.Pool    // db is the shared pool
	access *access.Service  // access resolves subject permissions
	now    func() time.Time // now lets tests inject a fixed clock
}

// NewService constructs a Service with the real clock.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc, now: time.Now}
}

// SetClock replaces the clock; intended for tests only.
func (s *Service) SetClock(f func() time.Time) { s.now = f }

// GetState returns the streak + today's goal.
func (s *Service) GetState(ctx context.Context, uid int64) (Streak, DailyGoal, error) {
	st, err := s.streak(ctx, uid, false)
	if err != nil {
		return Streak{}, DailyGoal{}, err
	}
	dg, err := s.dailyGoal(ctx, uid, s.today())
	if err != nil {
		return Streak{}, DailyGoal{}, err
	}
	return st, dg, nil
}

// RecordSession inserts a training session and updates streak, daily goal, achievements.
// The caller must have at least viewer access on the subject.
func (s *Service) RecordSession(ctx context.Context, uid int64, in RecordSessionInput) (*RecordSessionResult, error) {
	if in.CardCount < 0 || in.DurationMs < 0 {
		return nil, myErrors.ErrInvalidInput
	}
	if err := s.assertSubjectAccess(ctx, uid, in.SubjectID); err != nil {
		return nil, err
	}
	sess, err := s.insertSession(ctx, uid, in)
	if err != nil {
		return nil, err
	}
	st, err := s.bumpStreak(ctx, uid)
	if err != nil {
		return nil, err
	}
	dg, err := s.bumpDailyGoal(ctx, uid, in.CardCount)
	if err != nil {
		return nil, err
	}
	awards, err := s.evaluateAchievements(ctx, uid, st)
	if err != nil {
		return nil, err
	}
	return &RecordSessionResult{Session: sess, Streak: st, DailyGoal: dg, NewlyAwarded: awards}, nil
}

// assertSubjectAccess fails when the user can't even read the target subject.
func (s *Service) assertSubjectAccess(ctx context.Context, uid, subjectID int64) error {
	lvl, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !lvl.CanRead() {
		return myErrors.ErrForbidden
	}
	return nil
}

// insertSession persists one training_sessions row and returns it populated.
func (s *Service) insertSession(ctx context.Context, uid int64, in RecordSessionInput) (TrainingSession, error) {
	var sess TrainingSession
	err := s.db.QueryRow(ctx, `
		INSERT INTO training_sessions (user_id, subject_id, chapter_id, total_cards, duration_ms, score)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, user_id, subject_id, chapter_id, total_cards, duration_ms, score, completed_at
	`, uid, in.SubjectID, in.ChapterID, in.CardCount, in.DurationMs, in.Score).Scan(
		&sess.ID, &sess.UserID, &sess.SubjectID, &sess.ChapterID, &sess.CardCount, &sess.DurationMs,
		&sess.Score, &sess.CreatedAt,
	)
	if err != nil {
		return TrainingSession{}, fmt.Errorf("insert training session:\n%w", err)
	}
	return sess, nil
}

// GetUserStats returns aggregate stats for the profile screen.
func (s *Service) GetUserStats(ctx context.Context, uid int64) (*UserStats, error) {
	var stats UserStats
	err := s.db.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM flashcards f
		   JOIN subjects s ON s.id=f.subject_id WHERE s.owner_id=$1),
		  (SELECT count(*) FROM training_sessions WHERE user_id=$1),
		  coalesce((SELECT current_days FROM streaks WHERE user_id=$1), 0),
		  coalesce((SELECT best_days FROM streaks WHERE user_id=$1), 0)
	`, uid).Scan(&stats.TotalCards, &stats.TotalSessions, &stats.CurrentStreak, &stats.LongestStreak)
	if err != nil {
		return nil, fmt.Errorf("user stats:\n%w", err)
	}
	return &stats, nil
}

// ListAchievements returns the full catalog with unlock timestamps for the user.
func (s *Service) ListAchievements(ctx context.Context, uid int64) ([]Achievement, error) {
	unlocked, err := s.loadUnlocked(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]Achievement, 0, len(achievementDefs))
	for _, def := range achievementDefs {
		a := def
		if t, ok := unlocked[def.Code]; ok {
			t := t
			a.UnlockedAt = &t
		}
		out = append(out, a)
	}
	return out, nil
}

// loadUnlocked reads every unlocked_achievements row for uid into a code→time map.
func (s *Service) loadUnlocked(ctx context.Context, uid int64) (map[string]time.Time, error) {
	rows, err := s.db.Query(ctx,
		`SELECT achievement_key, unlocked_at FROM unlocked_achievements WHERE user_id=$1`, uid)
	if err != nil {
		return nil, fmt.Errorf("list unlocked achievements:\n%w", err)
	}
	defer rows.Close()
	unlocked := map[string]time.Time{}
	for rows.Next() {
		var code string
		var at time.Time
		if err := rows.Scan(&code, &at); err != nil {
			return nil, fmt.Errorf("scan achievement:\n%w", err)
		}
		unlocked[code] = at
	}
	return unlocked, nil
}

// today returns today's calendar day at UTC midnight, using the injected clock.
func (s *Service) today() time.Time {
	t := s.now().UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// streak loads the streak row for uid. When missing and mustExist is false it returns a
// zero-valued Streak with UserID populated; otherwise it returns ErrNotFound.
func (s *Service) streak(ctx context.Context, uid int64, mustExist bool) (Streak, error) {
	var st Streak
	err := s.db.QueryRow(ctx, `
		SELECT user_id, current_days, best_days, last_studied_date, updated_at
		FROM streaks WHERE user_id=$1
	`, uid).Scan(&st.UserID, &st.CurrentStreak, &st.LongestStreak, &st.LastDay, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if mustExist {
			return Streak{}, myErrors.ErrNotFound
		}
		return Streak{UserID: uid}, nil
	}
	if err != nil {
		return Streak{}, fmt.Errorf("load streak:\n%w", err)
	}
	return st, nil
}

// bumpStreak increments or resets the streak based on the gap to the last recorded day,
// then upserts the new row.
func (s *Service) bumpStreak(ctx context.Context, uid int64) (Streak, error) {
	today := s.today()
	st, err := s.streak(ctx, uid, false)
	if err != nil {
		return Streak{}, err
	}
	current := nextStreakValue(st, today)
	longest := st.LongestStreak
	if current > longest {
		longest = current
	}
	return s.upsertStreak(ctx, uid, current, longest, today)
}

// nextStreakValue computes the new current-streak count given the prior row and today's date.
func nextStreakValue(st Streak, today time.Time) int {
	current := st.CurrentStreak
	switch {
	case st.LastDay == nil:
		return 1
	case sameDay(*st.LastDay, today):
		if current == 0 {
			return 1
		}
		return current
	case sameDay(st.LastDay.Add(24*time.Hour), today):
		return current + 1
	default:
		return 1
	}
}

// upsertStreak writes the computed streak counters and returns the refreshed row.
func (s *Service) upsertStreak(ctx context.Context, uid int64, current, longest int, today time.Time) (Streak, error) {
	var out Streak
	err := s.db.QueryRow(ctx, `
		INSERT INTO streaks (user_id, current_days, best_days, last_studied_date, updated_at)
		VALUES ($1,$2,$3,$4, now())
		ON CONFLICT (user_id) DO UPDATE
		  SET current_days=EXCLUDED.current_days,
		      best_days=EXCLUDED.best_days,
		      last_studied_date=EXCLUDED.last_studied_date,
		      updated_at=now()
		RETURNING user_id, current_days, best_days, last_studied_date, updated_at
	`, uid, current, longest, today).Scan(
		&out.UserID, &out.CurrentStreak, &out.LongestStreak, &out.LastDay, &out.UpdatedAt,
	)
	if err != nil {
		return Streak{}, fmt.Errorf("upsert streak:\n%w", err)
	}
	return out, nil
}

// dailyGoal ensures a daily_goals row exists for (uid, day) and returns it.
func (s *Service) dailyGoal(ctx context.Context, uid int64, day time.Time) (DailyGoal, error) {
	var dg DailyGoal
	err := s.db.QueryRow(ctx, `
		INSERT INTO daily_goals (user_id, day, target, done_today)
		VALUES ($1,$2, coalesce((SELECT daily_goal_target FROM preferences WHERE user_id=$1), 20), 0)
		ON CONFLICT (user_id, day) DO UPDATE SET user_id=EXCLUDED.user_id
		RETURNING user_id, day, done_today, target
	`, uid, day).Scan(&dg.UserID, &dg.Day, &dg.DoneToday, &dg.Target)
	if err != nil {
		return DailyGoal{}, fmt.Errorf("upsert daily goal:\n%w", err)
	}
	return dg, nil
}

// bumpDailyGoal ensures today's daily_goals row exists then adds inc to done_today.
func (s *Service) bumpDailyGoal(ctx context.Context, uid int64, inc int) (DailyGoal, error) {
	day := s.today()
	if _, err := s.dailyGoal(ctx, uid, day); err != nil {
		return DailyGoal{}, err
	}
	var out DailyGoal
	err := s.db.QueryRow(ctx, `
		UPDATE daily_goals SET done_today = done_today + $1
		WHERE user_id=$2 AND day=$3
		RETURNING user_id, day, done_today, target
	`, inc, uid, day).Scan(&out.UserID, &out.Day, &out.DoneToday, &out.Target)
	if err != nil {
		return DailyGoal{}, fmt.Errorf("bump daily goal:\n%w", err)
	}
	return out, nil
}

// sameDay returns true iff a and b fall on the same calendar day.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// evaluateAchievements checks and records unlocks triggered by the just-recorded session.
func (s *Service) evaluateAchievements(ctx context.Context, uid int64, st Streak) ([]Achievement, error) {
	total, err := s.totalSessions(ctx, uid)
	if err != nil {
		return nil, err
	}
	totalCards, err := s.totalCardsReviewed(ctx, uid)
	if err != nil {
		return nil, err
	}
	earned := evaluateThresholds(st, total, totalCards)
	return s.persistUnlocks(ctx, uid, earned)
}

// evaluateThresholds maps streak + totals onto the achievement codes that are earned.
func evaluateThresholds(st Streak, totalSessions, totalCards int) map[string]bool {
	earned := map[string]bool{}
	if totalSessions >= 1 {
		earned["first_session"] = true
	}
	if st.CurrentStreak >= 3 {
		earned["streak_3"] = true
	}
	if st.CurrentStreak >= 7 {
		earned["streak_7"] = true
	}
	if st.CurrentStreak >= 30 {
		earned["streak_30"] = true
	}
	if totalCards >= 100 {
		earned["cards_100"] = true
	}
	if totalCards >= 1000 {
		earned["cards_1000"] = true
	}
	return earned
}

// totalSessions returns the lifetime training_sessions count for uid.
func (s *Service) totalSessions(ctx context.Context, uid int64) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM training_sessions WHERE user_id=$1`, uid,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count sessions:\n%w", err)
	}
	return n, nil
}

// totalCardsReviewed returns the lifetime sum of total_cards across all of uid's sessions.
func (s *Service) totalCardsReviewed(ctx context.Context, uid int64) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT coalesce(sum(total_cards),0) FROM training_sessions WHERE user_id=$1`, uid,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("sum cards:\n%w", err)
	}
	return n, nil
}

// persistUnlocks writes each earned achievement, skipping those already unlocked, and
// returns the set newly awarded by this call. Always non-nil so it serializes as
// [] rather than null when nothing new was unlocked.
func (s *Service) persistUnlocks(ctx context.Context, uid int64, earned map[string]bool) ([]Achievement, error) {
	cat := catalog()
	newly := make([]Achievement, 0, len(earned))
	for code := range earned {
		var at time.Time
		err := s.db.QueryRow(ctx, `
			INSERT INTO unlocked_achievements (user_id, achievement_key)
			VALUES ($1,$2) ON CONFLICT DO NOTHING
			RETURNING unlocked_at
		`, uid, code).Scan(&at)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("insert achievement %s:\n%w", code, err)
		}
		a := cat[code]
		a.UnlockedAt = &at
		newly = append(newly, a)
	}
	return newly, nil
}

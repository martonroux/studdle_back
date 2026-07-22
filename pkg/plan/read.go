package plan

import (
	"context"
	"errors"
	"fmt"
	"time"

	"studdle/backend/internal/myErrors"
)

// GetPlan returns the stored plan for examID along with today's bucket and drift.
// Caller must own the exam.
func (s *Service) GetPlan(ctx context.Context, userID, examID int64) (*PlanView, error) {
	if _, err := s.exam.Get(ctx, userID, examID); err != nil {
		return nil, err
	}
	plan, err := s.loadPlanByExam(ctx, examID)
	if err != nil {
		if notFound(err) {
			return nil, myErrors.ErrNotFound
		}
		return nil, err
	}
	progress, err := s.loadProgressByDate(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &PlanView{
		Plan:  *plan,
		Today: buildToday(plan.Days, progress, now),
		Drift: CalculateDrift(plan.Days, progress, now),
	}, nil
}

// MarkDone records a flashcard completion against today's plan-progress row.
// Idempotent: a duplicate insert is silently ignored.
// Errors when the user does not own examID.
func (s *Service) MarkDone(ctx context.Context, userID, examID, fcID int64) error {
	if _, err := s.exam.Get(ctx, userID, examID); err != nil {
		return err
	}
	if err := assertFlashcardExists(ctx, s, fcID); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
        INSERT INTO revision_plan_progress (user_id, fc_id, plan_date)
        VALUES ($1, $2, current_date)
        ON CONFLICT (user_id, fc_id, plan_date) DO NOTHING
    `, userID, fcID)
	if err != nil {
		return fmt.Errorf("mark done:\n%w", err)
	}
	return nil
}

// assertFlashcardExists rejects mark-done calls for a deleted/non-existent FC.
// This makes a stale UI's mark-done call return a clear 404 rather than silently
// inserting an orphan row that would later fail the FK constraint.
func assertFlashcardExists(ctx context.Context, s *Service, fcID int64) error {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM flashcards WHERE id = $1`, fcID).Scan(&n)
	if err != nil {
		return fmt.Errorf("check flashcard:\n%w", err)
	}
	if n == 0 {
		return errors.Join(myErrors.ErrNotFound, fmt.Errorf("flashcard %d", fcID))
	}
	return nil
}

// buildToday picks the day matching `now` (or returns an empty bucket) and
// projects it into a TodayBucket with completion flags.
func buildToday(days []Day, progressByDate map[string]map[int64]bool, now time.Time) TodayBucket {
	todayKey := startOfDay(now).Format(dateLayout)
	for _, d := range days {
		if d.Date != todayKey {
			continue
		}
		return makeTodayBucket(d, progressByDate[todayKey])
	}
	return TodayBucket{Date: todayKey}
}

// makeTodayBucket projects a Day plus its progress set into the user-facing form.
func makeTodayBucket(d Day, doneIDs map[int64]bool) TodayBucket {
	done := []int64{}
	required := append([]int64(nil), d.PrimarySubjectCards...)
	required = append(required, d.CrossSubjectCards...)
	for _, id := range required {
		if doneIDs[id] {
			done = append(done, id)
		}
	}
	return TodayBucket{
		Date:                d.Date,
		PrimarySubjectCards: d.PrimarySubjectCards,
		CrossSubjectCards:   d.CrossSubjectCards,
		DeeperDives:         d.DeeperDives,
		Done:                done,
		DailyGoalMet:        len(required) > 0 && len(done) >= len(required),
	}
}

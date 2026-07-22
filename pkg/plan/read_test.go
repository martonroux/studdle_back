package plan

import (
	"context"
	"testing"
	"time"

	"studdle/backend/pkg/access"
	"studdle/backend/pkg/exam"
	"studdle/backend/testutil"
)

// TestGetPlan_AfterMarkDone is a regression test for AI-2: pgx v5's binary
// protocol cannot decode a Postgres `date` column straight into a Go string,
// so any row written by MarkDone previously made every subsequent GetPlan
// call fail while scanning revision_plan_progress.plan_date. Reproduces by
// persisting a plan, marking a card done, then reading the plan back.
func TestGetPlan_AfterMarkDone(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	ctx := context.Background()

	owner := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, owner.ID)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")

	var examID int64
	if err := pool.QueryRow(ctx, `
        INSERT INTO exams (user_id, subject_id, date, title)
        VALUES ($1, $2, $3, 'Partiel') RETURNING id
    `, owner.ID, subj.ID, time.Now().AddDate(0, 0, 14)).Scan(&examID); err != nil {
		t.Fatalf("seed exam: %v", err)
	}

	acc := access.NewService(pool)
	s := &Service{db: pool, exam: exam.NewService(pool, acc), model: "test-model"}

	today := time.Now().UTC().Format(dateLayout)
	days := []Day{{Date: today, PrimarySubjectCards: []int64{fcID}}}
	if _, err := s.persist(ctx, examID, days, "test-model", "deadbeef", nil); err != nil {
		t.Fatalf("persist: %v", err)
	}

	if err := s.MarkDone(ctx, owner.ID, examID, fcID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	// GetPlan must not error: this is the literal repro for AI-2 (pgx v5
	// cannot scan a Postgres `date` column into a Go string, so any row
	// written by MarkDone previously broke every subsequent GetPlan call).
	if _, err := s.GetPlan(ctx, owner.ID, examID); err != nil {
		t.Fatalf("GetPlan after MarkDone: %v", err)
	}

	// loadProgressByDate must decode plan_date into a well-formed YYYY-MM-DD
	// map key (not scan garbage), independent of the DB session's timezone
	// relative to the Go process's UTC clock.
	progress, err := s.loadProgressByDate(ctx, owner.ID)
	if err != nil {
		t.Fatalf("loadProgressByDate: %v", err)
	}
	if len(progress) != 1 {
		t.Fatalf("progress has %d date keys, want 1: %v", len(progress), progress)
	}
	for key, ids := range progress {
		if _, err := time.Parse(dateLayout, key); err != nil {
			t.Errorf("progress date key %q is not well-formed YYYY-MM-DD: %v", key, err)
		}
		if !ids[fcID] {
			t.Errorf("progress[%q] = %v, want to contain flashcard %d", key, ids, fcID)
		}
	}
}

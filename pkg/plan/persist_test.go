package plan

import (
	"context"
	"testing"
	"time"

	"studdle/backend/testutil"
)

// TestPersist_WritesGenerationID confirms the generation_id column is populated
// when persist receives a non-nil jobID, and that loadPlanByExam round-trips it.
func TestPersist_WritesGenerationID(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	ctx := context.Background()

	var userID, subjectID, examID, jobID int64
	if err := pool.QueryRow(ctx, `
        INSERT INTO users (username, email, password_hash)
        VALUES ('persist-gen-id', 'persist-gen-id@example.com', 'x') RETURNING id
    `).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
        INSERT INTO subjects (owner_id, name) VALUES ($1, 'Bio') RETURNING id
    `, userID).Scan(&subjectID); err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	if err := pool.QueryRow(ctx, `
        INSERT INTO exams (user_id, subject_id, date, title)
        VALUES ($1, $2, $3, 'Partiel') RETURNING id
    `, userID, subjectID, time.Now().AddDate(0, 0, 14)).Scan(&examID); err != nil {
		t.Fatalf("seed exam: %v", err)
	}
	if err := pool.QueryRow(ctx, `
        INSERT INTO ai_jobs (user_id, feature_key, model, status)
        VALUES ($1, 'revision_plan', 'test-model', 'complete') RETURNING id
    `, userID).Scan(&jobID); err != nil {
		t.Fatalf("seed ai_job: %v", err)
	}

	s := &Service{db: pool, model: "test-model"}
	days := []Day{{Date: "2026-05-09", PrimarySubjectCards: []int64{}}}
	plan, err := s.persist(ctx, examID, days, "test-model", "deadbeef", &jobID)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if plan.GenerationID == nil || *plan.GenerationID != jobID {
		t.Fatalf("plan.GenerationID = %v, want %d", plan.GenerationID, jobID)
	}

	loaded, err := s.loadPlanByExam(ctx, examID)
	if err != nil {
		t.Fatalf("loadPlanByExam: %v", err)
	}
	if loaded.GenerationID == nil || *loaded.GenerationID != jobID {
		t.Fatalf("loaded.GenerationID = %v, want %d", loaded.GenerationID, jobID)
	}
}

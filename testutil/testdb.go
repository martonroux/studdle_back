package testutil

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	dbsql "studdle/backend/db_sql"
)

var (
	poolOnce sync.Once
	poolRef  *pgxpool.Pool
	poolErr  error
)

// MustTestEnv aborts the test unless ENV=test and DATABASE_URL points at studbud_test.
func MustTestEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("ENV") != "test" {
		t.Skip("ENV must be 'test' to run DB-backed tests")
	}
	dsn := os.Getenv("DATABASE_URL")
	if !strings.HasSuffix(dsn, "/studbud_test") &&
		!strings.HasSuffix(dsn, "/studbud_test?sslmode=disable") {
		t.Fatalf("refusing to run tests against %q — must end with /studbud_test", dsn)
	}
}

// OpenTestDB returns the shared test pool, running SetupAll once per process.
func OpenTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	MustTestEnv(t)
	poolOnce.Do(func() {
		ctx := context.Background()
		poolRef, poolErr = pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
		if poolErr != nil {
			return
		}
		poolErr = dbsql.SetupAll(ctx, poolRef)
	})
	if poolErr != nil {
		t.Fatalf("test db setup: %v", poolErr)
	}
	return poolRef
}

// Reset truncates every table. Run at the start of each test.
func Reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateAllSQL)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

const truncateAllSQL = `
TRUNCATE TABLE
  user_reports, duel_head_to_head, duel_user_stats, duel_round_answers,
  duel_round_questions, duel_invite_tokens, duels,
  quiz_quality_reports, quiz_sent_to_friends, quiz_share_links,
  quiz_attempt_answers, quiz_attempts, quiz_questions, quizzes,
  revision_plan_progress, revision_plans, exams,
  billing_events, user_subscriptions,
  flashcard_keywords, ai_extraction_jobs, ai_quota_daily, ai_jobs,
  unlocked_achievements, user_session_bests, training_sessions,
  daily_goals, streaks, preferences,
  invite_links, collaborators, subject_subscriptions, friendships,
  flashcards, chapters, subjects,
  images, email_verification_throttle, email_verifications, users
RESTART IDENTITY CASCADE;
`

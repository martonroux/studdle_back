package testutil

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// UserFixture is a minimal user row returned by NewUser.
type UserFixture struct {
	ID            int64
	Username      string
	Email         string
	EmailVerified bool
}

// fixtureCounter produces collision-free usernames / subject names within a test binary.
var fixtureCounter atomic.Int64

// nextName returns a fresh identifier with the given prefix.
func nextName(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, fixtureCounter.Add(1))
}

// NewUser inserts an unverified user with an auto-generated username. Returns the fixture.
func NewUser(t *testing.T, pool *pgxpool.Pool) *UserFixture {
	t.Helper()
	return insertUser(t, pool, nextName("user"), false)
}

// NewVerifiedUser inserts a verified user with an auto-generated username.
func NewVerifiedUser(t *testing.T, pool *pgxpool.Pool) *UserFixture {
	t.Helper()
	return insertUser(t, pool, nextName("user"), true)
}

// NewVerifiedUserNamed inserts a verified user with an explicit username.
func NewVerifiedUserNamed(t *testing.T, pool *pgxpool.Pool, username string) *UserFixture {
	t.Helper()
	return insertUser(t, pool, username, true)
}

func insertUser(t *testing.T, pool *pgxpool.Pool, username string, verified bool) *UserFixture {
	t.Helper()
	email := username + "@example.com"
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	var id int64
	err = pool.QueryRow(context.Background(), `
        INSERT INTO users (username, email, password_hash, email_verified, verified_at)
        VALUES ($1, $2, $3, $4, CASE WHEN $4 THEN now() ELSE NULL END)
        RETURNING id
    `, username, email, string(hash), verified).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return &UserFixture{ID: id, Username: username, Email: email, EmailVerified: verified}
}

// SubjectFixture is a minimal subject row returned by NewSubject.
type SubjectFixture struct {
	ID      int64
	OwnerID int64
	Name    string
}

// NewSubject inserts a private subject owned by ownerID with an auto-generated name.
func NewSubject(t *testing.T, pool *pgxpool.Pool, ownerID int64) *SubjectFixture {
	t.Helper()
	return insertSubject(t, pool, ownerID, nextName("subj"), "private")
}

// NewSubjectNamed inserts a subject with an explicit name and visibility.
func NewSubjectNamed(t *testing.T, pool *pgxpool.Pool, ownerID int64, name, visibility string) *SubjectFixture {
	t.Helper()
	return insertSubject(t, pool, ownerID, name, visibility)
}

func insertSubject(t *testing.T, pool *pgxpool.Pool, ownerID int64, name, visibility string) *SubjectFixture {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
        INSERT INTO subjects (owner_id, name, visibility)
        VALUES ($1, $2, $3)
        RETURNING id
    `, ownerID, name, visibility).Scan(&id)
	if err != nil {
		t.Fatalf("insert subject: %v", err)
	}
	return &SubjectFixture{ID: id, OwnerID: ownerID, Name: name}
}

// NewQuiz inserts a kind=global quizzes row owned by ownerID, plus questionCount
// MCQ quiz_questions rows (correct index = 2 for all). Returns the new quiz id.
// Creates a minimal subject internally so callers don't need to seed one.
func NewQuiz(t *testing.T, pool *pgxpool.Pool, ownerID int64, questionCount int) int64 {
	t.Helper()
	sub := NewSubject(t, pool, ownerID)
	var qid int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO quizzes (user_id, subject_id, kind, source, card_pool_jsonb, settings_jsonb,
		                     question_count, model, prompt_hash)
		VALUES ($1, $2, 'global', 'user', '[]'::jsonb, '{}'::jsonb, $3, 'test', 'h')
		RETURNING id`, ownerID, sub.ID, questionCount,
	).Scan(&qid)
	if err != nil {
		t.Fatalf("insert quiz: %v", err)
	}
	for i := 1; i <= questionCount; i++ {
		_, err := pool.Exec(context.Background(), `
			INSERT INTO quiz_questions (quiz_id, ordinal, question_type, stem,
			                            options_jsonb, correct_jsonb, referenced_fc_ids_jsonb)
			VALUES ($1, $2, 'multi_choice', $3, '["A","B","C","D"]'::jsonb, '{"index":2}'::jsonb, '[]'::jsonb)`,
			qid, i, fmt.Sprintf("Question %d", i))
		if err != nil {
			t.Fatalf("insert quiz_questions[%d]: %v", i, err)
		}
	}
	return qid
}

// NewChapter inserts a chapter under the subject.
func NewChapter(t *testing.T, pool *pgxpool.Pool, subjectID int64, title string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
        INSERT INTO chapters (subject_id, title)
        VALUES ($1, $2)
        RETURNING id
    `, subjectID, title).Scan(&id)
	if err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	return id
}

// NewFlashcard inserts a flashcard under the subject (chapter optional; pass 0 for null).
func NewFlashcard(t *testing.T, pool *pgxpool.Pool, subjectID, chapterID int64, q, a string) int64 {
	t.Helper()
	var id int64
	var chPtr *int64
	if chapterID > 0 {
		chPtr = &chapterID
	}
	err := pool.QueryRow(context.Background(), `
        INSERT INTO flashcards (subject_id, chapter_id, question, answer)
        VALUES ($1, $2, $3, $4)
        RETURNING id
    `, subjectID, chPtr, q, a).Scan(&id)
	if err != nil {
		t.Fatalf("insert flashcard: %v", err)
	}
	return id
}

// GiveAIAccess inserts an active user_subscriptions row so user_has_ai_access returns true.
func GiveAIAccess(t *testing.T, pool *pgxpool.Pool, uid int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
        INSERT INTO user_subscriptions (user_id, plan, status, current_period_end)
        VALUES ($1, 'pro_monthly', 'active', now() + interval '30 days')
    `, uid)
	if err != nil {
		t.Fatalf("give AI access: %v", err)
	}
}

// ExhaustQuota bumps the named counter to 10_000 for today.
func ExhaustQuota(t *testing.T, pool *pgxpool.Pool, uid int64, column string) {
	t.Helper()
	if !isKnownQuotaColumn(column) {
		t.Fatalf("unknown quota column %q", column)
	}
	sql := fmt.Sprintf(`
        INSERT INTO ai_quota_daily (user_id, day, %[1]s)
        VALUES ($1, current_date, 10000)
        ON CONFLICT (user_id, day) DO UPDATE SET %[1]s = 10000
    `, column)
	if _, err := pool.Exec(context.Background(), sql, uid); err != nil {
		t.Fatalf("exhaust quota: %v", err)
	}
}

func isKnownQuotaColumn(col string) bool {
	switch col {
	case "prompt_calls", "pdf_calls", "pdf_pages", "check_calls",
		"plan_calls", "cross_subject_rank_calls", "quiz_calls",
		"extract_keywords_calls":
		return true
	}
	return false
}

// Now returns a fixed clock value used in time-sensitive fixtures.
func Now() time.Time { return time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC) }

// SeedQuotaAt sets the named quota counter for the given user to value for today.
// Inserts a row if none exists.
func SeedQuotaAt(t *testing.T, pool *pgxpool.Pool, uid int64, column string, value int) {
	t.Helper()
	if !isKnownQuotaColumn(column) {
		t.Fatalf("unknown quota column %q", column)
	}
	sql := fmt.Sprintf(`
        INSERT INTO ai_quota_daily (user_id, day, %[1]s)
        VALUES ($1, current_date, $2)
        ON CONFLICT (user_id, day) DO UPDATE SET %[1]s = EXCLUDED.%[1]s
    `, column)
	if _, err := pool.Exec(context.Background(), sql, uid, value); err != nil {
		t.Fatalf("seed quota: %v", err)
	}
}

// SeedRunningJob inserts an ai_jobs row in status=running and returns its id.
// Use to simulate concurrent-generation scenarios.
func SeedRunningJob(t *testing.T, pool *pgxpool.Pool, uid int64, feature string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
        INSERT INTO ai_jobs (user_id, feature_key, model, status, metadata)
        VALUES ($1, $2, 'test-model', 'running', '{}'::jsonb)
        RETURNING id
    `, uid, feature).Scan(&id)
	if err != nil {
		t.Fatalf("seed running job: %v", err)
	}
	return id
}

// GiveAICompAccess inserts a comp user_subscriptions row (admin-granted).
func GiveAICompAccess(t *testing.T, pool *pgxpool.Pool, uid int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
        INSERT INTO user_subscriptions (user_id, plan, status)
        VALUES ($1, 'comp', 'comped')
    `, uid)
	if err != nil {
		t.Fatalf("give comp access: %v", err)
	}
}

// CountAIJobs returns the number of ai_jobs rows for user uid.
func CountAIJobs(t *testing.T, pool *pgxpool.Pool, uid int64) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `SELECT count(*) FROM ai_jobs WHERE user_id = $1`, uid).Scan(&n)
	if err != nil {
		t.Fatalf("count ai_jobs: %v", err)
	}
	return n
}

// MakeAdmin sets users.is_admin = true for uid.
func MakeAdmin(t *testing.T, pool *pgxpool.Pool, uid int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE users SET is_admin = true WHERE id = $1`, uid); err != nil {
		t.Fatalf("make admin: %v", err)
	}
}

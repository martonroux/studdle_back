package db_sql_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/testutil"
)

// TestQuizSchema_MatchesSpec asserts the post-migration shape per Spec D §2.
// Runs against the shared test pool; the migration sequence in SetupAll is
// expected to leave the tables in the spec shape.
func TestQuizSchema_MatchesSpec(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	// quizzes: required columns
	for _, col := range []string{
		"user_id", "subject_id", "chapter_id", "kind", "source",
		"source_plan_id", "source_share_token",
		"card_pool_jsonb", "settings_jsonb",
		"question_count", "model", "prompt_hash", "created_at",
	} {
		requireColumn(t, pool, "quizzes", col)
	}

	// quizzes: removed columns are gone
	for _, col := range []string{"owner_id", "title", "parent_quiz_id", "duel_id"} {
		requireColumnAbsent(t, pool, "quizzes", col)
	}

	// quizzes.subject_id must be NOT NULL
	requireNotNull(t, pool, "quizzes", "subject_id")

	// quizzes.kind CHECK accepts 'specific' and 'global'; quizzes.source accepts
	// 'user', 'plan', 'shared_copy' and rejects 'duel'.
	requireCheckAccepts(t, pool, `INSERT INTO quizzes (user_id, subject_id, kind, source, card_pool_jsonb, settings_jsonb, question_count, model, prompt_hash) SELECT id, $1, 'specific', 'user', '[]'::jsonb, '{}'::jsonb, 0, 'm', 'h' FROM users LIMIT 1`)
	requireCheckRejects(t, pool, `INSERT INTO quizzes (user_id, subject_id, kind, source, card_pool_jsonb, settings_jsonb, question_count, model, prompt_hash) SELECT id, $1, 'specific', 'duel', '[]'::jsonb, '{}'::jsonb, 0, 'm', 'h' FROM users LIMIT 1`)

	// quiz_questions: renamed/added columns
	for _, col := range []string{
		"ordinal", "question_type", "stem", "options_jsonb",
		"correct_jsonb", "referenced_fc_ids_jsonb", "explanation",
	} {
		requireColumn(t, pool, "quiz_questions", col)
	}
	for _, col := range []string{"position", "prompt", "choices", "correct_index", "source_flashcard_id"} {
		requireColumnAbsent(t, pool, "quiz_questions", col)
	}
	requireUniqueIndex(t, pool, "quiz_questions", []string{"quiz_id", "ordinal"})

	// quiz_attempts: state column + indexes
	for _, col := range []string{
		"state", "correct_count", "total_count", "score_pct",
		"completed_at", "plan_id", "plan_date",
	} {
		requireColumn(t, pool, "quiz_attempts", col)
	}
	requireColumnAbsent(t, pool, "quiz_attempts", "finished_at")
	requirePartialUniqueIndex(t, pool, "quiz_attempts",
		[]string{"quiz_id", "user_id"}, "state = 'in_progress'")

	// quiz_attempt_answers: renamed columns
	requireColumn(t, pool, "quiz_attempt_answers", "user_answer_jsonb")
	requireColumn(t, pool, "quiz_attempt_answers", "correct")
	for _, col := range []string{"chosen_index", "is_correct"} {
		requireColumnAbsent(t, pool, "quiz_attempt_answers", col)
	}
}

// requireColumn is defined in setup_billing_test.go (same package db_sql_test).

// requireColumnAbsent fails the test if table.col exists in the schema.
func requireColumnAbsent(t *testing.T, pool *pgxpool.Pool, table, col string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		    WHERE table_name = $1 AND column_name = $2)`, table, col).Scan(&exists)
	if err != nil {
		t.Fatalf("query column %s.%s: %v", table, col, err)
	}
	if exists {
		t.Fatalf("column %s.%s should not exist", table, col)
	}
}

// requireNotNull fails the test if table.col is nullable.
func requireNotNull(t *testing.T, pool *pgxpool.Pool, table, col string) {
	t.Helper()
	var nullable string
	err := pool.QueryRow(context.Background(),
		`SELECT is_nullable FROM information_schema.columns
		   WHERE table_name = $1 AND column_name = $2`, table, col).Scan(&nullable)
	if err != nil {
		t.Fatalf("query nullable %s.%s: %v", table, col, err)
	}
	if nullable != "NO" {
		t.Fatalf("%s.%s is_nullable = %q, want NO", table, col, nullable)
	}
}

// requireUniqueIndex fails the test if no UNIQUE index covers all of cols on table.
func requireUniqueIndex(t *testing.T, pool *pgxpool.Pool, table string, cols []string) {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = $1
		  AND indexdef ILIKE '%UNIQUE%'
		  AND `+columnsLike(cols), table).Scan(&n)
	if err != nil {
		t.Fatalf("query unique index on %s%v: %v", table, cols, err)
	}
	if n == 0 {
		t.Fatalf("no UNIQUE index on %s%v", table, cols)
	}
}

// requirePartialUniqueIndex fails the test if no UNIQUE index with the given WHERE clause covers cols on table.
func requirePartialUniqueIndex(t *testing.T, pool *pgxpool.Pool, table string, cols []string, where string) {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = $1
		  AND indexdef ILIKE '%UNIQUE%'
		  AND indexdef ILIKE '%' || $2 || '%'
		  AND `+columnsLike(cols), table, where).Scan(&n)
	if err != nil {
		t.Fatalf("query partial unique on %s: %v", table, err)
	}
	if n == 0 {
		t.Fatalf("no partial UNIQUE on %s%v WHERE %s", table, cols, where)
	}
}

// columnsLike returns a SQL predicate fragment that ANDs an ILIKE on indexdef for each column name.
func columnsLike(cols []string) string {
	q := ""
	for i, c := range cols {
		if i > 0 {
			q += " AND "
		}
		q += "indexdef ILIKE '%" + c + "%'"
	}
	return q
}

// requireCheckAccepts fails the test if the given INSERT (parameterised on $1=subject_id) is rejected by a CHECK constraint.
func requireCheckAccepts(t *testing.T, pool *pgxpool.Pool, q string) {
	t.Helper()
	subjectID := seedSubject(t, pool)
	if _, err := pool.Exec(context.Background(), q, subjectID); err != nil {
		t.Fatalf("CHECK should have accepted; got %v", err)
	}
}

// requireCheckRejects fails the test if the given INSERT (parameterised on $1=subject_id) is accepted by a CHECK constraint.
func requireCheckRejects(t *testing.T, pool *pgxpool.Pool, q string) {
	t.Helper()
	subjectID := seedSubject(t, pool)
	if _, err := pool.Exec(context.Background(), q, subjectID); err == nil {
		t.Fatalf("CHECK should have rejected; INSERT succeeded")
	}
}

// seedSubject inserts a minimal subject (and its required user) for CHECK probes.
// NOTE: subjects.owner_id is the FK column (not user_id) per setup_core.go.
func seedSubject(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	u := testutil.NewVerifiedUser(t, pool)
	var sid int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO subjects (owner_id, name) VALUES ($1, 'probe') RETURNING id`, u.ID,
	).Scan(&sid)
	if err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	return sid
}

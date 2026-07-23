package keywordWorker

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/pkg/aipipeline"
	"studdle/backend/testutil"
)

// fakeProv emits a single fixed chunk on each Stream call.
type fakeProv struct {
	body string // body is the JSON payload returned in one Done chunk
}

// Stream returns a channel that yields one chunk and closes.
func (f *fakeProv) Stream(_ context.Context, _ aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	ch := make(chan aiProvider.Chunk, 1)
	ch <- aiProvider.Chunk{Text: f.body, Done: true}
	close(ch)

	return ch, nil
}

// blockingProv never yields a chunk, forcing callers to observe ctx
// cancellation (used to simulate a job interrupted by shutdown).
type blockingProv struct{}

// Stream returns a channel that never produces a value.
func (b *blockingProv) Stream(_ context.Context, _ aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	return make(chan aiProvider.Chunk), nil
}

func TestRunOnce_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.NewSubject(t, pool, uid)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Phases?", "Pro/meta/ana/telo.")

	if err := NewEnqueuer(pool).EnqueueForFlashcard(context.Background(), fcID, PriorityUser); err != nil {
		t.Fatal(err)
	}

	prov := &fakeProv{body: `{"keywords":[{"keyword":"mitose","weight":1.0},{"keyword":"phase","weight":0.6}]}`}
	ai := aipipeline.NewServiceForTest(pool, prov, "claude-test")

	r := &Runner{db: pool, ai: ai}

	n, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if n != 1 {
		t.Fatalf("want 1 job processed, got %d", n)
	}

	var state string

	if err := pool.QueryRow(context.Background(),
		`SELECT state FROM ai_extraction_jobs WHERE fc_id=$1`, fcID).Scan(&state); err != nil {
		t.Fatal(err)
	}

	if state != "done" {
		t.Errorf("want done, got %q", state)
	}

	var count int

	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM flashcard_keywords WHERE fc_id=$1`, fcID).Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 2 {
		t.Errorf("want 2 keywords stored, got %d", count)
	}
}

func TestRunOnce_EmptyAfterCleanupMarksFailed(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.NewSubject(t, pool, uid)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")

	if err := NewEnqueuer(pool).EnqueueForFlashcard(context.Background(), fcID, PriorityUser); err != nil {
		t.Fatal(err)
	}

	prov := &fakeProv{body: `{"keywords":[{"keyword":"   ","weight":0.5}]}`}
	ai := aipipeline.NewServiceForTest(pool, prov, "claude-test")

	r := &Runner{db: pool, ai: ai}

	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	var state, lastErr string

	if err := pool.QueryRow(context.Background(),
		`SELECT state, COALESCE(last_error,'') FROM ai_extraction_jobs WHERE fc_id=$1`, fcID).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}

	if state != "failed" || lastErr != "empty_after_cleanup" {
		t.Errorf("want failed/empty_after_cleanup, got %s/%s", state, lastErr)
	}
}

// TestRun_ContextCanceledRequeuesInsteadOfOrphaning reproduces STU-65: a job
// interrupted by shutdown (ctx cancelled mid-run) must land back in 'pending'
// so it's retried on next boot, not silently left stuck in 'running' forever
// because the failure-bookkeeping write itself uses the same cancelled ctx.
func TestRun_ContextCanceledRequeuesInsteadOfOrphaning(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.NewSubject(t, pool, uid)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")

	if err := NewEnqueuer(pool).EnqueueForFlashcard(context.Background(), fcID, PriorityUser); err != nil {
		t.Fatal(err)
	}

	ai := aipipeline.NewServiceForTest(pool, &blockingProv{}, "claude-test")
	r := NewRunner(pool, ai)

	jobs, err := r.claim(context.Background(), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: err=%v jobs=%d", err, len(jobs))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate shutdown signal firing mid-job

	r.run(ctx, jobs[0])

	var state string

	var lastErr sql.NullString

	if err := pool.QueryRow(context.Background(),
		`SELECT state, last_error FROM ai_extraction_jobs WHERE fc_id=$1`, fcID).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}

	if state != "pending" {
		t.Errorf("want job requeued to 'pending' after ctx cancellation, got %q (last_error=%v)", state, lastErr)
	}
}

func TestWorker_ProcessesMultipleJobs(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.NewSubject(t, pool, uid)

	const N = 5

	fcs := make([]int64, N)

	for i := 0; i < N; i++ {
		fcs[i] = testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")
	}

	enq := NewEnqueuer(pool)

	for _, id := range fcs {
		if err := enq.EnqueueForFlashcard(context.Background(), id, PriorityUser); err != nil {
			t.Fatal(err)
		}
	}

	prov := &fakeProv{body: `{"keywords":[{"keyword":"x","weight":0.5}]}`}
	ai := aipipeline.NewServiceForTest(pool, prov, "claude-test")

	w := New(pool, ai, Config{Workers: 2, RatePerMin: 6000, Burst: 100, PollInterval: 10 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		var done int

		_ = pool.QueryRow(ctx, `SELECT count(*) FROM ai_extraction_jobs WHERE state='done' AND fc_id = ANY($1)`, fcs).Scan(&done)

		if done == N {
			cancel()
			w.Stop()
			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("worker did not process %d jobs in time", N)
}

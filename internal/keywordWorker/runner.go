package keywordWorker

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/pkg/aipipeline"
)

// Runner consumes one claimed job: invokes the AI, post-processes, writes
// keyword rows.
type Runner struct {
	db *pgxpool.Pool       // db is the shared pool
	ai *aipipeline.Service // ai is the keyword-extraction primitive
}

// NewRunner constructs a Runner.
func NewRunner(db *pgxpool.Pool, ai *aipipeline.Service) *Runner {
	return &Runner{db: db, ai: ai}
}

// claimedJob is one row returned by sqlClaimPending.
type claimedJob struct {
	id       int64 // id is the ai_extraction_jobs row id
	fcID     int64 // fcID is the target flashcard
	attempts int16 // attempts is the prior failure count
}

// RunOnce claims at most one pending job and runs it. Returns the number of
// jobs processed (0 or 1). Used by tests; the poller invokes claim+run repeatedly.
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	jobs, err := r.claim(ctx, 1)
	if err != nil {
		return 0, err
	}

	if len(jobs) == 0 {
		return 0, nil
	}

	r.run(ctx, jobs[0])

	return 1, nil
}

// claim runs the FOR UPDATE SKIP LOCKED claim transaction and returns the rows.
func (r *Runner) claim(ctx context.Context, n int) ([]claimedJob, error) {
	rows, err := r.db.Query(ctx, sqlClaimPending, n)
	if err != nil {
		return nil, fmt.Errorf("claim jobs:\n%w", err)
	}

	defer rows.Close()

	var out []claimedJob

	for rows.Next() {
		var j claimedJob

		if err := rows.Scan(&j.id, &j.fcID, &j.attempts); err != nil {
			return nil, fmt.Errorf("scan claimed job:\n%w", err)
		}

		out = append(out, j)
	}

	return out, rows.Err()
}

// run executes one claimed job to completion (success or failure).
func (r *Runner) run(ctx context.Context, j claimedJob) {
	in, err := r.loadFlashcard(ctx, j.fcID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.markDone(ctx, j.id) // FC deleted; cascade handles keyword rows
			return
		}

		r.markFailed(ctx, j.id, "load_fc:"+err.Error())
		return
	}

	res, err := r.ai.ExtractKeywords(ctx, *in)
	if err != nil {
		r.markFailed(ctx, j.id, "ai:"+err.Error())
		return
	}

	cleaned := postprocess(res.Keywords)

	if len(cleaned) == 0 {
		r.markFailed(ctx, j.id, "empty_after_cleanup")
		return
	}

	if err := r.replaceKeywords(ctx, j.fcID, cleaned); err != nil {
		r.markFailed(ctx, j.id, "store:"+err.Error())
		return
	}

	r.markDone(ctx, j.id)
}

// loadFlashcard reads the title/question/answer for the prompt input.
func (r *Runner) loadFlashcard(ctx context.Context, fcID int64) (*aipipeline.ExtractInput, error) {
	var in aipipeline.ExtractInput

	err := r.db.QueryRow(ctx,
		`SELECT title, question, answer FROM flashcards WHERE id=$1`, fcID,
	).Scan(&in.Title, &in.Question, &in.Answer)
	if err != nil {
		return nil, err
	}

	return &in, nil
}

// replaceKeywords swaps the keyword set for a flashcard atomically.
func (r *Runner) replaceKeywords(ctx context.Context, fcID int64, kws []aipipeline.ExtractedKeyword) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin:\n%w", err)
	}

	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, sqlReplaceKeywordsDelete, fcID); err != nil {
		return fmt.Errorf("delete keywords:\n%w", err)
	}

	for _, k := range kws {
		if _, err := tx.Exec(ctx, sqlReplaceKeywordsInsert, fcID, k.Keyword, k.Weight); err != nil {
			return fmt.Errorf("insert keyword:\n%w", err)
		}
	}

	return tx.Commit(ctx)
}

// markDone flips the job to the 'done' state. Failures are logged.
func (r *Runner) markDone(ctx context.Context, id int64) {
	if _, err := r.db.Exec(ctx, sqlMarkDone, id); err != nil {
		log.Printf("keywordWorker: markDone job %d: %v", id, err)
	}
}

// markFailed flips the job to the 'failed' state with the given reason.
// Failures are logged.
func (r *Runner) markFailed(ctx context.Context, id int64, reason string) {
	if _, err := r.db.Exec(ctx, sqlMarkFailed, id, reason); err != nil {
		log.Printf("keywordWorker: markFailed job %d: %v", id, err)
	}
}

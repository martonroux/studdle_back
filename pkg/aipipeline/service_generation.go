package aipipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/myErrors"
)

// RunStructuredGeneration validates entitlement + quota + concurrency, inserts an
// ai_jobs row, then spawns a goroutine that drives the provider stream and emits
// validated AIChunks on the returned channel. The channel closes when the stream
// ends. Callers always receive jobID (for audit), even on synchronous errors.
func (s *Service) RunStructuredGeneration(
	ctx context.Context,
	req AIRequest,
) (<-chan AIChunk, int64, error) {
	if err := s.preflight(ctx, req); err != nil {
		return nil, 0, err
	}
	jobID, err := s.insertJob(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	out := make(chan AIChunk, 16)
	go s.drive(ctx, req, jobID, out)
	return out, jobID, nil
}

// preflight runs entitlement + subject-access + quota + concurrent-cap checks in order.
func (s *Service) preflight(ctx context.Context, req AIRequest) error {
	if err := s.checkEntitlement(ctx, req.UserID); err != nil {
		return err
	}
	if err := s.checkSubjectAccess(ctx, req.UserID, req.SubjectID); err != nil {
		return err
	}
	if err := s.CheckQuota(ctx, req.UserID, req.Feature, req.PDFPages); err != nil {
		return err
	}
	return s.checkConcurrency(ctx, req)
}

// checkSubjectAccess rejects requests targeting a subject the caller can't
// read. SubjectID <= 0 is treated as "not subject-scoped" and always allowed.
func (s *Service) checkSubjectAccess(ctx context.Context, uid, subjectID int64) error {
	if subjectID <= 0 {
		return nil
	}
	lvl, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !lvl.CanRead() {
		return myErrors.ErrForbidden
	}
	return nil
}

// checkEntitlement fails fast when the user lacks AI access.
func (s *Service) checkEntitlement(ctx context.Context, uid int64) error {
	ok, err := s.access.HasAIAccess(ctx, uid)
	if err != nil {
		return fmt.Errorf("has ai access:\n%w", err)
	}
	if !ok {
		return myErrors.ErrNoAIAccess
	}
	return nil
}

// checkConcurrency rejects a second generate request while one is already running.
// Check-flashcard calls are not capped this way.
func (s *Service) checkConcurrency(ctx context.Context, req AIRequest) error {
	if req.Feature == FeatureCheckFlashcard {
		return nil
	}
	var existingID int64
	err := s.db.QueryRow(ctx, sqlSelectRunningGenerationID, req.UserID).Scan(&existingID)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("check concurrency:\n%w", err)
	}
	return &myErrors.AppError{
		Code:    "concurrent_generation",
		Message: fmt.Sprintf("generation already running (jobId #%d)", existingID),
		Wrapped: myErrors.ErrConflict,
	}
}

// insertJob creates the ai_jobs row and returns its id.
func (s *Service) insertJob(ctx context.Context, req AIRequest) (int64, error) {
	meta, err := json.Marshal(req.Metadata)
	if err != nil {
		meta = []byte(`{}`)
	}
	var subjectID, flashcardID *int64
	if req.SubjectID > 0 {
		subjectID = &req.SubjectID
	}
	if req.FlashcardID > 0 {
		flashcardID = &req.FlashcardID
	}
	var jobID int64
	err = s.db.QueryRow(ctx, sqlInsertAIJob,
		req.UserID, string(req.Feature), s.model,
		subjectID, flashcardID, req.PDFPages, meta,
	).Scan(&jobID)
	if err != nil {
		return 0, fmt.Errorf("insert ai_job:\n%w", err)
	}
	return jobID, nil
}

// drive runs the provider stream (with one transparent retry on transport
// transients), parses JSON, emits chunks, and finalizes the ai_jobs row.
func (s *Service) drive(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) {
	defer close(out)
	r := s.streamOnce(ctx, req, jobID, out)
	if r.err != nil && retryable(r.err) {
		r = s.streamOnce(ctx, req, jobID, out)
	}
	s.finalize(ctx, jobID, req, r, out)
}

// streamResult aggregates what happened during one provider stream.
type streamResult struct {
	inputTokens  int   // inputTokens is the provider's prompt-token count
	outputTokens int   // outputTokens is the provider's completion-token count
	centsSpent   int   // centsSpent is the rounded cost estimate
	emitted      int   // emitted counts items that passed validation
	dropped      int   // dropped counts items that failed validation
	err          error // err is nil on success
}

// streamOnce calls the provider once and drives the parser. Caller handles retry.
func (s *Service) streamOnce(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) streamResult {
	chunks, err := s.provider.Stream(ctx, aiProvider.Request{
		FeatureKey: string(req.Feature),
		Model:      s.model,
		Prompt:     req.Prompt,
		Images:     req.Images,
		Schema:     req.Schema,
		MaxTokens:  16384,
	})
	if err != nil {
		classified := classifyProviderError(err, len(req.Images) > 0)
		return streamResult{err: classifyProviderStartErr(classified)}
	}
	return s.consumeStream(ctx, chunks, out, req.DropChapters)
}

// consumeStream reads chunks, forwards items + chapters, counts accepted/dropped items.
// Two parsers run in lockstep over the same byte stream: one for the "items"
// array, one for "chapters". They are independent and order-agnostic.
// When dropChapters is true, chapter elements are parsed (to keep the stream
// well-formed) but never emitted downstream.
func (s *Service) consumeStream(ctx context.Context, chunks <-chan aiProvider.Chunk, out chan<- AIChunk, dropChapters bool) streamResult {
	r := streamResult{}
	items := newArrayParser("items")
	items.onElement = elementEmitter(ctx, out, &r)
	chapters := newArrayParser("chapters")
	if dropChapters {
		chapters.onElement = func([]byte) {}
	} else {
		chapters.onElement = chapterEmitter(ctx, out)
	}
	feedChunks(ctx, []*arrayParser{items, chapters}, chunks, &r)
	return r
}

// elementEmitter returns the onElement callback that forwards valid items to out.
func elementEmitter(ctx context.Context, out chan<- AIChunk, r *streamResult) func([]byte) {
	return func(b []byte) {
		if !isWellFormedObject(b) {
			r.dropped++
			return
		}
		cp := append([]byte(nil), b...)
		select {
		case out <- AIChunk{Kind: ChunkItem, Item: cp}:
			r.emitted++
		case <-ctx.Done():
		}
	}
}

// chapterEmitter returns the onElement callback that forwards chapter objects.
// Chapter telemetry is metadata, so it does not affect the items emitted/dropped counters.
func chapterEmitter(ctx context.Context, out chan<- AIChunk) func([]byte) {
	return func(b []byte) {
		if !isWellFormedObject(b) {
			return
		}
		cp := append([]byte(nil), b...)
		select {
		case out <- AIChunk{Kind: ChunkChapter, Item: cp}:
		case <-ctx.Done():
		}
	}
}

// feedChunks drains the provider channel into every parser until ctx cancels or the channel closes.
func feedChunks(ctx context.Context, parsers []*arrayParser, chunks <-chan aiProvider.Chunk, r *streamResult) {
	for {
		select {
		case <-ctx.Done():
			r.err = ctx.Err()
			return
		case c, ok := <-chunks:
			if !ok {
				return
			}
			b := []byte(c.Text)
			for _, p := range parsers {
				p.feed(b)
			}
			if c.Done {
				return
			}
		}
	}
}

// finalize writes the terminal state to ai_jobs and emits the last chunk.
func (s *Service) finalize(ctx context.Context, jobID int64, req AIRequest, r streamResult, out chan<- AIChunk) {
	bg := context.Background() // decouple finalize from request cancellation
	if r.err != nil {
		s.finalizeError(bg, jobID, r, out)
		return
	}
	_ = s.finalizeSuccess(bg, jobID, r.inputTokens, r.outputTokens, r.centsSpent, r.emitted, r.dropped)
	if r.emitted > 0 {
		_ = s.DebitQuota(bg, req.UserID, req.Feature, 1, 0)
	}
	out <- AIChunk{Kind: ChunkDone}
}

// finalizeError marks the job failed and surfaces the error to the client.
func (s *Service) finalizeError(ctx context.Context, jobID int64, r streamResult, out chan<- AIChunk) {
	kind, msg := classifyErrForPersistence(r.err)
	_, _ = s.db.Exec(ctx, sqlFinalizeAIJobFailure, jobID, statusFor(r.err),
		r.inputTokens, r.outputTokens, r.centsSpent, r.emitted, r.dropped, kind, msg)
	out <- AIChunk{Kind: ChunkError, Err: r.err}
}

// finalizeSuccess marks a job complete with the provided telemetry.
func (s *Service) finalizeSuccess(ctx context.Context, jobID int64, inTok, outTok, cents, emitted, dropped int) error {
	_, err := s.db.Exec(ctx, sqlFinalizeAIJobSuccess, jobID, inTok, outTok, cents, emitted, dropped)
	if err != nil {
		return fmt.Errorf("finalize success:\n%w", err)
	}
	return nil
}

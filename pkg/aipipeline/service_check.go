package aipipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/myErrors"
)

// CheckInput describes one AI-check request.
type CheckInput struct {
	UserID        int64  // UserID is the caller
	FlashcardID   int64  // FlashcardID is the flashcard to check
	DraftQuestion string // DraftQuestion overrides the stored question when non-empty
	DraftAnswer   string // DraftAnswer overrides the stored answer when non-empty
}

// CheckSuggestion is the AI's suggested rewrite.
type CheckSuggestion struct {
	Title    string `json:"title"`    // Title is the suggested heading
	Question string `json:"question"` // Question is the suggested prompt
	Answer   string `json:"answer"`   // Answer is the suggested answer
}

// CheckFinding is one issue the AI called out.
type CheckFinding struct {
	Kind string `json:"kind"` // Kind is "factual" | "style" | "typo"
	Text string `json:"text"` // Text is the human-readable finding
}

// CheckOutput is the AI-check response.
type CheckOutput struct {
	JobID      int64           `json:"jobId"`      // JobID is the ai_jobs row id
	Verdict    string          `json:"verdict"`    // Verdict is "ok" | "minor_issues" | "major_issues"
	Findings   []CheckFinding  `json:"findings"`   // Findings is the list of issues
	Suggestion CheckSuggestion `json:"suggestion"` // Suggestion is the AI's rewrite
}

// CheckFlashcard runs a non-streaming AI check and returns the parsed result.
func (s *Service) CheckFlashcard(ctx context.Context, in CheckInput) (*CheckOutput, error) {
	fc, err := s.loadFlashcard(ctx, in.UserID, in.FlashcardID)
	if err != nil {
		return nil, err
	}
	prompt, err := RenderCheck(CheckValues{
		SubjectName: fc.SubjectName,
		Title:       fc.Title,
		Question:    orDefaultString(in.DraftQuestion, fc.Question),
		Answer:      orDefaultString(in.DraftAnswer, fc.Answer),
	})
	if err != nil {
		return nil, err
	}
	req := AIRequest{
		UserID:      in.UserID,
		Feature:     FeatureCheckFlashcard,
		SubjectID:   fc.SubjectID,
		FlashcardID: in.FlashcardID,
		Prompt:      prompt,
		Schema:      checkSchema(),
		Metadata:    map[string]any{},
	}
	return s.runCheck(ctx, req)
}

// runCheck uses the generation primitive but assembles a single JSON payload
// instead of streaming items; emits a ChunkDone after accumulating the buffer.
func (s *Service) runCheck(ctx context.Context, req AIRequest) (*CheckOutput, error) {
	if err := s.preflight(ctx, req); err != nil {
		return nil, err
	}
	jobID, err := s.insertJob(ctx, req)
	if err != nil {
		return nil, err
	}
	buf, err := s.collectStream(ctx, req)
	if err != nil {
		_ = s.finalizeCheckFailure(ctx, jobID, err)
		return nil, err
	}
	parsed, err := parseCheckOutput(buf)
	if err != nil {
		_ = s.finalizeCheckFailure(ctx, jobID, fmt.Errorf("malformed output:\n%w", err))
		return nil, err
	}
	if err := s.finalizeSuccess(ctx, jobID, 0, 0, 0, 1, 0); err != nil {
		return nil, err
	}
	_ = s.DebitQuota(ctx, req.UserID, FeatureCheckFlashcard, 1, 0)
	return &CheckOutput{JobID: jobID, Verdict: parsed.Verdict, Findings: parsed.Findings, Suggestion: parsed.Suggestion}, nil
}

// parseCheckOutput decodes the provider's JSON into the public Check shape.
func parseCheckOutput(buf []byte) (*CheckOutput, error) {
	var p struct {
		Verdict    string          `json:"verdict"`
		Findings   []CheckFinding  `json:"findings"`
		Suggestion CheckSuggestion `json:"suggestion"`
	}
	if err := json.Unmarshal(buf, &p); err != nil {
		return nil, err
	}
	return &CheckOutput{Verdict: p.Verdict, Findings: p.Findings, Suggestion: p.Suggestion}, nil
}

// collectStream concatenates all Chunk.Text from the provider into one buffer.
func (s *Service) collectStream(ctx context.Context, req AIRequest) ([]byte, error) {
	chunks, err := s.provider.Stream(ctx, aiProvider.Request{
		FeatureKey: string(req.Feature),
		Model:      s.model,
		Prompt:     req.Prompt,
		Schema:     req.Schema,
		MaxTokens:  2048,
	})
	if err != nil {
		return nil, classifyProviderStartErr(err)
	}
	return drainChunks(ctx, chunks)
}

// drainChunks consumes chunks until channel close or ctx cancel.
func drainChunks(ctx context.Context, chunks <-chan aiProvider.Chunk) ([]byte, error) {
	var buf []byte
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case c, ok := <-chunks:
			if !ok {
				return buf, nil
			}
			buf = append(buf, c.Text...)
			if c.Done {
				return buf, nil
			}
		}
	}
}

// finalizeCheckFailure marks the job failed for an AI-check error.
func (s *Service) finalizeCheckFailure(ctx context.Context, jobID int64, err error) error {
	kind, msg := classifyErrForPersistence(err)
	_, dbErr := s.db.Exec(ctx, sqlFinalizeAIJobFailure, jobID, statusFor(err), 0, 0, 0, 0, 0, kind, msg)
	if dbErr != nil {
		return fmt.Errorf("finalize check failure:\n%w", dbErr)
	}
	return nil
}

// checkSchema returns the tool-use JSON schema for an AI check.
func checkSchema() []byte {
	return []byte(`{
      "type":"object",
      "properties":{
        "verdict":{"type":"string","enum":["ok","minor_issues","major_issues"]},
        "findings":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string"},"text":{"type":"string"}}}},
        "suggestion":{"type":"object","properties":{"title":{"type":"string"},"question":{"type":"string"},"answer":{"type":"string"}}}
      },
      "required":["verdict","suggestion"]
    }`)
}

// flashcardRow is the read projection used by loadFlashcard.
type flashcardRow struct {
	Title       string // Title is the card's heading
	Question    string // Question is the prompt text
	Answer      string // Answer is the answer text
	SubjectID   int64  // SubjectID joins to subjects
	SubjectName string // SubjectName is the joined subject name
}

// loadFlashcard reads the target flashcard plus its subject name, after
// verifying uid has at least read access to the owning subject. Strangers
// get ErrForbidden.
func (s *Service) loadFlashcard(ctx context.Context, uid, id int64) (*flashcardRow, error) {
	var r flashcardRow
	err := s.db.QueryRow(ctx, `
        SELECT f.title, f.question, f.answer, f.subject_id, s.name
        FROM flashcards f JOIN subjects s ON s.id = f.subject_id
        WHERE f.id = $1
    `, id).Scan(&r.Title, &r.Question, &r.Answer, &r.SubjectID, &r.SubjectName)
	if err != nil {
		if isNoRows(err) {
			return nil, myErrors.ErrNotFound
		}
		return nil, fmt.Errorf("load flashcard:\n%w", err)
	}
	lvl, err := s.access.SubjectLevel(ctx, uid, r.SubjectID)
	if err != nil {
		return nil, err
	}
	if !lvl.CanRead() {
		return nil, myErrors.ErrForbidden
	}
	return &r, nil
}

// orDefaultString returns s unless empty, in which case fallback.
func orDefaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

package aipipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"studdle/backend/internal/aiProvider"
)

// RankInput is the input to the cross-subject ranker.
type RankInput struct {
	ExamSubject string                  // ExamSubject is the exam's subject name
	ExamTitle   string                  // ExamTitle is the exam's display title
	Candidates  []CrossSubjectCandidate // Candidates is the keyword-overlap shortlist
	TopK        int                     // TopK caps the number of selected ids
}

// RankResult is the parsed model output for cross-subject ranking.
type RankResult struct {
	SelectedIDs []int64 `json:"selectedIds"` // SelectedIDs are flashcard IDs in priority order
}

// RankCrossSubjects asks the model to pick the most relevant cross-subject cards.
// It does NOT debit user quota (sub-step of plan generation, counted at the
// outer call). Empty Candidates short-circuits with an empty result.
func (s *Service) RankCrossSubjects(ctx context.Context, in RankInput) (*RankResult, error) {
	if len(in.Candidates) == 0 {
		return &RankResult{}, nil
	}

	prompt, err := RenderCrossSubjectRank(CrossSubjectRankValues{
		ExamSubject: in.ExamSubject,
		ExamTitle:   in.ExamTitle,
		Candidates:  in.Candidates,
		TopK:        in.TopK,
	})
	if err != nil {
		return nil, fmt.Errorf("render rank prompt:\n%w", err)
	}

	chunks, err := s.provider.Stream(ctx, aiProvider.Request{
		FeatureKey: string(FeatureCrossSubjectRank),
		Model:      s.model,
		Prompt:     prompt,
		Schema:     rankSchema(),
		MaxTokens:  512,
	})
	if err != nil {
		return nil, classifyProviderStartErr(err)
	}

	buf, err := drainChunks(ctx, chunks)
	if err != nil {
		return nil, err
	}

	var out RankResult

	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, fmt.Errorf("parse rank result:\n%w", err)
	}

	return &out, nil
}

// rankSchema returns the JSON schema for cross-subject ranker output.
func rankSchema() []byte {
	return []byte(`{
  "type":"object",
  "required":["selectedIds"],
  "properties":{
    "selectedIds":{
      "type":"array",
      "minItems":0,
      "maxItems":50,
      "items":{"type":"integer"}
    }
  }
}`)
}

// PlanGenerateInput is the input to the streaming plan-generation primitive.
type PlanGenerateInput struct {
	UserID        int64                  // UserID is the caller (used for quota debit + audit)
	ExamID        int64                  // ExamID is for ai_jobs metadata
	SubjectID     int64                  // SubjectID is for ai_jobs metadata
	Prompt        string                 // Prompt is the rendered plan-generation prompt
	AnnalesImages []aiProvider.ImagePart // AnnalesImages are optional past-paper page images
}

// PlanGenerateOutput exposes the streaming chunks + the audit job id.
type PlanGenerateOutput struct {
	Chunks <-chan AIChunk // Chunks is the provider stream forwarded to SSE
	JobID  int64          // JobID is the ai_jobs row id for client correlation
}

// GenerateRevisionPlan launches a streaming plan generation. It calls into
// RunStructuredGeneration and returns the stream channel plus the job id.
// Quota is debited only on success (handled by the underlying primitive's
// post-run accounting).
func (s *Service) GenerateRevisionPlan(ctx context.Context, in PlanGenerateInput) (*PlanGenerateOutput, error) {
	req := AIRequest{
		UserID:    in.UserID,
		Feature:   FeatureGenerateRevisionPlan,
		SubjectID: in.SubjectID,
		Prompt:    in.Prompt,
		Images:    in.AnnalesImages,
		PDFPages:  len(in.AnnalesImages),
		Metadata: map[string]any{
			"exam_id":    in.ExamID,
			"page_count": len(in.AnnalesImages),
		},
	}

	ch, jobID, err := s.RunStructuredGeneration(ctx, req)
	if err != nil {
		return nil, err
	}

	return &PlanGenerateOutput{Chunks: ch, JobID: jobID}, nil
}

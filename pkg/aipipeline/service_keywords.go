package aipipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"studdle/backend/internal/aiProvider"
)

// ExtractInput describes one keyword-extraction request.
type ExtractInput struct {
	Title    string // Title is the flashcard title (may be empty)
	Question string // Question is the flashcard prompt
	Answer   string // Answer is the flashcard answer
}

// ExtractedKeyword is one keyword/weight pair from the model.
type ExtractedKeyword struct {
	Keyword string  `json:"keyword"` // Keyword is the topical token (raw, pre-postprocess)
	Weight  float64 `json:"weight"`  // Weight is the model-assigned 0..1 centrality
}

// KeywordResult is the parsed model output.
type KeywordResult struct {
	Keywords []ExtractedKeyword `json:"keywords"` // Keywords is the unprocessed list
}

// ExtractKeywords runs a non-streaming keyword extraction call against the provider.
// It does NOT touch the quota tables (system-side cost) and does NOT insert ai_jobs
// rows — it is the lowest-level primitive used by the keyword worker.
func (s *Service) ExtractKeywords(ctx context.Context, in ExtractInput) (*KeywordResult, error) {
	prompt, err := RenderExtractKeywords(ExtractKeywordsValues{
		Title:    in.Title,
		Question: in.Question,
		Answer:   in.Answer,
	})
	if err != nil {
		return nil, fmt.Errorf("render extract prompt:\n%w", err)
	}
	chunks, err := s.provider.Stream(ctx, aiProvider.Request{
		FeatureKey: string(FeatureExtractKeywords),
		Model:      s.model,
		Prompt:     prompt,
		Schema:     extractKeywordsSchema(),
		MaxTokens:  1024,
	})
	if err != nil {
		return nil, classifyProviderStartErr(err)
	}
	buf, err := drainChunks(ctx, chunks)
	if err != nil {
		return nil, err
	}
	var out KeywordResult
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, fmt.Errorf("parse keywords:\n%w", err)
	}
	return &out, nil
}

// extractKeywordsSchema returns the tool-use JSON schema for keyword extraction.
// minItems is 1 (not 5) because the prompt-level "5 to 12 keywords" target is a
// soft instruction; the worker's post-processor enforces the floor and marks
// jobs failed with last_error="empty_after_cleanup" when nothing survives cleanup.
func extractKeywordsSchema() []byte {
	return []byte(`{
      "type":"object",
      "required":["keywords"],
      "properties":{
        "keywords":{
          "type":"array",
          "minItems":1,
          "maxItems":12,
          "items":{
            "type":"object",
            "required":["keyword","weight"],
            "properties":{
              "keyword":{"type":"string","minLength":1,"maxLength":64},
              "weight":{"type":"number","minimum":0,"maximum":1}
            }
          }
        }
      }
    }`)
}

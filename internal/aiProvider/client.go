package aiProvider

import (
	"context"

	"studdle/backend/internal/myErrors"
)

// Chunk is one streamed piece of AI output.
type Chunk struct {
	Text string // Text is a partial token sequence from the provider's streamed tool_use input
	Done bool   // Done marks the last chunk of the stream
}

// ImagePart is one rasterized PDF page sent as image content.
type ImagePart struct {
	MediaType string // MediaType is the IANA image type (e.g., "image/jpeg")
	Data      []byte // Data is the raw image bytes (not base64)
}

// Request is the structured-generation invocation shape.
type Request struct {
	FeatureKey string      // FeatureKey is persisted as ai_jobs.feature_key
	Model      string      // Model is the Anthropic model identifier
	Prompt     string      // Prompt is the user message body
	Images     []ImagePart // Images are optional page images (non-empty for PDF flow)
	Schema     []byte      // Schema is the tool-use JSON schema (raw bytes)
	MaxTokens  int         // MaxTokens caps the provider response
}

// Client is the AI provider interface.
type Client interface {
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

// NoopClient returns ErrNotImplemented for every call.
type NoopClient struct{}

// Stream always returns ErrNotImplemented.
func (NoopClient) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	return nil, myErrors.ErrNotImplemented
}

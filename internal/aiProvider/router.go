package aiProvider

import (
	"context"
	"strings"
)

// Router dispatches each Stream call to the vendor client that serves the
// requested model: OpenAI-family identifiers go to the OpenAI client,
// everything else goes to the Anthropic client.
type Router struct {
	anthropic Client // anthropic serves claude-* (and unrecognized) models
	openai    Client // openai serves gpt-* / chatgpt-* / o-series models
}

// NewRouter constructs a Router over the two per-vendor clients.
func NewRouter(anthropic, openai Client) *Router {
	return &Router{anthropic: anthropic, openai: openai}
}

// Stream forwards the request to the client selected by the model identifier.
func (r *Router) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	if IsOpenAIModel(req.Model) {
		return r.openai.Stream(ctx, req)
	}
	return r.anthropic.Stream(ctx, req)
}

// IsOpenAIModel reports whether model is an OpenAI model identifier
// (gpt-*, chatgpt-*, or an o-series reasoning model like o3-mini).
func IsOpenAIModel(model string) bool {
	if strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "chatgpt-") {
		return true
	}
	return len(model) >= 2 && model[0] == 'o' && model[1] >= '0' && model[1] <= '9'
}

package aiProvider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider calls OpenAI's Chat Completions API with SSE streaming.
// Structured output uses a single forced "emit" function tool whose
// parameters schema is the caller's Schema, mirroring ClaudeProvider.
type OpenAIProvider struct {
	endpoint string       // endpoint is the base URL, e.g. https://api.openai.com
	apiKey   string       // apiKey is the OpenAI API key
	httpCli  *http.Client // httpCli is the underlying HTTP client
}

// NewOpenAIProvider constructs an OpenAIProvider pointed at endpoint.
// Pass https://api.openai.com in production.
func NewOpenAIProvider(endpoint, apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		endpoint: endpoint,
		apiKey:   apiKey,
		httpCli:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Stream submits a chat-completions request and returns a channel of partial output text.
func (p *OpenAIProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	body, err := buildChatCompletionsBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := p.newHTTPRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpCli.Do(httpReq)
	if err != nil {
		return nil, wrapProviderErr(err)
	}
	if resp.StatusCode != http.StatusOK {
		drainAndCloseWithError(resp)
		return nil, providerStatusErr(resp.StatusCode)
	}
	out := make(chan Chunk, 32)
	go pumpOpenAISSE(ctx, resp, out)
	return out, nil
}

// newHTTPRequest constructs the POST with OpenAI bearer-auth headers.
func (p *OpenAIProvider) newHTTPRequest(ctx context.Context, body []byte) (*http.Request, error) {
	url := strings.TrimRight(p.endpoint, "/") + "/v1/chat/completions"
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request:\n%w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+p.apiKey)
	return r, nil
}

// buildChatCompletionsBody assembles the JSON body for /v1/chat/completions.
// The token cap uses max_completion_tokens (not the legacy max_tokens, which
// GPT-5-family models reject).
func buildChatCompletionsBody(req Request) ([]byte, error) {
	payload := map[string]any{
		"model":                 req.Model,
		"max_completion_tokens": orDefaultInt(req.MaxTokens, 4096),
		"stream":                true,
		"messages":              []map[string]any{{"role": "user", "content": buildOpenAIUserContent(req)}},
	}
	if tools := buildOpenAITools(req.Schema); tools != nil {
		payload["tools"] = tools
		payload["tool_choice"] = map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "emit"},
		}
	}
	return json.Marshal(payload)
}

// buildOpenAIUserContent assembles the content array: optional images as
// base64 data URLs, then the prompt text.
func buildOpenAIUserContent(req Request) []map[string]any {
	parts := make([]map[string]any, 0, len(req.Images)+1)
	for _, img := range req.Images {
		dataURL := "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": dataURL},
		})
	}
	parts = append(parts, map[string]any{"type": "text", "text": req.Prompt})
	return parts
}

// buildOpenAITools returns a single forced "emit" function tool wrapping the
// caller's schema.
func buildOpenAITools(schema []byte) []map[string]any {
	if len(schema) == 0 {
		return nil
	}
	var raw any
	if json.Unmarshal(schema, &raw) != nil {
		raw = map[string]any{"type": "object"}
	}
	return []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "emit",
			"description": "Emit the structured output required by the caller.",
			"parameters":  raw,
		},
	}}
}

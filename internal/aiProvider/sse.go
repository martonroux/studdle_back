package aiProvider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"studdle/backend/internal/myErrors"
)

// pumpSSE reads Anthropic's SSE stream and forwards input_json_delta payloads as Chunks.
func pumpSSE(ctx context.Context, resp *http.Response, out chan<- Chunk) {
	defer close(out)
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		forwardEvent(payload, out)
	}
}

// forwardEvent inspects one SSE "data:" line and emits a Chunk if it's a tool delta or stop.
func forwardEvent(payload string, out chan<- Chunk) {
	var env struct {
		Type  string          `json:"type"`
		Delta json.RawMessage `json:"delta"`
	}
	if json.Unmarshal([]byte(payload), &env) != nil {
		return
	}
	switch env.Type {
	case "content_block_delta":
		emitDelta(env.Delta, out)
	case "message_stop":
		out <- Chunk{Done: true}
	}
}

// emitDelta forwards a Chunk for either tool-input deltas (schema-enforced
// path used by Check) or text deltas (free-form streaming used by generation).
// Forced tool_use deltas are buffered server-side by Anthropic until the call
// completes, so generation must use text streaming to actually stream.
func emitDelta(delta json.RawMessage, out chan<- Chunk) {
	var d struct {
		Type        string `json:"type"`
		PartialJSON string `json:"partial_json"`
		Text        string `json:"text"`
	}
	if json.Unmarshal(delta, &d) != nil {
		return
	}
	switch d.Type {
	case "input_json_delta":
		out <- Chunk{Text: d.PartialJSON}
	case "text_delta":
		out <- Chunk{Text: d.Text}
	}
}

// drainAndCloseWithError consumes any response body so the HTTP connection can be reused.
func drainAndCloseWithError(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// providerStatusErr maps an upstream HTTP status code to a sentinel-wrapped AppError.
func providerStatusErr(status int) error {
	code := "provider_5xx"
	switch {
	case status == 429:
		code = "provider_rate_limit"
	case status == 422:
		return &myErrors.AppError{Code: "content_policy", Message: "provider refused content", Wrapped: myErrors.ErrContentPolicy}
	case status >= 400 && status < 500:
		return &myErrors.AppError{Code: "bad_request", Message: fmt.Sprintf("provider returned %d", status), Wrapped: myErrors.ErrAIProvider}
	}
	return &myErrors.AppError{Code: code, Message: fmt.Sprintf("provider returned %d", status), Wrapped: myErrors.ErrAIProvider}
}

// wrapProviderErr maps a transport-level error to AppError{provider_timeout}.
func wrapProviderErr(err error) error {
	return &myErrors.AppError{Code: "provider_timeout", Message: err.Error(), Wrapped: myErrors.ErrAIProvider}
}

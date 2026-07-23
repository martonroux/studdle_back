package aiProvider

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// pumpOpenAISSE reads a chat-completions SSE stream and forwards text and
// tool-call argument deltas as Chunks. The "[DONE]" sentinel terminates the
// stream.
func pumpOpenAISSE(ctx context.Context, resp *http.Response, out chan<- Chunk) {
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
		if payload == "[DONE]" {
			out <- Chunk{Done: true}
			return
		}
		forwardOpenAIEvent(payload, out)
	}
}

// forwardOpenAIEvent inspects one SSE "data:" line and emits Chunks for
// content deltas (free-form streaming) and tool-call argument deltas
// (schema-enforced path). Unlike Anthropic, OpenAI streams forced tool-call
// arguments incrementally, so both paths stream live.
func forwardOpenAIEvent(payload string, out chan<- Chunk) {
	var env struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(payload), &env) != nil || len(env.Choices) == 0 {
		return
	}
	delta := env.Choices[0].Delta
	if delta.Content != "" {
		out <- Chunk{Text: delta.Content}
	}
	for _, tc := range delta.ToolCalls {
		if tc.Function.Arguments != "" {
			out <- Chunk{Text: tc.Function.Arguments}
		}
	}
}

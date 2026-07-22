package aiProvider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"studdle/backend/internal/aiProvider"
)

func TestClaudeProvider_StreamsInputJsonDeltasAsTextChunks(t *testing.T) {
	sse := strings.Join([]string{
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"emit","input":{}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"items\":[{\"q\":\"a\""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":",\"a\":\"b\"}]}"}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			t.Errorf("missing x-api-key header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()

	p := aiProvider.NewClaudeProvider(srv.URL, "fake-key")
	ch, err := p.Stream(context.Background(), aiProvider.Request{
		FeatureKey: "generate_prompt",
		Model:      "claude-sonnet-4-6",
		Prompt:     "hello",
		Schema:     json.RawMessage(`{"type":"object"}`),
		MaxTokens:  128,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var combined strings.Builder
	var sawDone bool
	for c := range ch {
		combined.WriteString(c.Text)
		if c.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Error("stream ended without Done chunk")
	}
	wantPrefix := `{"items":[`
	if !strings.HasPrefix(combined.String(), wantPrefix) {
		t.Errorf("combined = %q, want prefix %q", combined.String(), wantPrefix)
	}
}

func TestClaudeProvider_Non2xxMapsToProvider5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := aiProvider.NewClaudeProvider(srv.URL, "fake-key")
	_, err := p.Stream(context.Background(), aiProvider.Request{Model: "m", Prompt: "x"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

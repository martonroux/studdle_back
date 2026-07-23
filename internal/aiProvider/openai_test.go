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

func TestOpenAIProvider_StreamsToolCallArgumentsAsTextChunks(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"function":{"name":"emit","arguments":""}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"items\":[{\"q\":\"a\""}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":",\"a\":\"b\"}]}"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
		"",
		"",
	}, "\n")

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Authorization bearer header")
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()

	p := aiProvider.NewOpenAIProvider(srv.URL, "fake-key")
	ch, err := p.Stream(context.Background(), aiProvider.Request{
		FeatureKey: "extract_keywords",
		Model:      "gpt-4.1-nano",
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

	body := string(gotBody)
	if !strings.Contains(body, `"max_completion_tokens":128`) {
		t.Errorf("body missing max_completion_tokens: %s", body)
	}
	if !strings.Contains(body, `"tool_choice"`) || !strings.Contains(body, `"emit"`) {
		t.Errorf("body missing forced emit tool_choice: %s", body)
	}
}

func TestOpenAIProvider_StreamsContentDeltasWithoutSchema(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":""}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"{\"items\":"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"[]}"}}]}`,
		"",
		"data: [DONE]",
		"",
		"",
	}, "\n")

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()

	p := aiProvider.NewOpenAIProvider(srv.URL, "fake-key")
	ch, err := p.Stream(context.Background(), aiProvider.Request{
		FeatureKey: "generate_prompt",
		Model:      "gpt-5.4-nano",
		Prompt:     "hello",
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var combined strings.Builder
	for c := range ch {
		combined.WriteString(c.Text)
	}
	if combined.String() != `{"items":[]}` {
		t.Errorf("combined = %q, want %q", combined.String(), `{"items":[]}`)
	}
	if strings.Contains(string(gotBody), `"tools"`) {
		t.Errorf("schemaless request must not send tools: %s", gotBody)
	}
}

func TestOpenAIProvider_Non2xxMapsToProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := aiProvider.NewOpenAIProvider(srv.URL, "fake-key")
	_, err := p.Stream(context.Background(), aiProvider.Request{Model: "gpt-5.4-mini", Prompt: "x"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

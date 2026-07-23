package aiProvider_test

import (
	"context"
	"testing"

	"studdle/backend/internal/aiProvider"
)

// recordingClient records that Stream was called and returns a closed channel.
type recordingClient struct {
	called bool // called flips to true on the first Stream call
}

// Stream records the call and returns an immediately-closed chunk channel.
func (r *recordingClient) Stream(ctx context.Context, req aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	r.called = true
	ch := make(chan aiProvider.Chunk)
	close(ch)
	return ch, nil
}

func TestIsOpenAIModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-5.4-mini", true},
		{"gpt-5.4-nano", true},
		{"gpt-4.1-nano", true},
		{"chatgpt-4o-latest", true},
		{"o3-mini", true},
		{"claude-sonnet-4-6", false},
		{"claude-haiku-4-5", false},
		{"", false},
		{"open-mistral-nemo", false},
	}
	for _, tc := range cases {
		if got := aiProvider.IsOpenAIModel(tc.model); got != tc.want {
			t.Errorf("IsOpenAIModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestRouter_DispatchesByModel(t *testing.T) {
	anthropic := &recordingClient{}
	openai := &recordingClient{}
	r := aiProvider.NewRouter(anthropic, openai)

	if _, err := r.Stream(context.Background(), aiProvider.Request{Model: "gpt-5.4-nano"}); err != nil {
		t.Fatalf("Stream(gpt): %v", err)
	}
	if !openai.called || anthropic.called {
		t.Fatalf("gpt model routed wrong: openai=%v anthropic=%v", openai.called, anthropic.called)
	}

	openai.called = false
	if _, err := r.Stream(context.Background(), aiProvider.Request{Model: "claude-sonnet-4-6"}); err != nil {
		t.Fatalf("Stream(claude): %v", err)
	}
	if !anthropic.called || openai.called {
		t.Fatalf("claude model routed wrong: openai=%v anthropic=%v", openai.called, anthropic.called)
	}
}

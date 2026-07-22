package testutil

import (
	"context"
	"sync"
	"sync/atomic"

	"studdle/backend/internal/aiProvider"
)

// FakeAIClient replays a fixed sequence of chunks on each Stream call.
// Set FailFirstN to simulate transient provider errors (returned as Err)
// on the first N calls; subsequent calls succeed with Chunks.
type FakeAIClient struct {
	Chunks     []aiProvider.Chunk // Chunks is the replay buffer for successful calls
	Err        error              // Err is returned synchronously when set
	FailFirstN int32              // FailFirstN fails that many calls with Err before succeeding
	calls      atomic.Int32       // calls counts total Stream invocations
	mu         sync.Mutex         // mu guards lastReq
	lastReq    aiProvider.Request // lastReq is the most recent Request received by Stream
}

// Stream returns either Err (for the first FailFirstN calls) or a channel
// that yields Chunks then closes.
func (f *FakeAIClient) Stream(ctx context.Context, req aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	f.mu.Lock()
	f.lastReq = req
	f.mu.Unlock()
	n := f.calls.Add(1)
	if f.Err != nil && n <= f.FailFirstN {
		return nil, f.Err
	}
	if f.Err != nil && f.FailFirstN == 0 {
		return nil, f.Err
	}
	out := make(chan aiProvider.Chunk, len(f.Chunks))
	for _, c := range f.Chunks {
		out <- c
	}
	close(out)
	return out, nil
}

// Calls returns the total number of Stream invocations so far.
func (f *FakeAIClient) Calls() int32 {
	return f.calls.Load()
}

// LastRequest returns the most recent Request received by Stream.
// Used by handler tests to assert on what was sent to the provider.
func (f *FakeAIClient) LastRequest() aiProvider.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/corporealshift/nabu/protocol"
)

// Fake is a scripted Provider for tests. Each Complete pops the next Response
// (or the error scheduled for that call index), records the Request, and
// streams Content through onDelta in one piece.
type Fake struct {
	Script []Response
	Errors map[int]error // by zero-based call index
	// Partial is what arrived before a scheduled error, returned with it.
	Partial map[int]Response
	BlockOn chan struct{} // when non-nil, Complete waits for close or ctx

	mu    sync.Mutex
	Calls []Request
}

// Complete implements Provider.
func (f *Fake) Complete(ctx context.Context, req Request, onDelta, onThinking func(string)) (Response, error) {
	f.mu.Lock()
	i := len(f.Calls)
	f.Calls = append(f.Calls, req)
	f.mu.Unlock()
	if f.BlockOn != nil {
		select {
		case <-f.BlockOn:
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if err := f.Errors[i]; err != nil {
		return f.Partial[i], err
	}
	if i >= len(f.Script) {
		return Response{}, fmt.Errorf("fake provider: no scripted response for call %d", i+1)
	}
	r := f.Script[i]
	if onThinking != nil && r.Reasoning != "" {
		onThinking(r.Reasoning)
	}
	if onDelta != nil && r.Content != "" {
		onDelta(r.Content)
	}
	if r.Usage == (protocol.Usage{}) {
		r.Usage = protocol.Usage{InputTokens: 100, OutputTokens: 10}
	}
	if r.FinishReason == "" {
		if len(r.ToolCalls) > 0 {
			r.FinishReason = "tool_calls"
		} else {
			r.FinishReason = "stop"
		}
	}
	return r, nil
}

// CallCount returns how many times Complete ran.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

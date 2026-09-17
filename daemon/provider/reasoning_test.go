package provider

import (
	"strings"
	"testing"
)

// The chunks below are the shape llama-server actually sends for a reasoning
// model: reasoning_content arrives first, then content, on the same stream.
const reasoningStream = `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":null}}]}

data: {"choices":[{"index":0,"delta":{"reasoning_content":"17 times 20 is 340, "}}]}

data: {"choices":[{"index":0,"delta":{"reasoning_content":"plus 51 is 391."}}]}

data: {"choices":[{"index":0,"delta":{"content":"391"}}]}

data: {"choices":[{"index":0,"delta":{"content":"."},"finish_reason":"stop"}]}

data: [DONE]
`

func TestReasoningIsCaptured(t *testing.T) {
	resp, err := readStream(strings.NewReader(reasoningStream), func(string) {}, nil)
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}

	if got, want := resp.Reasoning, "17 times 20 is 340, plus 51 is 391."; got != want {
		t.Errorf("reasoning = %q, want %q", got, want)
	}
	if got, want := resp.Content, "391."; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// Reasoning and content are separate streams to the reader: a TUI showing
// thinking live must not splice it into the answer.
func TestReasoningAndContentStreamSeparately(t *testing.T) {
	var content, thinking strings.Builder

	if _, err := readStream(
		strings.NewReader(reasoningStream),
		func(s string) { content.WriteString(s) },
		func(s string) { thinking.WriteString(s) },
	); err != nil {
		t.Fatalf("readStream: %v", err)
	}

	if got, want := content.String(), "391."; got != want {
		t.Errorf("content deltas = %q, want %q", got, want)
	}
	if got, want := thinking.String(), "17 times 20 is 340, plus 51 is 391."; got != want {
		t.Errorf("thinking deltas = %q, want %q", got, want)
	}
}

// Most providers report no reasoning at all, and must not be made to look as
// though they thought about nothing.
func TestNoReasoningIsEmpty(t *testing.T) {
	const plain = `data: {"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":"stop"}]}

data: [DONE]
`
	resp, err := readStream(strings.NewReader(plain), func(string) {}, nil)
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if resp.Reasoning != "" {
		t.Errorf("reasoning = %q, want empty", resp.Reasoning)
	}
}

// A nil thinking callback is the normal case for callers that do not want it.
func TestNilThinkingCallbackIsSafe(t *testing.T) {
	if _, err := readStream(strings.NewReader(reasoningStream), nil, nil); err != nil {
		t.Fatalf("readStream with nil callbacks: %v", err)
	}
}

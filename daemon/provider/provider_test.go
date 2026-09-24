package provider

import (
	"context"
	"strings"
	"testing"
)

func TestFakePopsScriptInOrder(t *testing.T) {
	f := &Fake{Script: []Response{
		{Content: "first"},
		{ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: []byte(`{"command":"ls"}`)}}},
	}}
	var deltas []string
	r1, err := f.Complete(context.Background(), Request{Model: "m"}, func(s string) { deltas = append(deltas, s) }, nil)
	if err != nil || r1.Content != "first" || len(deltas) != 1 {
		t.Fatalf("r1: %+v %v deltas=%v", r1, err, deltas)
	}
	r2, _ := f.Complete(context.Background(), Request{Model: "m"}, nil, nil)
	if len(r2.ToolCalls) != 1 || r2.ToolCalls[0].Name != "bash" {
		t.Fatalf("r2: %+v", r2)
	}
	if _, err := f.Complete(context.Background(), Request{}, nil, nil); err == nil || !strings.Contains(err.Error(), "no scripted response") {
		t.Fatalf("exhausted: %v", err)
	}
	if len(f.Calls) != 3 {
		t.Fatalf("calls recorded: %d", len(f.Calls))
	}
	if r1.Usage.InputTokens == 0 {
		t.Fatal("fake must report non-zero usage by default")
	}
	if r1.FinishReason != "stop" || r2.FinishReason != "tool_calls" {
		t.Fatalf("finish reasons: %q %q", r1.FinishReason, r2.FinishReason)
	}
}

func TestFakeBlocksUntilReleasedOrCancelled(t *testing.T) {
	f := &Fake{Script: []Response{{Content: "x"}}, BlockOn: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Complete(ctx, Request{}, nil, nil); err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRegistryResolve(t *testing.T) {
	r := NewRegistry()
	local := &Fake{}
	hosted := &Fake{}
	r.Add(Config{Name: "local", ContextWindow: 32768}, local, true)
	r.Add(Config{Name: "openrouter"}, hosted, false)

	p, model, cfg, err := r.Resolve("qwen3.6-35b-a3b")
	if err != nil || p != local || model != "qwen3.6-35b-a3b" || cfg.ContextWindow != 32768 {
		t.Fatalf("bare model: %v %v %q", p, err, model)
	}
	p, model, _, err = r.Resolve("openrouter/deepseek/deepseek-v4")
	if err != nil || p != hosted || model != "deepseek/deepseek-v4" {
		t.Fatalf("prefixed: %v %v %q", p, err, model)
	}
	p, model, _, err = r.Resolve("unknown/thing")
	if err != nil || p != local || model != "unknown/thing" {
		t.Fatalf("unknown prefix falls through to default: %v %v %q", p, err, model)
	}
	if _, _, _, err := NewRegistry().Resolve("m"); err == nil {
		t.Fatal("no default provider must be an error")
	}
}

// A provider that sets no repeat limit gets the default, and a negative one
// stays off: the runner reads it from the registry.
func TestRepeatLimitDefaults(t *testing.T) {
	r := NewRegistry()
	for name, limit := range map[string]int{"plain": 0, "set": 12, "off": -1} {
		r.Add(Config{Name: name, RepeatLimit: limit}, &Fake{}, false)
	}
	for name, want := range map[string]int{"plain": DefaultRepeatLimit, "set": 12, "off": -1} {
		_, _, cfg, err := r.Resolve(name + "/m")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RepeatLimit != want {
			t.Errorf("%s: repeat limit %d, want %d", name, cfg.RepeatLimit, want)
		}
	}
}

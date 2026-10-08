package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// recorder calls its own tool from BeforeRequest at the second request, the
// way the claude module records an answer it fetched in the background.
type recorder struct {
	host     module.Host
	requests int
}

func (*recorder) Name() string { return "recorder" }
func (r *recorder) Init(h module.Host, _ module.Config) error {
	r.host = h
	return nil
}
func (*recorder) Tools() []module.Tool {
	return []module.Tool{{
		Name:   "recorder.note",
		Schema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, module.Session, json.RawMessage) (string, error) {
			return "the answer from the background", nil
		},
	}}
}
func (r *recorder) BeforeRequest(ctx context.Context, s module.Session) ([]module.ContextBlock, error) {
	r.requests++
	if r.requests == 2 {
		if _, err := r.host.Tools().Call(ctx, s, "recorder.note", json.RawMessage(`{}`)); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// A tool call a module makes at the request boundary reaches the model as a
// call and its result, after the model's own call has its result: a request
// never carries a call without one.
func TestAModuleCallAtTheBoundaryIsAWellFormedExchange(t *testing.T) {
	rec := &recorder{}
	h := newHarness(t, []module.Module{rec}, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"*"}`)}}},
		{Content: "done"},
	})
	s := h.create(t)
	if _, err := h.m.Prompt(context.Background(), s.ID(), "go"); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())

	if err := protocol.ValidateLog(s.Events()); err != nil {
		t.Fatalf("the log is not valid: %v", err)
	}
	if n := h.fake.CallCount(); n != 2 {
		t.Fatalf("model calls = %d, want 2", n)
	}

	msgs := h.fake.Calls[1].Messages
	answered := map[string]bool{}
	var order []string
	for i, m := range msgs {
		if m.Role == "tool" {
			answered[m.ToolCallID] = true
			order = append(order, "result:"+m.ToolCallID)
			if i == 0 || (msgs[i-1].Role != "assistant" && msgs[i-1].Role != "tool") {
				t.Errorf("tool result %s does not follow its call", m.ToolCallID)
			}
		}
		for _, c := range m.ToolCalls {
			order = append(order, "call:"+c.Name)
		}
	}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if !answered[c.ID] {
				t.Errorf("call %s (%s) went to the model without its result", c.ID, c.Name)
			}
		}
	}
	got := strings.Join(order, " ")
	if !strings.Contains(got, "call:glob result:c1 call:recorder.note result:mod_") {
		t.Errorf("order = %s, want the model's call and result, then the module's", got)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || last.Content != "the answer from the background" {
		t.Errorf("the request should end with the module's result, got %+v", last)
	}
}

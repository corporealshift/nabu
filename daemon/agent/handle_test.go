package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

type fakeCore struct {
	calls []protocol.ToolCallData
}

func (f *fakeCore) invokeTool(ctx context.Context, h *sessionHandle, call protocol.ToolCallData) protocol.ToolResultData {
	f.calls = append(f.calls, call)
	return protocol.ToolResultData{CallID: call.CallID, Tool: call.Tool, Content: "ran", Status: "ok"}
}

func (f *fakeCore) complete(ctx context.Context, h *sessionHandle, req module.CompletionRequest) (module.CompletionResponse, error) {
	return module.CompletionResponse{Content: "judged"}, nil
}

func (f *fakeCore) ask(ctx context.Context, h *sessionHandle, q string, choices []string) (string, error) {
	return "yes", nil
}

func (f *fakeCore) dataDir(name string) (string, error) { return "/tmp/" + name, nil }

func newHandle(t *testing.T) (*sessionHandle, *fakeCore) {
	t.Helper()
	st, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := st.Create("C:/w", "w-1", protocol.Options{
		Model: "m", CompactionEnabled: true, PermissionMode: protocol.PermissionAsk}, 0)
	if err != nil {
		t.Fatal(err)
	}
	core := &fakeCore{}
	return &sessionHandle{core: core, s: s, ws: module.Workspace{Path: "C:/w", Key: "w-1"}}, core
}

func TestHandleAppendAllowsOnlyModuleEvents(t *testing.T) {
	h, _ := newHandle(t)
	if _, err := h.Append(protocol.EventCheck, protocol.CheckData{Name: "x", Kind: "module", Status: "pass", Summary: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Append(protocol.EventNotice, protocol.NoticeData{Source: "module:t", Level: "info", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Append(protocol.EventGoal, protocol.GoalData{Condition: "c", State: "met", Source: "module:t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Append(protocol.EventGoal, protocol.GoalData{Condition: "c", State: "set", Source: "module:t"}); err == nil {
		t.Fatal("modules may not set goals")
	}
	if _, err := h.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "forged"}); err == nil ||
		!strings.Contains(err.Error(), "may not append") {
		t.Fatalf("modules may not append messages: %v", err)
	}
	if h.State().Goal == nil || h.State().Goal.State != "met" {
		t.Fatal("state must reflect the appended goal")
	}
	ev, _ := h.Events(nil)
	if len(ev) != 4 {
		t.Fatalf("events: %d", len(ev))
	}
}

func TestHostToolCallCarriesModuleSource(t *testing.T) {
	h, core := newHandle(t)
	host := newHost(core, "verify", nil)
	res, err := host.Tools().Call(context.Background(), h, "bash", json.RawMessage(`{"command":"go test ./..."}`))
	if err != nil || res.Content != "ran" {
		t.Fatalf("call: %+v %v", res, err)
	}
	if len(core.calls) != 1 || core.calls[0].Source != "module:verify" || core.calls[0].Tool != "bash" || core.calls[0].CallID == "" {
		t.Fatalf("recorded call: %+v", core.calls)
	}
	out, err := host.Model().Complete(context.Background(), h, module.CompletionRequest{
		Messages: []module.Message{{Role: "user", Content: "?"}}})
	if err != nil || out.Content != "judged" {
		t.Fatalf("complete: %+v %v", out, err)
	}
	dir, _ := host.DataDir("verify")
	if !strings.HasSuffix(dir, "verify") {
		t.Fatalf("data dir: %s", dir)
	}
	if host.Log() == nil {
		t.Fatal("logger must not be nil")
	}
	ans, err := host.UI().Ask(context.Background(), h, "which?", nil)
	if err != nil || ans != "yes" {
		t.Fatalf("ask: %q %v", ans, err)
	}
}

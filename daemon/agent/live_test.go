package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
	"github.com/corporealshift/nabu/protocol"
)

// TestLiveLocalModel drives the whole loop against a real OpenAI-compatible
// server. It is skipped unless NABU_LIVE_BASE_URL is set, so the default gate
// stays hermetic.
//
//	NABU_LIVE_BASE_URL=http://localhost:8033/v1 \
//	NABU_LIVE_MODEL=qwen3.6-35b-a3b \
//	go test ./daemon/agent/ -run Live -v -timeout 10m
func TestLiveLocalModel(t *testing.T) {
	base := os.Getenv("NABU_LIVE_BASE_URL")
	if base == "" {
		t.Skip("set NABU_LIVE_BASE_URL to run the live smoke test")
	}
	model := os.Getenv("NABU_LIVE_MODEL")
	if model == "" {
		model = "local-model"
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "greeting.txt"), []byte("hello from nabu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	pcfg := provider.Config{
		Name: "local", BaseURL: base, MaxInFlight: 1,
		ContextWindow: 32768, Timeout: 5 * time.Minute,
	}
	pr := provider.NewRegistry()
	pr.Add(pcfg, provider.NewOpenAI(pcfg, nil), true)

	builtins := &tools.Builtins{}
	mr := module.NewRegistry([]module.Module{builtins}, module.Options{Log: testLogger()})
	m, err := New(Deps{Store: store, Providers: pr, Modules: mr, Builtins: builtins, Root: dir, Log: testLogger()},
		Config{DefaultModel: "local/" + model})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown(context.Background())

	s, err := m.Create(context.Background(), dir, CreateOptions{PermissionMode: protocol.PermissionBypass})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetBudget(context.Background(), s.ID(), protocol.BudgetData{MaxTurns: 8, Source: "client"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prompt(context.Background(), s.ID(),
		"Read the file greeting.txt in the workspace and tell me exactly what it says. Use the read tool."); err != nil {
		t.Fatal(err)
	}
	m.WaitIdle(s.ID())

	st := s.State()
	t.Logf("state=%s turns=%d usage=%+v", st.State, st.Turns, st.Usage)
	for _, e := range s.Events() {
		t.Logf("  %-14s %s", e.Type, summaryOf(e))
	}
	if st.State == protocol.StateError {
		t.Fatal("the live run ended in error; see the log above")
	}
	var readOK bool
	for _, e := range s.Events() {
		if e.Type != protocol.EventToolResult {
			continue
		}
		d := protocol.MustData[protocol.ToolResultData](e)
		if d.Tool == "read" && d.Status == "ok" && strings.Contains(d.Content, "hello from nabu") {
			readOK = true
		}
	}
	if !readOK {
		t.Fatal("the model never successfully read greeting.txt")
	}
	if st.Usage.InputTokens == 0 {
		t.Error("the provider reported no usage; compaction thresholds depend on it")
	}
}

func summaryOf(e protocol.Event) string {
	v, err := protocol.DecodeData(e)
	if err != nil {
		return err.Error()
	}
	switch d := v.(type) {
	case *protocol.MessageData:
		return d.Role + ": " + firstLine(d.Content)
	case *protocol.ToolCallData:
		return d.Tool + " " + firstLine(string(d.Arguments))
	case *protocol.ToolResultData:
		return d.Status + ": " + firstLine(d.Content)
	case *protocol.StateChangeData:
		return string(d.To) + " (" + d.Reason + ")"
	case *protocol.NoticeData:
		return d.Level + ": " + d.Message
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len(s) > 120 {
		s = s[:120] + " …"
	}
	return s
}

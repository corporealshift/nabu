package module

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// fakeSession records appended events; that is all the registry needs.
type fakeSession struct {
	id       string
	appended []protocol.Event
}

func (f *fakeSession) ID() string                                     { return f.id }
func (f *fakeSession) Workspace() Workspace                           { return Workspace{Path: "/w", Key: "w"} }
func (f *fakeSession) Events(after *string) ([]protocol.Event, error) { return nil, nil }
func (f *fakeSession) State() protocol.State                          { return protocol.State{} }
func (f *fakeSession) Append(t protocol.EventType, data any) (protocol.Event, error) {
	b, _ := json.Marshal(data)
	e := protocol.Event{ID: protocol.NewULID(), Type: t, Data: b}
	f.appended = append(f.appended, e)
	return e, nil
}

func (f *fakeSession) notices() []string {
	var out []string
	for _, e := range f.appended {
		if e.Type == protocol.EventNotice {
			out = append(out, protocol.MustData[protocol.NoticeData](e).Message)
		}
	}
	return out
}

// base satisfies Module; embed it and add hook methods per test.
type base struct{ name string }

func (b base) Name() string            { return b.name }
func (b base) Init(Host, Config) error { return nil }

type panicker struct{ base }

func (panicker) BeforeStop(context.Context, Session, StopInfo) StopVerdict { panic("boom") }

type sleeper struct{ base }

func (sleeper) BeforeStop(ctx context.Context, _ Session, _ StopInfo) StopVerdict {
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
	}
	return StopVerdict{Allow: false, Reason: "late"}
}

type vetoer struct {
	base
	reason string
	calls  *[]string
}

func (v vetoer) BeforeStop(context.Context, Session, StopInfo) StopVerdict {
	*v.calls = append(*v.calls, v.name)
	return StopVerdict{Allow: false, Reason: v.reason}
}

type allower struct {
	base
	calls *[]string
}

func (a allower) BeforeStop(context.Context, Session, StopInfo) StopVerdict {
	*a.calls = append(*a.calls, a.name)
	return StopVerdict{Allow: true}
}

type gate struct {
	base
	v Verdict
}

func (g gate) GateTool(context.Context, Session, protocol.ToolCallData) Verdict { return g.v }

type prefixer struct{ base }

func (prefixer) BeforeRequest(context.Context, Session) ([]ContextBlock, error) {
	return []ContextBlock{{Slot: "prefix", Content: "nope"}, {Slot: "suffix", Content: "ok"}}, nil
}

type toolish struct {
	base
	names []string
}

func (t toolish) Tools() []Tool {
	var out []Tool
	for _, n := range t.names {
		out = append(out, Tool{Name: n})
	}
	return out
}

func quiet() Options {
	return Options{Log: slog.New(slog.NewTextHandler(discard{}, nil))}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestStopGatesAreAllAskedInOrder(t *testing.T) {
	var calls []string
	r := NewRegistry([]Module{
		vetoer{base{"a"}, "open tasks", &calls},
		allower{base{"b"}, &calls},
		vetoer{base{"c"}, "dirty tree", &calls},
	}, quiet())
	s := &fakeSession{id: "s1"}
	vetoes := r.BeforeStop(context.Background(), s, StopInfo{})
	if strings.Join(calls, ",") != "a,b,c" {
		t.Fatalf("order: %v", calls)
	}
	if len(vetoes) != 2 || vetoes[0].Module != "a" || vetoes[1].Module != "c" {
		t.Fatalf("vetoes: %+v", vetoes)
	}
}

func TestPanickingModuleIsDisabledWithNotice(t *testing.T) {
	var calls []string
	r := NewRegistry([]Module{panicker{base{"bad"}}, allower{base{"ok"}, &calls}}, quiet())
	s := &fakeSession{id: "s1"}
	if v := r.BeforeStop(context.Background(), s, StopInfo{}); len(v) != 0 {
		t.Fatalf("panic must not produce a veto: %+v", v)
	}
	if len(calls) != 1 {
		t.Fatalf("later modules must still run: %v", calls)
	}
	n := s.notices()
	if len(n) != 1 || !strings.Contains(n[0], "module bad disabled") || !strings.Contains(n[0], "panic: boom") {
		t.Fatalf("notice: %v", n)
	}
	// Second call: bad is skipped silently, no second notice.
	r.BeforeStop(context.Background(), s, StopInfo{})
	if len(s.notices()) != 1 {
		t.Fatalf("expected exactly one notice, got %v", s.notices())
	}
	// A different session is unaffected.
	s2 := &fakeSession{id: "s2"}
	r.BeforeStop(context.Background(), s2, StopInfo{})
	if len(s2.notices()) != 1 {
		t.Fatalf("disable state must be per session")
	}
}

func TestTimedOutModuleIsDisabled(t *testing.T) {
	opts := quiet()
	opts.GateTimeout = 50 * time.Millisecond
	r := NewRegistry([]Module{sleeper{base{"slow"}}}, opts)
	s := &fakeSession{id: "s1"}
	start := time.Now()
	vetoes := r.BeforeStop(context.Background(), s, StopInfo{})
	if time.Since(start) > time.Second {
		t.Fatal("timeout not enforced")
	}
	if len(vetoes) != 0 {
		t.Fatalf("timed-out gate must not veto: %+v", vetoes)
	}
	if n := s.notices(); len(n) != 1 || !strings.Contains(n[0], "timed out") {
		t.Fatalf("notice: %v", n)
	}
}

func TestCallerCancellationIsNotTheModulesFault(t *testing.T) {
	r := NewRegistry([]Module{sleeper{base{"slow"}}}, quiet())
	s := &fakeSession{id: "s1"}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	r.BeforeStop(ctx, s, StopInfo{})
	if len(s.notices()) != 0 {
		t.Fatalf("caller cancellation must not disable the module: %v", s.notices())
	}
}

func TestToolGateDenyShortCircuitsAskAggregates(t *testing.T) {
	r := NewRegistry([]Module{
		gate{base{"a"}, Verdict{Decision: Ask, Summary: "run tests", Risk: "low"}},
		gate{base{"b"}, Verdict{Decision: Deny, Reason: "rm -rf"}},
		gate{base{"c"}, Verdict{Decision: Ask, Summary: "other"}},
	}, quiet())
	s := &fakeSession{id: "s1"}
	v := r.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash"})
	if v.Decision != Deny || v.Reason != "rm -rf" {
		t.Fatalf("want deny from b, got %+v", v)
	}
	r2 := NewRegistry([]Module{
		gate{base{"a"}, Verdict{Decision: Allow}},
		gate{base{"b"}, Verdict{Decision: Ask, Summary: "first ask"}},
		gate{base{"c"}, Verdict{Decision: Ask, Summary: "second ask"}},
	}, quiet())
	v = r2.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash"})
	if v.Decision != Ask || v.Summary != "first ask" {
		t.Fatalf("want first ask, got %+v", v)
	}
	r3 := NewRegistry([]Module{
		gate{base{"a"}, Verdict{Decision: Halt, Reason: "refused again", Summary: "stopped"}},
		gate{base{"b"}, Verdict{Decision: Deny, Reason: "later"}},
	}, quiet())
	v = r3.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "write"})
	if v.Decision != Halt || v.Summary != "stopped" {
		t.Fatalf("a halt short-circuits like a deny, got %+v", v)
	}
}

func TestBeforeRequestDropsPrefixBlocks(t *testing.T) {
	r := NewRegistry([]Module{prefixer{base{"p"}}}, quiet())
	s := &fakeSession{id: "s1"}
	blocks := r.BeforeRequest(context.Background(), s)
	if len(blocks) != 1 || blocks[0].Block.Slot != "suffix" || blocks[0].Module != "p" {
		t.Fatalf("blocks: %+v", blocks)
	}
	if n := s.notices(); len(n) != 1 || !strings.Contains(n[0], "only suffix is allowed") {
		t.Fatalf("notice: %v", n)
	}
}

func TestDuplicateToolNamesRejected(t *testing.T) {
	r := NewRegistry([]Module{toolish{base{"a"}, []string{"bash", "x"}}, toolish{base{"b"}, []string{"x"}}}, quiet())
	if _, err := r.Tools(); err == nil || !strings.Contains(err.Error(), `tool "x"`) {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

type disabledByConfig struct{ base }

func TestInitHonoursEnabledFlagAndSurvivesFailure(t *testing.T) {
	r := NewRegistry([]Module{disabledByConfig{base{"off"}}, allower{base{"on"}, new([]string)}}, quiet())
	r.Init(func(string) Host { return nil }, func(name string) Config {
		if name == "off" {
			return Config{"enabled": false}
		}
		return Config{}
	})
	mods := r.Modules()
	if len(mods) != 1 || mods[0].Name() != "on" {
		t.Fatalf("active modules: %v", mods)
	}
	if _, dead := r.Dead()["off"]; !dead {
		t.Fatal("off should be recorded as dead")
	}
}

func TestConfigAccessors(t *testing.T) {
	c := Config{"enabled": false, "n": int64(3), "f": 2.0, "s": "x", "list": []any{"a", "b"}}
	if c.Enabled() || c.Int("n", 0) != 3 || c.Int("f", 0) != 2 || c.String("s", "") != "x" || len(c.Strings("list", nil)) != 2 {
		t.Fatalf("accessors: %+v", c)
	}
	empty := Config{}
	if !empty.Enabled() || empty.Int("missing", 7) != 7 {
		t.Fatal("defaults")
	}
}

// A config that says something unreadable should lose that entry, not gain a
// stringified one: a root spelled as a number is a mistake, and coercing it to
// "42" would turn it into a path.
func TestConfigStringMap(t *testing.T) {
	cfg := Config{
		"workspaces": map[string]any{"a": "/one", "b": "/two", "n": 42, "nested": map[string]any{}},
		"notamap":    "just a string",
	}

	got := cfg.StringMap("workspaces")
	if len(got) != 2 || got["a"] != "/one" || got["b"] != "/two" {
		t.Fatalf("StringMap = %v, want just the two string entries", got)
	}
	if cfg.StringMap("notamap") != nil {
		t.Error("a non-map key should yield nil")
	}
	if cfg.StringMap("absent") != nil {
		t.Error("an absent key should yield nil")
	}

	// A decoder that already produced map[string]string is accepted as is.
	direct := Config{"m": map[string]string{"k": "v"}}
	if direct.StringMap("m")["k"] != "v" {
		t.Error("an already-typed map should pass through")
	}
}

// slowCompactor stands in for the memory curator: its BeforeCompaction makes
// what would be a model call, and takes as long as that call does.
type slowCompactor struct {
	base
	delay time.Duration
}

func (c slowCompactor) BeforeCompaction(ctx context.Context, _ Session, _ Range) []string {
	select {
	case <-time.After(c.delay):
		return []string{"kept"}
	case <-ctx.Done():
		return nil
	}
}

func (slowCompactor) AfterCompaction(context.Context, Session) ([]ContextBlock, error) {
	return nil, nil
}

// A compaction hook may call the model, so it is held to the compaction budget
// rather than to the one for hooks that only look at the log. Issue 87: the
// memory curator was killed at 30 seconds while summarising a long session.
func TestCompactionHooksGetTheCompactionBudget(t *testing.T) {
	cases := []struct {
		name      string
		delay     time.Duration
		wantKept  bool
		wantNotes int
	}{
		{"slower than an ordinary hook is allowed", 60 * time.Millisecond, true, 0},
		{"slower than the compaction budget", 400 * time.Millisecond, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry([]Module{slowCompactor{base{"memory"}, tc.delay}}, Options{
				HookTimeout:       20 * time.Millisecond,
				CompactionTimeout: 200 * time.Millisecond,
				Log:               slog.New(slog.DiscardHandler),
			})
			s := &fakeSession{id: "S1"}
			got := r.BeforeCompaction(context.Background(), s, Range{})
			if kept := len(got) == 1 && got[0] == "kept"; kept != tc.wantKept {
				t.Fatalf("preserve = %v, want kept=%v", got, tc.wantKept)
			}
			if n := s.notices(); len(n) != tc.wantNotes {
				t.Fatalf("notices = %v, want %d", n, tc.wantNotes)
			}
		})
	}
}

// slowEnder is the memory curator at the end of a session.
type slowEnder struct {
	base
	delay time.Duration
	done  *atomic.Bool
}

func (e slowEnder) SessionEnd(ctx context.Context, _ Session) {
	select {
	case <-time.After(e.delay):
		e.done.Store(true)
	case <-ctx.Done():
	}
}

// SessionEnd may call the model too: the curator reads the session there, and
// at 30 seconds it timed out on seven passes in ten on a local model, taking
// the session's memory commit with it. Issue 87, again, at the other end.
func TestSessionEndGetsItsOwnBudget(t *testing.T) {
	cases := []struct {
		name      string
		delay     time.Duration
		wantDone  bool
		wantNotes int
	}{
		{"slower than an ordinary hook is allowed", 60 * time.Millisecond, true, 0},
		{"slower than the session-end budget", 400 * time.Millisecond, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			done := &atomic.Bool{}
			r := NewRegistry([]Module{slowEnder{base{"memory"}, tc.delay, done}}, Options{
				HookTimeout:       20 * time.Millisecond,
				SessionEndTimeout: 200 * time.Millisecond,
				Log:               slog.New(slog.DiscardHandler),
			})
			s := &fakeSession{id: "S1"}
			r.SessionEnd(context.Background(), s)
			if done.Load() != tc.wantDone {
				t.Fatalf("finished = %v, want %v", done.Load(), tc.wantDone)
			}
			if n := s.notices(); len(n) != tc.wantNotes {
				t.Fatalf("notices = %v, want %d", n, tc.wantNotes)
			}
		})
	}
}

func TestSessionEndBudgetDefaultsToTenMinutes(t *testing.T) {
	r := NewRegistry(nil, Options{})
	if r.opts.SessionEndTimeout != 10*time.Minute {
		t.Errorf("default: %s", r.opts.SessionEndTimeout)
	}
}

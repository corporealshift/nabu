package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// fakeSession holds a log the test builds, and the notices the module adds.
type fakeSession struct {
	log     []protocol.Event
	notices []string
	clock   time.Time
	n       int
}

func (f *fakeSession) ID() string                               { return "01ARZ3NDEKTSV4RRFFQ69G5FAV" }
func (f *fakeSession) Workspace() module.Workspace              { return module.Workspace{Path: "/w"} }
func (f *fakeSession) State() protocol.State                    { return protocol.State{} }
func (f *fakeSession) Events(*string) ([]protocol.Event, error) { return f.log, nil }
func (f *fakeSession) Append(t protocol.EventType, data any) (protocol.Event, error) {
	if n, ok := data.(protocol.NoticeData); ok {
		f.notices = append(f.notices, n.Message)
	}
	return protocol.Event{}, nil
}

func (f *fakeSession) add(t protocol.EventType, data any) {
	raw, _ := json.Marshal(data)
	f.n++
	f.clock = f.clock.Add(20 * time.Second)
	f.log = append(f.log, protocol.Event{ID: fmt.Sprintf("E%03d", f.n), Timestamp: f.clock, Type: t, Data: raw})
}

func (f *fakeSession) said(role, text string) *fakeSession {
	f.add(protocol.EventMessage, protocol.MessageData{Role: role, Content: text})
	return f
}

// ran logs one model turn that made a call and got a result.
func (f *fakeSession) ran(tool, args, status, content string) *fakeSession {
	f.said("assistant", "")
	id := fmt.Sprintf("c%d", f.n)
	f.add(protocol.EventToolCall, protocol.ToolCallData{CallID: id, Tool: tool, Arguments: json.RawMessage(args), Source: "model"})
	f.add(protocol.EventToolResult, protocol.ToolResultData{CallID: id, Tool: tool, Status: status, Content: content})
	return f
}

// through runs a call through the module's gate as the agent would, logging
// the refusal in place of the result when it refuses.
func (f *fakeSession) through(t *testing.T, m *Module, tool, args, content string) module.Verdict {
	t.Helper()
	v := m.GateTool(context.Background(), f, protocol.ToolCallData{Tool: tool, Arguments: json.RawMessage(args), Source: "model"})
	if v.Decision == module.Allow {
		f.ran(tool, args, "ok", content)
	} else {
		f.ran(tool, args, "error", "denied: "+v.Reason)
	}
	return v
}

func newSession() *fakeSession {
	f := &fakeSession{clock: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}
	return f.said("user", "fix the build")
}

func loaded(t *testing.T, cfg module.Config) *Module {
	t.Helper()
	m := &Module{}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatal(err)
	}
	return m
}

func suffix(t *testing.T, m *Module, s *fakeSession) string {
	t.Helper()
	blocks, err := m.BeforeRequest(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range blocks {
		if b.Slot != "suffix" {
			t.Fatalf("only suffix blocks may come from BeforeRequest: %+v", b)
		}
		out = append(out, b.Content)
	}
	return strings.Join(out, "\n")
}

const (
	errorRS  = `{"path":"src/error.rs","content":"pub enum Error {}\n"}`
	build    = `{"command":"cargo build 2>&1"}`
	buildErr = "error[E0432]: unresolved import `breezeway_server`\n --> src/routes/auth.rs:3:5\n[exit status 101]"
)

// The 32-write loop from the logs: the same write, the same result, over and
// over. It gets a notice with facts, then refusals, then the session stops.
func TestAnIdenticalChangeIsNoticedThenRefusedThenStopped(t *testing.T) {
	m := loaded(t, module.Config{})
	s := newSession().ran("bash", build, "error", buildErr)

	var decisions []module.Decision
	var notices []string
	for i := 0; i < 6; i++ {
		v := s.through(t, m, "write", errorRS, "wrote 18 bytes to src/error.rs")
		decisions = append(decisions, v.Decision)
		notices = append(notices, suffix(t, m, s))
		if v.Decision == module.Halt {
			if !strings.Contains(v.Summary, "`write src/error.rs` repeated 3 times") {
				t.Fatalf("halt summary: %q", v.Summary)
			}
			break
		}
	}
	want := []module.Decision{module.Allow, module.Allow, module.Allow, module.Deny, module.Deny, module.Halt}
	if fmt.Sprint(decisions) != fmt.Sprint(want) {
		t.Fatalf("decisions: %v, want %v", decisions, want)
	}
	if notices[0] != "" || notices[1] != "" {
		t.Fatalf("no notice before the third identical write: %q", notices[:2])
	}
	for _, w := range []string{"`write src/error.rs` has now run 3 times", "Nothing else has changed src/error.rs in the 2 minutes",
		"The last failing check was `bash cargo build 2>&1`", "unresolved import", "src/routes/auth.rs:3", "will be refused"} {
		if !strings.Contains(notices[2], w) {
			t.Fatalf("the notice should carry %q:\n%s", w, notices[2])
		}
	}
	if notices[3] != "" {
		t.Fatalf("a refusal is its own news; no notice on top of it: %q", notices[3])
	}
	if len(s.notices) != 1 || !strings.Contains(s.notices[0], "`write src/error.rs` has run 3 times") {
		t.Fatalf("the person's clients should see one notice: %q", s.notices)
	}
}

// The refusal says what the model has stopped looking at.
func TestARefusalCarriesTheFacts(t *testing.T) {
	m := loaded(t, module.Config{})
	s := newSession().ran("bash", build, "error", buildErr)
	for i := 0; i < 3; i++ {
		s.ran("write", errorRS, "ok", "unchanged: src/error.rs already has exactly this content (18 bytes)")
	}
	v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "write", Arguments: json.RawMessage(errorRS), Source: "model"})
	if v.Decision != module.Deny {
		t.Fatalf("got %+v", v)
	}
	for _, w := range []string{"loop: ", "already run 3 times", "unchanged: src/error.rs", "cannot change anything",
		"The last failing check was `bash cargo build 2>&1`", "refused 1 time more"} {
		if !strings.Contains(v.Reason, w) {
			t.Fatalf("the refusal should carry %q:\n%s", w, v.Reason)
		}
	}
}

// Only an identical change counts. Anything that would make the next call do
// something different starts the count again.
func TestWhatIsNotAnIdenticalChange(t *testing.T) {
	m := loaded(t, module.Config{})
	cases := []struct {
		name string
		log  func(s *fakeSession)
	}{
		{"different content each time", func(s *fakeSession) {
			for i := 0; i < 5; i++ {
				s.ran("write", fmt.Sprintf(`{"path":"src/error.rs","content":"v%d"}`, i), "ok", "wrote 2 bytes to src/error.rs")
			}
		}},
		{"the file changed in between", func(s *fakeSession) {
			for i := 0; i < 4; i++ {
				s.ran("write", errorRS, "ok", "wrote 18 bytes to src/error.rs")
				s.ran("edit", `{"path":"src/error.rs","old":"{}","new":"{ Io }"}`, "ok", "replaced 1 occurrence(s) in src/error.rs")
			}
		}},
		{"a command changed the tree in between", func(s *fakeSession) {
			for i := 0; i < 4; i++ {
				s.ran("write", errorRS, "ok", "wrote 18 bytes to src/error.rs")
				s.ran("bash", `{"command":"git checkout -- src"}`, "ok", "")
			}
		}},
		{"the person said something", func(s *fakeSession) {
			for i := 0; i < 3; i++ {
				s.ran("write", errorRS, "ok", "wrote 18 bytes to src/error.rs")
			}
			s.said("user", "keep going, that write is fine")
		}},
		{"the same check run again and again", func(s *fakeSession) {
			for i := 0; i < 8; i++ {
				s.ran("bash", build, "error", buildErr)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession()
			tc.log(s)
			for _, next := range []string{errorRS, `{"path":"src/error.rs","content":"v4"}`} {
				v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "write", Arguments: json.RawMessage(next), Source: "model"})
				if v.Decision != module.Allow {
					t.Fatalf("refused: %+v", v)
				}
			}
			v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(build), Source: "model"})
			if v.Decision != module.Allow {
				t.Fatalf("a check is never refused: %+v", v)
			}
		})
	}
}

// A check failing the same way after edits: say the edits are not reaching
// it, and refuse nothing.
func TestAChangeNotLandingIsToldNotRefused(t *testing.T) {
	m := loaded(t, module.Config{})
	s := newSession()
	for i, file := range []string{"src/lib.rs", "src/error.rs", "src/lib.rs"} {
		s.ran("bash", build, "error", buildErr)
		s.ran("edit", fmt.Sprintf(`{"path":%q,"old":"a%d","new":"b"}`, file, i), "ok", "replaced 1 occurrence(s)")
	}
	if got := suffix(t, m, s); got != "" {
		t.Fatalf("an edit is not a check: %q", got)
	}
	s.ran("bash", build, "error", buildErr)
	got := suffix(t, m, s)
	for _, w := range []string{"`bash cargo build 2>&1` has failed with the same output 4 times", "3 changes made in between",
		"edit src/lib.rs, edit src/error.rs, edit src/lib.rs", "unresolved import", "points at src/routes/auth.rs:3"} {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in:\n%s", w, got)
		}
	}
	v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(build), Source: "model"})
	if v.Decision != module.Allow {
		t.Fatalf("a check is never refused: %+v", v)
	}

	// A fix that lands changes the output, and the notice goes away.
	s.ran("edit", `{"path":"src/routes/auth.rs","old":"x","new":"y"}`, "ok", "replaced 1 occurrence(s)")
	s.ran("bash", build, "ok", "Finished dev")
	if got := suffix(t, m, s); got != "" {
		t.Fatalf("no notice once the output changed: %q", got)
	}
}

// Polling is waiting, not looping. A hint at the fifth identical look, and
// again at the tenth; never a refusal.
func TestWatchingGetsAHintAtAHighThreshold(t *testing.T) {
	m := loaded(t, module.Config{})
	s := newSession()
	view := `{"command":"gh run view 42"}`
	var hinted []int
	for i := 1; i <= 10; i++ {
		if v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(view), Source: "model"}); v.Decision != module.Allow {
			t.Fatalf("watching is never refused: %+v", v)
		}
		s.ran("bash", view, "ok", "in_progress")
		if got := suffix(t, m, s); got != "" {
			hinted = append(hinted, i)
			if !strings.Contains(got, "the `wait` tool") || !strings.Contains(got, fmt.Sprintf("same output %d times", i)) {
				t.Fatalf("hint %d: %q", i, got)
			}
		}
	}
	if fmt.Sprint(hinted) != "[5 10]" {
		t.Fatalf("hinted at %v, want [5 10]", hinted)
	}

	// Output that changes along the way is a check doing its job.
	s = newSession()
	for i := 0; i < 10; i++ {
		s.ran("bash", view, "ok", fmt.Sprintf("in_progress, step %d", i/3))
		if got := suffix(t, m, s); got != "" {
			t.Fatalf("output that changes is not watching in vain: %q", got)
		}
	}

	// Waiting on purpose is never counted.
	s = newSession()
	for i := 0; i < 10; i++ {
		s.ran("bash", `{"command":"sleep 30 && gh run view 42"}`, "ok", "in_progress")
		if got := suffix(t, m, s); got != "" {
			t.Fatalf("an explicit sleep is waiting on purpose: %q", got)
		}
	}
}

func TestThresholdsAreConfigurableAndTheModuleCanBeOff(t *testing.T) {
	s := newSession()
	for i := 0; i < 5; i++ {
		s.ran("write", errorRS, "ok", "wrote 18 bytes to src/error.rs")
	}
	call := protocol.ToolCallData{Tool: "write", Arguments: json.RawMessage(errorRS), Source: "model"}
	if v := loaded(t, module.Config{"enabled": false}).GateTool(context.Background(), s, call); v.Decision != module.Allow {
		t.Fatalf("disabled: %+v", v)
	}
	if v := loaded(t, module.Config{"identical_after": 6}).GateTool(context.Background(), s, call); v.Decision != module.Allow {
		t.Fatalf("identical_after 6: %+v", v)
	}
	if v := loaded(t, module.Config{"identical_after": 5}).GateTool(context.Background(), s, call); v.Decision != module.Deny {
		t.Fatalf("identical_after 5: %+v", v)
	}
}

// A module's own calls are its business, and a nil session is survivable.
func TestOnlyTheModelsCallsCount(t *testing.T) {
	m := loaded(t, module.Config{})
	s := newSession()
	for i := 0; i < 5; i++ {
		s.ran("write", errorRS, "ok", "wrote 18 bytes to src/error.rs")
	}
	if v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "write", Arguments: json.RawMessage(errorRS), Source: "module:notes"}); v.Decision != module.Allow {
		t.Fatalf("a module's call: %+v", v)
	}
	if v := m.GateTool(context.Background(), nil, protocol.ToolCallData{Tool: "write", Source: "model"}); v.Decision != module.Allow {
		t.Fatalf("nil session: %+v", v)
	}
	if blocks, err := m.BeforeRequest(context.Background(), nil); blocks != nil || err != nil {
		t.Fatalf("nil session: %v %v", blocks, err)
	}
}

// Key order and spacing do not make an identical call look different.
func TestArgumentsAreComparedCanonically(t *testing.T) {
	m := loaded(t, module.Config{})
	s := newSession()
	for _, args := range []string{errorRS, `{"content":"pub enum Error {}\n","path":"src/error.rs"}`, `{ "path": "src/error.rs", "content": "pub enum Error {}\n" }`} {
		s.ran("write", args, "ok", "wrote 18 bytes to src/error.rs")
	}
	if v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "write", Arguments: json.RawMessage(errorRS), Source: "model"}); v.Decision != module.Deny {
		t.Fatalf("got %+v", v)
	}
}

// A build piped through head exits 0 whatever happened; its output decides.
// A search for "error" is not a failure.
func TestFailedReadsTheOutputOfAPipedCheck(t *testing.T) {
	cases := []struct {
		tool, command, status, content string
		want                           bool
	}{
		{"bash", "cargo build 2>&1 | head -80", "ok", "error[E0432]: unresolved import", true},
		{"bash", "cd /c/x && cargo test 2>&1 | tail -40", "ok", "test result: FAILED. 3 passed; 1 failed", true},
		{"bash", "cargo build 2>&1 | head -80", "ok", "Finished `dev` profile", false},
		{"bash", "cargo test 2>&1 | tail -3", "ok", "test result: ok. 63 passed; 0 failed; 0 ignored", false},
		{"bash", "cargo check 2>&1 | tail -1", "ok", "warning: 0 errors, 2 warnings", false},
		{"bash", "go test ./... | tail", "ok", "--- FAIL: TestX (0.00s)", true},
		{"bash", "pytest -q | tail -1", "ok", "2 failed, 40 passed in 1.2s", true},
		{"bash", "go test ./...", "error", "[exit status 1]", true},
		{"bash", "grep -rn error src", "ok", "src/error.rs:1: pub enum Error", false},
		{"bash", "cd /c/x && cat build.log", "ok", "error: linking failed", false},
		{"read", "", "ok", "1	pub enum Error { Failed }", false},
	}
	for _, c := range cases {
		a := attempt{tool: c.tool, command: c.command, ok: c.status == "ok", content: c.content}
		if got := failed(a); got != c.want {
			t.Errorf("%s %q -> %q: failed = %v, want %v", c.tool, c.command, c.content, got, c.want)
		}
	}
}

func TestLabelsNameTheCallBriefly(t *testing.T) {
	cases := []struct{ tool, args, want string }{
		{"bash", `{"command":"cd /c/Users/corpo/Documents/projects/breezeway && cargo build 2>&1 | head -80"}`, "bash cargo build 2>&1 | head -80"},
		{"edit", `{"path":"C:/Users/corpo/Documents/projects/breezeway/crates/breezeway-server/tests/scheduler.rs"}`, "edit …rojects/breezeway/crates/breezeway-server/tests/scheduler.rs"},
		{"write", `{"path":"src/error.rs","content":"x"}`, "write src/error.rs"},
		{"ask", `{}`, "ask"},
	}
	for _, c := range cases {
		if got := label(protocol.ToolCallData{Tool: c.tool, Arguments: json.RawMessage(c.args)}); got != c.want {
			t.Errorf("label(%s %s) = %q, want %q", c.tool, c.args, got, c.want)
		}
	}
}

// A check that failed and then passed is not what the model is stuck on.
func TestTheLastFailureIsOneThatStillFails(t *testing.T) {
	s := newSession().
		ran("bash", build, "error", buildErr).
		ran("bash", build, "ok", "Finished dev").
		ran("bash", `{"command":"cargo test"}`, "error", "test result: FAILED. 1 failed\n[exit status 101]")
	got := lastFailure(history(s.log), "")
	if !strings.Contains(got, "`bash cargo test`") || strings.Contains(got, "cargo build") {
		t.Fatalf("got %q", got)
	}
	s.ran("bash", `{"command":"cargo test"}`, "ok", "test result: ok. 2 passed; 0 failed")
	if got := lastFailure(history(s.log), ""); got != "" {
		t.Fatalf("everything passes now: %q", got)
	}
}

// thought logs one model turn that thought, made a call and got a result.
func (f *fakeSession) thought(opening, tool, args, content string) *fakeSession {
	f.add(protocol.EventThinking, protocol.ThinkingData{Content: opening, Source: "model"})
	return f.ran(tool, args, "ok", content)
}

const (
	logGrep  = `{"command":"git log --oneline --all | grep -n 2bc77dd"}`
	logTail  = `{"command":"git log --oneline --all -- clients/android | tail -1"}`
	plan     = "The Android work is already on main (56 commits). I need to reset main back."
	grepOut  = "201:2bc77dd android: project skeleton\n"
	tailOut  = "2bc77dd android: project skeleton\n"
	stalling = "returned nothing new"
)

// stale logs n turns that alternate two calls whose results the model has
// already seen, after one turn of each that saw them first.
func stale(s *fakeSession, n int) *fakeSession {
	s.thought(plan, "bash", logGrep, grepOut)
	s.thought(plan, "bash", logTail, tailOut)
	return more(s, n)
}

// more logs n further turns that return nothing new.
func more(s *fakeSession, n int) *fakeSession {
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			s.thought(plan, "bash", logGrep, grepOut)
		} else {
			s.thought(plan, "bash", logTail, tailOut)
		}
	}
	return s
}

// 01M39RT5: different calls, every one returning what it returned before, and
// the same plan opening every turn, never carried out. The notice says so and
// points at stopping or asking, not at wait.
func TestStalledTurnsAreNoticed(t *testing.T) {
	m := loaded(t, module.Config{})
	s := stale(newSession(), 4)
	if got := suffix(t, m, s); strings.Contains(got, stalling) {
		t.Fatalf("four stale turns are not yet a stall: %q", got)
	}
	more(s, 1)
	got := suffix(t, m, s)
	for _, w := range []string{"Your last 5 turns have returned nothing new", "`bash git log --oneline --all | grep -n 2bc77dd`",
		`"201:2bc77dd android: project skeleton"`, `"The Android work is already on main (56 commits)."`,
		"say so and stop, or `ask`", "2 more turns like this will stop the session"} {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "`wait`") {
		t.Fatalf("a stall is not waiting; no wait hint: %q", got)
	}
	if len(s.notices) != 1 || !strings.Contains(s.notices[0], "5 turns returned nothing new") {
		t.Fatalf("the person's clients should see one notice: %q", s.notices)
	}
	if v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(logGrep), Source: "model"}); v.Decision != module.Allow {
		t.Fatalf("a notice is not yet a stop: %+v", v)
	}
}

// Anything new resets the run.
func TestSomethingNewBreaksAStall(t *testing.T) {
	m := loaded(t, module.Config{})
	s := stale(newSession(), 4)
	s.thought("Let me look at the branch list.", "bash", `{"command":"git branch -v"}`, "* main 64cbd95\n")
	more(s, 4)
	if got := suffix(t, m, s); strings.Contains(got, stalling) {
		t.Fatalf("a new result in between breaks the run: %q", got)
	}
}

// A single call repeated is watching, which the stalled case leaves alone:
// a check is never refused.
func TestOneCallRepeatedIsWatchingNotStalled(t *testing.T) {
	m := loaded(t, module.Config{})
	s := newSession()
	for i := 0; i < 10; i++ {
		s.thought("Waiting for CI.", "bash", `{"command":"gh run view 42"}`, "in_progress")
		if got := suffix(t, m, s); strings.Contains(got, stalling) {
			t.Fatalf("turn %d: polling is watching, not a stall: %q", i+1, got)
		}
	}
	if v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(`{"command":"gh run view 42"}`), Source: "model"}); v.Decision != module.Allow {
		t.Fatalf("watching is never stopped: %+v", v)
	}
}

// Two more stale turns after the notice, and the next call stops the session.
func TestAStallThatGoesOnStops(t *testing.T) {
	m := loaded(t, module.Config{})
	s := stale(newSession(), 6)
	if v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(logGrep), Source: "model"}); v.Decision != module.Allow {
		t.Fatalf("one more stale turn is not yet a stop: %+v", v)
	}
	more(s, 1)
	v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "read", Arguments: json.RawMessage(`{"path":"a.go"}`), Source: "model"})
	if v.Decision != module.Halt {
		t.Fatalf("got %+v, want Halt", v)
	}
	if !strings.Contains(v.Summary, "7 turns in a row returned nothing new") || !strings.HasPrefix(v.Reason, refusedPrefix) {
		t.Fatalf("halt: %+v", v)
	}
}

// A stuck model gets something new now and then. A second stall since the
// person spoke stops the session: in 01M39RT5 the runs were 5 and 6 long.
func TestASecondStallStops(t *testing.T) {
	m := loaded(t, module.Config{})
	s := stale(newSession(), 5)
	suffix(t, m, s)
	s.thought("Let me look at the branch list.", "bash", `{"command":"git branch -v"}`, "* main 64cbd95\n")
	more(s, 4)
	if v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(logGrep), Source: "model"}); v.Decision != module.Allow {
		t.Fatalf("four stale turns into the second run is not yet a stop: %+v", v)
	}
	more(s, 1)
	got := suffix(t, m, s)
	if !strings.Contains(got, "the second time since the person's last message") || !strings.Contains(got, "next call will stop the session") {
		t.Fatalf("the second notice should say what comes next:\n%s", got)
	}
	v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(logGrep), Source: "model"})
	if v.Decision != module.Halt || !strings.Contains(v.Summary, "stalled twice") {
		t.Fatalf("got %+v, want Halt", v)
	}
}

// A word from the person starts everything again.
func TestThePersonResetsAStall(t *testing.T) {
	m := loaded(t, module.Config{})
	s := stale(newSession(), 5)
	suffix(t, m, s)
	s.said("user", "the work is already on main, just open the PR from it")
	stale(s, 5)
	if got := suffix(t, m, s); !strings.Contains(got, "Your last 5 turns") || strings.Contains(got, "second time") {
		t.Fatalf("a fresh first notice after the person spoke: %q", got)
	}
	if v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(logGrep), Source: "model"}); v.Decision != module.Allow {
		t.Fatalf("the first stall since the person spoke is not a stop: %+v", v)
	}
}

// When a repeated change is the fact, it is the one given.
func TestAnIdenticalChangeNoticeWinsOverAStall(t *testing.T) {
	const same = "unchanged: src/error.rs already has exactly this content (18 bytes)"
	s := newSession()
	s.thought(plan, "bash", logGrep, grepOut)
	s.thought(plan, "bash", logTail, tailOut)
	s.thought(plan, "write", errorRS, same)
	more(s, 2)
	s.thought(plan, "write", errorRS, same)
	more(s, 1)
	s.thought(plan, "write", errorRS, same)

	// Without the identical-change notice, these five turns are a stall.
	if got := suffix(t, loaded(t, module.Config{"identical_after": 9}), s); !strings.Contains(got, stalling) {
		t.Fatalf("the turns should stall: %q", got)
	}
	got := suffix(t, loaded(t, module.Config{}), s)
	if !strings.Contains(got, "has now run 3 times") {
		t.Fatalf("the identical-change notice: %q", got)
	}
	if strings.Contains(got, stalling) {
		t.Fatalf("one notice per request, the more specific: %q", got)
	}
}

// An edit that lands is a change, however alike its result reads: building
// and editing in turn is a change not landing, not a stall.
func TestEditsThatLandAreNotAStall(t *testing.T) {
	m := loaded(t, module.Config{})
	s := newSession()
	for i := 0; i < 6; i++ {
		s.thought("Let me fix the import.", "bash", build, buildErr)
		s.thought("Let me fix the import.", "edit", fmt.Sprintf(`{"path":"src/lib.rs","old":"a%d","new":"b"}`, i), "replaced 1 occurrence(s) in src/lib.rs")
	}
	if got := suffix(t, m, s); strings.Contains(got, stalling) {
		t.Fatalf("edits that land are not a stall: %q", got)
	}
	if v := m.GateTool(context.Background(), s, protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(build), Source: "model"}); v.Decision != module.Allow {
		t.Fatalf("a check is never refused: %+v", v)
	}
}

func TestStallThresholdsAreConfigurable(t *testing.T) {
	s := stale(newSession(), 3)
	if got := suffix(t, loaded(t, module.Config{"stalled_after": 3}), s); !strings.Contains(got, "Your last 3 turns") {
		t.Fatalf("stalled_after 3: %q", got)
	}
	more(s, 1)
	v := loaded(t, module.Config{"stalled_after": 3, "stalled_halt_after": 1}).GateTool(context.Background(), s,
		protocol.ToolCallData{Tool: "bash", Arguments: json.RawMessage(logGrep), Source: "model"})
	if v.Decision != module.Halt {
		t.Fatalf("stalled_halt_after 1: %+v", v)
	}
}

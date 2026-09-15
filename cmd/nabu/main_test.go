package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tui "github.com/corporealshift/nabu/clients/go-tui"
	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/daemon"
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// runCLI invokes the CLI with an injected writer pair, so exit codes and
// output are asserted without spawning a binary.
func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// startDaemon runs a daemon over a temp root and returns that root.
func startDaemon(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	d, err := daemon.New(daemon.Options{Root: root, Bind: "127.0.0.1:0", LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	if _, err := daemon.WaitForDaemon(context.Background(), root, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestMain keeps the real TUI out of the suite. Without it any test that
// reaches the default command blocks forever waiting on a terminal.
func TestMain(m *testing.M) {
	launchTUI = func(context.Context, tui.Options) error { return nil }
	os.Exit(m.Run())
}

func TestHelpWordPrintsUsage(t *testing.T) {
	code, stdout, _ := runCLI("help")
	if code != exitOK {
		t.Errorf("exit: got %d, want 0", code)
	}
	if !strings.Contains(stdout, "usage: nabu") {
		t.Errorf("stdout should carry usage, got %q", stdout)
	}
}

func TestHelpGoesToStdout(t *testing.T) {
	code, stdout, _ := runCLI("--help")
	if code != exitOK {
		t.Errorf("exit: got %d, want 0", code)
	}
	if !strings.Contains(stdout, "usage: nabu") {
		t.Error("help should print usage to stdout")
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := runCLI("teleport")
	if code != exitUsage {
		t.Errorf("exit: got %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr: %q", stderr)
	}
}

func TestCommandsRequiringAnIDRejectAnEmptyOne(t *testing.T) {
	for _, name := range []string{"attach", "stop", "resume"} {
		code, _, stderr := runCLI(name)
		if code != exitUsage {
			t.Errorf("%s exit: got %d, want %d", name, code, exitUsage)
		}
		if !strings.Contains(stderr, "session id is required") {
			t.Errorf("%s stderr: %q", name, stderr)
		}
	}
}

func TestRunRequiresAPrompt(t *testing.T) {
	code, _, stderr := runCLI("run", "--root", t.TempDir())
	if code != exitUsage {
		t.Errorf("exit: got %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "prompt is required") {
		t.Errorf("stderr: %q", stderr)
	}
}

// Exit codes mirror the final session state, which is what lets a caller tell
// what happened without parsing output (spec 13).
func TestExitCodeMirrorsSessionState(t *testing.T) {
	for _, tc := range []struct {
		state protocol.SessionState
		want  int
	}{
		{protocol.StateCompleted, exitOK},
		{protocol.StateBlocked, exitBlocked},
		{protocol.StatePaused, exitPaused},
		{protocol.StateError, exitError},
	} {
		if got := exitCode(tc.state); got != tc.want {
			t.Errorf("exitCode(%q) = %d, want %d", tc.state, got, tc.want)
		}
	}
}

func TestTerminalStates(t *testing.T) {
	for _, s := range []protocol.SessionState{
		protocol.StateCompleted, protocol.StateBlocked,
		protocol.StatePaused, protocol.StateError,
	} {
		if !terminal(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range []protocol.SessionState{protocol.StateIdle, protocol.StateRunning} {
		if terminal(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
}

// --json must emit one complete JSON object per line.
func TestEmitWritesOneJSONLinePerValue(t *testing.T) {
	var buf bytes.Buffer
	emit(&buf, map[string]any{"a": 1})
	emit(&buf, map[string]any{"b": 2})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines: got %d, want 2", len(lines))
	}
	for i, line := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestStatusListsSessionsAgainstALiveDaemon(t *testing.T) {
	root := startDaemon(t)
	code, stdout, stderr := runCLI("status", "--root", root)
	if code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "no sessions") {
		t.Errorf("stdout: %q", stdout)
	}
}

func TestStatusOfAnUnknownSessionErrors(t *testing.T) {
	root := startDaemon(t)
	code, _, stderr := runCLI("status", "--root", root, "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if code != exitError {
		t.Errorf("exit: got %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "nabu:") {
		t.Errorf("stderr should explain the failure, got %q", stderr)
	}
}

func TestStopOfAnUnknownSessionErrors(t *testing.T) {
	root := startDaemon(t)
	code, _, stderr := runCLI("stop", "--root", root, "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if code != exitError {
		t.Errorf("exit: got %d, want %d", code, exitError)
	}
	if stderr == "" {
		t.Error("a failure should be explained on stderr")
	}
}

func TestDaemonStopWithNoDaemonSaysSo(t *testing.T) {
	code, stdout, _ := runCLI("daemon", "stop", "--root", t.TempDir())
	if code != exitOK {
		t.Errorf("exit: got %d, want 0", code)
	}
	if !strings.Contains(stdout, "no daemon is running") {
		t.Errorf("stdout: %q", stdout)
	}
}

func TestDaemonStopShutsALiveDaemonDown(t *testing.T) {
	root := startDaemon(t)
	code, stdout, stderr := runCLI("daemon", "stop", "--root", root)
	if code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "daemon stopped") {
		t.Errorf("stdout: %q", stdout)
	}

	// The pid file goes with it, so nothing is discoverable afterwards.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := daemon.RunningAddr(root); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the daemon is still discoverable after daemon stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A session created with a turn budget records it, because spec 12 sets the
// budget at creation and a fresh session cannot be resumed into one.
func TestRunSetsTheTurnBudgetAtCreation(t *testing.T) {
	root := startDaemon(t)
	addr, ok := daemon.RunningAddr(root)
	if !ok {
		t.Fatal("daemon not discoverable")
	}
	ctx := context.Background()
	c, err := goclient.Dial(ctx, addr, "", "nabu-cli-test", version)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ws := t.TempDir()
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := callInto(ctx, c, "nabu.session.create", map[string]any{
		"workspace": ws,
		"budget":    map[string]any{"max_turns": 7, "source": "client"},
	}, &created); err != nil {
		t.Fatal(err)
	}

	var st protocol.State
	if err := callInto(ctx, c, "nabu.session.state",
		map[string]any{"session_id": created.SessionID}, &st); err != nil {
		t.Fatal(err)
	}
	if st.Budget.MaxTurns != 7 {
		t.Errorf("max_turns: got %d, want 7", st.Budget.MaxTurns)
	}
}

func TestResolveRootPrefersTheFlag(t *testing.T) {
	want := filepath.Join("a", "b")
	got, err := resolveRoot(want)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("root: got %q, want %q", got, want)
	}
}

func TestResolveRootFallsBackToTheEnvironment(t *testing.T) {
	want := filepath.Join(os.TempDir(), "nabu-root-test")
	t.Setenv("NABU_ROOT", want)
	got, err := resolveRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("root: got %q, want %q", got, want)
	}
}

// startDaemonWithModel runs a daemon whose only provider is scripted, so a
// whole turn runs without a model server.
func startDaemonWithModel(t *testing.T, replies ...string) string {
	t.Helper()
	root := t.TempDir()

	script := make([]provider.Response, 0, len(replies))
	for _, r := range replies {
		script = append(script, provider.Response{Content: r})
	}
	reg := provider.NewRegistry()
	cfg := provider.Config{Name: "fake", MaxInFlight: 1}
	reg.Add(cfg, &provider.Fake{Script: script}, true)

	d, err := daemon.New(daemon.Options{
		Root: root, Bind: "127.0.0.1:0", LogWriter: io.Discard,
		Providers: reg,
		Modules:   []module.Module{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	if _, err := daemon.WaitForDaemon(context.Background(), root, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	return root
}

// Regression: every successful run hung. The agent leaves a session idle and
// a headless run was waiting for a terminal state nothing would produce.
func TestRunExitsWhenTheTurnCompletes(t *testing.T) {
	root := startDaemonWithModel(t, "The deploy command is make ship.")
	ws := t.TempDir()

	done := make(chan int, 1)
	go func() {
		code, _, _ := runCLI("run", "--root", root, "--workspace", ws, "how is this deployed?")
		done <- code
	}()

	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit: got %d, want %d", code, exitOK)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("nabu run never exited after the turn completed")
	}
}

// The report is only emitted when a session actually ends, so it guards the
// other half of the same defect.
func TestRunLeavesTheSessionCompletedWithAReport(t *testing.T) {
	root := startDaemonWithModel(t, "done")
	ws := t.TempDir()

	done := make(chan string, 1)
	go func() {
		_, stdout, _ := runCLI("run", "--root", root, "--workspace", ws, "do the thing")
		done <- stdout
	}()
	var stdout string
	select {
	case stdout = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("nabu run never exited")
	}

	// Reaching the log is not enough; the caller has to see it.
	if !strings.Contains(stdout, "report") {
		t.Errorf("the run report was never rendered:\n%s", stdout)
	}
	// A report line with nothing on it verifies nothing.
	if !strings.Contains(stdout, "completed") || !strings.Contains(stdout, "tasks") {
		t.Errorf("the report line carries no outcome:\n%s", stdout)
	}

	addr, ok := daemon.RunningAddr(root)
	if !ok {
		t.Fatal("daemon not discoverable")
	}
	ctx := context.Background()
	c, err := goclient.Dial(ctx, addr, "", "nabu-cli-test", version)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	sessions, err := c.List(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("List: %v %+v", err, sessions)
	}
	id := sessions[0].SessionID

	st, err := c.State(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != protocol.StateCompleted {
		t.Errorf("state = %q, want completed", st.State)
	}

	events, _, err := c.EventsAfter(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	var reports int
	for _, e := range events {
		if e.Type == protocol.EventReport {
			reports++
		}
	}
	if reports != 1 {
		t.Errorf("got %d report events, want exactly one", reports)
	}

	// A follower stops at the terminal state change, so the report must precede it.
	reportAt, terminalAt := -1, -1
	for i, e := range events {
		switch e.Type {
		case protocol.EventReport:
			reportAt = i
		case protocol.EventStateChange:
			var sc protocol.StateChangeData
			if err := json.Unmarshal(e.Data, &sc); err == nil && sc.To == protocol.StateCompleted {
				terminalAt = i
			}
		}
	}
	if reportAt < 0 || terminalAt < 0 {
		t.Fatalf("missing events: report at %d, terminal at %d", reportAt, terminalAt)
	}
	if reportAt > terminalAt {
		t.Errorf("the report comes after the session ended, so no follower sees it")
	}
}

// stubTUI replaces the launcher so the default command can be driven without a
// terminal, and records what it was asked to open.
func stubTUI(t *testing.T) *tui.Options {
	t.Helper()
	var got tui.Options
	prev := launchTUI
	launchTUI = func(_ context.Context, o tui.Options) error {
		got = o
		return nil
	}
	t.Cleanup(func() { launchTUI = prev })
	return &got
}

// No arguments opens the interactive UI, which is the daily driver. Needing a
// second command to get a session first is the thing this replaces.
func TestNoArgsOpensTheTUIOnAFreshSession(t *testing.T) {
	root := startDaemon(t)
	opened := stubTUI(t)
	ws := t.TempDir()

	code, _, stderr := runCLI("--root", root, "--workspace", ws)
	if code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if opened.SessionID == "" {
		t.Fatal("the TUI was opened without a session")
	}
	if opened.Root != root {
		t.Errorf("root = %q, want %q", opened.Root, root)
	}

	// The session is real, and in the workspace that was asked for.
	addr, ok := daemon.RunningAddr(root)
	if !ok {
		t.Fatal("daemon not discoverable")
	}
	ctx := context.Background()
	c, err := goclient.Dial(ctx, addr, "", "nabu-cli-test", version)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sessions, err := c.List(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("List: %v %+v", err, sessions)
	}
	if sessions[0].SessionID != opened.SessionID {
		t.Errorf("opened %q, daemon has %q", opened.SessionID, sessions[0].SessionID)
	}
	if !strings.EqualFold(sessions[0].Workspace, ws) {
		t.Errorf("workspace = %q, want %q", sessions[0].Workspace, ws)
	}
}

// --session attaches instead of creating, for getting back to a run.
func TestSessionFlagAttachesInsteadOfCreating(t *testing.T) {
	root := startDaemon(t)
	opened := stubTUI(t)
	ws := t.TempDir()

	if code, _, stderr := runCLI("--root", root, "--workspace", ws); code != exitOK {
		t.Fatalf("first open: exit %d, %s", code, stderr)
	}
	first := opened.SessionID

	code, _, stderr := runCLI("--root", root, "--session", first)
	if code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if opened.SessionID != first {
		t.Errorf("opened %q, want the session asked for %q", opened.SessionID, first)
	}

	addr, _ := daemon.RunningAddr(root)
	c, err := goclient.Dial(context.Background(), addr, "", "nabu-cli-test", version)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sessions, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Errorf("attaching created a second session: %d exist", len(sessions))
	}
}

func TestHelpStillPrintsUsage(t *testing.T) {
	stubTUI(t)
	code, stdout, _ := runCLI("--help")
	if code != exitOK {
		t.Errorf("exit: got %d, want 0", code)
	}
	if !strings.Contains(stdout, "usage: nabu") {
		t.Error("--help should print usage, not open the TUI")
	}
}

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

	"github.com/corporealshift/nabu/daemon"
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

func TestNoArgsPrintsUsage(t *testing.T) {
	code, _, stderr := runCLI()
	if code != exitUsage {
		t.Errorf("exit: got %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "usage: nabu") {
		t.Errorf("stderr should carry usage, got %q", stderr)
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
	c, err := dial(ctx, addr, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

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

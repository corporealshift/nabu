package tools

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func requireShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
}

func TestBashRunsInWorkspaceAndReportsExit(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	s := fakeSession{dir}
	b := &Builtins{}
	out, err := run(t, b, s, "bash", `{"command":"echo hello && pwd"}`)
	if err != nil {
		t.Fatalf("err: %v out: %q", err, out)
	}
	if !strings.HasPrefix(out, "hello\n") {
		t.Fatalf("out: %q", out)
	}
	out, err = run(t, b, s, "bash", `{"command":"echo oops >&2; exit 3"}`)
	if err == nil || !strings.Contains(out, "oops") || !strings.Contains(out, "[exit status 3]") {
		t.Fatalf("non-zero exit: err=%v out=%q", err, out)
	}
}

func TestBashTimeout(t *testing.T) {
	requireShell(t)
	b := &Builtins{BashTimeout: 300 * time.Millisecond}
	start := time.Now()
	out, err := run(t, b, fakeSession{t.TempDir()}, "bash", `{"command":"sleep 5"}`)
	if err == nil || !strings.Contains(out, "timed out") {
		t.Fatalf("want timeout, got err=%v out=%q", err, out)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout not enforced promptly")
	}
}

func TestBashOutputTruncated(t *testing.T) {
	requireShell(t)
	b := &Builtins{MaxOutput: 200}
	out, _ := run(t, b, fakeSession{t.TempDir()}, "bash", `{"command":"seq 1 1000"}`)
	if len(out) > 300 || !strings.Contains(out, "truncated") {
		t.Fatalf("len=%d out=%q", len(out), out)
	}
}

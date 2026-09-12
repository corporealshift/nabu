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

func TestBashFailsClearlyWhenNoShellFound(t *testing.T) {
	t.Setenv("PATH", "")
	b := &Builtins{}
	out, err := run(t, b, fakeSession{t.TempDir()}, "bash", `{"command":"echo hi"}`)
	if err == nil {
		t.Fatalf("want an error when no shell is available, got out=%q", out)
	}
	if !strings.Contains(err.Error(), "shell") {
		t.Fatalf("error should name the shell config key, got: %v", err)
	}
}

func TestShellCommandFlagMatchesInterpreter(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shell string
		flag  string
	}{
		{"bare cmd", "cmd", "/C"},
		{"cmd with extension", "cmd.exe", "/C"},
		{"absolute path to cmd", `C:\Windows\System32\cmd.exe`, "/C"},
		{"uppercase path to cmd", `C:\Windows\System32\CMD.EXE`, "/C"},
		{"forward-slash path to cmd", "C:/Windows/System32/cmd.exe", "/C"},
		{"bare bash", "bash", "-c"},
		{"absolute path to bash", `C:\Program Files\Git\usr\bin\bash.exe`, "-c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Builtins{Shell: tc.shell}
			sh, flag, err := b.shellCommand()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sh != tc.shell {
				t.Fatalf("interpreter: got %q want %q", sh, tc.shell)
			}
			if flag != tc.flag {
				t.Fatalf("flag: got %q want %q", flag, tc.flag)
			}
		})
	}
}

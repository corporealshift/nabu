package bench

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// A timeout has to end the call, not just the process.
//
// pi leaves a detached child behind, and that child keeps the output pipes
// open. CombinedOutput waits on the pipes rather than on the process, so a
// timed-out run hung for as long as the orphan lived — which in practice meant
// the whole suite stopped, hours in, with no result and no message.
func TestATimedOutRunDoesNotHangTheSuite(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the orphan-holds-the-pipe case is being reproduced with a Windows shell")
	}
	if _, err := exec.LookPath("cmd"); err != nil {
		t.Skip("cmd is not available")
	}

	// start /b launches a child that outlives its parent and inherits its
	// handles, which is the shape of the problem.
	argv := []string{"cmd", "/c", "start", "/b", "powershell", "-NonInteractive",
		"-Command", "Start-Sleep -Seconds 45"}

	// Not t.TempDir: the orphan keeps the directory open after the run
	// returns, and the cleanup would fail on it. The real suite has the same
	// experience, and its workspaces are temporary anyway.
	dir, err := os.MkdirTemp("", "orphan-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	done := make(chan Attempt, 1)
	go func() {
		a, _ := run(context.Background(), dir, 2*time.Second, argv, nil)
		done <- a
	}()

	select {
	case a := <-done:
		if !a.TimedOut {
			t.Error("the run should have been recorded as timed out")
		}
	case <-time.After(40 * time.Second):
		t.Fatal("a timed-out run did not return: the suite would have hung here")
	}
}

// The ordinary path still works: a command that finishes is not a timeout.
func TestAQuickRunIsNotATimeout(t *testing.T) {
	exe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}

	a, err := run(context.Background(), t.TempDir(), 60*time.Second, []string{exe, "version"}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if a.TimedOut {
		t.Error("go version was recorded as a timeout")
	}
	if a.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", a.ExitCode)
	}
}

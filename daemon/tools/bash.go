package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
)

// shellCommand returns the interpreter and the flag that takes a command.
func (b *Builtins) shellCommand() (string, string) {
	if b.Shell != "" {
		if b.Shell == "cmd" || b.Shell == "cmd.exe" {
			return b.Shell, "/C"
		}
		return b.Shell, "-c"
	}
	for _, sh := range []string{"bash", "sh"} {
		if p, err := exec.LookPath(sh); err == nil {
			return p, "-c"
		}
	}
	if runtime.GOOS == "windows" {
		return "cmd", "/C"
	}
	return "sh", "-c"
}

func (b *Builtins) bashTool() module.Tool {
	type args struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	return module.Tool{
		Name: "bash",
		Description: "Run a shell command in the workspace. Returns combined stdout and stderr; " +
			"a non-zero exit is reported as [exit status N]. timeout_seconds defaults to the configured limit.",
		Schema: schema(`{"type":"object","required":["command"],"properties":{
			"command":{"type":"string"},"timeout_seconds":{"type":"integer"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			if a.Command == "" {
				return "", fmt.Errorf("command is required")
			}
			timeout := b.BashTimeout
			if timeout == 0 {
				timeout = 120 * time.Second
			}
			if a.TimeoutSeconds > 0 {
				timeout = time.Duration(a.TimeoutSeconds) * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			sh, flag := b.shellCommand()
			cmd := exec.CommandContext(cctx, sh, flag, a.Command)
			cmd.Dir = s.Workspace().Path
			// On a timeout the shell is killed, but a grandchild it spawned
			// (sleep, a server) inherits the output pipes and can hold them
			// open. WaitDelay bounds how long Wait blocks on that, so a
			// timeout costs a second rather than the grandchild's lifetime.
			// Killing the whole process tree needs a job object: P1b.
			cmd.WaitDelay = time.Second
			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Stderr = &buf
			runErr := cmd.Run()
			out := truncate(buf.String(), b.maxOutput())
			if cctx.Err() == context.DeadlineExceeded {
				out += fmt.Sprintf("\n[timed out after %s]", timeout)
				return out, fmt.Errorf("command timed out after %s", timeout)
			}
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				out += fmt.Sprintf("\n[exit status %d]", exitErr.ExitCode())
				return out, fmt.Errorf("exit status %d", exitErr.ExitCode())
			}
			if runErr != nil {
				return out, runErr
			}
			return out, nil
		},
	}
}

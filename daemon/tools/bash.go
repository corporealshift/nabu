package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// shellBase reduces a configured shell to a bare lowercase interpreter name, so
// a full path such as C:\Windows\System32\cmd.exe is still recognized as cmd.
func shellBase(shell string) string {
	s := strings.ToLower(shell)
	s = s[strings.LastIndexAny(s, `/\`)+1:]
	return strings.TrimSuffix(s, ".exe")
}

// shellCommand returns the interpreter and the flag that takes a command. It
// fails rather than falling back to another shell: the tool is named bash and
// the model writes bash syntax, so handing that to cmd.exe silently corrupts
// commands instead of refusing them.
func (b *Builtins) shellCommand() (string, string, error) {
	if b.Shell != "" {
		if shellBase(b.Shell) == "cmd" {
			return b.Shell, "/C", nil
		}
		return b.Shell, "-c", nil
	}
	for _, sh := range []string{"bash", "sh"} {
		if p, err := exec.LookPath(sh); err == nil {
			return p, "-c", nil
		}
	}
	return "", "", errors.New(`no shell found: install bash or sh, or set the "shell" config key`)
}

func (b *Builtins) bashTool() module.Tool {
	type args struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	return module.Tool{
		Name: "bash",
		// The two sentences after the first each answer a failure: a model
		// ran `cd breezeway` five times from inside breezeway, and a PR body
		// in double quotes lost every `backticked` name to the shell.
		Description: "Run a shell command in the workspace. Returns combined stdout and stderr; " +
			"a non-zero exit is reported as [exit status N]. timeout_seconds defaults to the configured limit. " +
			"Every call starts in the workspace root, and a cd does not carry over to the next call. " +
			"Inside double quotes the shell expands backticks and $, so pass long text such as a " +
			"commit message or PR body through a file or a quoted heredoc (<<'EOF').",
		Schema: schema(`{"type":"object","required":["command"],"properties":{
			"command":{"type":"string"},"timeout_seconds":{"type":"integer"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", module.FailWith(protocol.ToolErrorInvalidArgs, err)
			}
			if a.Command == "" {
				return "", module.Fail(protocol.ToolErrorInvalidArgs, "command is required")
			}
			timeout := b.BashTimeout
			if timeout == 0 {
				timeout = 120 * time.Second
			}
			if a.TimeoutSeconds > 0 {
				timeout = time.Duration(a.TimeoutSeconds) * time.Second
			}
			return b.runShell(ctx, s, a.Command, timeout)
		},
	}
}

// runShell runs one command in the workspace with a timeout. It returns the
// combined output, with a bracketed note for a timeout or a non-zero exit,
// and the classified error for either.
func (b *Builtins) runShell(ctx context.Context, s module.Session, command string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sh, flag, err := b.shellCommand()
	if err != nil {
		return "", module.FailWith(protocol.ToolErrorNotFound, err)
	}
	cmd := exec.CommandContext(cctx, sh, flag, command)
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
	// The bracketed note stays in the output: it is what a reader sees,
	// and the structured kind beside it is what the model branches on.
	if cctx.Err() == context.DeadlineExceeded {
		out += fmt.Sprintf("\n[timed out after %s]", timeout)
		return out, module.Fail(protocol.ToolErrorTimeout, "command timed out after %s", timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		out += fmt.Sprintf("\n[exit status %d]", exitErr.ExitCode())
		return out, module.Exited(exitErr.ExitCode(), "exit status %d", exitErr.ExitCode())
	}
	if runErr != nil {
		// The shell never ran: not found, not executable, no fork.
		return out, module.FailWith(protocol.ToolErrorIO, runErr)
	}
	return out, nil
}

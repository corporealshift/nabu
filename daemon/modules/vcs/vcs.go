// Package vcs gives the agent git and gh as tools that return structured
// results instead of text to parse.
//
// It is a module rather than a built-in because a curated set of subcommands is
// an opinion about workflow, and CLAUDE.md puts opinions in modules. Being a
// module also means a workspace that is not a repository, or a benchmark run
// that should not see these tools, can switch them off.
//
// Both tools wrap a program that is already on PATH. Neither handles
// credentials: gh uses the ambient login or it fails saying so.
package vcs

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

// Defaults chosen so one call costs a readable number of lines and cannot fill
// the context on its own.
const (
	defaultTimeout   = 60 * time.Second
	defaultMaxOutput = 32 << 10
	defaultLogLimit  = 20
	maxLogLimit      = 200
)

// Module provides the git and gh tools.
type Module struct {
	enabled   bool
	timeout   time.Duration
	maxOutput int
	// gitPath and ghPath are resolved once at Init. An empty path means the
	// program is not installed, and its tool is not offered at all: a tool the
	// model can see but never use is a tax on every request.
	gitPath string
	ghPath  string
}

func (m *Module) Name() string { return "vcs" }

// Init locates git and gh. Neither is required: a machine with git and no gh
// gets the git tool alone, and one with neither gets no tools and no error,
// because not having gh installed is a fact about the machine, not a
// misconfiguration.
func (m *Module) Init(_ module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	if m.timeout = time.Duration(cfg.Int("timeout_seconds", 0)) * time.Second; m.timeout <= 0 {
		m.timeout = defaultTimeout
	}
	if m.maxOutput = cfg.Int("max_output", 0); m.maxOutput <= 0 {
		m.maxOutput = defaultMaxOutput
	}
	m.gitPath = lookPath(cfg.String("git_path", ""), "git")
	m.ghPath = lookPath(cfg.String("gh_path", ""), "gh")
	return nil
}

// lookPath honours a configured path, else finds the program on PATH, else "".
func lookPath(configured, program string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	if p, err := exec.LookPath(program); err == nil {
		return p
	}
	return ""
}

// Tools implements module.ToolProvider.
func (m *Module) Tools() []module.Tool {
	if !m.enabled {
		return nil
	}
	var out []module.Tool
	if m.gitPath != "" {
		out = append(out, module.Tool{
			Name:        "git",
			Description: gitDescription,
			Schema:      gitSchema,
			Run:         m.runGit,
		})
	}
	if m.ghPath != "" {
		out = append(out, module.Tool{
			Name:        "gh",
			Description: ghDescription,
			Schema:      ghSchema,
			Run:         m.runGH,
		})
	}
	return out
}

// exec runs a program in the session's workspace and returns its stdout.
//
// Failures arrive classified, which is the whole point of the tool: a model
// that asked for a branch that does not exist should learn that from a kind,
// not by reading git's prose.
func (m *Module) exec(ctx context.Context, s module.Session, program string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, program, args...)
	if s != nil {
		cmd.Dir = s.Workspace().Path
	}
	// Separate pipes, unlike the bash tool: here stdout is data to be parsed
	// and stderr is the explanation of a failure. Merging them would put git's
	// warnings inside the JSON a caller is about to decode.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second

	runErr := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return "", module.Fail(protocol.ToolErrorTimeout, "%s timed out after %s", program, m.timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", module.Exited(exitErr.ExitCode(), "%s", firstLines(msg, 10))
	}
	if runErr != nil {
		return "", module.FailWith(protocol.ToolErrorIO, runErr)
	}
	return stdout.String(), nil
}

// reply marshals a structured result, truncating the rendered JSON rather than
// the value, so what comes back is always what the tool computed.
func (m *Module) reply(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", module.FailWith(protocol.ToolErrorIO, err)
	}
	if len(b) > m.maxOutput {
		return string(b[:m.maxOutput]) +
			fmt.Sprintf("\n[truncated at %d bytes; narrow the request]", m.maxOutput), nil
	}
	return string(b), nil
}

// firstLines keeps a failure message short enough to read.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) <= n {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.Join(lines[:n], "\n")) + "\n…"
}

// badArgs is the failure for a request that was never going to work.
func badArgs(format string, args ...any) error {
	return module.Fail(protocol.ToolErrorInvalidArgs, format, args...)
}

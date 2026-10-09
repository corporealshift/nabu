// Package claude lets the agent fetch a second opinion from Claude Code.
//
// The loop it replaces had the owner in the middle of every round trip: nabu
// opened a pull request, the owner asked Claude to review it, the owner carried
// the answer back.
//
// # Why the CLI and not the API
//
// An anthropic provider plus verify.judge_model would have made Claude the goal
// judge with no new code, since verify already runs a model as a stop gate. It
// was rejected twice over. There is no API key on this machine — the CLI
// authenticates from the owner's subscription, and the API route would mean
// paying per token beside a subscription already bought. And the judge path
// hands Claude a transcript, where the CLI gives it the repository: it reads the
// diff, the spec and the code. A reviewer that can look at things finds
// different bugs from one reasoning about a summary.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

const (
	// defaultTimeout is longer than the built-in tools use, because a real
	// review takes minutes. A limit that cut one off mid-answer would make the
	// tool useless while appearing to work.
	defaultTimeout = 300 * time.Second
	// maxTimeout bounds what a call may ask for. A model that mistypes a
	// timeout should not be able to park a turn for an hour.
	maxTimeout = 30 * time.Minute
	// defaultMaxOutput caps the reply. A review that long has stopped being a
	// review.
	defaultMaxOutput = 32 << 10
	// maxPrompt bounds what is handed over. Past this the prompt is the work,
	// not a question about it.
	maxPrompt = 64 << 10
)

// defaultAllowedTools keeps the reviewer read-only.
//
// A reviewer that can quietly edit the repository removes the one thing that
// makes a review worth having: that a second party looked and did not touch it.
//
// The cost was that Claude could not run the tests, which turned out higher
// than it looked: every diagnosis was a reading of whatever results were on
// disk. The owner can name commands it may run (commands.go); it still cannot
// change a file.
var defaultAllowedTools = []string{"Read", "Grep", "Glob"}

// Module offers the claude.ask tool, asks on the model's behalf when a
// session has gone on too long (stuck.go), and keeps Claude's answers through
// a summary (kept.go).
type Module struct {
	enabled   bool
	exe       string
	model     string
	timeout   time.Duration
	maxOutput int
	allowed   []string
	// commands are the command prefixes Claude may run, besides reading.
	commands []string

	host module.Host
	// autoAfter is how many turns since a person spoke earn an automatic
	// ask, and again at each multiple; 0 is never. autoMax caps the asks
	// in one stretch.
	autoAfter, autoMax int
	// ask runs the CLI. A field so tests can stand in for it without a
	// subprocess.
	ask func(ctx context.Context, dir, prompt string, timeout time.Duration) (string, error)

	mu sync.Mutex
	// waiting holds each session's automatic ask, from when it starts until
	// its answer is recorded or dropped.
	waiting map[string]*autoAsk
}

func (m *Module) Name() string { return "claude" }

// Init finds the CLI. Not having it installed is a fact about the machine
// rather than a misconfiguration, so Init does not fail over it; the tool is
// simply not offered.
func (m *Module) Init(h module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	m.host = h
	m.ask = m.runCLI
	m.waiting = map[string]*autoAsk{}
	m.autoAfter = max(cfg.Int("auto_ask_after", defaultAutoAfter), 0)
	m.autoMax = max(cfg.Int("auto_ask_max", defaultAutoMax), 0)

	m.exe = strings.TrimSpace(cfg.String("path", ""))
	if m.exe == "" {
		if p, err := exec.LookPath("claude"); err == nil {
			m.exe = p
		}
	}

	m.model = strings.TrimSpace(cfg.String("model", ""))
	if secs := cfg.Int("timeout_seconds", 0); secs > 0 {
		m.timeout = time.Duration(secs) * time.Second
	} else {
		m.timeout = defaultTimeout
	}
	if m.maxOutput = cfg.Int("max_output", 0); m.maxOutput <= 0 {
		m.maxOutput = defaultMaxOutput
	}
	m.allowed = cfg.Strings("allowed_tools", defaultAllowedTools)
	for _, c := range cfg.Strings("commands", nil) {
		if c, ok := runnable(c); ok {
			m.commands = append(m.commands, c)
		} else if h != nil && h.Log() != nil && strings.TrimSpace(c) != "" {
			h.Log().Warn("claude: a command that would let Claude run anything is not allowed", "command", c)
		}
	}
	return nil
}

// Tools implements module.ToolProvider.
func (m *Module) Tools() []module.Tool {
	if !m.enabled || m.exe == "" {
		return nil
	}
	can := "It cannot run commands: if a test matters, run it yourself and put the output in the prompt. "
	if len(m.commands) > 0 {
		can = "It can also run " + commandList(m.commands) + " to see for itself what fails. " +
			"It cannot change files. "
	}
	return []module.Tool{{
		Name: "claude.ask",
		Description: "Ask Claude — a larger model, with its own tools — to review your work or " +
			"answer something you are stuck on. It can read this repository, so point it at " +
			"files and say what to look for. " + can + "Use it for a review before you finish " +
			"something substantial, or for a question you have already tried and failed to " +
			"answer. Each call is slow and costs the owner real money, so ask once, with " +
			"everything needed to answer, rather than several times.",
		Schema: json.RawMessage(`{"type":"object","required":["prompt"],"properties":{
			"prompt":{"type":"string","description":"the question or review request, with the context needed to answer it"},
			"timeout_seconds":{"type":"integer","description":"how long to wait; defaults to the configured limit"}}}`),
		Run: m.runAsk,
	}}
}

func (m *Module) runAsk(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
	var a struct {
		Prompt         string `json:"prompt"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", module.Fail(protocol.ToolErrorInvalidArgs, "invalid arguments: %s", err)
		}
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return "", module.Fail(protocol.ToolErrorInvalidArgs, "prompt is required")
	}
	// An automatic ask already answered in the background is recorded by
	// calling this tool with its prompt; it returns at once (stuck.go).
	if s != nil {
		if answer, err, ok := m.answered(s.ID(), a.Prompt); ok {
			if err != nil {
				return "", err
			}
			return m.truncate(answer), nil
		}
	}
	if len(a.Prompt) > maxPrompt {
		return "", module.Fail(protocol.ToolErrorInvalidArgs,
			"the prompt is %d bytes, over the %d limit; point at the files instead of pasting them",
			len(a.Prompt), maxPrompt)
	}

	timeout := m.timeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	if timeout > maxTimeout {
		return "", module.Fail(protocol.ToolErrorInvalidArgs,
			"timeout_seconds %d is above the maximum of %d", a.TimeoutSeconds, int(maxTimeout.Seconds()))
	}

	dir := ""
	if s != nil {
		dir = s.Workspace().Path
	}
	answer, err := m.ask(ctx, dir, a.Prompt, timeout)
	if err != nil {
		return "", err
	}
	return m.truncate(answer), nil
}

// runCLI asks the CLI in dir and returns its answer, or an error that says
// how it failed.
func (m *Module) runCLI(ctx context.Context, dir, prompt string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, m.exe, m.argv(prompt)...)
	cmd.Dir = dir
	// Separate pipes: stdout is the answer, stderr explains a failure. Merged,
	// a warning would land in the middle of a review.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second

	runErr := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return "", module.Fail(protocol.ToolErrorTimeout,
			"claude did not answer within %s; ask something smaller, or raise timeout_seconds", timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", module.Exited(exitErr.ExitCode(), "claude: %s", firstLines(msg, 10))
	}
	if runErr != nil {
		return "", module.FailWith(protocol.ToolErrorIO, runErr)
	}

	answer := strings.TrimSpace(stdout.String())
	if answer == "" {
		return "", module.Fail(protocol.ToolErrorIO, "claude exited cleanly but said nothing")
	}
	return answer, nil
}

// argv builds the command line.
//
// The prompt is one argument, never interpolated into a shell string: it is
// model-written text that will contain quotes and newlines.
func (m *Module) argv(prompt string) []string {
	args := []string{"-p", prompt}
	for _, t := range m.allowed {
		if t = strings.TrimSpace(t); t != "" {
			args = append(args, "--allowedTools", t)
		}
	}
	// Both shells: on Windows the CLI runs a command through its PowerShell
	// tool as readily as through Bash, and a Bash rule alone left go test
	// blocked when checked against the real CLI.
	for _, c := range m.commands {
		args = append(args, "--allowedTools", "Bash("+c+" *)", "--allowedTools", "PowerShell("+c+" *)")
	}
	if m.model != "" {
		args = append(args, "--model", m.model)
	}
	return args
}

func (m *Module) truncate(s string) string {
	if len(s) <= m.maxOutput {
		return s
	}
	return s[:m.maxOutput] +
		fmt.Sprintf("\n[truncated at %d bytes; ask something narrower]", m.maxOutput)
}

// firstLines keeps a failure message short enough to read.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) <= n {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.Join(lines[:n], "\n")) + "\n…"
}

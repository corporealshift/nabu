package claude

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

type fakeSession struct{ dir string }

func (f fakeSession) ID() string                               { return "s" }
func (f fakeSession) Workspace() module.Workspace              { return module.Workspace{Path: f.dir, Key: "k"} }
func (f fakeSession) Events(*string) ([]protocol.Event, error) { return nil, nil }
func (f fakeSession) State() protocol.State                    { return protocol.State{} }
func (f fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

func newModule(t *testing.T, cfg module.Config) *Module {
	t.Helper()
	m := &Module{}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatal(err)
	}
	return m
}

func names(m *Module) []string {
	var out []string
	for _, tool := range m.Tools() {
		out = append(out, tool.Name)
	}
	return out
}

// A tool the model can see but never use is prompt paid on every request. Not
// having the CLI installed is a fact about the machine, not a misconfiguration,
// so Init must not fail over it either.
func TestTheToolIsOfferedOnlyWhenTheCLIExists(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		m := newModule(t, module.Config{"path": ""})
		m.exe = "" // whatever the machine has, pretend it does not
		if got := names(m); len(got) != 0 {
			t.Errorf("tools = %v, want none", got)
		}
	})

	t.Run("present", func(t *testing.T) {
		m := newModule(t, module.Config{"path": "/usr/bin/claude"})
		if got := names(m); len(got) != 1 || got[0] != "claude.ask" {
			t.Errorf("tools = %v, want [claude.ask]", got)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		m := newModule(t, module.Config{"path": "/usr/bin/claude", "enabled": false})
		if got := names(m); len(got) != 0 {
			t.Errorf("tools = %v, want none when disabled", got)
		}
	})
}

// The reviewer is read-only on purpose: one that can quietly edit the
// repository removes the only thing a review is for. Verified against the real
// CLI separately; this pins that the flags are actually passed.
func TestArgvKeepsTheReviewerReadOnly(t *testing.T) {
	m := newModule(t, module.Config{"path": "/usr/bin/claude"})
	argv := m.argv("look at the diff")

	joined := strings.Join(argv, " ")
	for _, tool := range []string{"Read", "Grep", "Glob"} {
		if !strings.Contains(joined, "--allowedTools "+tool) {
			t.Errorf("argv = %v, want %s allowed", argv, tool)
		}
	}
	for _, forbidden := range []string{"Write", "Edit", "Bash"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("argv = %v must not permit %s", argv, forbidden)
		}
	}
	if strings.Contains(joined, "dangerously") || strings.Contains(joined, "bypassPermissions") {
		t.Errorf("argv = %v must never bypass permissions", argv)
	}
}

// The prompt is model-written and will contain quotes and newlines. It goes as
// one argument, never interpolated into anything that parses it.
func TestThePromptIsASingleArgument(t *testing.T) {
	m := newModule(t, module.Config{"path": "/usr/bin/claude"})
	prompt := "review this\nit has \"quotes\" and 'these' and $HOME and `backticks`"

	argv := m.argv(prompt)

	if argv[0] != "-p" {
		t.Fatalf("argv[0] = %q, want -p", argv[0])
	}
	if argv[1] != prompt {
		t.Errorf("the prompt was altered:\n got %q\nwant %q", argv[1], prompt)
	}
}

func TestAllowedToolsCanBeWidenedByConfig(t *testing.T) {
	m := newModule(t, module.Config{
		"path":          "/usr/bin/claude",
		"allowed_tools": []any{"Read", "Bash"},
	})
	joined := strings.Join(m.argv("x"), " ")
	if !strings.Contains(joined, "--allowedTools Bash") {
		t.Errorf("argv = %q, want the configured tools", joined)
	}
}

func TestModelIsPassedWhenConfigured(t *testing.T) {
	plain := newModule(t, module.Config{"path": "/usr/bin/claude"})
	if strings.Contains(strings.Join(plain.argv("x"), " "), "--model") {
		t.Error("no model configured, so none should be passed")
	}

	named := newModule(t, module.Config{"path": "/usr/bin/claude", "model": "opus"})
	if !strings.Contains(strings.Join(named.argv("x"), " "), "--model opus") {
		t.Error("a configured model should be passed")
	}
}

func TestBadRequestsAreInvalidArgs(t *testing.T) {
	m := newModule(t, module.Config{"path": "/usr/bin/claude"})
	s := fakeSession{t.TempDir()}

	cases := []struct{ name, args string }{
		{"no prompt", `{}`},
		{"blank prompt", `{"prompt":"   "}`},
		{"a prompt past the limit", `{"prompt":"` + strings.Repeat("x", maxPrompt+1) + `"}`},
		{"a timeout past the maximum", `{"prompt":"hi","timeout_seconds":99999}`},
		{"arguments that are not an object", `"nope"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.runAsk(context.Background(), s, json.RawMessage(tc.args))
			if err == nil {
				t.Fatal("want an error")
			}
			if kind, _ := module.ClassifyToolError(err); kind != protocol.ToolErrorInvalidArgs {
				t.Errorf("kind = %q, want %q", kind, protocol.ToolErrorInvalidArgs)
			}
		})
	}
}

// A review that runs long has to come back as a timeout, not as a silent empty
// answer, or the model cannot tell "it said nothing" from "it never finished".
//
// The deadline is set past expiry rather than racing a slow subprocess: argv is
// always "-p <prompt>", so there is no portable program that can be made to
// sleep on command, and a test that waits on a real timeout is a test that
// costs seconds on every run.
func TestATimeoutIsReportedAsOne(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this machine")
	}
	m := newModule(t, module.Config{"path": sh})
	m.allowed = nil
	m.timeout = time.Nanosecond // already expired when Run is reached

	_, err = m.runAsk(context.Background(), fakeSession{t.TempDir()},
		json.RawMessage(`{"prompt":"anything"}`))
	if err == nil {
		t.Fatal("want a timeout")
	}
	if kind, _ := module.ClassifyToolError(err); kind != protocol.ToolErrorTimeout {
		t.Errorf("kind = %q, want %q", kind, protocol.ToolErrorTimeout)
	}
	if !strings.Contains(err.Error(), "timeout_seconds") {
		t.Errorf("the message should say how to ask for longer, got %q", err)
	}
}

// A non-zero exit carries its code, so the model can tell a refusal from a
// crash without reading prose.
func TestANonZeroExitCarriesItsCode(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this machine")
	}
	m := newModule(t, module.Config{"path": sh})
	m.allowed = nil

	// argv becomes: sh -p "<prompt>" — sh rejects it and exits non-zero.
	_, err = m.runAsk(context.Background(), fakeSession{t.TempDir()},
		json.RawMessage(`{"prompt":"--this-is-not-a-flag"}`))
	if err == nil {
		t.Fatal("want an error")
	}
	kind, code := module.ClassifyToolError(err)
	if kind != protocol.ToolErrorExit {
		t.Fatalf("kind = %q, want %q", kind, protocol.ToolErrorExit)
	}
	if code == nil || *code == 0 {
		t.Errorf("exit code = %v, want a non-zero one", code)
	}
}

func TestTruncationIsAnnounced(t *testing.T) {
	m := newModule(t, module.Config{"path": "/usr/bin/claude", "max_output": 32})
	out := m.truncate(strings.Repeat("a", 200))
	if !strings.Contains(out, "truncated") {
		t.Errorf("a cut reply must say so, got %q", out)
	}
	if len(out) <= 32 {
		t.Error("the note should be appended, not counted inside the budget")
	}
}

func TestSchemaIsValidJSON(t *testing.T) {
	m := newModule(t, module.Config{"path": "/usr/bin/claude"})
	for _, tool := range m.Tools() {
		if !json.Valid(tool.Schema) {
			t.Errorf("%s has an invalid schema: %s", tool.Name, tool.Schema)
		}
	}
}

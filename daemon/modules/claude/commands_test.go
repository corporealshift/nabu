package claude

import (
	"slices"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// bashRules is every shell rule the argv allows, in order.
func bashRules(argv []string) []string {
	var out []string
	for i, a := range argv {
		if a == "--allowedTools" && i+1 < len(argv) && (strings.HasPrefix(argv[i+1], "Bash") || strings.HasPrefix(argv[i+1], "PowerShell")) {
			out = append(out, argv[i+1])
		}
	}
	return out
}

func description(t *testing.T, m *Module) string {
	t.Helper()
	tools := m.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	return tools[0].Description
}

func TestWithNoCommandsClaudeRunsNothing(t *testing.T) {
	m := newModule(t, module.Config{"path": "/usr/bin/claude"})
	if rules := bashRules(m.argv("x")); len(rules) != 0 {
		t.Errorf("Bash rules = %q, want none", rules)
	}
	if d := description(t, m); !strings.Contains(d, "It cannot run commands") {
		t.Errorf("the description should say Claude cannot run anything:\n%s", d)
	}
}

// Claude ran nothing in two liftoff sessions, and said so in all four answers
// (docs/specs/2026-10-09-claude-runs-the-tests-design.md).
func TestConfiguredCommandsMayBeRun(t *testing.T) {
	m := newModule(t, module.Config{"path": "/usr/bin/claude", "commands": []any{"bash gradlew.sh", " go test "}})
	argv := m.argv("x")
	// Both shells: on Windows the CLI ran go test through its PowerShell
	// tool, and a Bash rule alone left it blocked.
	want := []string{"Bash(bash gradlew.sh *)", "PowerShell(bash gradlew.sh *)", "Bash(go test *)", "PowerShell(go test *)"}
	if rules := bashRules(argv); !slices.Equal(rules, want) {
		t.Errorf("shell rules = %q\nwant %q", rules, want)
	}
	joined := strings.Join(argv, " ")
	for _, tool := range []string{"--allowedTools Read", "--allowedTools Grep", "--allowedTools Glob"} {
		if !strings.Contains(joined, tool) {
			t.Errorf("argv = %q, want %s kept", joined, tool)
		}
	}
	for _, forbidden := range []string{"Write", "Edit"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("argv = %q must not permit %s", joined, forbidden)
		}
	}
	d := description(t, m)
	if strings.Contains(d, "It cannot run commands") || !strings.Contains(d, "`bash gradlew.sh` and `go test`") ||
		!strings.Contains(d, "It cannot change files") {
		t.Errorf("the description should name what Claude can run, and that it still cannot write:\n%s", d)
	}
}

func TestAPrefixThatWouldAllowAnythingIsDropped(t *testing.T) {
	m := newModule(t, module.Config{"path": "/usr/bin/claude", "commands": []any{
		"bash", "sh", "pwsh -Command", "bash -c", "cmd /c", "python", "python -c", "node -e", "C:/Program Files/Git/bin/bash.exe",
		"env go test", "sudo go test", "go test (all)", "go *", "  ",
		"go test", "python manage.py test", "./gradlew",
	}})
	var want []string
	for _, c := range []string{"go test", "python manage.py test", "./gradlew"} {
		want = append(want, "Bash("+c+" *)", "PowerShell("+c+" *)")
	}
	if rules := bashRules(m.argv("x")); !slices.Equal(rules, want) {
		t.Errorf("Bash rules = %q\nwant %q", rules, want)
	}
}

func TestTheAutomaticQuestionSaysWhatClaudeCanRun(t *testing.T) {
	log := []protocol.Event{user("fix the screen test"), said("trying")}

	if q := question(log, nil); strings.Contains(q, "You may run") {
		t.Errorf("with no commands the question offers none:\n%s", q)
	}
	q := question(log, []string{"bash gradlew.sh", "go test"})
	for _, want := range []string{"You may run `bash gradlew.sh` and `go test`", "keeps working in this checkout", "Read the repository."} {
		if !strings.Contains(q, want) {
			t.Errorf("the question is missing %q:\n%s", want, q)
		}
	}
}

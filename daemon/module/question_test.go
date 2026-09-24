package module

import (
	"encoding/json"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

// The first cases are messages from real sessions, and what each actually was.
func TestAsksOnly(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		// Questions. Both of these were answered by starting work (issue 76).
		{"what's the status here?", true},
		{"why did you stop here? I am curious if something is causing the stopping, that's three times I think and it looks like tool calls are somehow getting into thoughts and I wonder if that's causing the issue", true},
		{"does the build pass now?", true},
		{"ok so why is this test flaky?", true},
		{"I pushed a fix. is it green?", true},
		{"should we split this into two PRs?", true},
		{"Why did you stop here", false}, // no question mark: not confidently a question

		// Requests, however they are phrased.
		{"can you get some ci set up in m0? rust fmt check, clippy, tests, etc?", false},
		{"what's going on here? ask Claude to help if you're struggling to get ci to pass", false},
		{"what about the nabu system prompt? I'd like you to have a bit of your own personality", false},
		{"could you take a look at the failing test?", false},
		{"why is this failing? fix it", false},
		{"please rename the package", false},
		{"bleh. never commit to main, always create a new branch if you're started work on main", false},
		{"keep working here", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := AsksOnly(tc.msg); got != tc.want {
			t.Errorf("AsksOnly(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestChangesWorkspace(t *testing.T) {
	call := func(tool string, args any) protocol.ToolCallData {
		raw, _ := json.Marshal(args)
		return protocol.ToolCallData{Tool: tool, Arguments: raw}
	}
	cmd := func(c string) protocol.ToolCallData { return call("bash", map[string]string{"command": c}) }
	cases := []struct {
		name string
		call protocol.ToolCallData
		want bool
	}{
		{"edit", call("edit", map[string]string{"path": "a.go"}), true},
		{"write", call("write", map[string]string{"path": "a.go"}), true},
		{"git commit", call("git", map[string]string{"op": "commit"}), true},
		{"git log", call("git", map[string]string{"op": "log"}), false},
		{"read", call("read", map[string]string{"path": "a.go"}), false},
		{"task.update", call("task.update", map[string]any{}), false},

		// From the log: how the answer to "why did you stop" got committed.
		{"bash git add and commit", cmd(`cd /c/p && git add docs/plan.md && git commit -m "docs"`), true},
		{"bash redirect", cmd(`echo hi > notes.txt`), true},
		{"bash append", cmd(`echo hi >> notes.txt`), true},
		{"bash sed -i", cmd(`sed -i 's/a/b/' x.go`), true},
		{"bash rm", cmd(`rm -rf build`), true},

		// Moving a ref changes history.
		{"bash git branch -D", cmd(`git branch -D x`), true},
		{"bash git branch -f", cmd(`git branch -f x HEAD`), true},
		{"bash git branch -m", cmd(`git branch -m old new`), true},
		{"bash git update-ref", cmd(`git update-ref refs/heads/x HEAD`), true},
		{"bash git checkout -B", cmd(`git checkout -B x`), true},
		{"bash git switch -C", cmd(`git switch -C x`), true},
		{"bash git branch listing", cmd(`git branch`), false},
		{"bash git branch -a", cmd(`git branch -a`), false},
		{"bash git branch -v", cmd(`git branch -v | grep android`), false},

		// Looking is answering.
		{"bash tests", cmd(`cargo test --package breezeway-core 2>&1`), false},
		{"bash wc", cmd(`wc -l docs/plans/m2.md`), false},
		{"bash git status", cmd(`git status && git log --oneline -5`), false},
		{"bash to null", cmd(`go build ./... > /dev/null`), false},
		{"bash grep", cmd(`grep -rn "rm " daemon`), false},
	}
	for _, tc := range cases {
		if got := ChangesWorkspace(tc.call); got != tc.want {
			t.Errorf("%s: ChangesWorkspace = %v, want %v", tc.name, got, tc.want)
		}
	}
}

package vcs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// The command line is where every gh decision lives, and it can be checked
// without a network, a login, or a repository. Running real gh in a test would
// measure the reviewer's GitHub account rather than this code.
func TestGHArgv(t *testing.T) {
	cases := []struct {
		name string
		args ghArgs
		want []string
	}{
		{
			"listing defaults to open and a sane limit",
			ghArgs{Op: "pr.list"},
			[]string{"pr", "list", "--limit", "20", "--json"},
		},
		{
			"a state is passed through",
			ghArgs{Op: "issue.list", State: "closed", Limit: 5},
			[]string{"issue", "list", "--limit", "5", "--json"},
		},
		{
			"viewing takes the number",
			ghArgs{Op: "pr.view", Number: 54},
			[]string{"pr", "view", "54", "--json"},
		},
		{
			"runs are listed like anything else",
			ghArgs{Op: "run.list", Limit: 3},
			[]string{"run", "list", "--limit", "3", "--json"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ghArgv(tc.args, ghFields[tc.args.Op])
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for i, want := range tc.want {
				if i >= len(got) || got[i] != want {
					t.Fatalf("argv = %v, wanted %v as a prefix", got, tc.want)
				}
			}
			if !strings.Contains(strings.Join(got, " "), "--json") {
				t.Error("every call must ask for JSON; a page of prose is what this tool exists to avoid")
			}
		})
	}

	t.Run("a state is passed through verbatim", func(t *testing.T) {
		got, err := ghArgv(ghArgs{Op: "issue.list", State: "closed"}, ghFields["issue.list"])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(got, " "), "--state closed") {
			t.Errorf("argv = %v, want --state closed", got)
		}
	})
}

func TestBadGHRequestsAreInvalidArgs(t *testing.T) {
	cases := []struct {
		name string
		args ghArgs
	}{
		{"viewing without a number", ghArgs{Op: "pr.view"}},
		{"viewing number zero", ghArgs{Op: "issue.view", Number: 0}},
		{"a limit beyond the maximum", ghArgs{Op: "pr.list", Limit: 9999}},
		// gh run list has no --state: its states are status and conclusion.
		// Passing one through would fail inside gh with a worse message.
		{"a state on run.list", ghArgs{Op: "run.list", State: "open"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ghArgv(tc.args, ghFields[tc.args.Op])
			if err == nil {
				t.Fatal("want an error")
			}
			kind, _ := module.ClassifyToolError(err)
			if kind != protocol.ToolErrorInvalidArgs {
				t.Errorf("kind = %q, want %q", kind, protocol.ToolErrorInvalidArgs)
			}
		})
	}
}

func TestUnknownGHOpIsRejectedBeforeRunningAnything(t *testing.T) {
	m := &Module{maxOutput: defaultMaxOutput}
	// ghPath is empty: reaching exec would panic or run the wrong thing, so
	// this also proves the op is checked first.
	for _, args := range []string{`{}`, `{"op":"pr.merge"}`, `{"op":"repo.delete"}`} {
		_, err := m.runGH(context.Background(), nil, json.RawMessage(args))
		if err == nil {
			t.Fatalf("%s should be rejected", args)
		}
		kind, _ := module.ClassifyToolError(err)
		if kind != protocol.ToolErrorInvalidArgs {
			t.Errorf("%s: kind = %q, want %q", args, kind, protocol.ToolErrorInvalidArgs)
		}
	}
}

// Every op has to ask for fields, or gh prints a page for a person instead of
// data for a program.
func TestEveryGHOpNamesItsFields(t *testing.T) {
	for op, fields := range ghFields {
		if strings.TrimSpace(fields) == "" {
			t.Errorf("op %q asks for no fields", op)
		}
		if !strings.Contains(fields, "url") {
			t.Errorf("op %q returns no url, so nothing it names can be opened", op)
		}
	}
}

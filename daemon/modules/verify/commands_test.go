package verify

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// The workspace overlay is never applied, so one global command ran in every
// repository or none. A per-workspace gate lets breezeway build its Android app
// at every stop without nabu's own sessions doing it too.
func TestAWorkspaceCommandReplacesTheGlobalOne(t *testing.T) {
	android, other, off := t.TempDir(), t.TempDir(), t.TempDir()
	m := newVerify(t, module.Config{
		"command":            "exit 0",
		"require_clean_tree": false,
		"commands": map[string]any{
			// Written with the other separator and case, as a person would.
			strings.ToUpper(filepath.ToSlash(android)): "echo building the app; exit 4",
			off: "",
		},
	})

	v := m.BeforeStop(context.Background(), worked(t, android), module.StopInfo{})
	if v.Allow || !strings.Contains(v.Reason, "echo building the app") || !strings.Contains(v.Reason, "building the app") {
		t.Errorf("the workspace's own gate should run and veto, got allow=%v %q", v.Allow, v.Reason)
	}
	if v := m.BeforeStop(context.Background(), worked(t, other), module.StopInfo{}); !v.Allow {
		t.Errorf("another workspace should get the global gate, got %q", v.Reason)
	}
	s := worked(t, off)
	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("an empty workspace command turns the gate off, got %q", v.Reason)
	}
	if got := s.checkEvents(); len(got) != 0 {
		t.Errorf("a gate that is off should record nothing, got %+v", got)
	}
}

func TestWorkspaceCommandsWithoutAGlobalOne(t *testing.T) {
	ws := t.TempDir()
	m := newVerify(t, module.Config{"require_clean_tree": false,
		"commands": map[string]any{ws: "exit 2"}})
	if v := m.BeforeStop(context.Background(), worked(t, ws), module.StopInfo{}); v.Allow {
		t.Error("the workspace gate should run with no global command")
	}
	if v := m.BeforeStop(context.Background(), worked(t, t.TempDir()), module.StopInfo{}); !v.Allow {
		t.Errorf("no gate anywhere else, got %q", v.Reason)
	}
}

func TestWorkspaceCommandsConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		raw  any
		want string
	}{
		{"cargo test", "must be an object"},
		{map[string]any{"C:/x": 3.0}, "must be a string"},
	} {
		err := (&Module{}).Init(nil, module.Config{"commands": tc.raw})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("commands=%v: want %q, got %v", tc.raw, tc.want, err)
		}
	}
}

// The GitHub watcher works in a worktree beside the checkout. A gate keyed by
// the checkout's path has to follow it there, or an unattended session pushes
// work nothing built.
func TestARepositorysCommandCoversItsWorktrees(t *testing.T) {
	repo := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	cmd := exec.Command("git", "worktree", "add", "--detach", wt, "HEAD")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git worktree add: %v\n%s", err, out)
	}

	m := newVerify(t, module.Config{"command": "exit 0", "require_clean_tree": false,
		"commands": map[string]any{repo: "echo the repository gate; exit 4"}})
	if v := m.BeforeStop(context.Background(), worked(t, wt), module.StopInfo{}); v.Allow || !strings.Contains(v.Reason, "the repository gate") {
		t.Errorf("the worktree should get its repository's gate, got allow=%v %q", v.Allow, v.Reason)
	}

	own := newVerify(t, module.Config{"command": "exit 0", "require_clean_tree": false,
		"commands": map[string]any{repo: "exit 4", wt: "exit 0"}})
	if v := own.BeforeStop(context.Background(), worked(t, wt), module.StopInfo{}); !v.Allow {
		t.Errorf("an entry for the worktree itself should win, got %q", v.Reason)
	}

	if v := m.BeforeStop(context.Background(), worked(t, t.TempDir()), module.StopInfo{}); !v.Allow {
		t.Errorf("a directory outside any repository should get the global gate, got %q", v.Reason)
	}
}

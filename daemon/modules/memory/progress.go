package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// progressSuffix names the memory that says where a session stopped.
const progressSuffix = "-in-progress"

// recordProgress writes, or removes, the memory that lets a new session pick
// up where the last one stopped (spec 11.4).
//
// Removing it matters as much as writing it. A stale in-progress memory tells
// the next session to resume work that is already finished, which is worse
// than it knowing nothing.
func (m *Module) recordProgress(ctx context.Context, s module.Session) {
	if !m.enabled || !m.Curator || s == nil || m.host == nil || m.host.Tools() == nil {
		return
	}
	name := progressName(s)
	open := openTasks(s.State().Tasks)

	if len(open) == 0 {
		m.forgetProgress(ctx, s, name)
		return
	}

	args, err := json.Marshal(proposal{
		Scope:       string(ScopeWorkspace),
		Name:        name,
		Type:        "project",
		Description: "where the last session stopped, and what is still open",
		Body:        progressBody(open),
	})
	if err != nil {
		return
	}
	if _, err := m.host.Tools().Call(ctx, s, "memory.save", args); err != nil {
		m.log.Warn("memory: cannot record progress", "error", err)
	}
}

// forgetProgress removes the memory, but only if it is there: forgetting one
// that does not exist is an error the log does not need.
func (m *Module) forgetProgress(ctx context.Context, s module.Session, name string) {
	if _, err := os.Stat(m.WorkspaceStore(s).Path(name)); err != nil {
		return // nothing to clear
	}
	args, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return
	}
	if _, err := m.host.Tools().Call(ctx, s, "memory.forget", args); err != nil {
		m.log.Warn("memory: cannot clear progress", "error", err)
	}
}

// progressName is the workspace key plus a fixed suffix, so each repository
// has exactly one and they never collide.
func progressName(s module.Session) string {
	key := s.Workspace().Key
	if key == "" {
		key = "workspace"
	}
	return Slug(key + progressSuffix)
}

// openTasks is the work that is not finished. A done task is not outstanding,
// and a cancelled one is not either.
func openTasks(tasks []protocol.Task) []protocol.Task {
	var out []protocol.Task
	for _, t := range tasks {
		switch t.Status {
		case protocol.TaskPending, protocol.TaskInProgress, protocol.TaskBlocked, protocol.TaskFailed:
			out = append(out, t)
		}
	}
	return out
}

// progressBody lists what is outstanding. A blocked task's note is the reason
// it stopped, which is the thing the next session most needs to read.
func progressBody(open []protocol.Task) string {
	var b strings.Builder
	b.WriteString("Work was left unfinished on this project.\n\n")
	for _, t := range open {
		fmt.Fprintf(&b, "- %s (%s)", strings.TrimSpace(t.Title), t.Status)
		if note := strings.TrimSpace(t.Note); note != "" {
			b.WriteString(" — " + note)
		}
		if dw := strings.TrimSpace(t.DoneWhen); dw != "" {
			b.WriteString("; done when " + dw)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n**Why:** a new session on this repository would otherwise start over.\n")
	b.WriteString("**How to apply:** pick these up before starting anything new, " +
		"and confirm with the owner if the work looks stale.\n")
	return b.String()
}

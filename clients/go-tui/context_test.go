package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

func sessionEvent(window int) protocol.Event {
	return event("e0", protocol.EventSession, protocol.SessionData{
		Workspace: "C:/w", WorkspaceKey: "w-1", ContextWindow: window,
	})
}

func assistantEvent(id string, input int) protocol.Event {
	return event(id, protocol.EventMessage, protocol.MessageData{
		Role: "assistant", Content: "done",
		Usage: &protocol.Usage{InputTokens: input, OutputTokens: 10},
	})
}

// Issue 37: how full the context is, before compaction rather than after.
func TestContextBadgeReportsFullness(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(sessionEvent(1000))
	m.appendEvent(assistantEvent("e1", 400))

	if got := m.contextBadge(); !strings.Contains(got, "40%") {
		t.Errorf("badge = %q, want it to mention 40%%", got)
	}
}

// Each request carries the whole conversation again, so the fullness is the
// last request's input, not the sum of every request's.
func TestContextUsesTheLastRequestNotTheSum(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(sessionEvent(1000))
	m.appendEvent(assistantEvent("e1", 300))
	m.appendEvent(assistantEvent("e2", 500))

	if got := m.contextBadge(); !strings.Contains(got, "50%") {
		t.Errorf("badge = %q, want 50%% from the last message alone", got)
	}
}

// A percentage of an unknown number would be an invention.
func TestNoWindowNoBadge(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(sessionEvent(0))
	m.appendEvent(assistantEvent("e1", 400))

	if got := m.contextBadge(); got != "" {
		t.Errorf("badge = %q, want nothing when the window is unknown", got)
	}
}

func TestNoUsageNoBadge(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(sessionEvent(1000))

	if got := m.contextBadge(); got != "" {
		t.Errorf("badge = %q, want nothing before the first turn", got)
	}
}

// Switching sessions must not carry the old one's numbers across.
func TestResetClearsTheContext(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(sessionEvent(1000))
	m.appendEvent(assistantEvent("e1", 900))
	m.reset("S2")

	if got := m.contextBadge(); got != "" {
		t.Errorf("badge = %q, want nothing after switching sessions", got)
	}
}

// The two kinds of compaction lose different things, and the transcript should
// say which happened.
func TestCompactionSaysWhichKind(t *testing.T) {
	tests := []struct {
		mode protocol.CompactionMode
		want string
	}{
		{protocol.CompactionSummarize, "summarised"},
		{protocol.CompactionClearResults, "tool output cleared"},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			lines := renderEvent(event("e1", protocol.EventCompaction,
				protocol.CompactionData{Mode: tt.mode, Summary: "..."}))

			if len(lines) != 1 || !strings.Contains(lines[0], tt.want) {
				t.Errorf("rendered %q, want it to mention %q", lines, tt.want)
			}
		})
	}
}

func TestMessageTimestampAndIdleAge(t *testing.T) {
	ev := event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "user", Content: "hello"})
	ev.Timestamp = time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	if got := renderEvent(ev); len(got) != 1 || !strings.Contains(got[0], "Sep 16 00:00") {
		t.Errorf("rendered message = %q, want timestamp", got)
	}
	if got := idleAge(time.Unix(0, 0), time.Unix(5*60, 0)); got != "5 minutes ago" {
		t.Errorf("idle age = %q, want 5 minutes ago", got)
	}
	if got := idleAge(time.Unix(0, 0), time.Unix(4*60, 0)); got != "" {
		t.Errorf("idle age before threshold = %q, want empty", got)
	}
}

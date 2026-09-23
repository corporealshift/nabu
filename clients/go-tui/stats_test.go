package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/daemon/stats"
)

func TestSparklineKeepsPeaks(t *testing.T) {
	if got := sparkline([]int{0, 1, 2, 3, 4, 5, 6, 7}, 8); got != "▁▂▃▄▅▆▇█" {
		t.Errorf("one cell per value: %q", got)
	}
	// Squeezed into fewer cells, a spike must survive rather than be averaged away.
	if got := sparkline([]int{1, 1, 1, 100, 1, 1, 1, 1}, 4); got != "▁█▁▁" {
		t.Errorf("the spike was lost: %q", got)
	}
	if sparkline(nil, 10) != "" {
		t.Error("no values, no line")
	}
}

func TestCompact(t *testing.T) {
	for n, want := range map[int]string{999: "999", 1284: "1.3K", 12900: "13K", 77085054: "77.1M"} {
		if got := compact(n); got != want {
			t.Errorf("compact(%d) = %q, want %q", n, got, want)
		}
	}
}

// Issue 38: /stats shows how much work the session was, and esc goes back.
func TestStatsPanel(t *testing.T) {
	actions := make(chan action, 2)
	m := sized(t, actions)
	m, _ = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = typeIn(m, "/stats")
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyEnter})
	cmd()
	if a := <-actions; a.kind != actStats || a.sessionID == "" {
		t.Fatalf("want a stats request for this session, got %+v", a)
	}

	m, _ = send(m, statsMsg{view: statsView{
		session: stats.Session{SessionID: "01M30HBKN9CZTKTW6QBXMY4657", Turns: 584, Prompts: 10,
			Tokens:  stats.Tokens{Input: 77085054, Cached: 75960852, Output: 262725, PeakContext: 190741, ContextWindow: 256000},
			PerTurn: []stats.Turn{{Input: 4162}, {Input: 16342}, {Input: 190741}, {Input: 40000}},
			Tools:   []stats.Tool{{Name: "bash", Calls: 189, Errors: 42}, {Name: "read", Calls: 116, Errors: 1}},
			Rereads: []stats.Reread{{Path: "crates/breezeway-core/src/domain.rs", Reads: 42}}},
		days: []stats.Day{{Input: 10}, {Input: 0}, {Input: 1_200_000, Output: 3000}},
	}})
	view := stripANSI(m.View())
	t.Log("\n" + view)
	for _, want := range []string{"584 turns", "77.1M in", "peak context 191K of 256K (74%)", "42 failed", "domain.rs ×42", "today 1.2M"} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel never says %q", want)
		}
	}

	m, _ = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.stats != nil {
		t.Error("esc should close the panel")
	}
}

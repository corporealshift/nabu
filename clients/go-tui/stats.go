package tui

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/corporealshift/nabu/daemon/stats"
)

// statsView is what /stats shows: one session's numbers and the days around it
// (issue 38).
type statsView struct {
	session stats.Session
	days    []stats.Day
}

// statsMsg carries the numbers in from the connection goroutine.
type statsMsg struct{ view statsView }

// sparks are eight heights, lowest first. A spark line is a trend at a glance,
// not a reading: the numbers beside it are the reading.
var sparks = []rune("▁▂▃▄▅▆▇█")

// sparkline draws values in at most width cells. Where there are more values
// than cells, each cell shows the largest in its span, so a peak is never
// averaged away.
func sparkline(values []int, width int) string {
	if len(values) == 0 || width < 1 {
		return ""
	}
	cells := values
	if len(values) > width {
		cells = make([]int, width)
		for i := range cells {
			lo, hi := i*len(values)/width, (i+1)*len(values)/width
			for _, v := range values[lo:max(hi, lo+1)] {
				cells[i] = max(cells[i], v)
			}
		}
	}
	top := 0
	for _, v := range cells {
		top = max(top, v)
	}
	var b strings.Builder
	for _, v := range cells {
		if top == 0 {
			b.WriteRune(sparks[0])
			continue
		}
		b.WriteRune(sparks[v*(len(sparks)-1)/top])
	}
	return b.String()
}

// compact writes a count the way a person reads one: 1,284 → 1.3K, 77085054 → 77.1M.
func compact(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.0fK", float64(n)/1_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprint(n)
}

// statsPanel draws the numbers. It takes the screen like the picker does, and
// esc goes back.
func (m model) statsPanel() string {
	v := m.stats
	s := v.session
	width := min(m.width-8, 90)
	label := lipgloss.NewStyle().Bold(true)

	var b strings.Builder
	b.WriteString(label.Render("Session stats") + "  " + dim.Render(shortID(s.SessionID)) + "\n\n")

	fmt.Fprintf(&b, "%d turns · %d prompts · working %s · %d vetoes · %d interrupted\n",
		s.Turns, s.Prompts, (time.Duration(s.WorkingSeconds) * time.Second).String(), s.Vetoes, s.Interruptions)
	fmt.Fprintf(&b, "compactions: %d summarised, %d tool-output clears\n\n",
		s.Compactions.Summarize, s.Compactions.ClearResults)

	t := s.Tokens
	line := fmt.Sprintf("tokens: %s in", compact(t.Input))
	if t.Cached > 0 {
		line += fmt.Sprintf(" (%s cached)", compact(t.Cached))
	}
	line += fmt.Sprintf(" · %s out · peak context %s", compact(t.Output), compact(t.PeakContext))
	if t.ContextWindow > 0 {
		line += fmt.Sprintf(" of %s (%d%%)", compact(t.ContextWindow), t.PeakContext*100/t.ContextWindow)
	}
	b.WriteString(line + "\n")
	b.WriteString(dim.Render("input is every request summed: each one carries the conversation again") + "\n\n")

	if len(s.PerTurn) > 0 {
		inputs := make([]int, len(s.PerTurn))
		for i, turn := range s.PerTurn {
			inputs[i] = turn.Input
		}
		b.WriteString("context per turn\n")
		b.WriteString(toolStyle.Render(sparkline(inputs, width)) + "\n\n")
	}

	if len(s.Tools) > 0 {
		b.WriteString("tools\n")
		top := s.Tools[0].Calls
		nameWidth := 0
		for _, tool := range s.Tools[:min(len(s.Tools), 8)] {
			nameWidth = max(nameWidth, len(tool.Name))
		}
		for _, tool := range s.Tools[:min(len(s.Tools), 8)] {
			bar := strings.Repeat("█", max(1, tool.Calls*24/max(top, 1)))
			row := fmt.Sprintf("  %-*s %5d ", nameWidth, tool.Name, tool.Calls) + toolStyle.Render(bar)
			if tool.Errors > 0 {
				row += dim.Render(fmt.Sprintf("  %d failed", tool.Errors))
			}
			b.WriteString(row + "\n")
		}
		b.WriteString("\n")
	}

	if len(s.Rereads) > 0 {
		var parts []string
		for _, r := range s.Rereads[:min(len(s.Rereads), 4)] {
			parts = append(parts, fmt.Sprintf("%s ×%d", path.Base(r.Path), r.Reads))
		}
		b.WriteString("read again and again: " + strings.Join(parts, ", ") + "\n\n")
	}

	if len(v.days) > 0 {
		per := make([]int, len(v.days))
		for i, d := range v.days {
			per[i] = d.Input + d.Output
		}
		today := v.days[len(v.days)-1]
		fmt.Fprintf(&b, "tokens per day, last %d days: %s  today %s\n",
			len(v.days), toolStyle.Render(sparkline(per, len(per))), compact(today.Input+today.Output))
	}

	b.WriteString("\n" + dim.Render("esc close"))
	box := overlayBox.Render(b.String())
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

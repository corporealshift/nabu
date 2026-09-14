package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/corporealshift/nabu/protocol"
)

var (
	statusBar = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	badgeOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	badgeWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	badgeErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)

	overlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("3")).
			Padding(0, 2)
)

func (m model) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "connecting…"
	}
	if m.pending != nil {
		return m.overlay()
	}
	return m.viewport.View() + "\n" + m.status() + "\n" + m.help()
}

// status is the one line that says what is happening: connection, session
// state, and how far in.
func (m model) status() string {
	var parts []string

	switch m.conn {
	case connected:
		parts = append(parts, badgeOK.Render("●")+" connected")
	case connecting:
		parts = append(parts, badgeWarn.Render("◐")+" connecting")
	case disconnected:
		// Never render a stale view as if it were live. A disconnected
		// transcript is short, and saying so is the whole point.
		note := "disconnected — reconnecting"
		if m.connNote != "" {
			note = m.connNote
		}
		parts = append(parts, badgeErr.Render("○")+" "+note)
	}

	parts = append(parts, stateBadge(m.state))
	if n := len(m.queued); n > 0 {
		parts = append(parts, badgeWarn.Render(fmt.Sprintf("%d prompts queued", n)))
	}
	if m.sessionID != "" {
		parts = append(parts, dim.Render(shortID(m.sessionID)))
	}
	return statusBar.Render(strings.Join(parts, "  "))
}

func stateBadge(s protocol.SessionState) string {
	switch s {
	case protocol.StateRunning:
		return badgeOK.Render("running")
	case protocol.StateCompleted:
		return badgeOK.Render("completed")
	case protocol.StateBlocked, protocol.StateError:
		return badgeErr.Render(string(s))
	case protocol.StatePaused:
		return badgeWarn.Render("paused")
	default:
		return dim.Render(string(s))
	}
}

func (m model) help() string {
	if terminal(m.state) {
		return dim.Render("q quit · g/G top/bottom · ↑↓ scroll — the session has ended")
	}
	return dim.Render("q quit (the run continues) · g/G top/bottom · ↑↓ scroll")
}

// overlay is the permission prompt. It takes the whole screen deliberately:
// approving something you did not read is the failure this exists to prevent.
func (m model) overlay() string {
	p := m.pending
	risk := badgeOK.Render("low")
	switch p.req.Risk {
	case "high":
		risk = badgeErr.Render("HIGH")
	case "medium":
		risk = badgeWarn.Render("medium")
	}

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Permission needed") + "\n\n")
	fmt.Fprintf(&b, "%s  %s\n\n", risk, toolStyle.Render(p.req.Tool))
	b.WriteString(wrap(p.req.Summary, 68) + "\n\n")
	b.WriteString(badgeOK.Render("y") + " approve    " + badgeErr.Render("n") + " deny")
	if n := len(m.queued); n > 0 {
		b.WriteString(dim.Render(fmt.Sprintf("\n\n%d more waiting", n)))
	}

	box := overlayBox.Render(b.String())
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

// wrap breaks text at width without splitting words, so a long command stays
// readable in the box.
func wrap(s string, width int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		line += " " + w
	}
	return strings.Join(append(lines, line), "\n")
}

// shortID is the tail of a ULID, which is enough to tell sessions apart.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return "…" + id[len(id)-8:]
}

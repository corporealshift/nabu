package tui

import (
	"fmt"
	"strings"
	"time"

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
	if m.pending != nil {
		return m.overlay()
	}
	if m.picking {
		return m.picker()
	}

	main := m.viewport.View()
	if m.showTasks() {
		main = lipgloss.JoinHorizontal(lipgloss.Top, main, m.taskPane())
	}
	composer := strings.Join(m.composerLines(), "\n")
	if m.asking != nil {
		composer = m.askPanel()
	}
	return main + "\n" + composer + "\n" + m.status() + "\n" + m.help()
}

// spinnerFrames is a braille cycle: it reads as motion in any terminal font.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// workingIndicator is what says the agent is alive while a slow model thinks.
// Without it a screen that has stopped changing is indistinguishable from a
// hang, and a local turn can take minutes before its first token.
func (m model) workingIndicator() string {
	frame := spinnerFrames[m.spinner%len(spinnerFrames)]
	switch {
	case m.working():
		return badgeWarn.Render(frame + " working " + m.elapsed().String())
	case m.compacting():
		took := time.Since(m.compactingSince).Truncate(time.Second)
		return badgeWarn.Render(frame + " summarising " + took.String())
	}
	return ""
}

// taskPane shows what the agent believes it is doing.
func (m model) taskPane() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Tasks") + "\n\n")
	for _, t := range m.tasks {
		b.WriteString(taskLine(t) + "\n")
	}
	return lipgloss.NewStyle().
		Width(taskPaneWidth).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("8")).
		PaddingLeft(1).
		Height(m.viewport.Height).
		Render(b.String())
}

// taskLine marks a task by status. The marks differ in shape, not only colour,
// so the pane is readable without it.
func taskLine(t protocol.Task) string {
	mark, style := " ", dim
	switch t.Status {
	case protocol.TaskInProgress:
		mark, style = "▸", toolStyle
	case protocol.TaskDone:
		mark, style = "✓", okStyle
	case protocol.TaskBlocked:
		mark, style = "!", warnStyle
	case protocol.TaskFailed:
		mark, style = "✗", errStyle
	}
	return style.Render(mark + " " + truncate(t.Title, taskPaneWidth-4))
}

// picker lists the sessions to switch between.
func (m model) picker() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Sessions") + "\n\n")
	if len(m.sessions) == 0 {
		b.WriteString(dim.Render("no sessions yet — start one with `nabu run`"))
	}
	for i, s := range m.sessions {
		line := shortID(s.SessionID) + "  " + s.State + "  " + truncate(s.Workspace, 48)
		if i == m.cursorAt {
			b.WriteString(badgeOK.Render("› " + line))
		} else {
			b.WriteString(dim.Render("  " + line))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + dim.Render("↑↓ move · enter attach · esc cancel"))

	box := overlayBox.Render(b.String())
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
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

	if ind := m.workingIndicator(); ind != "" {
		parts = append(parts, ind)
	} else {
		label := stateBadge(m.state)
		if m.state == protocol.StateIdle {
			if age := idleAge(m.lastEventAt, time.Now()); age != "" {
				label += " " + dim.Render(age)
			}
		}
		parts = append(parts, label)
	}
	if g := m.goalBadge(); g != "" {
		parts = append(parts, g)
	}

	if c := m.contextBadge(); c != "" {
		parts = append(parts, c)
	}
	if n := len(m.queued); n > 0 {
		parts = append(parts, badgeWarn.Render(fmt.Sprintf("%d prompts queued", n)))
	}
	if m.sessionID != "" {
		parts = append(parts, dim.Render(shortID(m.sessionID)))
	}
	return statusBar.Render(strings.Join(parts, "  "))
}

const idleThreshold = 5 * time.Minute

func idleAge(at, now time.Time) string {
	if at.IsZero() || now.Before(at) {
		return ""
	}
	age := now.Sub(at)
	if age < idleThreshold {
		return ""
	}
	if age >= time.Hour {
		hours := int(age / time.Hour)
		return fmt.Sprintf("%d hour%s ago", hours, plural(hours))
	}
	minutes := int(age / time.Minute)
	return fmt.Sprintf("%d minute%s ago", minutes, plural(minutes))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// contextBadge says how full the context is, and warns before compaction
// rather than after it. Nothing is shown when the window was never configured:
// a percentage of an unknown number would be an invention.
func (m model) contextBadge() string {
	if m.contextWindow <= 0 || m.lastInput <= 0 {
		return ""
	}
	used := float64(m.lastInput) / float64(m.contextWindow)
	label := fmt.Sprintf("context %.0f%%", used*100)

	switch {
	case used >= 0.85:
		return badgeErr.Render(label)
	case used >= 0.6:
		return badgeWarn.Render(label)
	default:
		return dim.Render(label)
	}
}

// goalBadge shows the run goal. An unmet goal is the interesting one: it means
// the stop gate refused to let the run finish.
func (m model) goalBadge() string {
	if m.goal == nil {
		return ""
	}
	label := "goal " + m.goal.State
	switch m.goal.State {
	case "unmet":
		return badgeErr.Render(label)
	case "met":
		return badgeOK.Render(label)
	default:
		return dim.Render(label)
	}
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
	if m.composing {
		return dim.Render("enter send · esc cancel · /help for commands")
	}
	if terminal(m.state) {
		return dim.Render("q quit · i type · s sessions · t thinking · g/G top/bottom — the session has ended")
	}
	return dim.Render("q quit (the run continues) · i type · s sessions · t thinking · ctrl+x interrupt")
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

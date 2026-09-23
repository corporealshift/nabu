package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

var (
	statusBar = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	badgeOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	badgeWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	badgeErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)

	// overlayBox frames what takes the whole screen: the picker, a permission
	// prompt, /stats and the keys. There is room, so it breathes.
	overlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("3")).
			Padding(1, 3)
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
	if m.stats != nil {
		return m.statsPanel()
	}
	if m.showKeys {
		return m.keysPanel()
	}

	main := m.viewport.View()
	if m.showTasks() {
		main = lipgloss.JoinHorizontal(lipgloss.Top, main, m.taskPane())
	}
	composer := strings.Join(m.composerLines(), "\n")
	if m.asking != nil {
		composer = m.askPanel()
	}
	return main + "\n\n" + composer + "\n" + m.status()
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
	// Width is inside the border and the margin, so both come out of it: the
	// pane used to draw a column wider than the room it was given. The margin
	// keeps the transcript's last column off the border.
	return lipgloss.NewStyle().
		Width(taskPaneWidth - 2).
		MarginLeft(1).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("8")).
		PaddingLeft(taskPanePad).
		Height(m.viewport.Height).
		Render(b.String())
}

// doneTasks counts the tasks that are finished, however they finished.
func doneTasks(tasks []protocol.Task) int {
	n := 0
	for _, t := range tasks {
		if t.Status == protocol.TaskDone || t.Status == protocol.TaskCancelled {
			n++
		}
	}
	return n
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
	return style.Render(mark + " " + truncate(t.Title, taskTitleRoom))
}

// picker lists the sessions to switch between.
func (m model) picker() string {
	var b strings.Builder
	title, empty, help := "Sessions", "no sessions yet — start one with `nabu run`",
		"↑↓ move · enter attach · a archive · tab archived · esc cancel"
	if m.pickingArchived {
		title, empty, help = "Archived sessions", "nothing archived",
			"↑↓ move · enter restore and attach · tab back · esc cancel"
	}
	from, to := m.pickerWindow()
	if to-from < len(m.sessions) {
		title += fmt.Sprintf(" (%d–%d of %d)", from+1, to, len(m.sessions))
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(title) + "\n\n")
	if len(m.sessions) == 0 {
		b.WriteString(dim.Render(empty))
	}
	width := m.pickerWidth()
	now := time.Now()
	for i := from; i < to; i++ {
		if i > from {
			b.WriteString("\n")
		}
		b.WriteString(pickerRow(m.sessions[i], i == m.cursorAt, width, now))
	}
	b.WriteString("\n" + dim.Render(help))

	box := overlayBox.Render(b.String())
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

// pickerRow is one session as the phone's list shows it (issue 101): what was
// last asked, since several sessions share a workspace and an id tells nobody
// anything, then the project, state and how long it has sat idle.
func pickerRow(s goclient.SessionSummary, selected bool, width int, now time.Time) string {
	lead := "  "
	if selected {
		lead = "› "
	}
	prompt := strings.Join(strings.Fields(s.LastPrompt), " ")
	var first string
	switch {
	case prompt == "":
		first = dim.Render(lead + "nothing asked yet")
	case selected:
		first = badgeOK.Render(lead + truncate(prompt, width-3))
	default:
		first = lead + truncate(prompt, width-3)
	}

	meta := projectName(s.Workspace)
	if meta == "" {
		meta = shortID(s.SessionID)
	}
	meta += " · " + s.State
	if s.State == string(protocol.StateIdle) {
		if age := idleAge(s.UpdatedAt, now); age != "" {
			meta += " · " + age
		}
	}
	return first + "\n" + dim.Render("    "+truncate(meta, width-5)) + "\n"
}

// projectName is the last element of a workspace path, whichever separator
// the daemon's platform uses.
func projectName(workspace string) string {
	w := strings.TrimRight(workspace, `/\`)
	if i := strings.LastIndexAny(w, `/\`); i >= 0 {
		return w[i+1:]
	}
	return w
}

// pickerWidth is how much of a row the box has room for.
func (m model) pickerWidth() int {
	return max(20, min(72, m.width-8))
}

// pickerWindow is the range of sessions that fits on screen, moved to keep
// the cursor in it. Three lines a row, two and a gap, less what is around
// them: title and gap, gap and help, and two rows each of border and padding.
// The last row's gap is the one before the help, so that is 3n+7 in all.
func (m model) pickerWindow() (from, to int) {
	rows := max(1, (m.height-7)/3)
	if len(m.sessions) <= rows {
		return 0, len(m.sessions)
	}
	from = max(0, m.cursorAt-rows+1)
	return from, from + rows
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
	if len(m.tasks) > 0 && !m.showTasks() {
		// The pane is put away or has no room; the count still says how far.
		parts = append(parts, dim.Render(fmt.Sprintf("tasks %d/%d", doneTasks(m.tasks), len(m.tasks))))
	}

	if c := m.contextBadge(); c != "" {
		parts = append(parts, c)
	}
	if n := len(m.queued); n > 0 {
		parts = append(parts, badgeWarn.Render(fmt.Sprintf("%d prompts queued", n)))
	}

	// The status is what matters, so it is the hints that give way.
	left := statusBar.Render(margin + strings.Join(parts, statusBar.Render(" · ")))
	left = lipgloss.NewStyle().MaxWidth(m.width).Render(left)
	hints := m.statusHints()
	gap := m.width - visibleWidth(left) - visibleWidth(hints) - len(margin)
	if hints == "" || gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + hints + margin
}

// statusHints is the few keys worth knowing right now. The rest are behind ?:
// all of them on one line wrapped on an 80-column terminal (issue 106).
func (m model) statusHints() string {
	switch {
	case m.asking != nil:
		return "" // the question says how to answer it
	case m.composing:
		return dim.Render("enter send · esc cancel · /help")
	case terminal(m.state):
		return dim.Render("session ended · ? keys · q quit")
	}
	return dim.Render("s sessions · ? keys · q quit")
}

// keys is every key the transcript answers to, for the ? panel.
var keys = []struct{ key, what string }{
	{"i  enter", "type a prompt, or a /command"},
	{"s", "switch sessions"},
	{"t", "show or hide thinking"},
	{"p", "show or hide the task list"},
	{"o", "open the newest page the agent made"},
	{"ctrl+x", "interrupt the current turn"},
	{"↑↓  j k", "scroll a line"},
	{"pgup pgdn", "scroll a page, or u d for half"},
	{"g  G", "top, bottom"},
	{"q", "quit — the run continues"},
}

// keysPanel lists every key, and the full session id, which the status line
// no longer carries. The commands are /help's: both lists together are taller
// than an 80×24 terminal.
func (m model) keysPanel() string {
	keyWidth := 0
	for _, k := range keys {
		keyWidth = max(keyWidth, visibleWidth(k.key))
	}

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Keys") + "\n\n")
	for _, k := range keys {
		b.WriteString(toolStyle.Render(fmt.Sprintf("%-*s", keyWidth, k.key)) + "   " + k.what + "\n")
	}
	b.WriteString("\n" + fmt.Sprintf("%-*s", keyWidth, "/help") + "   " + "the commands, typed after i" + "\n")
	if m.sessionID != "" {
		b.WriteString("\n" + dim.Render("session "+m.sessionID) + "\n")
	}
	b.WriteString("\n" + dim.Render("esc close"))

	box := overlayBox.Render(b.String())
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
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

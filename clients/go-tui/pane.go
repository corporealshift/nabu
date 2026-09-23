package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// pane is the scrolling transcript. It stands in for bubbles' viewport, whose
// only way in is SetContent(string): that re-splits and re-measures every line
// of the whole history, and it ran on every streamed token. On a session two
// thousand events long that is the whole transcript per token, and the TUI fell
// behind the stream it was showing (issue 82).
//
// The pane takes lines already split and never measures them. Only the lines
// on screen are rendered.
type pane struct {
	Width, Height int

	lines  []string
	offset int // index of the top visible line
}

func newPane(width, height int) pane {
	return pane{Width: width, Height: height}
}

// SetLines replaces the content. The slice is kept, not copied: the caller
// hands over a fresh one each time.
func (p *pane) SetLines(lines []string) {
	p.lines = lines
	if p.offset > p.maxOffset() {
		p.GotoBottom()
	}
}

// TotalLineCount is how many lines the content has.
func (p pane) TotalLineCount() int { return len(p.lines) }

func (p pane) maxOffset() int {
	if n := len(p.lines) - p.Height; n > 0 {
		return n
	}
	return 0
}

// AtBottom reports whether the last line is on screen.
func (p pane) AtBottom() bool { return p.offset >= p.maxOffset() }

// AtTop reports whether the first line is on screen.
func (p pane) AtTop() bool { return p.offset <= 0 }

func (p *pane) GotoTop()    { p.offset = 0 }
func (p *pane) GotoBottom() { p.offset = p.maxOffset() }

// scroll moves by n lines, down when positive, and stays in bounds.
func (p *pane) scroll(n int) {
	p.offset += n
	if p.offset > p.maxOffset() {
		p.offset = p.maxOffset()
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

// Update scrolls on the keys bubbles' viewport answered to, so nothing anyone
// has in their fingers changes.
func (p pane) Update(msg tea.Msg) (pane, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "pgdown", " ", "f":
		p.scroll(p.Height)
	case "pgup", "b":
		p.scroll(-p.Height)
	case "ctrl+d", "d":
		p.scroll(p.Height / 2)
	case "ctrl+u", "u":
		p.scroll(-p.Height / 2)
	case "down", "j":
		p.scroll(1)
	case "up", "k":
		p.scroll(-1)
	}
	return p, nil
}

// View renders the visible window, padded to the pane's full size so the task
// pane beside it lines up.
func (p pane) View() string {
	end := p.offset + p.Height
	if end > len(p.lines) {
		end = len(p.lines)
	}
	start := p.offset
	if start > end {
		start = end
	}
	visible := make([]string, 0, p.Height)
	visible = append(visible, p.lines[start:end]...)
	for len(visible) < p.Height {
		visible = append(visible, "")
	}
	return lipgloss.NewStyle().Width(p.Width).MaxWidth(p.Width).Render(strings.Join(visible, "\n"))
}

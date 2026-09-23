package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// composerPrefix is "› ", and the same width of indent keeps wrapped lines
// aligned under the first one.
const composerPrefix = "› "

// wrapText breaks text to fit a width, at word boundaries where it can and
// mid-word where a single word is longer than the line. Counted in runes: a
// prompt with an accent in it is not narrower than it looks.
func wrapText(text string, width int) []string {
	if width < 1 || text == "" {
		return []string{text}
	}

	var lines []string
	line := ""

	flush := func() {
		lines = append(lines, line)
		line = ""
	}

	for _, word := range strings.Split(text, " ") {
		switch {
		case line == "":
			line = word
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) <= width:
			line += " " + word
		default:
			flush()
			line = word
		}

		// A word too long for any line is cut where the line ends.
		for utf8.RuneCountInString(line) > width {
			runes := []rune(line)
			lines = append(lines, string(runes[:width]))
			line = string(runes[width:])
		}
	}

	flush()
	return lines
}

// visibleWidth is what a string occupies on screen, ignoring styling.
func visibleWidth(s string) int { return lipgloss.Width(s) }

// composerBox frames the composer so it reads as the place to type, not as
// one more line of transcript. Dim, like the rest of the chrome: the eye
// belongs on what is typed in it.
var composerBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("8")).
	Padding(0, 1)

// composerChrome is the box's border and padding, one column each per side.
const composerChrome = 4

// composerLines is the input as it is drawn: the prompt in its box, wrapped
// to the terminal, with the cursor at the end of what has been typed.
func (m model) composerLines() []string {
	inner := max(m.width-composerChrome, 1)

	var body []string
	if !m.composing {
		body = []string{userStyle.Render(composerPrefix) + dim.Render("press i to type")}
	} else {
		// Room for the prefix, and for the cursor block after the last
		// character.
		width := max(inner-visibleWidth(composerPrefix)-1, 1)
		lines := wrapText(m.input, width)
		for i, line := range lines {
			lead := strings.Repeat(" ", visibleWidth(composerPrefix))
			if i == 0 {
				lead = userStyle.Render(composerPrefix)
			}
			if i == len(lines)-1 {
				line += badgeOK.Render("▌")
			}
			body = append(body, lead+line)
		}
	}
	// Width is inside the border, padding included.
	box := composerBox.Width(inner + 2).Render(strings.Join(body, "\n"))
	return strings.Split(box, "\n")
}

// composerHeight is how many rows the composer needs right now. A question
// takes the composer's place, and its rows come out of the transcript too.
func (m model) composerHeight() int {
	if m.asking != nil {
		return len(m.askLines()) + askBoxHeightChrome
	}
	return len(m.composerLines())
}

// viewportHeight is what the transcript gets: the screen, less the blank row
// above the composer and the status line below it, less however many rows the
// composer is using. A prompt that grows takes rows from the transcript rather
// than pushing the layout off-screen.
func (m model) viewportHeight() int {
	h := m.height - 2 - m.composerHeight()
	if h < 1 {
		return 1
	}
	return h
}

// wrapAll breaks transcript entries to the width they will be read in.
//
// It happens here rather than when an event is rendered, because the width is
// not known then and changes when the terminal does. A line is stored once and
// wrapped afresh every draw.
func wrapAll(entries []entry, width int) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, wrapEntry(e, width)...)
	}
	return out
}

// margin keeps the transcript off the terminal's edge.
const margin = "  "

// minAsideWidth is the narrowest pane an aside is kept on. Below it a
// timestamp would take the room the prompt needs.
const minAsideWidth = 40

// wrapEntry wraps one entry inside the margin, and sets its aside at the right
// edge of the first line. The text wraps short of the aside, so the two never
// collide.
func wrapEntry(e entry, width int) []string {
	if e.text == "" {
		return []string{""}
	}
	aside := e.aside
	if width < minAsideWidth {
		aside = ""
	}
	room := width - len(margin)
	if aside != "" {
		room -= visibleWidth(aside) + 2
	}
	var lines []string
	// An expanded thought is one entry holding several lines, and each wraps
	// on its own.
	for _, line := range strings.Split(e.text, "\n") {
		lines = append(lines, wrapHanging(line, max(room, 1))...)
	}
	for i := range lines {
		if lines[i] != "" {
			lines[i] = margin + lines[i]
		}
	}
	if aside != "" {
		gap := width - visibleWidth(lines[0]) - visibleWidth(aside)
		lines[0] += strings.Repeat(" ", max(gap, 2)) + dim.Render(aside)
	}
	return lines
}

// wrapHanging wraps a line so what it runs onto sits under its text rather
// than back at the margin: a long command stays inside its call's block, and
// a result's second line stays under its first.
func wrapHanging(line string, width int) []string {
	indent := hang(line)
	if indent > width/2 {
		indent = 0 // a hang that deep leaves no room for the text
	}
	out := wrapStyled(line, max(width-indent, 1))
	first := 1
	if len(out) > 1 && strings.TrimSpace(stripANSI(out[0])) == "" {
		// One long unbroken word after the indent, a line of JSON say, wraps
		// whole and leaves the indent alone on a line that reads as a gap.
		out, first = out[1:], 0
	}
	for i := first; i < len(out); i++ {
		out[i] = strings.Repeat(" ", indent) + out[i]
	}
	return out
}

// hangMarks lead a line and its text hangs after them: the transcript's own
// glyphs, and a markdown list's bullets.
const hangMarks = "→›~✓!✗▣-*•"

// hang is how far a line's continuation is indented: its leading spaces, and
// the mark and space after them if it has one.
func hang(line string) int {
	s := stripANSI(line)
	n := len(s) - len(strings.TrimLeft(s, " "))
	if rest := []rune(s[n:]); len(rest) > 1 && rest[1] == ' ' && strings.ContainsRune(hangMarks, rest[0]) {
		n += 2
	}
	return n
}

// stripANSI removes styling, leaving the text as it reads.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && (r == 'm' || r == 'K'):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wrapStyled wraps one line that already carries its styling. lipgloss counts
// what is visible rather than what is in the string, which matters because the
// escape sequences are several characters that occupy no columns at all.
func wrapStyled(line string, width int) []string {
	if visibleWidth(line) <= width {
		return []string{line}
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(line)

	out := strings.Split(wrapped, "\n")
	for i := range out {
		out[i] = strings.TrimRight(out[i], " ")
	}
	return out
}

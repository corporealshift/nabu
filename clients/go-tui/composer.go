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

// composerLines is the input as it is drawn: the prompt, wrapped to the
// terminal, with the cursor at the end of what has been typed.
func (m model) composerLines() []string {
	if !m.composing {
		return []string{dim.Render("press i to type, s for sessions")}
	}

	// Room for the prefix, and for the cursor block after the last character.
	width := m.width - visibleWidth(composerPrefix) - 1
	if width < 1 {
		width = 1
	}

	body := wrapText(m.input, width)
	out := make([]string, 0, len(body))
	for i, line := range body {
		lead := strings.Repeat(" ", visibleWidth(composerPrefix))
		if i == 0 {
			lead = userStyle.Render(composerPrefix)
		}
		if i == len(body)-1 {
			line += badgeOK.Render("▌")
		}
		out = append(out, lead+line)
	}
	return out
}

// composerHeight is how many rows the composer needs right now.
func (m model) composerHeight() int { return len(m.composerLines()) }

// viewportHeight is what the transcript gets: the screen, less the status and
// help lines, less however many rows the composer is using. A prompt that grows
// takes rows from the transcript rather than pushing the layout off-screen.
func (m model) viewportHeight() int {
	h := m.height - 2 - m.composerHeight()
	if h < 1 {
		return 1
	}
	return h
}

// wrapAll breaks rendered transcript lines to the width they will be read in.
//
// It happens here rather than when an event is rendered, because the width is
// not known then and changes when the terminal does. A line is stored once and
// wrapped afresh every draw.
func wrapAll(lines []string, width int) []string {
	if width < 2 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, wrapStyled(line, width)...)
	}
	return out
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

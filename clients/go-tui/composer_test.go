package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{"empty is one empty line", "", 10, []string{""}},
		{"short text is one line", "hello", 10, []string{"hello"}},
		{"exactly the width is one line", "0123456789", 10, []string{"0123456789"}},
		{
			"wraps between words",
			"the quick brown fox",
			10,
			[]string{"the quick", "brown fox"},
		},
		{
			"a word longer than the width is broken",
			"supercalifragilistic",
			10,
			[]string{"supercalif", "ragilistic"},
		},
		{
			"a long word after a short one starts its own line",
			"go supercalifragilistic",
			10,
			[]string{"go", "supercalif", "ragilistic"},
		},
		{
			"runes are counted, not bytes",
			"ééééé ééééé",
			5,
			[]string{"ééééé", "ééééé"},
		},
		{"a width of zero does not loop forever", "abc", 0, []string{"abc"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapText(tt.text, tt.width)
			if len(got) != len(tt.want) {
				t.Fatalf("wrapText(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Issue 23: a prompt longer than the terminal ran off the side of it.
func TestComposerWrapsToTheTerminal(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.width = 20
	m.composing = true
	m.input = strings.Repeat("word ", 12)

	lines := m.composerLines()

	if len(lines) < 3 {
		t.Fatalf("a 60-character prompt in a 20-column terminal should wrap, got %d lines", len(lines))
	}
	for i, line := range lines {
		if w := visibleWidth(line); w > m.width {
			t.Errorf("line %d is %d columns wide in a %d-column terminal: %q", i, w, m.width, line)
		}
	}
}

// The transcript gives up the rows the composer needs, rather than being pushed
// off the bottom of the screen.
func TestTranscriptShrinksAsTheComposerGrows(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 24})
	m = next.(model)

	m.composing = true
	one := m.viewportHeight()

	m.input = strings.Repeat("word ", 20)
	many := m.viewportHeight()

	if many >= one {
		t.Fatalf("a wrapped composer should cost transcript rows: %d before, %d after", one, many)
	}
	if many < 1 {
		t.Error("the transcript must keep at least one row")
	}
}

func TestClosedComposerIsOneLineInItsBox(t *testing.T) {
	m := newModel("S1", make(chan action, 1))

	if n := len(m.composerLines()); n != 3 {
		t.Errorf("a closed composer should be one line and a border above and below, got %d", n)
	}
}

// Backspace used to slice bytes, which cuts a multibyte rune in half.
func TestBackspaceRemovesOneRune(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.composing = true
	m.input = "café"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	if got := next.(model).input; got != "caf" {
		t.Errorf("backspace over a multibyte rune gave %q, want %q", got, "caf")
	}
}

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
)

func asked(m model, q string, choices ...string) model {
	next, _ := m.Update(askMsg{q: question{
		id:  "req-1",
		req: goclient.AskRequest{Question: q, Choices: choices},
	}})
	return next.(model)
}

func press(t *testing.T, m model, keys string) model {
	t.Helper()
	for _, r := range keys {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	return m
}

// Issue 36: the agent can now put a question, and it has to be answerable.
func TestAQuestionTakesOverTheScreen(t *testing.T) {
	m := asked(sized(t, nil), "which design do you want?", "the simple one", "the fast one")

	view := m.View()
	for _, want := range []string{"asking", "which design do you want?", "the simple one"} {
		if !strings.Contains(view, want) {
			t.Errorf("the overlay never shows %q:\n%s", want, view)
		}
	}
}

// A number picks the choice it labels, because typing out an option nobody
// wants to retype is how a question goes unanswered.
func TestANumberPicksAChoice(t *testing.T) {
	m := asked(sized(t, nil), "which?", "alpha", "beta")

	next, _, done := m.onAskKey("2", []rune{'2'})
	if !done {
		t.Fatal("a number should answer immediately")
	}
	_ = next
	_, answer, _ := m.onAskKey("2", []rune{'2'})
	if answer != "beta" {
		t.Errorf("answer = %q, want beta", answer)
	}
}

// The offered options are rarely the whole truth, so free text always works.
func TestFreeTextIsAlwaysAllowed(t *testing.T) {
	m := asked(sized(t, nil), "which?", "alpha", "beta")
	m = press(t, m, "neither")

	if m.asking.typed != "neither" {
		t.Fatalf("typed = %q", m.asking.typed)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(model).asking != nil {
		t.Error("enter should have sent the answer")
	}
}

// Once an answer is being typed, a digit belongs to it rather than picking an
// option. "3 files" must not answer "the third choice".
func TestDigitsBelongToAnAnswerInProgress(t *testing.T) {
	m := asked(sized(t, nil), "how many?", "one", "two", "three")
	m = press(t, m, "about ")

	next, answer, done := m.onAskKey("3", []rune{'3'})
	if done {
		t.Fatalf("a digit mid-answer should not pick a choice (%q)", answer)
	}
	if next.asking.typed != "about 3" {
		t.Errorf("typed = %q, want 'about 3'", next.asking.typed)
	}
}

// An empty answer is not an answer, and sending one would unblock the agent
// with nothing.
func TestEnterOnAnEmptyAnswerDoesNothing(t *testing.T) {
	m := asked(sized(t, nil), "which?")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(model).asking == nil {
		t.Error("an empty answer should not have been sent")
	}
}

// Every printable key belongs to the answer, so the agent is never left
// waiting because a keystroke was read as a command.
func TestQDoesNotQuitWhileAnswering(t *testing.T) {
	m := asked(sized(t, nil), "which?")
	m = press(t, m, "q")

	if m.quitting {
		t.Error("q should have been typed, not quit")
	}
	if m.asking.typed != "q" {
		t.Errorf("typed = %q", m.asking.typed)
	}
}

// Being asked a question must not trap anyone in the client.
func TestCtrlCStillQuitsWhileAnswering(t *testing.T) {
	m := asked(sized(t, nil), "which?")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !next.(model).quitting {
		t.Error("ctrl+c should still quit")
	}
}

func TestBackspaceEditsTheAnswer(t *testing.T) {
	m := asked(sized(t, nil), "which?")
	m = press(t, m, "abc")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := next.(model).asking.typed; got != "ab" {
		t.Errorf("typed = %q, want ab", got)
	}
}

// Switching sessions must not leave another session's question on screen.
func TestResetClearsTheQuestion(t *testing.T) {
	m := asked(sized(t, nil), "which?")
	m.reset("S2")

	if m.asking != nil {
		t.Error("the question should be gone after switching sessions")
	}
}

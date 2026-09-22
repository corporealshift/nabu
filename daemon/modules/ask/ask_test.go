package ask

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// fakeUI stands in for whoever is watching.
type fakeUI struct {
	answer   string
	err      error
	question string
	choices  []string
	asked    int
}

func (f *fakeUI) Ask(_ context.Context, _ module.Session, question string, choices []string) (string, error) {
	f.asked++
	f.question, f.choices = question, choices
	return f.answer, f.err
}

// hostShim is a Host that only knows how to reach the human. Nothing else in
// this module touches the host, so the rest stays nil and any use of it would
// fail loudly rather than quietly.
type hostShim struct{ ui *fakeUI }

func (h hostShim) Model() module.Model            { return nil }
func (h hostShim) Tools() module.ToolCaller       { return nil }
func (h hostShim) UI() module.UI                  { return h.ui }
func (h hostShim) DataDir(string) (string, error) { return "", nil }
func (h hostShim) Log() *slog.Logger              { return slog.Default() }

func moduleWith(t *testing.T, ui *fakeUI) *Module {
	t.Helper()
	m := &Module{enabled: true}
	m.host = hostShim{ui}
	return m
}

func run(t *testing.T, m *Module, args string) (string, error) {
	t.Helper()
	return m.run(context.Background(), nil, json.RawMessage(args))
}

func TestTheQuestionReachesTheHuman(t *testing.T) {
	ui := &fakeUI{answer: "the second one"}
	m := moduleWith(t, ui)

	got, err := run(t, m, `{"question":"which design do you want?","choices":["a","b"]}`)
	if err != nil {
		t.Fatalf("ask: %v", err)
	}

	if ui.question != "which design do you want?" {
		t.Errorf("question = %q", ui.question)
	}
	if strings.Join(ui.choices, ",") != "a,b" {
		t.Errorf("choices = %v", ui.choices)
	}
	if got != "the second one" {
		t.Errorf("answer = %q", got)
	}
}

// A model offers blank and duplicate options more often than it means to, and
// both make a picker confusing to answer.
func TestChoicesAreTidied(t *testing.T) {
	ui := &fakeUI{answer: "a"}
	m := moduleWith(t, ui)

	if _, err := run(t, m, `{"question":"q","choices":["a","","a"," b ","b"]}`); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got := strings.Join(ui.choices, ","); got != "a,b" {
		t.Errorf("choices = %q, want a,b", got)
	}
}

// A list too long to answer on a phone is a decision the agent should be
// making itself.
func TestTooManyChoicesIsRefused(t *testing.T) {
	ui := &fakeUI{answer: "a"}
	m := moduleWith(t, ui)

	_, err := run(t, m, `{"question":"q","choices":["1","2","3","4","5","6","7"]}`)
	if err == nil {
		t.Fatal("seven choices should be refused")
	}
	if ui.asked != 0 {
		t.Error("nobody should have been asked")
	}
}

func TestAnEmptyQuestionIsRefused(t *testing.T) {
	ui := &fakeUI{}
	m := moduleWith(t, ui)

	if _, err := run(t, m, `{"question":"   "}`); err == nil {
		t.Fatal("an empty question should be refused")
	}
	if ui.asked != 0 {
		t.Error("nobody should have been asked")
	}
}

// Nobody answering is a fact about the situation, not a broken tool. The agent
// has to be told plainly so it can decide what to do without a human.
func TestNobodyThereIsReportedNotSwallowed(t *testing.T) {
	ui := &fakeUI{err: errors.New("no client is attached to session S1")}
	m := moduleWith(t, ui)

	_, err := run(t, m, `{"question":"which one?"}`)
	if err == nil {
		t.Fatal("an unanswered question should be an error the agent can read")
	}
	if !strings.Contains(err.Error(), "no client is attached") {
		t.Errorf("the reason was lost: %v", err)
	}
}

// An empty answer is an answer: they were there and said nothing useful.
func TestAnEmptyAnswerIsNotAnError(t *testing.T) {
	ui := &fakeUI{answer: "  "}
	m := moduleWith(t, ui)

	got, err := run(t, m, `{"question":"which one?"}`)
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got == "" {
		t.Error("the agent should be told something rather than nothing")
	}
}

func TestADisabledModuleOffersNoTool(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	if n := len(m.Tools()); n != 0 {
		t.Errorf("a disabled module offered %d tools", n)
	}
}

func TestTheToolIsCalledAsk(t *testing.T) {
	ui := &fakeUI{}
	m := moduleWith(t, ui)

	tools := m.Tools()
	if len(tools) != 1 || tools[0].Name != "ask" {
		t.Fatalf("tools = %v", tools)
	}
	// The description has to steer it away from the obvious misuses, including
	// combining multiple decisions into a single unanswerable prompt.
	for _, want := range []string{"permission", "reading the repository", "exactly one question"} {
		if !strings.Contains(tools[0].Description, want) {
			t.Errorf("the description never mentions %q", want)
		}
	}
}

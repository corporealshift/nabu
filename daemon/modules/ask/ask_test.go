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
	if !json.Valid(tools[0].Schema) {
		t.Fatalf("the schema is not JSON: %s", tools[0].Schema)
	}
	for _, want := range []string{"permission", "reading the repository", "exactly one decision",
		"its own entry of `questions`", "do not ask again"} {
		if !strings.Contains(tools[0].Description, want) {
			t.Errorf("the description never mentions %q", want)
		}
	}
}

// scriptedUI answers each question in turn and records what it was shown.
type scriptedUI struct {
	answers   []string
	failAt    int // 1-based; 0 never fails
	questions []string
	choices   [][]string
}

func (u *scriptedUI) Ask(_ context.Context, _ module.Session, question string, choices []string) (string, error) {
	u.questions = append(u.questions, question)
	u.choices = append(u.choices, choices)
	if len(u.questions) == u.failAt {
		return "", errors.New("no client is attached to session S1")
	}
	return u.answers[len(u.questions)-1], nil
}

type uiHost struct{ ui module.UI }

func (h uiHost) Model() module.Model            { return nil }
func (h uiHost) Tools() module.ToolCaller       { return nil }
func (h uiHost) UI() module.UI                  { return h.ui }
func (h uiHost) DataDir(string) (string, error) { return "", nil }
func (h uiHost) Log() *slog.Logger              { return slog.Default() }

func moduleWithUI(ui module.UI) *Module {
	return &Module{enabled: true, host: uiHost{ui}}
}

// Issue 70: several decisions arrived as one prompt with one set of choices,
// and the only way to answer was to pick a combination. Each is now asked on
// its own, with its own choices, and the answers come back together.
func TestSeveralQuestionsAreAskedOneAtATime(t *testing.T) {
	ui := &scriptedUI{answers: []string{"direct", "comment discipline"}}
	m := moduleWithUI(ui)

	got, err := m.run(context.Background(), nil, json.RawMessage(`{"questions":[
		{"question":"Should the tone be direct or neutral?","choices":["direct","neutral"]},
		{"question":"Which development rule matters most?","choices":["comment discipline","read the spec first"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ui.questions) != 2 {
		t.Fatalf("asked %d times, want 2", len(ui.questions))
	}
	if ui.questions[0] != "(1 of 2) Should the tone be direct or neutral?" {
		t.Errorf("first question = %q", ui.questions[0])
	}
	if len(ui.choices[1]) != 2 || ui.choices[1][0] != "comment discipline" {
		t.Errorf("the second question must carry its own choices, got %v", ui.choices[1])
	}
	for _, want := range []string{
		"1. Should the tone be direct or neutral?\n   → direct",
		"2. Which development rule matters most?\n   → comment discipline",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the combined answer is missing %q:\n%s", want, got)
		}
	}
}

// The question from issue 70's session: three numbered inside one. It is
// refused before anyone is interrupted, with the way to ask it properly.
func TestQuestionsNumberedInsideOneAreRefused(t *testing.T) {
	ui := &scriptedUI{}
	m := moduleWithUI(ui)
	bundledQ := "What personality direction and development guidance do you want?\n\n" +
		"1. **Personality** — Should I lean into being direct, or keep it neutral?\n\n" +
		"2. **Development work** — What would actually help?\n\n" +
		"3. **Anything else** — Things you've caught me doing that you'd rather I didn't?"
	args, _ := json.Marshal(map[string]any{"question": bundledQ, "choices": []string{"Keep it lean"}})

	_, err := m.run(context.Background(), nil, args)
	if err == nil || !strings.Contains(err.Error(), "its own entry of questions") {
		t.Fatalf("a bundled question should be refused with directions, got %v", err)
	}
	if len(ui.questions) != 0 {
		t.Error("nobody should be interrupted by a question that will be refused")
	}

	// One decision with two question marks, and a numbered list of facts, are
	// one question each: both are from real sessions.
	for _, ok := range []string{
		"Should I add a Default impl? Or would you rather allow the lint?",
		"CI failed with 2 clippy warnings:\n1. new_without_default\n2. needless_range_loop\nShould I fix both?",
	} {
		if n := bundled(ok); n != 1 {
			t.Errorf("bundled(%q) = %d, want 1", ok, n)
		}
	}
}

func TestMalformedQuestionListsAreRefused(t *testing.T) {
	m := moduleWithUI(&scriptedUI{})
	for name, args := range map[string]string{
		"both forms":     `{"question":"a?","questions":[{"question":"b?"}]}`,
		"too many":       `{"questions":[{"question":"1?"},{"question":"2?"},{"question":"3?"},{"question":"4?"},{"question":"5?"}]}`,
		"an empty entry": `{"questions":[{"question":"a?"},{"question":"  "}]}`,
		"nothing":        `{}`,
	} {
		if _, err := m.run(context.Background(), nil, json.RawMessage(args)); err == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
}

// A session asked the same question six times running into nobody. The error
// now says not to, and keeps what was already answered.
func TestNobodyAnsweringSaysNotToAskAgain(t *testing.T) {
	ui := &scriptedUI{answers: []string{"yes"}, failAt: 2}
	m := moduleWithUI(ui)

	_, err := m.run(context.Background(), nil, json.RawMessage(`{"questions":[{"question":"first?"},{"question":"second?"}]}`))
	if err == nil {
		t.Fatal("an unanswered question is an error")
	}
	for _, want := range []string{"no client is attached", "do not ask again", "1. first?\n   → yes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is missing %q: %v", want, err)
		}
	}
}

package tui

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

func TestRunLabels(t *testing.T) {
	tests := []struct {
		have, want []string
	}{
		{nil, []string{"run:requested"}},
		{[]string{"mine"}, []string{"mine", "run:requested"}},
		// A failed run is resumed by asking again; its step labels go.
		{[]string{"run:failed", "run:attempt:10/10", "mine"}, []string{"mine", "run:requested"}},
	}
	for _, tt := range tests {
		if got := runLabels(tt.have); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("runLabels(%q) = %q, want %q", tt.have, got, tt.want)
		}
	}
}

func TestRunStatus(t *testing.T) {
	tests := []struct {
		labels []string
		want   string
	}{
		{nil, ""},
		{[]string{"mine"}, ""},
		{[]string{"run:requested"}, "run: requested"},
		{[]string{"run:fix", "run:attempt:3/10"}, "run: fix (3/10)"},
		{[]string{"run:attempt:3/10"}, ""},
	}
	for _, tt := range tests {
		if got := runStatus(tt.labels); got != tt.want {
			t.Errorf("runStatus(%q) = %q, want %q", tt.labels, got, tt.want)
		}
	}
}

func summaries(pairs ...string) []goclient.SessionSummary {
	var out []goclient.SessionSummary
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, goclient.SessionSummary{SessionID: pairs[i], Parent: pairs[i+1], State: "idle"})
	}
	return out
}

func ids(ss []goclient.SessionSummary) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.SessionID)
	}
	return out
}

func TestGroupByParent(t *testing.T) {
	// The daemon lists newest first, so a run's steps come before its home.
	got := groupByParent(summaries("step2", "home", "other", "", "step1", "home", "home", "", "orphan", "gone"))
	want := []string{"other", "home", "step2", "step1", "orphan"}
	if !reflect.DeepEqual(ids(got), want) {
		t.Errorf("order = %q, want %q", ids(got), want)
	}
}

func TestPickerIndentsStepsUnderTheirHome(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.width, m.height = 100, 60
	next, _ := m.Update(sessionsMsg{sessions: []goclient.SessionSummary{
		{SessionID: "01STEP", Parent: "01HOME", State: "running", LastPrompt: "the plan step"},
		{SessionID: "01HOME", State: "idle", LastPrompt: "add caching", Labels: []string{"run:plan"}},
		{SessionID: "01ORPH", Parent: "01GONE", State: "idle", LastPrompt: "an orphan"},
	}})
	// The home is folded until it is opened (issue 135).
	next, _ = next.(model).Update(tea.KeyMsg{Type: tea.KeyRight})
	view := next.(model).picker()

	lineOf := func(text string) string {
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, text) {
				return l
			}
		}
		t.Fatalf("%q not in the picker:\n%s", text, view)
		return ""
	}
	indent := func(l string) int {
		trimmed := strings.TrimLeft(l, " │›")
		return utf8.RuneCountInString(l) - utf8.RuneCountInString(trimmed)
	}
	if indent(lineOf("the plan step")) <= indent(lineOf("add caching")) {
		t.Errorf("a step is not indented under its home:\n%s", view)
	}
	if indent(lineOf("an orphan")) != indent(lineOf("add caching")) {
		t.Errorf("a session whose parent is not listed should sit at the top level:\n%s", view)
	}
	if !strings.Contains(view, "run: plan") {
		t.Errorf("the home does not show its run status:\n%s", view)
	}
	if strings.Index(view, "add caching") > strings.Index(view, "the plan step") {
		t.Errorf("the step is not listed after its home:\n%s", view)
	}
}

// submit types a line into the composer and presses enter, returning the
// action it emits, if any.
func submit(t *testing.T, m model, line string) (model, *action) {
	t.Helper()
	actions := make(chan action, 1)
	m.actions = actions
	m.composing, m.input = true, line
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		cmd()
	}
	select {
	case a := <-actions:
		return next.(model), &a
	default:
		return next.(model), nil
	}
}

func TestRunCommand(t *testing.T) {
	m := newModel("S1", nil)
	m.labels = []string{"mine", "run:failed"}

	_, a := submit(t, m, "/run add a cache to the reader")
	if a == nil || a.kind != actRun || a.text != "add a cache to the reader" || a.sessionID != "S1" {
		t.Fatalf("action = %+v", a)
	}
	if !reflect.DeepEqual(a.labels, []string{"mine", "run:requested"}) {
		t.Errorf("labels = %q", a.labels)
	}
	if _, a := submit(t, m, "/run"); a == nil || a.kind != actRun || a.text != "" {
		t.Errorf("bare /run = %+v", a)
	}
	if !strings.Contains(helpText, "/run") {
		t.Error("/help does not list /run")
	}
}

func TestTheModelTracksParentAndLabels(t *testing.T) {
	m := newModel("S1", nil)
	sess, _ := json.Marshal(protocol.SessionData{Workspace: "w", WorkspaceKey: "k", Options: protocol.Options{
		Model: "m", PermissionMode: "auto", Parent: "01M3PARENT0000000000000001", Labels: []string{"run:plan"}}})
	m.appendEvent(protocol.Event{ID: "01A", Type: protocol.EventSession, Data: sess})
	change, _ := json.Marshal(protocol.OptionsChangeData{Key: "labels", From: json.RawMessage(`["run:plan"]`),
		To: json.RawMessage(`["run:fix","run:attempt:2/10"]`), Source: "client"})
	m.appendEvent(protocol.Event{ID: "01B", Type: protocol.EventOptionsChange, Data: change})

	if m.parent != "01M3PARENT0000000000000001" || !reflect.DeepEqual(m.labels, []string{"run:fix", "run:attempt:2/10"}) {
		t.Fatalf("parent %q, labels %q", m.parent, m.labels)
	}
	status := m.status()
	if !strings.Contains(status, "run: fix (2/10)") || !strings.Contains(status, "↑ "+shortID(m.parent)) {
		t.Errorf("status = %q", status)
	}
	m.reset("S2")
	if m.parent != "" || m.labels != nil {
		t.Error("reset kept the last session's parent or labels")
	}
}

// /run against a real daemon: the brief is the description, the label is
// set, and the session stays idle. The brief was once set as the goal, which
// started the session working on it in the owner's checkout.
func TestRunReachesTheDaemon(t *testing.T) {
	addr := startDaemon(t)
	id := createSession(t, addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := newRecorder()
	actions := make(chan action, 1)
	done := make(chan struct{})
	go func() {
		connectLoop(ctx, rec, addr, "", id, actions)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	rec.waitFor(t, 0, "the session to connect", isConnected)

	from := rec.mark()
	actions <- action{kind: actRun, sessionID: id, text: "add a cache", labels: []string{"run:requested"}}
	rec.waitFor(t, from, "the hand-off note", func(m tea.Msg) bool {
		n, ok := m.(noteMsg)
		return ok && strings.Contains(n.text, "handed to the runner")
	})

	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ccancel()
	c, err := goclient.Dial(cctx, addr, "", "test", "0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st, err := c.State(cctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Options.Description != "add a cache" || !reflect.DeepEqual(st.Options.Labels, []string{"run:requested"}) {
		t.Errorf("description %q, labels %q", st.Options.Description, st.Options.Labels)
	}
	if st.Goal != nil || st.State != protocol.StateIdle {
		t.Errorf("/run started the session: goal %+v, state %q", st.Goal, st.State)
	}
}

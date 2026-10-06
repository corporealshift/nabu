package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

func TestGoalLabels(t *testing.T) {
	tests := []struct {
		have, want []string
	}{
		{nil, []string{"goal:requested"}},
		{[]string{"mine"}, []string{"mine", "goal:requested"}},
		// A blocked goal is resumed by asking again; its step labels go.
		{[]string{"goal:blocked", "goal:round:3", "mine"}, []string{"mine", "goal:requested"}},
	}
	for _, tt := range tests {
		if got := goalLabels(tt.have); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("goalLabels(%q) = %q, want %q", tt.have, got, tt.want)
		}
	}
}

func TestGoalStatus(t *testing.T) {
	tests := []struct {
		labels []string
		want   string
	}{
		{nil, ""},
		{[]string{"run:plan"}, ""},
		{[]string{"goal:requested"}, "goal: requested"},
		{[]string{"goal:runs", "goal:round:2"}, "goal: runs, round 2"},
		{[]string{"goal:round:2"}, ""},
	}
	for _, tt := range tests {
		if got := goalStatus(tt.labels); got != tt.want {
			t.Errorf("goalStatus(%q) = %q, want %q", tt.labels, got, tt.want)
		}
	}
}

// A goal's runs go under the goal, and each run's steps under the run.
func TestGroupByParentNests(t *testing.T) {
	list := summaries("step", "run1", "run2", "goal", "run1", "goal", "other", "", "goal", "")
	got := groupByParent(list)
	if want := []string{"other", "goal", "run2", "run1", "step"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("order = %q, want %q", ids(got), want)
	}
	if d := depths(list); d["goal"] != 0 || d["run1"] != 1 || d["step"] != 2 || d["other"] != 0 {
		t.Errorf("depths = %v", d)
	}
}

func TestGoalCommand(t *testing.T) {
	m := newModel("S1", nil)
	m.labels = []string{"mine"}
	_, a := submit(t, m, "/goal add offline sync")
	if a == nil || a.kind != actGoal || a.text != "add offline sync" || !reflect.DeepEqual(a.labels, []string{"mine", "goal:requested"}) {
		t.Fatalf("action = %+v", a)
	}

	// Bare /goal on a session that is no goal has nothing to hand over.
	if _, a := submit(t, m, "/goal"); a != nil {
		t.Errorf("bare /goal on a plain session = %+v", a)
	}
	// On a blocked goal it resumes it.
	m.labels = []string{"goal:blocked", "goal:round:2"}
	if _, a := submit(t, m, "/goal"); a == nil || a.kind != actGoal || a.text != "" || !reflect.DeepEqual(a.labels, []string{"goal:requested"}) {
		t.Errorf("bare /goal on a blocked goal = %+v", a)
	}
	if !strings.Contains(helpText, "/goal") {
		t.Error("/help does not list /goal")
	}
}

func TestPickerShowsAGoalThreeDeep(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.width, m.height = 100, 60
	next, _ := m.Update(sessionsMsg{sessions: []goclient.SessionSummary{
		{SessionID: "01STEP", Parent: "01RUN", State: "running", LastPrompt: "the plan step"},
		{SessionID: "01RUN", Parent: "01GOAL", State: "idle", LastPrompt: "the run", Labels: []string{"run:plan"}},
		{SessionID: "01GOAL", State: "idle", LastPrompt: "the goal", Labels: []string{"goal:runs", "goal:round:1"}},
	}})
	m = next.(model)
	// Folded at first: only the goal, saying what is under it (issue 135).
	if folded := m.picker(); strings.Contains(folded, "the run") || !strings.Contains(folded, "▸ 2 sessions") {
		t.Fatalf("the goal is not folded:\n%s", folded)
	}
	key := func(k tea.KeyType) {
		next, _ := m.Update(tea.KeyMsg{Type: k})
		m = next.(model)
	}
	key(tea.KeyRight) // the goal opens on its run, still folded
	if v := m.picker(); !strings.Contains(v, "the run") || strings.Contains(v, "the plan step") || !strings.Contains(v, "▾ 2 sessions") {
		t.Fatalf("after → on the goal:\n%s", v)
	}
	key(tea.KeyDown)
	key(tea.KeyRight) // and the run on its step
	view := m.picker()
	defer func() {
		key(tea.KeyLeft) // folds the run
		if v := m.picker(); strings.Contains(v, "the plan step") || m.sessions[m.cursorAt].SessionID != "01RUN" {
			t.Errorf("← did not fold the run:\n%s", v)
		}
		key(tea.KeyLeft) // a folded run goes up to its goal
		if m.sessions[m.cursorAt].SessionID != "01GOAL" {
			t.Errorf("← on a folded run is on %s", m.sessions[m.cursorAt].SessionID)
		}
	}()
	col := func(text string) int {
		for _, l := range strings.Split(view, "\n") {
			if i := strings.Index(l, text); i >= 0 {
				return i
			}
		}
		t.Fatalf("%q not in the picker:\n%s", text, view)
		return 0
	}
	if !(col("the goal") < col("the run") && col("the run") < col("the plan step")) {
		t.Errorf("not nested goal, run, step:\n%s", view)
	}
	if !strings.Contains(view, "goal: runs, round 1") {
		t.Errorf("the goal does not show its status:\n%s", view)
	}
}

// /goal against a real daemon: the text is the description, the label is
// set, and the session stays idle with no session goal.
func TestGoalReachesTheDaemon(t *testing.T) {
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
	actions <- action{kind: actGoal, sessionID: id, text: "add offline sync", labels: []string{"goal:requested"}}
	rec.waitFor(t, from, "the hand-off note", func(m tea.Msg) bool {
		n, ok := m.(noteMsg)
		return ok && strings.Contains(n.text, "as a goal")
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
	if st.Options.Description != "add offline sync" || !reflect.DeepEqual(st.Options.Labels, []string{"goal:requested"}) {
		t.Errorf("description %q, labels %q", st.Options.Description, st.Options.Labels)
	}
	if st.Goal != nil || st.State != protocol.StateIdle {
		t.Errorf("/goal started the session: goal %+v, state %q", st.Goal, st.State)
	}
}

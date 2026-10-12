package stats

import (
	"reflect"
	"testing"
	"time"

	"github.com/corporealshift/rho/protocol"
)

func under(hours float64, parent string, labels ...string) protocol.Event {
	return at(hours, protocol.EventSession, protocol.SessionData{Workspace: "/w",
		Options: protocol.Options{Parent: parent, Labels: labels}})
}

// A goal's home, one run's home under it, and two steps under that, the
// second archived; beside them a session that belongs to none of it.
var (
	goalHome = Log{ID: "01G", Events: []protocol.Event{under(0, "", "goal:runs"), said(0, "build it")}}
	runHome  = Log{ID: "01R", Events: []protocol.Event{under(1, "01G", "run:work")}}
	planStep = Log{ID: "01S1", Events: []protocol.Event{
		under(1, "01R", "unattended"), said(1, "plan"), turn(2, 1000, 100), turn(3, 2000, 50)}}
	workStep = Log{ID: "01S2", Archived: true, Events: []protocol.Event{
		under(4, "01R", "unattended"), said(4, "work"), turn(5, 3000, 300),
		called(5, "c1", "read", map[string]string{"path": "a.go"}),
		answered(5, "c1", "read", "ok", "", "package a")}}
	elsewhere = Log{ID: "01U", Events: []protocol.Event{opened(2, ""), said(2, "hi"), turn(2, 50, 5)}}
)

func TestTree(t *testing.T) {
	logs := []Log{workStep, elsewhere, planStep, runHome, goalHome}
	for _, tc := range []struct {
		name    string
		root    string
		members []string
	}{
		{"a goal counts its runs and their steps", "01G", []string{"01G", "01R", "01S1", "01S2"}},
		{"a run counts its steps and not its goal", "01R", []string{"01R", "01S1", "01S2"}},
		{"a step is a tree of one", "01S1", []string{"01S1"}},
		{"an unknown session is an empty tree", "01X", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Tree(tc.root, logs, time.UTC)

			var ids []string
			for _, m := range got.Members {
				ids = append(ids, m.SessionID)
			}
			if !reflect.DeepEqual(ids, tc.members) {
				t.Fatalf("members: got %v, want %v", ids, tc.members)
			}

			byID := map[string]Log{}
			for _, l := range logs {
				byID[l.ID] = l
			}
			var want tally
			for i, id := range tc.members {
				s := Of(id, byID[id].Events)
				want.add(s)
				m := got.Members[i]
				if m.Turns != s.Turns || m.WorkingSeconds != s.WorkingSeconds ||
					m.Tokens.Input != s.Tokens.Input || m.Tokens.Output != s.Tokens.Output ||
					!m.StartedAt.Equal(s.StartedAt) || !m.LastEventAt.Equal(s.LastEventAt) {
					t.Errorf("member %s: %+v does not match its own stats %+v", id, m, s)
				}
				if m.Archived != byID[id].Archived {
					t.Errorf("member %s: archived %v", id, m.Archived)
				}
			}
			w := want.window()
			if got.Sessions != w.Sessions || got.Turns != w.Turns || got.Prompts != w.Prompts ||
				got.Tokens != w.Tokens || got.WorkingSeconds != w.WorkingSeconds ||
				!reflect.DeepEqual(got.Tools, w.Tools) {
				t.Errorf("totals: got %+v, want %+v", got.Window, w)
			}
			if got.Tools == nil || got.PerDay == nil || got.Members == nil {
				t.Errorf("arrays must be present: %+v", got)
			}
		})
	}
}

func TestTreeMemberCarriesParentAndLabels(t *testing.T) {
	got := Tree("01G", []Log{goalHome, runHome, planStep}, time.UTC)
	run := got.Members[1]
	if run.Parent != "01G" || !reflect.DeepEqual(run.Labels, []string{"run:work"}) {
		t.Fatalf("run member: %+v", run)
	}
	if got.Turns != 2 || got.Tokens.Input != 3000 {
		t.Fatalf("totals: %+v", got.Window)
	}
	if !got.StartedAt.Equal(from) || !got.LastEventAt.Equal(from.Add(3*time.Hour)) {
		t.Fatalf("span: %s to %s", got.StartedAt, got.LastEventAt)
	}
}

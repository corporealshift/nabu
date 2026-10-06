package runs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

func fence(body string) string { return "Here it is.\n```json\n" + body + "\n```\n" }

func TestParseBreakdown(t *testing.T) {
	tests := []struct {
		name     string
		answer   string
		ok       bool
		doneWhen []string
		titles   []string
	}{
		{"good", fence(`{"done_when": ["sync works offline", " "], "briefs": [{"title": "Queue", "brief": "Add a queue."}, {"title": "UI", "brief": "Show it."}]}`),
			true, []string{"sync works offline"}, []string{"Queue", "UI"}},
		{"the last block counts", fence(`{"briefs": []}`) + fence(`{"briefs": [{"title": "Queue", "brief": "Add a queue."}]}`),
			true, nil, []string{"Queue"}},
		{"briefs with no text are dropped", fence(`{"briefs": [{"title": "Queue", "brief": ""}, {"title": "", "brief": "x"}, {"title": "UI", "brief": "Show it."}]}`),
			true, nil, []string{"UI"}},
		{"no block", "I would start with a queue.", false, nil, nil},
		{"bad json", fence(`{"briefs": [`), false, nil, nil},
		{"no briefs", fence(`{"done_when": ["x"], "briefs": []}`), false, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doneWhen, briefs, ok := ParseBreakdown(tt.answer)
			if ok != tt.ok {
				t.Fatalf("ok = %v", ok)
			}
			var titles []string
			for _, b := range briefs {
				titles = append(titles, b.Title)
			}
			if !reflect.DeepEqual(doneWhen, tt.doneWhen) || !reflect.DeepEqual(titles, tt.titles) {
				t.Errorf("done when %q, briefs %q", doneWhen, titles)
			}
		})
	}
}

func TestParseCheck(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		ok     bool
		met    bool
		briefs int
	}{
		{"met", fence(`{"met": true, "reason": "all of it is there"}`), true, true, 0},
		{"unmet with briefs", fence(`{"met": false, "reason": "no UI", "briefs": [{"title": "UI", "brief": "Show it."}]}`), true, false, 1},
		{"unmet with no briefs", fence(`{"met": false, "reason": "no UI"}`), false, false, 0},
		{"no verdict", fence(`{"reason": "hmm"}`), false, false, 0},
		{"no block", "It is met.", false, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			met, _, briefs, ok := ParseCheck(tt.answer)
			if ok != tt.ok || met != tt.met || len(briefs) != tt.briefs {
				t.Errorf("met %v, %d briefs, ok %v", met, len(briefs), ok)
			}
		})
	}
}

// aGoal is a goal two rounds in: the first round done, the second's first
// run failed and its second dropped.
func aGoal() Goal {
	return Goal{
		Home: "G1", Slug: "offline-sync-g1", Branch: "nabu/goal-offline-sync-g1", Base: "main",
		Text: "Add offline sync", DoneWhen: []string{"edits made offline reach the daemon"},
		Round: 2, RoundsFrom: 1, Failures: 1,
		Briefs: []GoalBrief{
			{Title: "Queue", Brief: "Add a queue.", Round: 1, State: BriefDone, Run: "R1", PRURL: "https://github.com/kyle/x/pull/3", Notes: []string{"consider batching"}},
			{Title: "Replay", Brief: "Replay it.", Round: 2, State: BriefFailed, Run: "R2", FailedAt: StepFix, Why: "verify.sh still fails after 10 fixes"},
			{Title: "UI", Brief: "Show it.", Round: 2, State: BriefDropped},
		},
		Verdicts: []Verdict{{Round: 1, Reason: "nothing replays the queue"}},
	}
}

func TestBreakdownPrompt(t *testing.T) {
	p := BreakdownPrompt(Goal{Text: "Add offline sync"})
	for _, s := range []string{"Add offline sync", "between 2 and 6 briefs", "done_when", "sees only its brief"} {
		if !strings.Contains(p, s) {
			t.Errorf("prompt lacks %q", s)
		}
	}
}

func TestCheckPrompt(t *testing.T) {
	g := aGoal()
	g.CIFailure = "### build\n\ngofmt found sync.go"
	p := CheckPrompt(g, []string{"sync.go"})
	for _, s := range []string{
		"Add offline sync", "edits made offline reach the daemon", "nabu/goal-offline-sync-g1", "- sync.go",
		"### Round 1", "**Queue**: done, merged from https://github.com/kyle/x/pull/3", "review note: consider batching",
		"The check said it was not met: nothing replays the queue",
		"**Replay**: the run failed at its fix step: verify.sh still fails after 10 fixes",
		"**UI**: not started", "gofmt found sync.go", "Do not give the same brief again",
	} {
		if !strings.Contains(p, s) {
			t.Errorf("prompt lacks %q:\n%s", s, p)
		}
	}
	g.Failures = 0
	if strings.Contains(CheckPrompt(g, nil), "Do not give the same brief again") {
		t.Error("a round that ended well is told about a failure")
	}
}

func TestGoalTasks(t *testing.T) {
	g := aGoal()
	g.Step, g.BlockedAt, g.Why = GoalBlocked, GoalRuns, "two runs failed in a row"
	got := map[string]protocol.Task{}
	for _, task := range GoalTasks(g) {
		got[task.ID] = task
	}
	want := map[string]protocol.TaskStatus{"b1": protocol.TaskDone, "b2": protocol.TaskFailed, "b3": protocol.TaskCancelled, "c1": protocol.TaskDone, "blocked": protocol.TaskBlocked}
	if len(got) != len(want) {
		t.Fatalf("tasks = %+v", got)
	}
	for id, status := range want {
		if got[id].Status != status || got[id].BlockedBy == nil {
			t.Errorf("%s = %+v, want %s", id, got[id], status)
		}
	}
	if got["b1"].Note != "https://github.com/kyle/x/pull/3" || got["blocked"].Note != "two runs failed in a row" || got["b2"].Title != "Round 2: Replay" {
		t.Errorf("notes: %+v", got)
	}
}

func TestRoadmapAndPRBody(t *testing.T) {
	g := aGoal()
	g.Verdicts = append(g.Verdicts, Verdict{Round: 2, Met: true, Reason: "it all syncs"})
	r := Roadmap(g)
	for _, s := range []string{"# Goal", "## Done when", "## Round 1", "### 1. Queue", "## Round 2", "**Check:** not met. nothing replays the queue", "**Check:** met. it all syncs"} {
		if !strings.Contains(r, s) {
			t.Errorf("roadmap lacks %q:\n%s", s, r)
		}
	}
	body := GoalPRBody(g)
	for _, s := range []string{"worked as 2 runs", "Add offline sync", "it all syncs", "pull/3", ".nabu/goals/offline-sync-g1/roadmap.md", "<!-- nabu -->"} {
		if !strings.Contains(body, s) {
			t.Errorf("body lacks %q:\n%s", s, body)
		}
	}
}

func TestGoalSaveAndLoad(t *testing.T) {
	root := t.TempDir()
	g := aGoal()
	if err := g.Save(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(GoalsDir(root), "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := LoadGoals(root)
	if err != nil || len(all) != 1 || !reflect.DeepEqual(all[0], g) {
		t.Fatalf("loaded %+v, %v", all, err)
	}
	if none, err := LoadGoals(t.TempDir()); none != nil || err != nil {
		t.Errorf("an empty root: %v, %v", none, err)
	}
}

func TestCurrentBrief(t *testing.T) {
	g := aGoal()
	if i := g.Current(); i != -1 {
		t.Errorf("a round with nothing left: current = %d", i)
	}
	g.Briefs[2].State = BriefPending
	if i := g.Current(); i != 2 {
		t.Errorf("current = %d, want 2", i)
	}
}

func TestGoalRoundsDefault(t *testing.T) {
	c, err := LoadConfig(t.TempDir())
	if err != nil || c.GoalRounds != 5 {
		t.Errorf("goal rounds = %d, %v", c.GoalRounds, err)
	}
}

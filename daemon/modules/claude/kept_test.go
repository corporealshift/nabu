package claude

import (
	"context"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

var _ module.CompactionHook = (*Module)(nil)

// keptAfter runs AfterCompaction over a log, as the daemon does once a
// summary has been appended.
func keptAfter(t *testing.T, cfg module.Config, log ...protocol.Event) []module.ContextBlock {
	t.Helper()
	m := &Module{}
	if err := m.Init(&fakeHost{m: m}, cfg); err != nil {
		t.Fatal(err)
	}
	blocks, err := m.AfterCompaction(context.Background(), &logSession{log: log})
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}

// only is the one block a test expects, failing if there is not exactly one.
func only(t *testing.T, blocks []module.ContextBlock) string {
	t.Helper()
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Slot != "prefix" {
		t.Errorf("slot = %q, want prefix: it has to last until the next summary", blocks[0].Slot)
	}
	return blocks[0].Content
}

func TestAnAnswerIsKeptWordForWordThroughASummary(t *testing.T) {
	answer := "## What's going on\n\nThe file names should end in `.preferences_pb`.\n\n| a | b |\n|---|---|"
	got := only(t, keptAfter(t, module.Config{},
		user("fix the check"),
		said("looking"),
		said("still looking"),
		call("a1", "claude.ask", selfSource, map[string]string{"prompt": "q"}),
		result("a1", "claude.ask", "ok", answer),
		said("on it"),
	))
	for _, want := range []string{"## Claude's answers in this task", "after turn 2", answer} {
		if !strings.Contains(got, want) {
			t.Errorf("the block is missing %q:\n%s", want, got)
		}
	}
}

func TestEveryAnswerInTheStretchIsKeptOldestFirst(t *testing.T) {
	got := only(t, keptAfter(t, module.Config{},
		user("fix the check"),
		said("one"),
		call("m1", "claude.ask", "model", map[string]string{"prompt": "mine"}),
		result("m1", "claude.ask", "ok", "THE MODEL'S OWN ANSWER"),
		said("two"),
		said("three"),
		call("a1", "claude.ask", selfSource, map[string]string{"prompt": "auto"}),
		result("a1", "claude.ask", "ok", "THE AUTOMATIC ANSWER"),
	))
	first, second := strings.Index(got, "THE MODEL'S OWN ANSWER"), strings.Index(got, "THE AUTOMATIC ANSWER")
	if first < 0 || second < 0 {
		t.Fatalf("both answers belong in the block:\n%s", got)
	}
	if first > second {
		t.Error("the older answer comes first")
	}
	if !strings.Contains(got, "Answer 1, after turn 1") || !strings.Contains(got, "Answer 2, after turn 3") {
		t.Errorf("each answer says which it is and when it came:\n%s", got)
	}
}

func TestOnlyThisStretchsAnswersThatWorkedAreKept(t *testing.T) {
	got := only(t, keptAfter(t, module.Config{},
		user("first task"),
		call("o1", "claude.ask", selfSource, map[string]string{"prompt": "old"}),
		result("o1", "claude.ask", "ok", "AN ANSWER TO THE FIRST TASK"),
		user("now this"),
		said("one"),
		call("f1", "claude.ask", "model", map[string]string{"prompt": "q"}),
		result("f1", "claude.ask", "error", "claude timed out after 10m0s"),
		call("r1", "read", "model", map[string]string{"path": "x.kt"}),
		result("r1", "read", "ok", "NOT CLAUDE"),
		call("a1", "claude.ask", selfSource, map[string]string{"prompt": "auto"}),
		result("a1", "claude.ask", "ok", "THE ANSWER THAT COUNTS"),
	))
	if !strings.Contains(got, "THE ANSWER THAT COUNTS") {
		t.Errorf("this stretch's answer belongs in the block:\n%s", got)
	}
	for _, not := range []string{"AN ANSWER TO THE FIRST TASK", "timed out", "NOT CLAUDE"} {
		if strings.Contains(got, not) {
			t.Errorf("%q does not belong in the block:\n%s", not, got)
		}
	}
	if !strings.Contains(got, "Answer 1, after turn 1") {
		t.Errorf("numbering and turns count from this stretch:\n%s", got)
	}
}

func TestNoAnswersMeansNoBlock(t *testing.T) {
	for name, log := range map[string][]protocol.Event{
		"empty":       nil,
		"no asks":     {user("fix it"), said("done")},
		"only failed": {user("fix it"), call("f1", "claude.ask", "model", nil), result("f1", "claude.ask", "error", "no")},
		"before the person spoke": {
			call("a1", "claude.ask", selfSource, nil), result("a1", "claude.ask", "ok", "old"), user("new task"),
		},
	} {
		if blocks := keptAfter(t, module.Config{}, log...); len(blocks) != 0 {
			t.Errorf("%s: got %d blocks, want none", name, len(blocks))
		}
	}
}

func TestADisabledModuleKeepsNothing(t *testing.T) {
	blocks := keptAfter(t, module.Config{"enabled": false},
		user("fix it"), call("a1", "claude.ask", selfSource, nil), result("a1", "claude.ask", "ok", "answer"))
	if len(blocks) != 0 {
		t.Errorf("got %d blocks from a disabled module", len(blocks))
	}
}

func TestTheNewestAnswerGetsTheRoomFirst(t *testing.T) {
	oldest := "OLDEST" + strings.Repeat("o", 1000)
	middle := "MIDDLE" + strings.Repeat("m", 8000)
	newest := "NEWEST" + strings.Repeat("é", 4000) // 8 KB, two bytes each
	got := only(t, keptAfter(t, module.Config{},
		user("fix it"),
		call("a1", "claude.ask", "model", nil), result("a1", "claude.ask", "ok", oldest),
		call("a2", "claude.ask", "model", nil), result("a2", "claude.ask", "ok", middle),
		call("a3", "claude.ask", "model", nil), result("a3", "claude.ask", "ok", newest),
	))
	if len(got) > keptMax {
		t.Errorf("the block is %d bytes, over its %d cap", len(got), keptMax)
	}
	if !strings.Contains(got, newest) {
		t.Error("the newest answer is kept whole")
	}
	if !strings.Contains(got, "MIDDLE") || strings.Contains(got, middle) || !strings.Contains(got, ellipsis) {
		t.Error("the older answer is cut to what is left")
	}
	if strings.Contains(got, "OLDEST") {
		t.Error("with no room left the oldest is dropped")
	}
	if !strings.Contains(got, "1 earlier answer did not fit") {
		t.Errorf("a dropped answer is counted:\n%s", got[:min(len(got), 600)])
	}
	if strings.Index(got, "MIDDLE") > strings.Index(got, "NEWEST") {
		t.Error("what is kept is still oldest first")
	}
}

func TestOneHugeAnswerIsCutToTheCap(t *testing.T) {
	huge := strings.Repeat("é", 20_000)
	got := only(t, keptAfter(t, module.Config{},
		user("fix it"), call("a1", "claude.ask", "model", nil), result("a1", "claude.ask", "ok", huge)))
	if len(got) > keptMax {
		t.Errorf("the block is %d bytes, over its %d cap", len(got), keptMax)
	}
	if !strings.Contains(got, "Answer 1") || !strings.HasSuffix(strings.TrimSpace(got), ellipsis) {
		t.Error("a single answer over the cap is cut, not dropped")
	}
	if strings.Contains(got, "did not fit") {
		t.Error("nothing was dropped")
	}
}

func TestBeforeCompactionAsksTheSummaryToKeepNothing(t *testing.T) {
	m := &Module{}
	if err := m.Init(&fakeHost{m: m}, module.Config{}); err != nil {
		t.Fatal(err)
	}
	s := &logSession{log: []protocol.Event{user("x"), call("a1", "claude.ask", "model", nil), result("a1", "claude.ask", "ok", "answer")}}
	if p := m.BeforeCompaction(context.Background(), s, module.Range{}); len(p) != 0 {
		t.Errorf("preserve = %q: the answers go back word for word afterwards, not through the summary", p)
	}
}

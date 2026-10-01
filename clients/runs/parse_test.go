package runs

import (
	"reflect"
	"strings"
	"testing"
)

const tasksMD = `# Tasks

Some intro text.

- [ ] Add the cache type
  A read-through cache in reader/cache.go.
  Unit tests for get and put.
- [x] Wire it in
* [X] Star bullets count too
    - [ ] nested items are tasks as well

- [ ] Document it
`

func TestParseTasks(t *testing.T) {
	got := ParseTasks(tasksMD)
	var titles []string
	var done []bool
	for _, t := range got {
		titles = append(titles, t.Title)
		done = append(done, t.Done)
	}
	if want := []string{"Add the cache type", "Wire it in", "Star bullets count too", "nested items are tasks as well", "Document it"}; !reflect.DeepEqual(titles, want) {
		t.Errorf("titles = %q", titles)
	}
	if want := []bool{false, true, true, false, false}; !reflect.DeepEqual(done, want) {
		t.Errorf("done = %v", done)
	}
	if got[0].Detail != "A read-through cache in reader/cache.go.\nUnit tests for get and put." {
		t.Errorf("detail = %q", got[0].Detail)
	}
	if next, left := NextTask(got); next != 0 || left != 3 {
		t.Errorf("NextTask = %d, %d", next, left)
	}
	if len(ParseTasks("no tasks here\n- a plain bullet")) != 0 {
		t.Error("lines without a checkbox are tasks")
	}
	if _, left := NextTask(nil); left != 0 {
		t.Error("NextTask of nothing")
	}
}

func TestTickAndAppendTasks(t *testing.T) {
	md := TickTask(tasksMD, 3)
	if !strings.Contains(md, "    - [x] nested items are tasks as well") || !strings.Contains(md, "- [ ] Add the cache type") {
		t.Errorf("tick:\n%s", md)
	}
	if TickTask(tasksMD, 9) != tasksMD {
		t.Error("ticking a task that does not exist changed the file")
	}
	md = AppendTasks(md, []Blocker{{Title: "Handle a nil reader", Detail: "Get panics on a nil reader.\nAdd a test."}})
	tasks := ParseTasks(md)
	last := tasks[len(tasks)-1]
	if last.Title != "Handle a nil reader" || last.Done || last.Detail != "Get panics on a nil reader.\nAdd a test." {
		t.Errorf("appended = %+v", last)
	}
	if next, _ := NextTask(tasks); tasks[next].Title != "Add the cache type" {
		t.Errorf("next = %q", tasks[next].Title)
	}
}

func TestParseRewrite(t *testing.T) {
	plan := "# Plan\n\n```go\nfunc f() {}\n```\n"
	tests := []struct {
		name, answer string
		text         string
		changed, ok  bool
	}{
		{name: "a rewrite, fences and all", answer: "Some changes.\n" + Begin(PlanFile) + "\n" + plan + End(PlanFile) + "\nThat's it.",
			text: plan, changed: true, ok: true},
		{name: "the sentinel", answer: "NO CHANGES", ok: true},
		{name: "the sentinel in bold", answer: "Looks right to me.\n\n**NO CHANGES**", ok: true},
		{name: "a rewrite wins over the sentinel", answer: "NO CHANGES to the approach, but\n" + Begin(PlanFile) + "\nnew\n" + End(PlanFile),
			text: "new\n", changed: true, ok: true},
		{name: "neither", answer: "The plan is mostly fine but I would reorder steps."},
		{name: "an unterminated rewrite", answer: Begin(PlanFile) + "\nhalf a plan"},
		{name: "crlf", answer: Begin(PlanFile) + "\r\nline\r\n" + End(PlanFile), text: "line\n", changed: true, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, changed, ok := ParseRewrite(tt.answer, PlanFile, "NO CHANGES")
			if text != tt.text || changed != tt.changed || ok != tt.ok {
				t.Errorf("ParseRewrite = %q, %v, %v; want %q, %v, %v", text, changed, ok, tt.text, tt.changed, tt.ok)
			}
		})
	}
	if _, _, ok := ParseRewrite("APPROVED", VerifyFile, "APPROVED"); !ok {
		t.Error("APPROVED not read")
	}
}

func TestRefusal(t *testing.T) {
	if got := Refusal("REFUSED: the test is right; Median sorts in place and mutates its input."); got != "the test is right; Median sorts in place and mutates its input." {
		t.Errorf("refusal = %q", got)
	}
	if Refusal("fine") != "" {
		t.Error("no refusal read as one")
	}
}

func TestParseFinal(t *testing.T) {
	blockers, notes, ok := ParseFinal("Reviewed.\n```json\n{\"blockers\":[{\"title\":\"Median of an even count\",\"detail\":\"returns the upper middle\"},{\"title\":\" \"}],\"notes\":[\"could use generics\"]}\n```")
	if !ok || len(blockers) != 1 || blockers[0].Title != "Median of an even count" || len(notes) != 1 {
		t.Errorf("ParseFinal = %+v, %q, %v", blockers, notes, ok)
	}
	if b, _, ok := ParseFinal("```json\n{\"blockers\":[],\"notes\":[]}\n```"); !ok || len(b) != 0 {
		t.Error("a clean review")
	}
	for _, bad := range []string{"no blockers", "```json\n{\"blockers\": \n```"} {
		if _, _, ok := ParseFinal(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ text, want string }{
		{"Add a Median function, with tests!", "add-a-median-function-with-tests-pqrs"},
		{"", "run-pqrs"},
		{"Ünïcode — only", "n-code-only-pqrs"},
		{"make the reader cache its results and invalidate them when the file changes on disk", "make-the-reader-cache-its-results-and-pqrs"},
	}
	for _, tt := range tests {
		got := Slug(tt.text, "01M3ABCDPQRS")
		if got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.text, got, tt.want)
		}
		if len(got) > maxSlug+5 {
			t.Errorf("Slug(%q) is %d long", tt.text, len(got))
		}
	}
}

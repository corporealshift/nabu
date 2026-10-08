package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// ---- fakes -------------------------------------------------------------

type fakeHome struct {
	workspace, description, lastPrompt, transcript string
	labels                                         []string
	labelHistory                                   [][]string
	// parent is the goal a run's home was made under; tasks is the home's
	// task list.
	parent string
	tasks  []protocol.Task
	// context is the home's context size.
	context string
}

type fakeSession struct {
	workspace, parent, context string
	turns                      int
	goals, prompts             []string
	state                      protocol.SessionState
	// log is the session's messages: a user one per prompt, an assistant
	// one per finish.
	log []protocol.Event
	// met is the session's goal judged met.
	met bool
}

func (s *fakeSession) say(role, text string) {
	b, _ := json.Marshal(protocol.MessageData{Role: role, Content: text})
	s.log = append(s.log, protocol.Event{Type: protocol.EventMessage, Data: b})
}

type fakeDaemon struct {
	homes    map[string]*fakeHome
	sessions map[string]*fakeSession
	order    []string
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{homes: map[string]*fakeHome{}, sessions: map[string]*fakeSession{}}
}

func (d *fakeDaemon) Requested(_ context.Context, label string) ([]Home, error) {
	var out []Home
	for id, h := range d.homes {
		if slices.Contains(h.labels, label) {
			out = append(out, Home{ID: id, Workspace: h.workspace, Labels: slices.Clone(h.labels), LastPrompt: h.lastPrompt})
		}
	}
	return out, nil
}

func (d *fakeDaemon) Home(_ context.Context, id string) (Home, error) {
	h, ok := d.homes[id]
	if !ok {
		return Home{}, errors.New("no home")
	}
	return Home{ID: id, Workspace: h.workspace, Brief: h.description, Labels: slices.Clone(h.labels), LastPrompt: h.lastPrompt, Context: h.context}, nil
}

func (d *fakeDaemon) Transcript(_ context.Context, id string) (string, error) {
	return d.homes[id].transcript, nil
}

func (d *fakeDaemon) SetLabels(_ context.Context, id string, labels []string) error {
	h := d.homes[id]
	h.labels = slices.Clone(labels)
	h.labelHistory = append(h.labelHistory, h.labels)
	return nil
}

func (d *fakeDaemon) SetDescription(_ context.Context, id, text string) error {
	d.homes[id].description = text
	return nil
}

func (d *fakeDaemon) SetGoal(_ context.Context, id, condition string) error {
	if _, ok := d.homes[id]; ok {
		// A goal on the home starts it working in the checkout.
		return errors.New("a goal was set on a run's home")
	}
	d.sessions[id].goals = append(d.sessions[id].goals, condition)
	return nil
}

func (d *fakeDaemon) Create(_ context.Context, ws, parent string, turns int, size string) (string, error) {
	id := fmt.Sprintf("S%d", len(d.order)+1)
	d.order = append(d.order, id)
	d.sessions[id] = &fakeSession{workspace: ws, parent: parent, context: size, turns: turns, state: protocol.StateIdle}
	return id, nil
}

func (d *fakeDaemon) CreateHome(_ context.Context, ws, parent, description, size string) (string, error) {
	id := fmt.Sprintf("R%d", len(d.homes)+1)
	d.homes[id] = &fakeHome{workspace: ws, parent: parent, description: description, context: size}
	return id, nil
}

// UpdateTasks checks what the daemon would refuse.
func (d *fakeDaemon) UpdateTasks(_ context.Context, id string, tasks []protocol.Task) error {
	if len(tasks) == 0 {
		return errors.New("tasks must not be empty")
	}
	for _, t := range tasks {
		if t.ID == "" || t.Title == "" || t.BlockedBy == nil || t.Status == "" {
			return fmt.Errorf("task %+v would be refused", t)
		}
	}
	d.homes[id].tasks = slices.Clone(tasks)
	return nil
}

func (d *fakeDaemon) SendPrompt(_ context.Context, id, text string) error {
	s := d.sessions[id]
	s.prompts = append(s.prompts, text)
	s.say("user", text)
	s.state = protocol.StateRunning
	return nil
}

func (d *fakeDaemon) State(_ context.Context, id string) (protocol.State, error) {
	s := d.sessions[id]
	st := protocol.State{State: s.state}
	if s.met {
		st.Goal = &protocol.GoalData{Condition: s.goals[0], State: "met"}
	}
	return st, nil
}

func (d *fakeDaemon) Events(_ context.Context, id string) ([]protocol.Event, error) {
	return slices.Clone(d.sessions[id].log), nil
}

func (d *fakeDaemon) Stop(_ context.Context, id string) error {
	d.sessions[id].state = protocol.StateCompleted
	return nil
}

func (d *fakeDaemon) Close() {}

// last is the newest session.
func (d *fakeDaemon) last() (string, *fakeSession) {
	id := d.order[len(d.order)-1]
	return id, d.sessions[id]
}

type commit struct {
	sha, msg string
	files    []string
}

// fakeGit keeps a commit log over a real worktree directory, so the runner's
// file reads and writes are real and its git calls are recorded.
type fakeGit struct {
	log     []commit
	dirty   map[string]bool
	resets  []string
	pushed  []string
	fetches int
	// pushErrs are taken in order by Push; an empty queue pushes.
	pushErrs []error
	// from is the base of each worktree added, in order.
	from []string
	// history is every commit message, in every worktree: log starts again
	// with each worktree added.
	history []string
}

func (g *fakeGit) DefaultBranch(context.Context, string) (string, error) { return "main", nil }
func (g *fakeGit) Fetch(context.Context, string) error                   { g.fetches++; return nil }

func (g *fakeGit) AddBranchWorktree(_ context.Context, _, path, _, from string) error {
	g.from = append(g.from, from)
	g.log = []commit{{sha: "base"}}
	return os.MkdirAll(path, 0o755)
}

func (g *fakeGit) Head(context.Context, string) (string, error) { return g.log[len(g.log)-1].sha, nil }

func (g *fakeGit) Changed(_ context.Context, _, from string) ([]string, error) {
	var out []string
	after := false
	for _, c := range g.log {
		if after {
			out = append(out, c.files...)
		}
		if c.sha == from {
			after = true
		}
	}
	return out, nil
}

func (g *fakeGit) Dirty(context.Context, string) ([]string, error) {
	var out []string
	for p := range g.dirty {
		out = append(out, p)
	}
	return out, nil
}

func (g *fakeGit) ResetHard(_ context.Context, _, sha string) error {
	g.resets = append(g.resets, sha)
	for i, c := range g.log {
		if c.sha == sha {
			g.log = g.log[:i+1]
		}
	}
	g.dirty = nil
	return nil
}

func (g *fakeGit) commit(msg string, files ...string) {
	g.log = append(g.log, commit{sha: fmt.Sprintf("c%d", len(g.log)), msg: msg, files: files})
	g.history = append(g.history, msg)
	for _, f := range files {
		delete(g.dirty, f)
	}
}

func (g *fakeGit) Commit(_ context.Context, _, msg string, paths ...string) error {
	g.commit(msg, paths...)
	return nil
}

func (g *fakeGit) Remove(_ context.Context, dir, msg, path string) error {
	os.Remove(filepath.Join(dir, filepath.FromSlash(path)))
	g.commit(msg, path)
	return nil
}

func (g *fakeGit) Push(_ context.Context, _, branch string) error {
	if len(g.pushErrs) > 0 {
		err := g.pushErrs[0]
		g.pushErrs = g.pushErrs[1:]
		if err != nil {
			return err
		}
	}
	g.pushed = append(g.pushed, branch)
	return nil
}

func (g *fakeGit) Diffed(context.Context, string, string) ([]string, error) {
	return []string{"stats.go"}, nil
}

func (g *fakeGit) messages() []string {
	var out []string
	for _, c := range g.log[1:] {
		out = append(out, c.msg)
	}
	return out
}

type fakeClaude struct {
	missing bool
	// answers are taken in order for each kind; an empty queue gives the
	// kind's easy answer.
	answers map[string][]string
	asked   []string
	// prompts is every prompt, by kind.
	prompts map[string][]string
}

func (c *fakeClaude) Available() bool { return !c.missing }

func kindOf(prompt string) string {
	switch {
	case strings.Contains(prompt, "break it into briefs"):
		return "breakdown"
	case strings.Contains(prompt, "judge whether the goal is met"):
		return "check"
	case strings.Contains(prompt, "Report only blockers"):
		return "final"
	case strings.Contains(prompt, "Decide who is right"):
		return "revise"
	case strings.Contains(prompt, "defines done for the whole run"):
		return "verify"
	default:
		return "plan"
	}
}

func (c *fakeClaude) Ask(_ context.Context, _, prompt string) (string, error) {
	k := kindOf(prompt)
	c.asked = append(c.asked, k)
	if c.prompts == nil {
		c.prompts = map[string][]string{}
	}
	c.prompts[k] = append(c.prompts[k], prompt)
	if q := c.answers[k]; len(q) > 0 {
		c.answers[k] = q[1:]
		if q[0] == "ERROR" {
			return "", errors.New("claude timed out")
		}
		return q[0], nil
	}
	return map[string]string{
		"plan":   "NO CHANGES",
		"verify": "APPROVED",
		"revise": "REFUSED: the check is right.",
		"final":  "```json\n{\"blockers\":[],\"notes\":[\"consider generics\"]}\n```",
		"breakdown": "```json\n{\"done_when\":[\"stats has Median and Mode\"],\"briefs\":[" +
			"{\"title\":\"Median\",\"brief\":\"Add a Median function to stats.\"}," +
			"{\"title\":\"Mode\",\"brief\":\"Add a Mode function to stats.\"}]}\n```",
		"check": "```json\n{\"met\":true,\"reason\":\"both are there, tested\"}\n```",
	}[k], nil
}

type fakeShell struct {
	// results are taken in order; an empty queue passes.
	results []bool
	runs    int
}

func (s *fakeShell) Verify(context.Context, string, string, time.Duration) (bool, string, error) {
	s.runs++
	if len(s.results) == 0 {
		return true, "ok", nil
	}
	r := s.results[0]
	s.results = s.results[1:]
	if r {
		return true, "ok", nil
	}
	return false, "FAIL TestMedian: got 2, want 2.5", nil
}

type poll struct {
	state  string
	checks []Check
	err    error
}

type fakeGH struct {
	prs []string
	// polls are taken in order by PRChecks; an empty queue is an open PR
	// whose one check passed.
	polls []poll
	logs  []string
	// issueComments are "#n: body".
	issueComments []string
	// existing is a PR already open for the run's branch, by number; zero
	// means none. edits are "n|title|label|body".
	existing int
	edits    []string
	// merged is each PR merged; mergeErrs are taken in order by MergePR.
	merged    []int
	mergeErrs []error
}

func (g *fakeGH) MergePR(_ context.Context, _ string, n int) error {
	if len(g.mergeErrs) > 0 {
		err := g.mergeErrs[0]
		g.mergeErrs = g.mergeErrs[1:]
		if err != nil {
			return err
		}
	}
	g.merged = append(g.merged, n)
	return nil
}

func (g *fakeGH) OpenPRFor(context.Context, string, string) (int, string, bool, error) {
	if g.existing == 0 {
		return 0, "", false, nil
	}
	return g.existing, fmt.Sprintf("https://github.com/kyle/x/pull/%d", g.existing), true, nil
}

func (g *fakeGH) EditPR(_ context.Context, _ string, n int, title, body, label string) error {
	g.edits = append(g.edits, strings.Join([]string{fmt.Sprint(n), title, label, body}, "|"))
	return nil
}

func (g *fakeGH) CommentIssue(_ context.Context, _ string, n int, body string) error {
	g.issueComments = append(g.issueComments, fmt.Sprintf("#%d: %s", n, body))
	return nil
}

func (g *fakeGH) PRChecks(context.Context, string, int) (string, []Check, error) {
	if len(g.polls) == 0 {
		return "OPEN", []Check{{Name: "build", State: "SUCCESS"}}, nil
	}
	p := g.polls[0]
	g.polls = g.polls[1:]
	return p.state, p.checks, p.err
}

func (g *fakeGH) FailedLog(_ context.Context, _, id string) (string, error) {
	g.logs = append(g.logs, id)
	return "log of run " + id + ": gofmt -l found stats.go", nil
}

var (
	pending = poll{state: "OPEN", checks: []Check{{Name: "build", State: "IN_PROGRESS"}}}
	failing = poll{state: "OPEN", checks: []Check{
		{Name: "lint", State: "FAILURE", Link: "https://github.com/kyle/x/actions/runs/42/job/1"},
		{Name: "lint (windows)", State: "FAILURE", Link: "https://github.com/kyle/x/actions/runs/42/job/2"},
		{Name: "build", State: "SUCCESS"}}}
)

func (g *fakeGH) CreatePR(_ context.Context, _, base, head, title, body, label string) (int, string, error) {
	g.prs = append(g.prs, strings.Join([]string{base, head, title, label, body}, "|"))
	return 7, "https://github.com/kyle/x/pull/7", nil
}

// ---- rig ---------------------------------------------------------------

type rig struct {
	t      *testing.T
	rn     *Runner
	d      *fakeDaemon
	git    *fakeGit
	claude *fakeClaude
	shell  *fakeShell
	gh     *fakeGH
	log    bytes.Buffer
	now    time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	g := &rig{t: t, d: newFakeDaemon(), git: &fakeGit{dirty: map[string]bool{}}, claude: &fakeClaude{answers: map[string][]string{}},
		shell: &fakeShell{}, gh: &fakeGH{}, now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	g.rn = g.runner(t.TempDir())
	return g
}

func (g *rig) runner(root string) *Runner {
	cfg, _ := LoadConfig(root)
	return &Runner{Cfg: cfg, Root: root, Git: g.git, GH: g.gh, Claude: g.claude, Shell: g.shell,
		Now: func() time.Time { return g.now }, Log: &g.log}
}

// ask is /run on a home: a goal when there is text, and the label.
func (g *rig) ask(id, brief string) {
	h := g.d.homes[id]
	if h == nil {
		h = &fakeHome{workspace: g.t.TempDir(), lastPrompt: "add a median function", transcript: "user: add a median function\n"}
		g.d.homes[id] = h
	}
	if brief != "" {
		h.description = brief
	}
	h.labels = withRun(h.labels, LabelRequested)
}

// tick is one poll with one slot: advance, then start what fits.
func (g *rig) tick() {
	g.t.Helper()
	if err := g.rn.Advance(context.Background(), g.d); err != nil {
		g.t.Fatalf("advance: %v\nlog:\n%s", err, g.log.String())
	}
	if free := g.rn.Cfg.MaxJobs - g.rn.Busy(); free > 0 {
		if _, err := g.rn.Start(context.Background(), g.d, free); err != nil {
			g.t.Fatalf("start: %v", err)
		}
	}
}

func (g *rig) run(id string) *Run { return g.rn.runs[id] }

// finish has the newest session write files, commit them, and end its turn.
func (g *rig) finish(files map[string]string) {
	g.t.Helper()
	_, s := g.d.last()
	var paths []string
	for p, text := range files {
		full := filepath.Join(s.workspace, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			g.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			g.t.Fatal(err)
		}
		paths = append(paths, p)
	}
	slices.Sort(paths)
	if len(paths) > 0 {
		g.git.commit("session: "+strings.Join(paths, ","), paths...)
	}
	s.say("assistant", "done")
	s.state = protocol.StateIdle
}

func (g *rig) file(r *Run, name string) string { return r.File(name) }

const twoTasks = "- [ ] Add Median\n  In stats.go.\n- [ ] Test Median\n  In stats_test.go.\n"

// planned takes a run with a text brief as far as its first work session.
func (g *rig) planned(id string) *Run {
	g.t.Helper()
	return g.plannedWith(id, "# Plan\n")
}

// plannedWith is planned, with the plan session writing plan.
func (g *rig) plannedWith(id, plan string) *Run {
	g.t.Helper()
	g.ask(id, "Add a Median function to stats")
	g.tick()
	r := g.run(id)
	g.finish(map[string]string{r.File(PlanFile): plan})
	g.tick()
	g.finish(map[string]string{r.File(TasksFile): twoTasks})
	g.tick()
	g.finish(map[string]string{r.File(VerifyFile): "go test ./...\n"})
	g.tick()
	if r.Step != StepWork {
		g.t.Fatalf("after planning the run is at %q", r.Step)
	}
	return r
}

// ---- tests -------------------------------------------------------------

func TestAWholeRun(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false} // verify-review's run before any work fails, as it should
	r := g.planned("H1")

	g.finish(map[string]string{"stats.go": "package stats\n"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "package stats\n"})
	g.tick()

	if r.Step != StepDone || r.PRURL == "" {
		t.Fatalf("run ended at %q (%s)\nlog:\n%s", r.Step, r.Why, g.log.String())
	}
	want := []string{
		"run: brief", "session: " + r.File(PlanFile), "session: " + r.File(TasksFile), "session: " + r.File(VerifyFile),
		"session: stats.go", "run: task 1 done", "session: stats_test.go", "run: task 2 done",
	}
	if got := g.git.messages(); !slices.Equal(got, want) {
		t.Errorf("commits =\n %q\nwant\n %q", got, want)
	}
	for id, s := range g.d.sessions {
		if s.parent != "H1" || s.workspace != r.Worktree {
			t.Errorf("session %s: parent %q, workspace %q", id, s.parent, s.workspace)
		}
	}
	// plan, tasks, verify, then two work sessions with goals.
	if len(g.d.order) != 5 {
		t.Fatalf("sessions = %d", len(g.d.order))
	}
	if s := g.d.sessions["S1"]; s.turns != 0 || len(s.goals) != 0 || !strings.Contains(s.prompts[0], r.File(PlanFile)) {
		t.Errorf("plan session: %+v", s)
	}
	if s := g.d.sessions["S4"]; s.turns != 0 || len(s.goals) != 1 || !strings.Contains(s.prompts[0], "task 1 of 2") {
		t.Errorf("first work session: %+v", s)
	}
	if s := g.d.sessions["S5"]; !strings.Contains(s.prompts[0], "task 2 of 2") || !strings.Contains(s.goals[0], "Test Median") {
		t.Errorf("second work session: %+v", s)
	}
	if !slices.Equal(g.claude.asked, []string{"plan", "verify", "final"}) {
		t.Errorf("claude was asked %q", g.claude.asked)
	}
	if len(g.gh.prs) != 1 || !strings.HasPrefix(g.gh.prs[0], "main|nabu/"+r.Slug+"|Add a Median function to stats|nabu|") ||
		!strings.Contains(g.gh.prs[0], "consider generics") || !strings.Contains(g.gh.prs[0], "<!-- nabu -->") {
		t.Errorf("pr = %q", g.gh.prs)
	}
	if !slices.Equal(g.git.pushed, []string{r.Branch}) {
		t.Errorf("pushed %q", g.git.pushed)
	}
	tasks, _ := g.rn.tasks(r)
	if _, left := NextTask(tasks); left != 0 {
		t.Errorf("%d tasks left unticked", left)
	}
	h := g.d.homes["H1"]
	if !slices.Equal(h.labels, []string{"run:done"}) {
		t.Errorf("home labels = %q", h.labels)
	}
	var steps []string
	for _, ls := range h.labelHistory {
		steps = append(steps, strings.Join(ls, ","))
	}
	for _, s := range []string{"run:setup", "run:plan", "run:tasks", "run:verify", "run:work", "run:done"} {
		if !slices.Contains(steps, s) {
			t.Errorf("the home was never labeled %s: %q", s, steps)
		}
	}
}

func TestAPlainRunWritesItsBrief(t *testing.T) {
	g := newRig(t)
	g.ask("H1", "")
	g.tick()
	r := g.run("H1")
	if r.Step != StepBrief || !strings.HasPrefix(r.Slug, "add-a-median-function-") {
		t.Fatalf("run = %+v", r)
	}
	if p := g.d.sessions["S1"].prompts[0]; !strings.Contains(p, "user: add a median function") {
		t.Errorf("brief prompt lacks the conversation:\n%s", p)
	}
	g.finish(map[string]string{r.File(BriefFile): "Add a Median function.\n"})
	g.tick()
	if r.Step != StepPlan || g.d.homes["H1"].description != "Add a Median function." {
		t.Errorf("step %q, home description %q", r.Step, g.d.homes["H1"].description)
	}
}

func TestASessionThatTouchesVerifyIsThrownAway(t *testing.T) {
	g := newRig(t)
	r := g.planned("H1")
	start := r.Start
	g.finish(map[string]string{"stats.go": "x", r.File(VerifyFile): "exit 0\n"})
	g.tick()
	if len(g.git.resets) != 1 || g.git.resets[0] != start {
		t.Fatalf("resets = %q, want back to %s", g.git.resets, start)
	}
	if r.Step != StepWork || r.Attempt != 1 || !strings.Contains(g.log.String(), "changed verify.sh") {
		t.Errorf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	g.finish(map[string]string{"stats.go": "x", r.File(VerifyFile): "exit 0\n"})
	g.tick()
	if r.Step != StepFailed || r.FailedAt != StepWork {
		t.Errorf("a second touch should fail the run: %+v", r)
	}
}

func TestCheckFailsThenAFixPasses(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false, false, false, true} // before work, then check, check, check
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()
	if r.Step != StepFix || r.Fixes != 1 {
		t.Fatalf("run = %+v", r)
	}
	_, s := g.d.last()
	if !strings.Contains(s.prompts[0], "FAIL TestMedian") || len(s.goals) != 1 {
		t.Errorf("fix session: %+v", s)
	}
	if h := g.d.homes["H1"].labels; !slices.Equal(h, []string{"run:fix", "run:attempt:1/10"}) {
		t.Errorf("home labels = %q", h)
	}
	g.finish(map[string]string{"stats.go": "y"})
	g.tick()
	g.finish(map[string]string{"stats.go": "z"})
	g.tick()
	if r.Step != StepDone || r.Fixes != 2 {
		t.Errorf("run = %+v\nlog:\n%s", r, g.log.String())
	}
}

func TestARevisionIsWrittenByClaude(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false, false}
	g.claude.answers["revise"] = []string{"The test name is wrong.\n" + Begin(VerifyFile) + "\ngo test -run TestMedianOf ./...\n" + End(VerifyFile)}
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick() // check fails; fix
	g.finish(map[string]string{r.File(RevisionFile): "The check runs TestMedian; the test is TestMedianOf."})
	g.tick() // revise, then check passes, final, pr

	if r.Step != StepDone || r.Revisions != 1 {
		t.Fatalf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	msgs := g.git.messages()
	i := slices.Index(msgs, "run: verify.sh revised by claude")
	if i < 0 || msgs[i+1] != "run: revision request answered" {
		t.Errorf("commits = %q", msgs)
	}
	if text, _ := g.rn.read(r, VerifyFile); text != "go test -run TestMedianOf ./...\n" {
		t.Errorf("verify.sh = %q", text)
	}
	if _, ok := g.rn.read(r, RevisionFile); ok {
		t.Error("the revision request was left in the worktree")
	}
}

func TestARefusedRevisionAdvisesTheNextFix(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false, false, false}
	g.claude.answers["revise"] = []string{"REFUSED: the check is right; Median sorts its input in place."}
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()
	g.finish(map[string]string{r.File(RevisionFile): "the check is wrong"})
	g.tick() // revise refuses, check fails again, next fix starts
	if r.Step != StepFix || r.Revisions != 1 {
		t.Fatalf("run = %+v", r)
	}
	_, s := g.d.last()
	if !strings.Contains(s.prompts[0], "Median sorts its input in place.") {
		t.Errorf("the refusal did not reach the next fix:\n%s", s.prompts[0])
	}
	if r.Advice != "" {
		t.Error("advice passed on is still kept")
	}
}

func TestBlockersBecomeTasksOnce(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.claude.answers["final"] = []string{"```json\n{\"blockers\":[{\"title\":\"Median of an even count\",\"detail\":\"averages the middle two\"}],\"notes\":[]}\n```"}
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()
	if r.Step != StepWork {
		t.Fatalf("blockers should send the run back to work: %+v", r)
	}
	_, s := g.d.last()
	if !strings.Contains(s.prompts[0], "Median of an even count") || !strings.Contains(s.prompts[0], "task 3 of 3") {
		t.Errorf("blocker task prompt:\n%s", s.prompts[0])
	}
	g.finish(map[string]string{"stats.go": "y"})
	g.tick()
	if r.Step != StepDone {
		t.Errorf("run = %+v", r)
	}
	if n := strings.Count(strings.Join(g.claude.asked, ","), "final"); n != 1 {
		t.Errorf("final review asked %d times", n)
	}
}

func TestTheFixCapFailsTheRunAndRunResumesIt(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	for range MaxFixes + 1 {
		g.shell.results = append(g.shell.results, false)
	}
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()
	for range MaxFixes {
		g.finish(map[string]string{"stats.go": "again"})
		g.tick()
	}
	if r.Step != StepFailed || r.FailedAt != StepFix {
		t.Fatalf("run = %+v", r)
	}
	if !slices.Equal(g.d.homes["H1"].labels, []string{"run:failed"}) {
		t.Errorf("home labels = %q", g.d.homes["H1"].labels)
	}
	g.ask("H1", "")
	g.tick() // resumed: check passes (queue empty), final, pr
	if r.Step != StepDone {
		t.Errorf("resumed run = %+v\nlog:\n%s", r, g.log.String())
	}
}

func TestARestartMidSessionCarriesOn(t *testing.T) {
	g := newRig(t)
	r := g.planned("H1")
	g.rn = g.runner(g.rn.Root) // a new process over the same state
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	if got := g.run("H1"); got.Step != StepWork || got.Task != 1 || r.Home != got.Home {
		t.Errorf("after restart: %+v", got)
	}
}

func TestAFailedSessionIsRetriedClean(t *testing.T) {
	g := newRig(t)
	g.ask("H1", "Add a Median function")
	g.tick()
	r := g.run("H1")
	_, s := g.d.last()
	s.state = protocol.StateBlocked
	g.tick()
	if r.Step != StepPlan || r.Attempt != 1 || len(g.git.resets) != 1 || len(g.d.order) != 2 {
		t.Errorf("run = %+v, resets %q, sessions %d", r, g.git.resets, len(g.d.order))
	}
}

func TestNoClaudeNoRun(t *testing.T) {
	g := newRig(t)
	g.claude.missing = true
	g.ask("H1", "Add x")
	g.tick()
	if g.run("H1") != nil || len(g.d.order) != 0 {
		t.Error("a run started without claude")
	}
	if !slices.Equal(g.d.homes["H1"].labels, []string{"run:failed"}) || !strings.Contains(g.log.String(), "claude CLI is not installed") {
		t.Errorf("labels %q, log %s", g.d.homes["H1"].labels, g.log.String())
	}
}

func TestClaudeFailuresAreRetriedAtTheNextPoll(t *testing.T) {
	g := newRig(t)
	// verify-review errors, then gives an answer that is neither form, then
	// approves: one try per poll.
	g.claude.answers["verify"] = []string{"ERROR", "I'm not sure about this script."}
	g.ask("H1", "Add a Median function")
	g.tick()
	run := g.run("H1")
	g.finish(map[string]string{run.File(PlanFile): "p"})
	g.tick()
	g.finish(map[string]string{run.File(TasksFile): twoTasks})
	g.tick()
	g.finish(map[string]string{run.File(VerifyFile): "v"})
	g.tick() // error: stays at verify-review
	if run.Step != StepVerifyReview || run.Attempt != 1 {
		t.Fatalf("run = %+v", run)
	}
	g.tick() // an unreadable answer: still there
	g.tick() // approved (queue empty)
	if run.Step != StepWork {
		t.Errorf("run = %+v\nlog:\n%s", run, g.log.String())
	}
}

// Seen live: a tasks session read the brief and the plan and ended its turn,
// twice, and the run failed. A session that ends without what its step needs
// is told so once, in the same session.
func TestASessionThatStopsShortIsToldOnce(t *testing.T) {
	g := newRig(t)
	g.ask("H1", "Add a Median function")
	g.tick()
	r := g.run("H1")
	g.finish(map[string]string{r.File(PlanFile): "p"})
	g.tick() // plan-review, then the tasks session
	g.finish(nil)
	g.tick()
	id, s := g.d.last()
	if len(s.prompts) != 2 || !strings.Contains(s.prompts[1], "without writing "+r.File(TasksFile)) {
		t.Fatalf("session %s prompts = %q", id, s.prompts)
	}
	if r.Step != StepTasks || r.Session != id || r.Attempt != 0 {
		t.Fatalf("a nudge is not a failure: %+v", r)
	}
	g.tick() // it has not answered the nudge yet: nothing happens
	if r.Step != StepTasks || len(s.prompts) != 2 {
		t.Fatalf("after a quiet tick: %+v, %d prompts", r, len(s.prompts))
	}
	g.finish(map[string]string{r.File(TasksFile): twoTasks})
	g.tick()
	if r.Step != StepVerify || len(g.d.order) != 3 {
		t.Errorf("run = %+v, sessions %d", r, len(g.d.order))
	}
}

func TestANudgeIsGivenOnlyOnce(t *testing.T) {
	g := newRig(t)
	g.ask("H1", "Add a Median function")
	g.tick()
	r := g.run("H1")
	g.finish(nil)
	g.tick() // nudged
	g.finish(nil)
	g.tick() // still nothing: a failed attempt, and a fresh session
	if r.Step != StepPlan || r.Attempt != 1 || len(g.d.order) != 2 {
		t.Errorf("run = %+v, sessions %d", r, len(g.d.order))
	}
	if _, s := g.d.last(); len(s.prompts) != 1 {
		t.Errorf("the retry was nudged before it began: %q", s.prompts)
	}
}

func TestAWorkSessionThatCommitsNothingIsToldOnce(t *testing.T) {
	g := newRig(t)
	r := g.planned("H1")
	g.finish(nil)
	g.tick()
	_, s := g.d.last()
	if len(s.prompts) != 2 || !strings.Contains(s.prompts[1], "without committing anything") {
		t.Fatalf("prompts = %q", s.prompts)
	}
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	if r.Step != StepWork || r.Task != 1 {
		t.Errorf("run = %+v", r)
	}
}

// Seen live: the verify session wrote the feature and its tests as well, so
// the check passed before any work. A planning step writes its own file only.
func TestAPlanningStepMayWriteOnlyItsFile(t *testing.T) {
	g := newRig(t)
	g.ask("H1", "Add a Median function")
	g.tick()
	r := g.run("H1")
	start := r.Start
	g.finish(map[string]string{r.File(PlanFile): "p", "stats.go": "func Median() {}"})
	g.tick()
	if r.Step != StepPlan || r.Attempt != 1 || len(g.git.resets) != 1 || g.git.resets[0] != start {
		t.Fatalf("run = %+v, resets %q", r, g.git.resets)
	}
	if !strings.Contains(g.log.String(), "may write only "+r.File(PlanFile)+", and the session also changed stats.go") {
		t.Errorf("log:\n%s", g.log.String())
	}
	if p := g.d.sessions["S1"].prompts[0]; !strings.Contains(p, "Write only "+r.File(PlanFile)) {
		t.Errorf("the plan prompt does not say so:\n%s", p)
	}
}

// Seen live: a task an earlier session had already done. The work session
// committed nothing and its goal was judged met; that task is done.
func TestATaskAlreadyDoneIsDone(t *testing.T) {
	g := newRig(t)
	r := g.planned("H1")
	_, s := g.d.last()
	s.met = true
	g.finish(nil)
	g.tick()
	if r.Step != StepWork || r.Task != 1 || len(g.git.resets) != 0 {
		t.Fatalf("run = %+v, resets %q", r, g.git.resets)
	}
	if len(s.prompts) != 1 {
		t.Errorf("a session whose goal was met was nudged: %q", s.prompts)
	}
	if msgs := g.git.messages(); msgs[len(msgs)-1] != "run: task 1 done" {
		t.Errorf("commits = %q", msgs)
	}
}

// opened takes a run to its open PR, with whatever CI polls the test queued.
func (g *rig) opened(id string) *Run {
	g.t.Helper()
	r := g.planned(id)
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()
	if r.PR == 0 {
		g.t.Fatalf("no PR: %+v\nlog:\n%s", r, g.log.String())
	}
	return r
}

func TestCIPendingThenPassing(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.gh.polls = []poll{pending, pending}
	r := g.opened("H1")
	if r.Step != StepCI || r.Attempt != 0 {
		t.Fatalf("run = %+v", r)
	}
	if !slices.Equal(g.d.homes["H1"].labels, []string{"run:ci"}) {
		t.Errorf("home labels = %q", g.d.homes["H1"].labels)
	}
	g.tick()
	if r.Step != StepCI {
		t.Fatalf("still pending, but the run is at %q", r.Step)
	}
	g.tick()
	if r.Step != StepDone {
		t.Errorf("run = %+v", r)
	}
}

func TestCIFailingIsFixedAndPushed(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.gh.polls = []poll{failing}
	r := g.opened("H1")
	if r.Step != StepCIFix || r.CIFixes != 1 {
		t.Fatalf("run = %+v", r)
	}
	if !slices.Equal(g.d.homes["H1"].labels, []string{"run:ci-fix", "run:attempt:1/5"}) {
		t.Errorf("home labels = %q", g.d.homes["H1"].labels)
	}
	_, s := g.d.last()
	for _, want := range []string{"### lint", "log of run 42: gofmt -l found stats.go", "### lint (windows)", "under another job of the same run"} {
		if !strings.Contains(s.prompts[0], want) {
			t.Errorf("ci-fix prompt lacks %q:\n%s", want, s.prompts[0])
		}
	}
	if len(g.gh.logs) != 1 || len(s.goals) != 1 || s.turns != 0 {
		t.Errorf("logs read %q, goals %q, turns %d", g.gh.logs, s.goals, s.turns)
	}
	g.finish(map[string]string{"stats.go": "gofmt'd"})
	g.tick() // check passes, push, ci passes
	if r.Step != StepDone || len(g.git.pushed) != 2 {
		t.Errorf("run = %+v, pushes %q\nlog:\n%s", r, g.git.pushed, g.log.String())
	}
}

func TestCIFixesAreCapped(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	for range MaxCIFixes + 1 {
		g.gh.polls = append(g.gh.polls, failing)
	}
	r := g.opened("H1")
	for range MaxCIFixes {
		g.finish(map[string]string{"stats.go": "again"})
		g.tick()
	}
	if r.Step != StepFailed || r.FailedAt != StepCIFix {
		t.Errorf("run = %+v", r)
	}
}

func TestCIMergedOrClosed(t *testing.T) {
	for state, want := range map[string]Step{"MERGED": StepDone, "CLOSED": StepFailed} {
		t.Run(state, func(t *testing.T) {
			g := newRig(t)
			g.shell.results = []bool{false}
			g.gh.polls = []poll{{state: state, checks: failing.checks}}
			if r := g.opened("H1"); r.Step != want {
				t.Errorf("run = %+v", r)
			}
		})
	}
}

func TestNoChecksAtAllIsDoneAfterAWait(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	none := poll{state: "OPEN"}
	g.gh.polls = []poll{none, none}
	r := g.opened("H1")
	if r.Step != StepCI {
		t.Fatalf("run = %+v", r)
	}
	g.now = g.now.Add(noChecksWait + time.Second)
	g.tick()
	if r.Step != StepDone {
		t.Errorf("run = %+v", r)
	}
}

// A push the remote refuses means the branch moved under the run. The run
// takes the moved head and watches its checks.
func TestARejectedPushFollowsTheMovedBranch(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.gh.polls = []poll{failing}
	g.git.pushErrs = []error{nil, errors.New("! [rejected] nabu/x -> nabu/x (fetch first)")}
	r := g.opened("H1")
	g.finish(map[string]string{"stats.go": "fixed"})
	g.tick()
	if r.Step != StepDone || !slices.Contains(g.git.resets, "origin/"+r.Branch) || g.git.fetches < 2 {
		t.Errorf("run = %+v, resets %q, fetches %d", r, g.git.resets, g.git.fetches)
	}
	if !strings.Contains(g.log.String(), "moved while it was being fixed") {
		t.Errorf("log:\n%s", g.log.String())
	}
}

func TestACIPollThatErrorsIsRetried(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.gh.polls = []poll{{err: errors.New("gh: HTTP 502")}}
	r := g.opened("H1")
	if r.Step != StepCI || r.Attempt != 1 {
		t.Fatalf("run = %+v", r)
	}
	g.tick()
	if r.Step != StepDone {
		t.Errorf("run = %+v", r)
	}
}

// askIssue is what the watcher does for a labeled issue: a home with the
// brief as its description, labeled for the runner and with the issue.
func (g *rig) askIssue(id, brief string) {
	g.ask(id, brief)
	h := g.d.homes[id]
	h.labels = append(h.labels, "issue:kyle/x/12")
}

func TestAnIssueRun(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.askIssue("H1", "Add a Median function\n\nIt should return a float64.\n\n(From issue kyle/x#12.)")
	g.tick()
	r := g.run("H1")
	if r.Issue != 12 || !strings.HasPrefix(r.Slug, "issue-12-add-a-median-function-") {
		t.Fatalf("run = %+v", r)
	}
	g.finish(map[string]string{r.File(PlanFile): "p"})
	g.tick()
	g.finish(map[string]string{r.File(TasksFile): twoTasks})
	g.tick()
	g.finish(map[string]string{r.File(VerifyFile): "v"})
	g.tick()
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()
	if r.Step != StepDone {
		t.Fatalf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	if !strings.Contains(g.gh.prs[0], "|nabu|Closes #12\n\n") {
		t.Errorf("pr body does not close the issue: %q", g.gh.prs[0])
	}
	if len(g.gh.issueComments) != 1 || !strings.HasPrefix(g.gh.issueComments[0], "#12: ") ||
		!strings.Contains(g.gh.issueComments[0], r.PRURL) || !strings.HasSuffix(g.gh.issueComments[0], "<!-- nabu -->") {
		t.Errorf("issue comments = %q", g.gh.issueComments)
	}
	g.tick()
	if len(g.gh.issueComments) != 1 {
		t.Error("the issue was told twice")
	}
}

func TestAFailedIssueRunSaysWhereAndResumesWithANewBrief(t *testing.T) {
	g := newRig(t)
	g.askIssue("H1", "Add a Median function")
	g.tick()
	r := g.run("H1")
	g.finish(nil)
	g.tick() // nudged
	g.finish(nil)
	g.tick() // retried
	g.finish(nil)
	g.tick() // nudged
	g.finish(nil)
	g.tick() // failed at plan
	if r.Step != StepFailed || len(g.gh.issueComments) != 1 || !strings.Contains(g.gh.issueComments[0], "at the `plan` step") ||
		!strings.Contains(g.gh.issueComments[0], "Edit this issue, or comment on it") {
		t.Fatalf("run %+v, comments %q", r, g.gh.issueComments)
	}

	// The issue is edited; the watcher sets the new brief and asks again.
	g.d.homes["H1"].description = "Add a Median function, returning a float64."
	g.ask("H1", "")
	g.tick()
	if r.Step != StepPlan || r.Brief != "Add a Median function, returning a float64." || r.Reported {
		t.Fatalf("resumed run = %+v", r)
	}
	if msgs := g.git.messages(); msgs[len(msgs)-1] != "run: brief updated from the issue" {
		t.Errorf("commits = %q", msgs)
	}
	if text, _ := g.rn.read(r, BriefFile); text != "Add a Median function, returning a float64.\n" {
		t.Errorf("brief.md = %q", text)
	}
}

func TestIssueOf(t *testing.T) {
	for _, tt := range []struct {
		labels []string
		want   int
	}{
		{nil, 0},
		{[]string{"run:requested"}, 0},
		{[]string{"run:requested", "issue:kyle/x/12"}, 12},
		{[]string{"issue:kyle/x/zero"}, 0},
	} {
		if got := IssueOf(tt.labels); got != tt.want {
			t.Errorf("IssueOf(%q) = %d, want %d", tt.labels, got, tt.want)
		}
	}
}

// Seen live: a session pushed its branch and opened its own PR, and the run's
// pr step failed on "a pull request already exists". The guard now stops a
// session doing that, but a PR open for the run's branch is the run's PR: it is
// taken over, with the run's title, body and label, and the run goes on.
func TestAnOpenPRForTheBranchIsTakenOver(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.gh.existing = 5
	r := g.opened("H1")
	if len(g.gh.prs) != 0 {
		t.Errorf("a second PR was opened: %q", g.gh.prs)
	}
	if len(g.gh.edits) != 1 || !strings.HasPrefix(g.gh.edits[0], "5|Add a Median function to stats|nabu|") ||
		!strings.Contains(g.gh.edits[0], "<!-- nabu -->") {
		t.Errorf("edits = %q", g.gh.edits)
	}
	if r.PR != 5 || r.PRURL != "https://github.com/kyle/x/pull/5" || r.Step != StepDone {
		t.Errorf("run = %+v", r)
	}
	if !strings.Contains(g.log.String(), "took over https://github.com/kyle/x/pull/5") {
		t.Errorf("log:\n%s", g.log.String())
	}
}

// A goal's run starts from the goal's branch, opens its PR against it with no
// label, and is merged into it once CI is green.
func TestAGoalsRunIsMergedIntoTheGoalBranch(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.d.homes["H1"] = &fakeHome{workspace: t.TempDir(), description: "Add a Median function to stats"}
	if err := g.rn.load(); err != nil {
		t.Fatal(err)
	}
	r := &Run{Home: "H1", Workspace: g.d.homes["H1"].workspace, Brief: "Add a Median function to stats", Slug: "median",
		Branch: "nabu/median", Base: "nabu/goal-stats", Goal: "G1", Worktree: filepath.Join(g.rn.Root, "runner", "worktrees", "median"),
		Step: StepSetup, Started: g.now}
	g.rn.runs["H1"] = r
	g.tick()
	g.finish(map[string]string{r.File(PlanFile): "# Plan\n"})
	g.tick()
	g.finish(map[string]string{r.File(TasksFile): twoTasks})
	g.tick()
	g.finish(map[string]string{r.File(VerifyFile): "go test ./...\n"})
	g.tick()
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()

	if r.Step != StepDone {
		t.Fatalf("run ended at %q (%s)\nlog:\n%s", r.Step, r.Why, g.log.String())
	}
	if !slices.Equal(g.git.from, []string{"nabu/goal-stats"}) {
		t.Errorf("worktree from %q, want the goal branch", g.git.from)
	}
	if len(g.gh.prs) != 1 || !strings.HasPrefix(g.gh.prs[0], "nabu/goal-stats|nabu/median|Add a Median function to stats||") {
		t.Errorf("pr = %q, want against the goal branch with no label", g.gh.prs)
	}
	if !slices.Equal(g.gh.merged, []int{7}) {
		t.Errorf("merged %v", g.gh.merged)
	}
}

// A run with no goal is never merged.
func TestARunIsNeverMergedByTheRunner(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	if r := g.opened("H1"); r.Step != StepDone || len(g.gh.merged) != 0 {
		t.Errorf("run at %q, merged %v", r.Step, g.gh.merged)
	}
}

const decidedPlan = "# Plan\n\nSort, then sweep.\n\n## Decisions\n\n- Even-length input: the mean of the two middle values, as statistics users expect.\n"

// A run settles what the brief leaves open; the PR says what it chose, so the
// owner finds out from the description rather than from a bug.
func TestThePRReportsTheRunsDecisions(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.plannedWith("H1", decidedPlan)
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()
	if len(g.gh.prs) != 1 {
		t.Fatalf("prs = %q\nlog:\n%s", g.gh.prs, g.log.String())
	}
	body := g.gh.prs[0]
	decisions := strings.Index(body, "## Decisions this run made\n\n- Even-length input: the mean of the two middle values")
	notes := strings.Index(body, "## Notes from the final review")
	if decisions < 0 || notes < decisions || !strings.Contains(body, "To change one, say so in a comment on this PR.") {
		t.Errorf("body:\n%s", body)
	}
}

func TestAPlanWithNoDecisionsSaysSo(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.opened("H1")
	if !strings.Contains(g.gh.prs[0], "## Decisions this run made\n\nThe plan recorded no open decisions.\n") {
		t.Errorf("body:\n%s", g.gh.prs[0])
	}
}

// The review may change a decision, but only in the open: a rewrite that
// drops the section gets the plan's decisions back.
func TestAReviewThatDropsTheDecisionsKeepsThem(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.claude.answers = map[string][]string{"plan": {Begin(PlanFile) + "\n# Plan\n\nSort with slices.SortFunc.\n" + End(PlanFile)}}
	r := g.plannedWith("H1", decidedPlan)
	plan, _ := g.rn.read(r, PlanFile)
	if !strings.Contains(plan, "Sort with slices.SortFunc.") || !strings.Contains(plan, "left this section out") ||
		!strings.Contains(plan, "- Even-length input: the mean of the two middle values") {
		t.Errorf("plan.md:\n%s", plan)
	}
}

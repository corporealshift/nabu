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
	workspace, goal, lastPrompt, transcript string
	labels                                  []string
	labelHistory                            [][]string
}

type fakeSession struct {
	workspace, parent string
	turns             int
	goals, prompts    []string
	state             protocol.SessionState
	answered          bool
}

type fakeDaemon struct {
	homes    map[string]*fakeHome
	sessions map[string]*fakeSession
	order    []string
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{homes: map[string]*fakeHome{}, sessions: map[string]*fakeSession{}}
}

func (d *fakeDaemon) Requested(context.Context) ([]Home, error) {
	var out []Home
	for id, h := range d.homes {
		if slices.Contains(h.labels, LabelRequested) {
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
	return Home{ID: id, Workspace: h.workspace, Goal: h.goal, Labels: slices.Clone(h.labels), LastPrompt: h.lastPrompt}, nil
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

func (d *fakeDaemon) SetGoal(_ context.Context, id, condition string) error {
	if h, ok := d.homes[id]; ok {
		h.goal = condition
		return nil
	}
	d.sessions[id].goals = append(d.sessions[id].goals, condition)
	return nil
}

func (d *fakeDaemon) Create(_ context.Context, ws, parent string, turns int) (string, error) {
	id := fmt.Sprintf("S%d", len(d.order)+1)
	d.order = append(d.order, id)
	d.sessions[id] = &fakeSession{workspace: ws, parent: parent, turns: turns, state: protocol.StateIdle}
	return id, nil
}

func (d *fakeDaemon) SendPrompt(_ context.Context, id, text string) error {
	s := d.sessions[id]
	s.prompts = append(s.prompts, text)
	s.state = protocol.StateRunning
	return nil
}

func (d *fakeDaemon) State(_ context.Context, id string) (protocol.State, error) {
	return protocol.State{State: d.sessions[id].state}, nil
}

func (d *fakeDaemon) Events(_ context.Context, id string) ([]protocol.Event, error) {
	if !d.sessions[id].answered {
		return nil, nil
	}
	b, _ := json.Marshal(protocol.MessageData{Role: "assistant", Content: "done"})
	return []protocol.Event{{Type: protocol.EventMessage, Data: b}}, nil
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
}

func (g *fakeGit) DefaultBranch(context.Context, string) (string, error) { return "main", nil }
func (g *fakeGit) Fetch(context.Context, string) error                   { g.fetches++; return nil }

func (g *fakeGit) AddBranchWorktree(_ context.Context, _, path, _, _ string) error {
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
}

func (c *fakeClaude) Available() bool { return !c.missing }

func kindOf(prompt string) string {
	switch {
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

type fakeGH struct{ prs []string }

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
}

func newRig(t *testing.T) *rig {
	t.Helper()
	g := &rig{t: t, d: newFakeDaemon(), git: &fakeGit{dirty: map[string]bool{}}, claude: &fakeClaude{answers: map[string][]string{}},
		shell: &fakeShell{}, gh: &fakeGH{}}
	g.rn = g.runner(t.TempDir())
	return g
}

func (g *rig) runner(root string) *Runner {
	cfg, _ := LoadConfig(root)
	return &Runner{Cfg: cfg, Root: root, Git: g.git, GH: g.gh, Claude: g.claude, Shell: g.shell,
		Now: func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) }, Log: &g.log}
}

// ask is /run on a home: a goal when there is text, and the label.
func (g *rig) ask(id, brief string) {
	h := g.d.homes[id]
	if h == nil {
		h = &fakeHome{workspace: g.t.TempDir(), lastPrompt: "add a median function", transcript: "user: add a median function\n"}
		g.d.homes[id] = h
	}
	if brief != "" {
		h.goal = brief
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
	s.answered, s.state = true, protocol.StateIdle
}

func (g *rig) file(r *Run, name string) string { return r.File(name) }

const twoTasks = "- [ ] Add Median\n  In stats.go.\n- [ ] Test Median\n  In stats_test.go.\n"

// planned takes a run with a text brief as far as its first work session.
func (g *rig) planned(id string) *Run {
	g.t.Helper()
	g.ask(id, "Add a Median function to stats")
	g.tick()
	r := g.run(id)
	g.finish(map[string]string{r.File(PlanFile): "# Plan\n"})
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
	if s := g.d.sessions["S1"]; s.turns != 30 || len(s.goals) != 0 || !strings.Contains(s.prompts[0], r.File(PlanFile)) {
		t.Errorf("plan session: %+v", s)
	}
	if s := g.d.sessions["S4"]; s.turns != 60 || len(s.goals) != 1 || !strings.Contains(s.prompts[0], "task 1 of 2") {
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
	if r.Step != StepPlan || g.d.homes["H1"].goal != "Add a Median function." {
		t.Errorf("step %q, home goal %q", r.Step, g.d.homes["H1"].goal)
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

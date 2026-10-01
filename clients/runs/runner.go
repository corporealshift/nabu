package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/corporealshift/nabu/clients/github"
	"github.com/corporealshift/nabu/protocol"
)

// Runner drives every run. It is asked to Advance each tick, which does all
// the work that needs no model, and to Start sessions when the shared
// admission has slots for them.
type Runner struct {
	Cfg    Config
	Root   string
	Git    Git
	GH     GH
	Claude Claude
	Shell  Shell
	Now    func() time.Time
	Log    io.Writer

	runs map[string]*Run
}

func (rn *Runner) logf(format string, args ...any) {
	if rn.Log != nil {
		fmt.Fprintf(rn.Log, "runner: "+format+"\n", args...)
	}
}

func (rn *Runner) load() error {
	if rn.runs != nil {
		return nil
	}
	all, err := LoadAll(rn.Root)
	if err != nil {
		return err
	}
	rn.runs = map[string]*Run{}
	for i := range all {
		rn.runs[all[i].Home] = &all[i]
	}
	return nil
}

func (rn *Runner) save(r *Run) error {
	r.Updated = rn.Now()
	return r.Save(rn.Root)
}

// Active is how many runs have not finished.
func (rn *Runner) Active() int {
	n := 0
	for _, r := range rn.runs {
		if !r.Step.Over() {
			n++
		}
	}
	return n
}

// Busy is how many runs have a session going: the slots they hold.
func (rn *Runner) Busy() int {
	n := 0
	for _, r := range rn.runs {
		if !r.Step.Over() && r.Session != "" {
			n++
		}
	}
	return n
}

// Waiting is how many runs need a session and have no slot yet.
func (rn *Runner) Waiting() int {
	n := 0
	for _, r := range rn.runs {
		if !r.Step.Over() && r.Waiting {
			n++
		}
	}
	return n
}

// Advance picks up runs asked for since the last tick, then moves every run
// as far as it can go without a new session: observing sessions, and doing
// the mechanical steps and Claude's gates.
func (rn *Runner) Advance(ctx context.Context, d Daemon) error {
	if err := rn.load(); err != nil {
		return err
	}
	var errs []error
	if err := rn.pickUp(ctx, d); err != nil {
		errs = append(errs, err)
	}
	homes := make([]string, 0, len(rn.runs))
	for h := range rn.runs {
		homes = append(homes, h)
	}
	sort.Strings(homes)
	for _, h := range homes {
		if err := rn.advance(ctx, d, rn.runs[h]); err != nil {
			errs = append(errs, fmt.Errorf("runner: run %s: %w", h, err))
		}
	}
	return errors.Join(errs...)
}

// pickUp turns each home labeled run:requested into a run, or resumes the
// failed run it already has.
func (rn *Runner) pickUp(ctx context.Context, d Daemon) error {
	homes, err := d.Requested(ctx)
	if err != nil {
		return err
	}
	for _, h := range homes {
		r, known := rn.runs[h.ID]
		switch {
		case known && r.Step == StepFailed:
			*r = Resume(*r)
			rn.logf("run %s resumed at %s", h.ID, r.Step)
		case known && r.Step == StepDone:
			rn.logf("run %s is already done (%s); /run on a finished run does nothing", h.ID, r.PRURL)
		case known:
			// Asked again while it runs: nothing to do but relabel.
		case !rn.Claude.Available():
			// No run is made: there is nothing to resume, and a later /run,
			// once claude is installed, starts it properly.
			rn.logf("run %s not started: the claude CLI is not installed, and a run never skips its reviews", h.ID)
			if err := d.SetLabels(ctx, h.ID, withRun(h.Labels, "run:failed")); err != nil {
				return err
			}
			continue
		default:
			full, err := d.Home(ctx, h.ID)
			if err != nil {
				return err
			}
			name := full.Brief
			if name == "" {
				name = full.LastPrompt
			}
			slug := Slug(firstLine(name), h.ID)
			r = &Run{Home: h.ID, Workspace: full.Workspace, Brief: full.Brief, Slug: slug, Branch: "nabu/" + slug,
				Worktree: filepath.Join(rn.Root, "runner", "worktrees", slug), Step: StepSetup, Started: rn.Now()}
			rn.runs[h.ID] = r
			rn.logf("run %s: %s", h.ID, r.Branch)
		}
		r.Labelled = "" // the home was relabeled by /run, so label it again
		if err := rn.save(r); err != nil {
			return err
		}
		if err := rn.label(ctx, d, r, h.Labels); err != nil {
			return err
		}
	}
	return nil
}

// label puts the run's step on its home, keeping the home's other labels.
func (rn *Runner) label(ctx context.Context, d Daemon, r *Run, current []string) error {
	want := "run:" + string(r.Step)
	attempt := ""
	if r.Step == StepFix || (r.Step == StepCheck && r.Fixes > 0) {
		attempt = fmt.Sprintf("run:attempt:%d/%d", r.Fixes, MaxFixes)
	}
	key := want + " " + attempt
	if r.Labelled == key {
		return nil
	}
	if current == nil {
		h, err := d.Home(ctx, r.Home)
		if err != nil {
			return err
		}
		current = h.Labels
	}
	labels := withRun(current, want)
	if attempt != "" {
		labels = append(labels, attempt)
	}
	if err := d.SetLabels(ctx, r.Home, labels); err != nil {
		return err
	}
	r.Labelled = key
	return rn.save(r)
}

// withRun is a home's labels with every run:* label replaced by one.
func withRun(current []string, label string) []string {
	out := []string{}
	for _, l := range current {
		if !strings.HasPrefix(l, "run:") {
			out = append(out, l)
		}
	}
	return append(out, label)
}

// maxSteps bounds how far one run moves in one tick, so a bug in the
// workflow cannot spin forever.
const maxSteps = 20

func (rn *Runner) advance(ctx context.Context, d Daemon, r *Run) error {
	for range maxSteps {
		if r.Step.Over() || r.Waiting {
			return rn.label(ctx, d, r, nil)
		}
		var o Outcome
		var done bool
		var err error
		if r.Step.Session() {
			o, done, err = rn.observe(ctx, d, r)
		} else {
			o, err = rn.perform(ctx, d, r)
			done = true
		}
		if err != nil || !done {
			return errors.Join(err, rn.label(ctx, d, r, nil))
		}
		from := r.Step
		*r = Transition(*r, o)
		if !o.OK {
			rn.logf("run %s: %s did not work: %s", r.Home, from, o.Why)
		}
		if r.Step == StepFailed {
			rn.logf("run %s failed at %s: %s; worktree %s", r.Home, r.FailedAt, r.Why, r.Worktree)
		}
		if err := rn.save(r); err != nil {
			return err
		}
		if err := rn.label(ctx, d, r, nil); err != nil {
			return err
		}
		if !o.OK {
			// A failed step is tried again at the next poll, not at once:
			// Claude timing out or git failing is rarely fixed in a second.
			return nil
		}
	}
	return nil
}

// Start begins up to n waiting sessions, oldest run first, and says how many
// it started.
func (rn *Runner) Start(ctx context.Context, d Daemon, n int) (int, error) {
	if err := rn.load(); err != nil {
		return 0, err
	}
	var waiting []*Run
	for _, r := range rn.runs {
		if !r.Step.Over() && r.Waiting {
			waiting = append(waiting, r)
		}
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].Started.Before(waiting[j].Started) })
	started := 0
	var errs []error
	for _, r := range waiting {
		if started >= n {
			break
		}
		if err := rn.begin(ctx, d, r); err != nil {
			errs = append(errs, fmt.Errorf("runner: run %s: %w", r.Home, err))
			continue
		}
		started++
	}
	return started, errors.Join(errs...)
}

// begin starts the current step's session. The session id is saved before
// the prompt is sent, so a restart in between sends it, and never makes a
// second session.
func (rn *Runner) begin(ctx context.Context, d Daemon, r *Run) error {
	prompt, goal, err := rn.prompt(ctx, d, r)
	if err != nil {
		return err
	}
	if r.Start, err = rn.Git.Head(ctx, r.Worktree); err != nil {
		return err
	}
	turns := rn.Cfg.PlanTurns
	if r.Step == StepWork || r.Step == StepFix {
		turns = rn.Cfg.WorkTurns
	}
	id, err := d.Create(ctx, r.Worktree, r.Home, turns)
	if err != nil {
		return err
	}
	r.Session, r.Waiting = id, false
	if err := rn.save(r); err != nil {
		return err
	}
	if goal != "" {
		if err := d.SetGoal(ctx, id, goal); err != nil {
			return err
		}
	}
	if err := d.SendPrompt(ctx, id, prompt); err != nil {
		return err
	}
	r.Prompted = true
	if r.Step == StepFix {
		r.Advice = "" // it has been passed on
	}
	rn.logf("run %s: %s session %s", r.Home, r.Step, id)
	return rn.save(r)
}

// prompt is the current step's prompt, and its goal for work and fix.
func (rn *Runner) prompt(ctx context.Context, d Daemon, r *Run) (string, string, error) {
	switch r.Step {
	case StepBrief:
		t, err := d.Transcript(ctx, r.Home)
		return BriefPrompt(*r, t), "", err
	case StepPlan:
		return PlanPrompt(*r), "", nil
	case StepTasks:
		return TasksPrompt(*r), "", nil
	case StepVerify:
		return VerifyPrompt(*r), "", nil
	case StepWork:
		tasks, err := rn.tasks(r)
		if err != nil {
			return "", "", err
		}
		i, _ := NextTask(tasks)
		if i < 0 {
			return "", "", errors.New("work with no task left")
		}
		r.Task = i
		p, g := WorkPrompt(*r, i, len(tasks), tasks[i])
		return p, g, nil
	case StepFix:
		p, g := FixPrompt(*r, r.Output, r.Advice)
		return p, g, nil
	}
	return "", "", fmt.Errorf("%s has no session", r.Step)
}

func (rn *Runner) path(r *Run, name string) string {
	return filepath.Join(r.Worktree, filepath.FromSlash(r.File(name)))
}

func (rn *Runner) read(r *Run, name string) (string, bool) {
	b, err := os.ReadFile(rn.path(r, name))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (rn *Runner) tasks(r *Run) ([]Task, error) {
	md, ok := rn.read(r, TasksFile)
	if !ok {
		return nil, errors.New("tasks.md is missing")
	}
	return ParseTasks(md), nil
}

// tasksLeft is how many tasks are unchecked, for Transition.
func (rn *Runner) tasksLeft(r *Run) int {
	tasks, err := rn.tasks(r)
	if err != nil {
		return 0
	}
	_, left := NextTask(tasks)
	return left
}

// write replaces one of the run's files and commits it, if it changed.
func (rn *Runner) write(ctx context.Context, r *Run, name, text, msg string) error {
	if old, ok := rn.read(r, name); ok && old == text {
		return nil
	}
	p := rn.path(r, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		return err
	}
	return rn.Git.Commit(ctx, r.Worktree, msg, r.File(name))
}

// observe checks on the current step's session. done is false while it is
// still working.
func (rn *Runner) observe(ctx context.Context, d Daemon, r *Run) (Outcome, bool, error) {
	if !r.Prompted {
		// A restart between creating the session and prompting it. The
		// session is idle and empty; prompt it now.
		prompt, goal, err := rn.prompt(ctx, d, r)
		if err != nil {
			return Outcome{}, false, err
		}
		if goal != "" {
			if err := d.SetGoal(ctx, r.Session, goal); err != nil {
				return Outcome{}, false, err
			}
		}
		if err := d.SendPrompt(ctx, r.Session, prompt); err != nil {
			return Outcome{}, false, err
		}
		r.Prompted = true
		return Outcome{}, false, rn.save(r)
	}
	st, err := d.State(ctx, r.Session)
	if err != nil {
		return Outcome{}, false, err
	}
	switch st.State {
	case protocol.StateRunning:
		return Outcome{}, false, nil
	case protocol.StateIdle:
		events, err := d.Events(ctx, r.Session)
		if err != nil {
			return Outcome{}, false, err
		}
		if !answeredAfter(events, r.NudgedAt) {
			// Not started yet, or not yet answered the nudge.
			return Outcome{}, false, nil
		}
		met := st.Goal != nil && st.Goal.State == "met"
		if !r.Stopped && r.NudgedAt == 0 {
			// The local model sometimes ends its turn having written
			// nothing. It is told once, in the same session, before that
			// counts as a failure: it keeps what it read, and the runner
			// knows exactly what is missing.
			missing, err := rn.missing(ctx, r, met)
			if err != nil {
				return Outcome{}, false, err
			}
			if missing != "" {
				if err := d.SendPrompt(ctx, r.Session, missing); err != nil {
					return Outcome{}, false, err
				}
				r.NudgedAt = len(events)
				rn.logf("run %s: %s session %s stopped short; told it so", r.Home, r.Step, r.Session)
				return Outcome{}, false, rn.save(r)
			}
		}
		if !r.Stopped {
			r.Stopped = true
			if err := rn.save(r); err != nil {
				return Outcome{}, false, err
			}
		}
		if err := d.Stop(ctx, r.Session); err != nil {
			rn.logf("run %s: stopping session %s: %v", r.Home, r.Session, err)
		}
	case protocol.StateCompleted:
		if !r.Stopped {
			return rn.discard(ctx, r, "the session was stopped before it finished")
		}
	default:
		return rn.discard(ctx, r, "the session ended "+string(st.State))
	}
	o, err := rn.produced(ctx, d, r, st.Goal != nil && st.Goal.State == "met")
	return o, true, err
}

// discard throws away a failed session's commits, so its retry starts clean.
func (rn *Runner) discard(ctx context.Context, r *Run, why string) (Outcome, bool, error) {
	if r.Start != "" {
		if err := rn.Git.ResetHard(ctx, r.Worktree, r.Start); err != nil {
			return Outcome{}, false, err
		}
	}
	return Outcome{Why: fmt.Sprintf("%s (session %s)", why, r.Session)}, true, nil
}

// missing is what a session that ended its turn still owes its step, as the
// message that says so, or "" when it owes nothing the runner can see.
func (rn *Runner) missing(ctx context.Context, r *Run, goalMet bool) (string, error) {
	if own := ownFile(r.Step); own != "" {
		if text, ok := rn.read(r, own); !ok || strings.TrimSpace(text) == "" {
			return fmt.Sprintf("You ended your turn without writing %s. Write it now, as the first message asked, and commit it.", r.File(own)), nil
		}
		return "", nil
	}
	if r.Step == StepWork {
		head, err := rn.Git.Head(ctx, r.Worktree)
		if err != nil {
			return "", err
		}
		if head == r.Start && !goalMet {
			return "You ended your turn without committing anything for this task. Do the task now, as the first message asked, and commit it.", nil
		}
	}
	return "", nil
}

// ownFile is the one file a planning step writes, or "" for other steps.
func ownFile(s Step) string {
	return map[Step]string{StepBrief: BriefFile, StepPlan: PlanFile, StepTasks: TasksFile, StepVerify: VerifyFile}[s]
}

// answeredAfter reports whether the model has said anything since the log
// had n events.
func answeredAfter(events []protocol.Event, n int) bool {
	if n > len(events) {
		return false
	}
	return answered(events[n:])
}

func answered(events []protocol.Event) bool {
	for _, e := range events {
		if e.Type != protocol.EventMessage {
			continue
		}
		var m protocol.MessageData
		if json.Unmarshal(e.Data, &m) == nil && m.Role == "assistant" {
			return true
		}
	}
	return false
}

// produced checks what a finished session left behind, which is what the
// step is judged on, never what the session said.
//
// goalMet is whether the session's goal was judged met, which is how a work
// session that found its task already done is told from one that did nothing.
func (rn *Runner) produced(ctx context.Context, d Daemon, r *Run, goalMet bool) (Outcome, error) {
	if r.Step != StepVerify {
		changed, err := rn.Git.Changed(ctx, r.Worktree, r.Start)
		if err != nil {
			return Outcome{}, err
		}
		dirty, err := rn.Git.Dirty(ctx, r.Worktree)
		if err != nil {
			return Outcome{}, err
		}
		if slices.Contains(changed, r.File(VerifyFile)) || slices.Contains(dirty, r.File(VerifyFile)) {
			o, _, err := rn.discard(ctx, r, "the session changed verify.sh, which only the runner may change")
			return o, err
		}
	}

	// A planning step writes its own file and nothing else. In a live run the
	// verify session also wrote the feature and its tests, so the check passed
	// before any work and task 1 found itself already done.
	own := ownFile(r.Step)
	if own != "" {
		changed, err := rn.Git.Changed(ctx, r.Worktree, r.Start)
		if err != nil {
			return Outcome{}, err
		}
		dirty, err := rn.Git.Dirty(ctx, r.Worktree)
		if err != nil {
			return Outcome{}, err
		}
		var other []string
		for _, f := range append(changed, dirty...) {
			if f != r.File(own) && !slices.Contains(other, f) {
				other = append(other, f)
			}
		}
		if len(other) > 0 {
			o, _, err := rn.discard(ctx, r, fmt.Sprintf("the %s step may write only %s, and the session also changed %s",
				r.Step, r.File(own), strings.Join(other, ", ")))
			return o, err
		}
	}

	// The step's own file, committed by the runner if the session left it
	// uncommitted: a smaller model forgets, and the file is what counts.
	if own != "" {
		text, ok := rn.read(r, own)
		if !ok || strings.TrimSpace(text) == "" {
			o, _, err := rn.discard(ctx, r, "the session did not write "+r.File(own))
			return o, err
		}
		dirty, err := rn.Git.Dirty(ctx, r.Worktree)
		if err != nil {
			return Outcome{}, err
		}
		if slices.Contains(dirty, r.File(own)) {
			if err := rn.Git.Commit(ctx, r.Worktree, "run: "+string(r.Step)+" (committed by the runner)", r.File(own)); err != nil {
				return Outcome{}, err
			}
		}
	}

	switch r.Step {
	case StepBrief:
		text, _ := rn.read(r, BriefFile)
		r.Brief = strings.TrimSpace(text)
		// The home carries the brief as its description. Never as its goal,
		// which would start the home working on it in the checkout.
		if err := d.SetDescription(ctx, r.Home, r.Brief); err != nil {
			return Outcome{}, err
		}
	case StepTasks:
		tasks, err := rn.tasks(r)
		if err != nil || len(tasks) == 0 {
			o, _, err := rn.discard(ctx, r, "tasks.md has no checkbox tasks")
			return o, err
		}
	case StepWork:
		head, err := rn.Git.Head(ctx, r.Worktree)
		if err != nil {
			return Outcome{}, err
		}
		if head == r.Start && !goalMet {
			o, _, err := rn.discard(ctx, r, "the session committed nothing")
			return o, err
		}
		// Committing nothing with the goal judged met is a task an earlier
		// one already did, which is done.
		md, _ := rn.read(r, TasksFile)
		if err := rn.write(ctx, r, TasksFile, TickTask(md, r.Task), fmt.Sprintf("run: task %d done", r.Task+1)); err != nil {
			return Outcome{}, err
		}
		return Outcome{OK: true, TasksLeft: rn.tasksLeft(r)}, nil
	case StepFix:
		if _, ok := rn.read(r, RevisionFile); ok {
			return Outcome{OK: true, Revision: true}, nil
		}
	}
	return Outcome{OK: true}, nil
}

// perform does a step that needs no session.
func (rn *Runner) perform(ctx context.Context, d Daemon, r *Run) (Outcome, error) {
	switch r.Step {
	case StepSetup:
		return rn.setup(ctx, r), nil
	case StepPlanReview:
		answer, err := rn.Claude.Ask(ctx, r.Worktree, PlanReviewPrompt(*r))
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		// An answer that neither revises the plan nor passes it is taken as
		// passing it: the plan still gets its tasks, and verify is reviewed.
		if text, changed, _ := ParseRewrite(answer, PlanFile, "NO CHANGES"); changed {
			if err := rn.write(ctx, r, PlanFile, text, "run: plan revised by claude review"); err != nil {
				return Outcome{}, err
			}
		}
		return Outcome{OK: true}, nil
	case StepVerifyReview:
		passed, output, err := rn.Shell.Verify(ctx, r.Worktree, r.File(VerifyFile), time.Duration(rn.Cfg.VerifyTimeout))
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		answer, err := rn.Claude.Ask(ctx, r.Worktree, VerifyReviewPrompt(*r, passed, output))
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		text, changed, ok := ParseRewrite(answer, VerifyFile, "APPROVED")
		if !ok {
			return Outcome{Why: "claude's review of verify.sh neither approved nor replaced it"}, nil
		}
		if changed {
			if err := rn.write(ctx, r, VerifyFile, text, "run: verify.sh rewritten by claude review"); err != nil {
				return Outcome{}, err
			}
		}
		return Outcome{OK: true, TasksLeft: rn.tasksLeft(r)}, nil
	case StepCheck:
		passed, output, err := rn.Shell.Verify(ctx, r.Worktree, r.File(VerifyFile), time.Duration(rn.Cfg.VerifyTimeout))
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		r.Output = output
		rn.logf("run %s: check %s", r.Home, map[bool]string{true: "passed", false: "failed"}[passed])
		return Outcome{OK: true, CheckPassed: passed}, nil
	case StepRevise:
		answer, err := rn.Claude.Ask(ctx, r.Worktree, RevisePrompt(*r, r.Output))
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		text, changed, ok := ParseRewrite(answer, VerifyFile, "REFUSED")
		if !ok {
			return Outcome{Why: "claude's answer to the revision neither replaced verify.sh nor refused"}, nil
		}
		if changed {
			if err := rn.write(ctx, r, VerifyFile, text, "run: verify.sh revised by claude"); err != nil {
				return Outcome{}, err
			}
		} else {
			r.Advice = Refusal(answer)
		}
		if err := rn.Git.Remove(ctx, r.Worktree, "run: revision request answered", r.File(RevisionFile)); err != nil {
			return Outcome{}, err
		}
		return Outcome{OK: true}, nil
	case StepFinalReview:
		changed, err := rn.Git.Diffed(ctx, r.Worktree, r.Base)
		if err != nil {
			return Outcome{}, err
		}
		answer, err := rn.Claude.Ask(ctx, r.Worktree, FinalReviewPrompt(*r, changed))
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		blockers, notes, ok := ParseFinal(answer)
		if !ok {
			return Outcome{Why: "claude's final review was not in the form asked for"}, nil
		}
		r.Notes = notes
		if len(blockers) > 0 {
			md, _ := rn.read(r, TasksFile)
			if err := rn.write(ctx, r, TasksFile, AppendTasks(md, blockers), "run: blockers from the final review"); err != nil {
				return Outcome{}, err
			}
			rn.logf("run %s: the final review found %d blockers", r.Home, len(blockers))
		}
		return Outcome{OK: true, Blockers: len(blockers)}, nil
	case StepPR:
		if err := rn.Git.Push(ctx, r.Worktree, r.Branch); err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		n, url, err := rn.GH.CreatePR(ctx, r.Worktree, r.Base, r.Branch, prTitle(r.Brief), rn.prBody(r), rn.Cfg.Label)
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		r.PR, r.PRURL = n, url
		rn.logf("run %s: opened %s", r.Home, url)
		return Outcome{OK: true}, nil
	}
	return Outcome{}, fmt.Errorf("%s is not a step the runner performs", r.Step)
}

// setup makes the run's branch and worktree, and commits the brief if the
// run was given one.
func (rn *Runner) setup(ctx context.Context, r *Run) Outcome {
	if err := rn.Git.Fetch(ctx, r.Workspace); err != nil {
		return Outcome{Why: err.Error()}
	}
	base, err := rn.Git.DefaultBranch(ctx, r.Workspace)
	if err != nil {
		return Outcome{Why: err.Error()}
	}
	r.Base = base
	if _, err := os.Stat(r.Worktree); err != nil {
		if err := rn.Git.AddBranchWorktree(ctx, r.Workspace, r.Worktree, r.Branch, base); err != nil {
			return Outcome{Why: err.Error()}
		}
	}
	if r.Brief != "" {
		if err := rn.write(ctx, r, BriefFile, strings.TrimSpace(r.Brief)+"\n", "run: brief"); err != nil {
			return Outcome{Why: err.Error()}
		}
	}
	return Outcome{OK: true}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.TrimLeft(s, "# "))
}

// maxTitle is how long a PR title runs.
const maxTitle = 70

func prTitle(brief string) string {
	t := firstLine(brief)
	if len(t) > maxTitle {
		t = strings.TrimSpace(t[:maxTitle-1]) + "…"
	}
	if t == "" {
		t = "nabu run"
	}
	return t
}

func (rn *Runner) prBody(r *Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s opened this from an orchestrated run.\n\n## Brief\n\n%s\n\n", github.Signature, strings.TrimSpace(r.Brief))
	fmt.Fprintf(&b, "The plan is `%s` and the tasks are `%s`. `%s` passes", r.File(PlanFile), r.File(TasksFile), r.File(VerifyFile))
	if r.Fixes > 0 {
		fmt.Fprintf(&b, " after %d fix sessions", r.Fixes)
	}
	b.WriteString(". Claude reviewed the plan, the check and the finished work; any blockers it found were fixed before this PR.\n")
	if len(r.Notes) > 0 {
		b.WriteString("\n## Notes from the final review\n\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(n))
		}
	}
	b.WriteString("\n" + github.Marker + "\n")
	return b.String()
}

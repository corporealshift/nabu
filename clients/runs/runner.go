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
	"strconv"
	"strings"
	"time"

	"github.com/corporealshift/rho/clients/github"
	"github.com/corporealshift/rho/protocol"
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

	runs  map[string]*Run
	goals map[string]*Goal
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
	goals, err := LoadGoals(rn.Root)
	if err != nil {
		return err
	}
	rn.runs = map[string]*Run{}
	for i := range all {
		rn.runs[all[i].Home] = &all[i]
	}
	rn.goals = map[string]*Goal{}
	for i := range goals {
		rn.goals[goals[i].Home] = &goals[i]
	}
	return nil
}

func (rn *Runner) save(r *Run) error {
	r.Updated = rn.Now()
	if key := fmt.Sprintf("%s %d", r.Step, r.Task); key != r.SinceKey {
		r.Since, r.SinceKey = r.Updated, key
	}
	return r.Save(rn.Root)
}

// Active is how many runs and goals have not finished. A blocked goal has:
// it waits for Kyle, not for the runner.
func (rn *Runner) Active() int {
	n := 0
	for _, r := range rn.runs {
		if !r.Step.Over() {
			n++
		}
	}
	for _, g := range rn.goals {
		if !g.Step.Over() {
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

// Advance picks up runs and goals asked for since the last tick, then moves
// every goal and every run as far as it can go without a new session:
// observing sessions, and doing the mechanical steps and Claude's gates.
// Goals go first, so a run a goal starts moves in the same tick.
func (rn *Runner) Advance(ctx context.Context, d Daemon) error {
	if err := rn.load(); err != nil {
		return err
	}
	var errs []error
	if err := rn.pickUp(ctx, d); err != nil {
		errs = append(errs, err)
	}
	if err := rn.pickUpSkips(ctx, d); err != nil {
		errs = append(errs, err)
	}
	if err := rn.pickUpGoals(ctx, d); err != nil {
		errs = append(errs, err)
	}
	if err := rn.pickUpGoalSkips(ctx, d); err != nil {
		errs = append(errs, err)
	}
	if err := rn.advanceGoals(ctx, d); err != nil {
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
	homes, err := d.Requested(ctx, LabelRequested)
	if err != nil {
		return err
	}
	for _, h := range homes {
		r, known := rn.runs[h.ID]
		switch {
		case known && r.Step == StepFailed:
			*r = Resume(*r)
			r.Reported = false
			if err := rn.rebrief(ctx, d, r); err != nil {
				return err
			}
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
			issue := IssueOf(full.Labels)
			if issue != 0 {
				name = fmt.Sprintf("issue %d %s", issue, firstLine(name))
			}
			slug := Slug(firstLine(name), h.ID)
			r = &Run{Home: h.ID, Workspace: full.Workspace, Brief: full.Brief, Slug: slug, Branch: "rho/" + slug,
				Worktree: filepath.Join(rn.Root, "runner", "worktrees", slug), Step: StepSetup, Started: rn.Now(), Issue: issue}
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

// pickUpSkips takes each home labeled run:skip: the owner passing the Claude
// gate its run is at without asking Claude
// (docs/specs/2026-10-13-claude-park-and-skip-design.md). A skip given
// anywhere else does nothing, so it never skips a gate the owner has not seen.
func (rn *Runner) pickUpSkips(ctx context.Context, d Daemon) error {
	homes, err := d.Requested(ctx, LabelSkip)
	if err != nil {
		return err
	}
	for _, h := range homes {
		r, known := rn.runs[h.ID]
		if !known {
			rn.logf("run %s: skip asked for, but there is no run", h.ID)
			if err := d.SetLabels(ctx, h.ID, withPrefix(h.Labels, LabelSkip)); err != nil {
				return err
			}
			continue
		}
		if r.Step.Claude() {
			r.Skip = true
			rn.logf("run %s: the owner skips Claude at %s", h.ID, r.Step)
		} else {
			rn.logf("run %s: skip asked for at %s, which is not one of Claude's gates; nothing skipped", h.ID, r.Step)
		}
		r.Labelled = "" // the home was relabeled by /skip, so label it again
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
	switch {
	case r.Step == StepCIFix:
		attempt = fmt.Sprintf("run:attempt:%d/%d", r.CIFixes, MaxCIFixes)
	case r.Step == StepFix || (r.Step == StepCheck && r.Fixes > 0):
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
	defer rn.show(ctx, d, r)
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
		if err != nil || !done || o.Wait {
			// A session still working, checks still running, or Claude not
			// answering: nothing to do until the next poll.
			if err == nil && done {
				err = rn.wait(r, o)
			}
			return errors.Join(err, rn.label(ctx, d, r, nil))
		}
		from, failedBefore, parked := r.Step, r.FailedAt, r.Parked
		rn.record(r, o)
		*r = Transition(*r, o)
		if parked != "" && !o.Skipped {
			rn.logf("run %s: Claude answered at %s", r.Home, from)
		}
		if !o.OK {
			rn.logf("run %s: %s did not work: %s", r.Home, from, o.Why)
		}
		// Said when it fails, not after the reflection that follows.
		if r.FailedAt != "" && failedBefore == "" {
			rn.logf("run %s failed at %s: %s; worktree %s", r.Home, r.FailedAt, r.Why, r.Worktree)
		}
		if r.Step.Over() && r.Issue != 0 && !r.Reported {
			if err := rn.report(ctx, r); err != nil {
				rn.logf("run %s: telling issue #%d: %v", r.Home, r.Issue, err)
			} else {
				r.Reported = true
			}
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

// wait takes a step that has nothing to do yet. A wait is saved only when it
// parks the run or ends a park, so a poll that finds CI still running writes
// nothing, and the log says once that Claude is not answering, not at every
// poll.
func (rn *Runner) wait(r *Run, o Outcome) error {
	before := r.Parked
	*r = Transition(*r, o)
	if r.Parked == before {
		return nil
	}
	if r.Parked != "" {
		rn.logf("run %s: waiting for Claude at %s: %s", r.Home, r.Step, r.Parked)
	}
	return rn.save(r)
}

// skipped passes the Claude gate a run is at without asking Claude, as the
// owner asked: the run's work stands as it is. A revision or re-plan request
// is answered the way Claude's refusal would answer it.
func (rn *Runner) skipped(ctx context.Context, r *Run) (Outcome, error) {
	if !slices.Contains(r.Skipped, r.Step) {
		r.Skipped = append(r.Skipped, r.Step)
	}
	o := Outcome{OK: true, Skipped: true}
	switch r.Step {
	case StepRevise:
		r.Advice = "The owner kept verify.sh as it is, without asking Claude. Make it pass."
		o.Note = r.Advice
		if err := rn.Git.Remove(ctx, r.Worktree, "run: revision request answered by the owner", r.File(RevisionFile)); err != nil {
			return Outcome{}, err
		}
	case StepReplanReview:
		o.Refused = "The owner skipped Claude's review and says the plan stands. Carry on with it."
		o.Note = o.Refused
		if err := rn.Git.Remove(ctx, r.Worktree, "run: re-plan request answered by the owner", r.File(ReplanFile)); err != nil {
			return Outcome{}, err
		}
	}
	rn.logf("run %s: %s skipped by the owner", r.Home, r.Step)
	return o, nil
}

// claudeFailed is a gate's outcome when its question got no answer: a wait
// while Claude is not answering, which costs no try, and a failed try
// otherwise.
func claudeFailed(err error) Outcome {
	var un ClaudeUnavailable
	if errors.As(err, &un) {
		return Outcome{Wait: true, Parked: un.Reason}
	}
	return Outcome{Why: err.Error()}
}

// record adds the step just finished to the run's history, before Transition
// moves on and forgets its session.
func (rn *Runner) record(r *Run, o Outcome) {
	rec := Record{Step: r.Step, Session: r.Session, OK: o.OK, Why: o.Why, Note: o.Note, At: rn.Now()}
	if r.Step == StepWork {
		rec.Task = r.Task
	}
	switch {
	case !o.OK:
	case o.Skipped:
		rec.Result = "skipped by the owner"
	case o.ReplanRequest:
		rec.Result = "said the plan cannot work"
	case r.Step == StepReplanReview && o.Replan != "":
		rec.Result = "re-planned"
	case r.Step == StepReplanReview:
		rec.Result = "refused"
	case o.Replan != "":
		rec.Result = "Claude judged the plan cannot work"
	case r.Step == StepWork:
	case r.Step == StepCheck:
		rec.Result = map[bool]string{true: "passed", false: "failed"}[o.CheckPassed]
	case r.Step == StepFinalReview:
		rec.Result = fmt.Sprintf("%d blockers", o.Blockers)
	case r.Step == StepCI:
		rec.Result = o.CI
	case o.Revision:
		rec.Result = "asked for a revision of verify.sh"
	}
	r.History = append(r.History, rec.cut())
	if len(r.History) > maxHistory {
		r.History = r.History[len(r.History)-maxHistory:]
	}
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
		rn.show(ctx, d, r)
		started++
	}
	return started, errors.Join(errs...)
}

// settingsOf is a home's context size and effort, for a session made under it
// to copy. A home that cannot be read gives neither, which the daemon takes as
// its defaults: the step goes on rather than failing over a setting.
func (rn *Runner) settingsOf(ctx context.Context, d Daemon, home string) Settings {
	h, err := d.Home(ctx, home)
	if err != nil {
		rn.logf("home %s: context size and effort unknown, so the defaults: %v", home, err)
		return Settings{}
	}
	return Settings{Context: h.Context, Effort: h.Effort}
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
	switch r.Step {
	case StepWork, StepFix, StepCIFix:
		turns = rn.Cfg.WorkTurns
	case StepReflect:
		turns = rn.Cfg.ReflectTurns
	}
	set := rn.settingsOf(ctx, d, r.Home)
	if set.Effort == "" {
		// The home's own effort wins, so a person can turn one run up or down.
		set.Effort = rn.Cfg.Effort[r.Step]
	}
	id, err := d.Create(ctx, r.Worktree, r.Home, turns, set)
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
	if r.Step == StepFix || r.Step == StepCIFix {
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
	case StepCIFix:
		p, g := CIFixPrompt(*r, r.Output)
		return p, g, nil
	case StepReflect:
		return ReflectPrompt(*r, rn.digest(ctx, d, r)), "", nil
	}
	return "", "", fmt.Errorf("%s has no session", r.Step)
}

// digest is the run's Digest, from its files and its sessions' logs. A log
// that cannot be read is left out, rather than holding up the run's end.
func (rn *Runner) digest(ctx context.Context, d Daemon, r *Run) string {
	run := *r
	if run.Brief == "" {
		run.Brief, _ = rn.read(r, BriefFile)
	}
	tasks, _ := rn.read(r, TasksFile)
	logs := map[string][]protocol.Event{}
	for _, rec := range r.History {
		if rec.Session == "" {
			continue
		}
		if _, ok := logs[rec.Session]; ok {
			continue
		}
		events, err := d.Events(ctx, rec.Session)
		if err != nil {
			rn.logf("run %s: the reflection goes without session %s: %v", r.Home, rec.Session, err)
			continue
		}
		logs[rec.Session] = events
	}
	return Digest(run, tasks, logs)
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
	if r.Finding != "" {
		// Claude judged the plan cannot work, and the session was stopped
		// for it. However it ended, the verdict is what the step produced.
		if st.State == protocol.StateRunning {
			return Outcome{}, false, nil
		}
		o, err := rn.produced(ctx, d, r, false)
		return o, true, err
	}
	switch st.State {
	case protocol.StateRunning:
		if claims(r.Step) {
			return Outcome{}, false, rn.watch(ctx, d, r)
		}
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
	case protocol.StateBlocked:
		if !judgedByCheck(r.Step) {
			return rn.discard(ctx, r, "the session ended blocked")
		}
		// A fix is judged by the check that follows it, not by its own stop
		// gate. Seen live: a fix session committed a revision request, its
		// gate then refused every stop over a check it could not clear, and
		// both commits were thrown away. What it left uncommitted goes: the
		// check would otherwise judge files the pull request never gets.
		if !r.Stopped {
			head, err := rn.Git.Head(ctx, r.Worktree)
			if err != nil {
				return Outcome{}, false, err
			}
			if err := rn.resetHard(ctx, r.Worktree, head); err != nil {
				return Outcome{}, false, err
			}
			r.Stopped = true
			if err := rn.save(r); err != nil {
				return Outcome{}, false, err
			}
			rn.logf("run %s: %s session %s ended blocked; judging what it committed", r.Home, r.Step, r.Session)
		}
		if err := d.Stop(ctx, r.Session); err != nil {
			rn.logf("run %s: stopping session %s: %v", r.Home, r.Session, err)
		}
	default:
		return rn.discard(ctx, r, "the session ended "+string(st.State))
	}
	o, err := rn.produced(ctx, d, r, st.Goal != nil && st.Goal.State == "met")
	return o, true, err
}

// planVerdict opens a claude.ask answer that says the task cannot be done as
// it is set out. The claude module asks for it in the questions it puts on a
// session's behalf, and defines it as PlanVerdict; a client never imports a
// module, so it is spelled here again.
const planVerdict = "PLAN CANNOT WORK"

// claudeSource is how the claude module's own calls are logged.
const claudeSource = "module:claude"

// watch reads what a running session has logged since the last poll, for
// Claude's verdict, put on its behalf, that the plan cannot work
// (docs/specs/2026-10-12-run-replan-design.md). On one, the session is
// stopped, and the verdict is acted on once it has.
func (rn *Runner) watch(ctx context.Context, d Daemon, r *Run) error {
	events, err := d.EventsAfter(ctx, r.Session, r.Seen)
	if err != nil {
		return err
	}
	finding, seen := verdictIn(events)
	if seen == "" || (seen == r.Seen && finding == "") {
		return nil
	}
	r.Seen = seen
	if finding != "" {
		r.Finding, r.Stopped = finding, true
		rn.logf("run %s: claude judged the plan cannot work; stopping %s session %s", r.Home, r.Step, r.Session)
	}
	if err := rn.save(r); err != nil {
		return err
	}
	if finding != "" {
		if err := d.Stop(ctx, r.Session); err != nil {
			rn.logf("run %s: stopping session %s: %v", r.Home, r.Session, err)
		}
	}
	return nil
}

// verdictIn finds the claude module's answer that says the plan cannot work.
// seen is the id of the last event read through: a call of the module's whose
// result is not logged yet is left for the next read, so the pair is seen
// together.
func verdictIn(events []protocol.Event) (finding, seen string) {
	calls := map[string]bool{}
	pending := -1
	for i, e := range events {
		switch e.Type {
		case protocol.EventToolCall:
			var c protocol.ToolCallData
			if json.Unmarshal(e.Data, &c) == nil && c.Tool == "claude.ask" && c.Source == claudeSource {
				calls[c.CallID] = true
				if pending < 0 {
					pending = i
				}
			}
		case protocol.EventToolResult:
			var res protocol.ToolResultData
			if json.Unmarshal(e.Data, &res) != nil || !calls[res.CallID] {
				continue
			}
			delete(calls, res.CallID)
			if len(calls) == 0 {
				pending = -1
			}
			if res.Status != "error" && opensWithVerdict(res.Content) {
				_, after, _ := strings.Cut(res.Content, planVerdict)
				finding = strings.TrimSpace(strings.TrimLeft(after, ":.-— \r\n*_`"))
				if finding == "" {
					// The verdict with no reason still stands; the planner
					// at least learns the last plan could not work.
					finding = planVerdict
				}
				return finding, e.ID
			}
		}
	}
	end := len(events)
	if pending >= 0 {
		end = pending
	}
	if end == 0 {
		return "", ""
	}
	return "", events[end-1].ID
}

// opensWithVerdict reports whether an answer's first line is the verdict.
func opensWithVerdict(answer string) bool {
	for l := range strings.SplitSeq(answer, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "*_`#"))
		if l == "" {
			continue
		}
		return strings.HasPrefix(l, planVerdict)
	}
	return false
}

// judgedByCheck says a step's session is followed by the check, which runs
// verify.sh on what it committed, whatever the session itself concluded.
func judgedByCheck(s Step) bool { return s == StepFix || s == StepCIFix }

// discard throws away a failed session's commits, so its retry starts clean.
func (rn *Runner) discard(ctx context.Context, r *Run, why string) (Outcome, bool, error) {
	if r.Start != "" {
		if err := rn.resetHard(ctx, r.Worktree, r.Start); err != nil {
			return Outcome{}, false, err
		}
	}
	return Outcome{Why: fmt.Sprintf("%s (session %s)", why, r.Session)}, true, nil
}

// resetHard is Git.ResetHard, except that a file left behind is logged rather
// than returned. Seen live: a session's shell made a file named nul, which
// git cannot delete on Windows, and the run reset and failed on every poll
// for hours. The tracked files are back where they should be; one stray
// file is not worth stopping a run for.
func (rn *Runner) resetHard(ctx context.Context, dir, sha string) error {
	err := rn.Git.ResetHard(ctx, dir, sha)
	var unclean *UncleanError
	if errors.As(err, &unclean) {
		rn.logf("worktree %s: %v; carrying on", dir, err)
		return nil
	}
	return err
}

// missing is what a session that ended its turn still owes its step, as the
// message that says so, or "" when it owes nothing the runner can see.
func (rn *Runner) missing(ctx context.Context, r *Run, goalMet bool) (string, error) {
	if own := ownFile(r.Step); own != "" {
		if text, ok := rn.read(r, own); !ok || strings.TrimSpace(text) == "" {
			return fmt.Sprintf("You ended your turn without writing %s. Write it now, as the first message asked, and commit it.", r.File(own)), nil
		}
		if mustRewrite(r) {
			done, err := rn.rewrote(ctx, r, own)
			if err != nil || done {
				return "", err
			}
			return fmt.Sprintf("You ended your turn without rewriting %s. The old one could not be carried out; write it again now, as the first message asked, and commit it.", r.File(own)), nil
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

// replanned acts on Claude's verdict that the plan cannot work, once the
// session it stopped has ended. What the session committed stays, as any
// session's work does when the run is planned again; what it left
// uncommitted goes. A session that changed verify.sh loses everything it
// did, as it would at any step.
func (rn *Runner) replanned(ctx context.Context, r *Run) (Outcome, error) {
	to, err := rn.Git.Head(ctx, r.Worktree)
	if err != nil {
		return Outcome{}, err
	}
	changed, err := rn.Git.Changed(ctx, r.Worktree, r.Start)
	if err != nil {
		return Outcome{}, err
	}
	if slices.Contains(changed, r.File(VerifyFile)) {
		to = r.Start
	}
	if err := rn.resetHard(ctx, r.Worktree, to); err != nil {
		return Outcome{}, err
	}
	if err := rn.found(ctx, r, r.Step, r.Finding); err != nil {
		return Outcome{}, err
	}
	rn.logf("run %s: planning again from claude's verdict", r.Home)
	return Outcome{OK: true, Replan: r.Finding, Note: r.Finding}, nil
}

// doing says what a session that claimed the plan cannot work was on, for
// Claude.
func (rn *Runner) doing(r *Run) string {
	switch r.ReplanFrom {
	case StepWork:
		if tasks, err := rn.tasks(r); err == nil && r.Task < len(tasks) {
			return fmt.Sprintf("task %d of %s, %q", r.Task+1, r.File(TasksFile), tasks[r.Task].Title)
		}
		return fmt.Sprintf("task %d of %s", r.Task+1, r.File(TasksFile))
	case StepFix:
		return "fixing the failing check"
	case StepCIFix:
		return "fixing the pull request's failing CI"
	}
	return string(r.ReplanFrom)
}

// found adds what was found to be impossible to findings.md, where the
// planner reads it, under where it was found.
func (rn *Runner) found(ctx context.Context, r *Run, from Step, finding string) error {
	where := string(from)
	if from == StepWork {
		where = fmt.Sprintf("task %d", r.Task+1)
	}
	old, _ := rn.read(r, FindingsFile)
	if old == "" {
		old = "# What this run found it could not do as planned\n\n"
	}
	text := fmt.Sprintf("%s## %s, at %s\n\n%s\n\n", old, rn.Now().Format("2006-01-02 15:04"), where, strings.TrimSpace(finding))
	return rn.write(ctx, r, FindingsFile, text, "run: what the plan could not do")
}

// mustRewrite reports whether a planning step has to change its file, not
// just leave one there: after a re-plan the old plan and tasks are still on
// the branch, and keeping them would repeat what could not be done.
func mustRewrite(r *Run) bool {
	own := ownFile(r.Step)
	return r.Replans > 0 && (own == PlanFile || own == TasksFile)
}

// rewrote reports whether the session changed one of the run's files,
// committed or not.
func (rn *Runner) rewrote(ctx context.Context, r *Run, name string) (bool, error) {
	changed, err := rn.Git.Changed(ctx, r.Worktree, r.Start)
	if err != nil {
		return false, err
	}
	dirty, err := rn.Git.Dirty(ctx, r.Worktree)
	if err != nil {
		return false, err
	}
	return slices.Contains(changed, r.File(name)) || slices.Contains(dirty, r.File(name)), nil
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
	if r.Step == StepReflect {
		// A reflection saves memories, which live outside the worktree, so
		// whatever it changed here goes.
		if err := rn.resetHard(ctx, r.Worktree, r.Start); err != nil {
			return Outcome{}, err
		}
		return Outcome{OK: true}, nil
	}
	if r.Finding != "" {
		return rn.replanned(ctx, r)
	}
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
		if mustRewrite(r) && !slices.Contains(changed, r.File(own)) && !slices.Contains(dirty, r.File(own)) {
			o, _, err := rn.discard(ctx, r, "the run is being planned again, and the session did not rewrite "+r.File(own))
			return o, err
		}
	}

	// A session that says the plan cannot work is judged on that claim, by
	// Claude, whatever else it did (docs/specs/2026-10-12-run-replan-design.md).
	if claims(r.Step) {
		if claim, ok := rn.read(r, ReplanFile); ok {
			dirty, err := rn.Git.Dirty(ctx, r.Worktree)
			if err != nil {
				return Outcome{}, err
			}
			if slices.Contains(dirty, r.File(ReplanFile)) {
				if err := rn.Git.Commit(ctx, r.Worktree, "run: re-plan request (committed by the runner)", r.File(ReplanFile)); err != nil {
					return Outcome{}, err
				}
			}
			rn.logf("run %s: %s session %s says the plan cannot work", r.Home, r.Step, r.Session)
			return Outcome{OK: true, ReplanRequest: true, Note: claim}, nil
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
		return Outcome{OK: true, TasksLeft: rn.tasksLeft(r)}, nil
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
	case StepFix, StepCIFix:
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
		if r.Skip {
			return rn.skipped(ctx, r)
		}
		answer, err := rn.Claude.Ask(ctx, r.Worktree, PlanReviewPrompt(*r))
		if err != nil {
			return claudeFailed(err), nil
		}
		// An answer that neither revises the plan nor passes it is taken as
		// passing it: the plan still gets its tasks, and verify is reviewed.
		if text, changed, _ := ParseRewrite(answer, PlanFile, "NO CHANGES"); changed {
			plan, _ := rn.read(r, PlanFile)
			if err := rn.write(ctx, r, PlanFile, keepDecisions(plan, text), "run: plan revised by claude review"); err != nil {
				return Outcome{}, err
			}
		}
		return Outcome{OK: true}, nil
	case StepVerifyReview:
		if r.Skip {
			return rn.skipped(ctx, r)
		}
		passed, output, err := rn.Shell.Verify(ctx, r.Worktree, r.File(VerifyFile), time.Duration(rn.Cfg.VerifyTimeout))
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		answer, err := rn.Claude.Ask(ctx, r.Worktree, VerifyReviewPrompt(*r, passed, output))
		if err != nil {
			return claudeFailed(err), nil
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
		return Outcome{OK: true}, nil
	case StepCheck:
		passed, output, err := rn.Shell.Verify(ctx, r.Worktree, r.File(VerifyFile), time.Duration(rn.Cfg.VerifyTimeout))
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		r.Output = output
		rn.logf("run %s: check %s", r.Home, map[bool]string{true: "passed", false: "failed"}[passed])
		o := Outcome{OK: true, CheckPassed: passed}
		if !passed {
			o.Note = output
		}
		return o, nil
	case StepRevise:
		if r.Skip {
			return rn.skipped(ctx, r)
		}
		answer, err := rn.Claude.Ask(ctx, r.Worktree, RevisePrompt(*r, r.Output))
		if err != nil {
			return claudeFailed(err), nil
		}
		text, changed, ok := ParseRewrite(answer, VerifyFile, "REFUSED")
		if !ok {
			return Outcome{Why: "claude's answer to the revision neither replaced verify.sh nor refused"}, nil
		}
		note := "Claude revised verify.sh."
		if changed {
			if err := rn.write(ctx, r, VerifyFile, text, "run: verify.sh revised by claude"); err != nil {
				return Outcome{}, err
			}
		} else {
			r.Advice = Refusal(answer)
			note = "Claude kept verify.sh: " + r.Advice
		}
		if err := rn.Git.Remove(ctx, r.Worktree, "run: revision request answered", r.File(RevisionFile)); err != nil {
			return Outcome{}, err
		}
		return Outcome{OK: true, Note: note}, nil
	case StepReplanReview:
		if r.Skip {
			return rn.skipped(ctx, r)
		}
		answer, err := rn.Claude.Ask(ctx, r.Worktree, ReplanReviewPrompt(*r, rn.doing(r)))
		if err != nil {
			return claudeFailed(err), nil
		}
		finding, advice, ok := ParseReplan(answer)
		if !ok {
			return Outcome{Why: "claude's answer to the re-plan request was neither REPLAN nor REFUSED"}, nil
		}
		if finding != "" {
			if err := rn.found(ctx, r, r.ReplanFrom, finding); err != nil {
				return Outcome{}, err
			}
		}
		if err := rn.Git.Remove(ctx, r.Worktree, "run: re-plan request answered", r.File(ReplanFile)); err != nil {
			return Outcome{}, err
		}
		if finding != "" {
			rn.logf("run %s: claude agrees the plan cannot work; planning again", r.Home)
			return Outcome{OK: true, Replan: finding, Note: finding}, nil
		}
		rn.logf("run %s: claude says the plan can work", r.Home)
		return Outcome{OK: true, Refused: advice, Note: advice}, nil
	case StepFinalReview:
		if r.Skip {
			return rn.skipped(ctx, r)
		}
		changed, err := rn.Git.Diffed(ctx, r.Worktree, r.Base)
		if err != nil {
			return Outcome{}, err
		}
		answer, err := rn.Claude.Ask(ctx, r.Worktree, FinalReviewPrompt(*r, changed))
		if err != nil {
			return claudeFailed(err), nil
		}
		blockers, notes, ok := ParseFinal(answer)
		if !ok {
			return Outcome{Why: "claude's final review was not in the form asked for"}, nil
		}
		r.Notes = notes
		var note []string
		if len(blockers) > 0 {
			md, _ := rn.read(r, TasksFile)
			if err := rn.write(ctx, r, TasksFile, AppendTasks(md, blockers), "run: blockers from the final review"); err != nil {
				return Outcome{}, err
			}
			rn.logf("run %s: the final review found %d blockers", r.Home, len(blockers))
			for _, b := range blockers {
				note = append(note, "- "+b.Title+": "+b.Detail)
			}
		}
		return Outcome{OK: true, Blockers: len(blockers), Note: strings.Join(note, "\n")}, nil
	case StepPR:
		if err := rn.Git.Push(ctx, r.Worktree, r.Branch); err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		// A PR for the branch may be open already: a session got one up
		// before the guard stopped that, or this step opened it and failed
		// before recording it. It is the run's branch, so it is the run's PR;
		// it gets the run's title, body and label rather than a failure.
		title, body := prTitle(r.Brief), rn.prBody(r)
		// A goal's run gets no label: it is merged into the goal's branch as
		// soon as it is green, so a review or a comments job on it would be
		// work nobody reads. The goal's own PR carries the label.
		label := rn.Cfg.Label
		if r.Goal != "" {
			label = ""
		}
		n, url, found, err := rn.GH.OpenPRFor(ctx, r.Worktree, r.Branch)
		if err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		if found {
			if err := rn.GH.EditPR(ctx, r.Worktree, n, title, body, label); err != nil {
				return Outcome{Why: err.Error()}, nil
			}
			rn.logf("run %s: took over %s, already open for %s", r.Home, url, r.Branch)
		} else {
			if n, url, err = rn.GH.CreatePR(ctx, r.Worktree, r.Base, r.Branch, title, body, label); err != nil {
				return Outcome{Why: err.Error()}, nil
			}
			rn.logf("run %s: opened %s", r.Home, url)
		}
		r.PR, r.PRURL, r.CISince = n, url, rn.Now()
		return Outcome{OK: true}, nil
	case StepCI:
		return rn.ci(ctx, r), nil
	case StepMerge:
		// Only ever into a goal's branch: Transition sends no other run here.
		if err := rn.GH.MergePR(ctx, r.Worktree, r.PR); err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		rn.logf("run %s: merged %s into %s", r.Home, r.PRURL, r.Base)
		return Outcome{OK: true}, nil
	case StepPush:
		err := rn.Git.Push(ctx, r.Worktree, r.Branch)
		if err == nil {
			rn.logf("run %s: pushed a fix to %s", r.Home, r.Branch)
			return Outcome{OK: true}, nil
		}
		if !rejected(err) {
			return Outcome{Why: err.Error()}, nil
		}
		// The branch moved under the run: the comments job, or Kyle, pushed
		// to it. The fix is dropped, and the moved head's own checks decide
		// what happens next.
		if err := rn.Git.Fetch(ctx, r.Worktree); err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		if err := rn.resetHard(ctx, r.Worktree, "origin/"+r.Branch); err != nil {
			return Outcome{Why: err.Error()}, nil
		}
		rn.logf("run %s: %s moved while it was being fixed; watching its new head", r.Home, r.Branch)
		return Outcome{OK: true}, nil
	}
	return Outcome{}, fmt.Errorf("%s is not a step the runner performs", r.Step)
}

// noChecksWait is how long a run waits for a PR's first check before
// deciding the repository has no CI.
const noChecksWait = 10 * time.Minute

// ci polls the PR's checks once.
func (rn *Runner) ci(ctx context.Context, r *Run) Outcome {
	result, failed, err := rn.pollCI(ctx, r.Worktree, r.PR, r.CISince)
	switch {
	case err != nil:
		return Outcome{Why: err.Error()}
	case result == CIPending:
		return Outcome{Wait: true}
	case result == CIFail:
		r.Output = rn.failures(ctx, r.Worktree, failed)
		rn.logf("run %s: CI failed", r.Home)
		return Outcome{OK: true, CI: result, Note: r.Output}
	case result == CIPass:
		rn.logf("run %s: CI passed", r.Home)
	}
	return Outcome{OK: true, CI: result}
}

// pollCI reads a PR's state and checks once: merged, closed, pending, failed
// with the checks that failed, or passed. A PR with no checks is pending
// until noChecksWait after since, and then passed: the repository has no CI.
func (rn *Runner) pollCI(ctx context.Context, dir string, n int, since time.Time) (string, []Check, error) {
	state, checks, err := rn.GH.PRChecks(ctx, dir, n)
	if err != nil {
		return "", nil, err
	}
	switch strings.ToUpper(state) {
	case "MERGED":
		return CIMerged, nil, nil
	case "CLOSED":
		return CIClosed, nil, nil
	}
	switch Classify(checks) {
	case CIPending:
		return CIPending, nil, nil
	case CINone:
		if rn.Now().Sub(since) < noChecksWait {
			return CIPending, nil, nil
		}
		rn.logf("PR %d: no checks reported in %s; taken as passed", n, noChecksWait)
		return CIPass, nil, nil
	case CIFail:
		return CIFail, Failed(checks), nil
	}
	return CIPass, nil, nil
}

// failures is each failed check, with the end of its Actions log when it has
// one. Jobs of one Actions run share a log, so each run is read once.
func (rn *Runner) failures(ctx context.Context, dir string, failed []Check) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, c := range failed {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", c.Name, c.Link)
		id := RunID(c.Link)
		switch {
		case id == "":
			b.WriteString("(not an Actions run, so there is no log to show)\n\n")
		case seen[id]:
			b.WriteString("(the log is above, under another job of the same run)\n\n")
		default:
			seen[id] = true
			log, err := rn.GH.FailedLog(ctx, dir, id)
			if err != nil {
				fmt.Fprintf(&b, "(the log was not available: %v)\n\n", err)
				continue
			}
			fmt.Fprintf(&b, "~~~\n%s\n~~~\n\n", strings.TrimSpace(log))
		}
	}
	return b.String()
}

// rejected reports whether a push failed because the branch moved.
func rejected(err error) bool {
	msg := err.Error()
	for _, s := range []string{"rejected", "non-fast-forward", "fetch first"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// setup makes the run's branch and worktree, and commits the brief if the
// run was given one.
func (rn *Runner) setup(ctx context.Context, r *Run) Outcome {
	if err := rn.Git.Fetch(ctx, r.Workspace); err != nil {
		return Outcome{Why: err.Error()}
	}
	// A goal's run has its base already: the goal's branch.
	if r.Base == "" {
		base, err := rn.Git.DefaultBranch(ctx, r.Workspace)
		if err != nil {
			return Outcome{Why: err.Error()}
		}
		r.Base = base
	}
	if _, err := os.Stat(r.Worktree); err != nil {
		if err := rn.Git.AddBranchWorktree(ctx, r.Workspace, r.Worktree, r.Branch, r.Base); err != nil {
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
		t = "rho run"
	}
	return t
}

func (rn *Runner) prBody(r *Run) string {
	var b strings.Builder
	if r.Issue != 0 {
		fmt.Fprintf(&b, "Closes #%d\n\n", r.Issue)
	}
	fmt.Fprintf(&b, "%s opened this from an orchestrated run.\n\n## Brief\n\n%s\n\n", github.Signature, strings.TrimSpace(r.Brief))
	fmt.Fprintf(&b, "The plan is `%s` and the tasks are `%s`. `%s` passes", r.File(PlanFile), r.File(TasksFile), r.File(VerifyFile))
	if r.Fixes > 0 {
		fmt.Fprintf(&b, " after %d fix sessions", r.Fixes)
	}
	b.WriteString(reviewed(r.Skipped))
	plan, _ := rn.read(r, PlanFile)
	b.WriteString(decisionsForPR(plan, r.File(PlanFile), r.File(VerifyFile)))
	if len(r.Notes) > 0 {
		b.WriteString("\n## Notes from the final review\n\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(n))
		}
	}
	b.WriteString("\n" + github.Marker + "\n")
	return b.String()
}

// reviewed ends the PR's sentence about the check with what Claude reviewed,
// and names each gate the owner skipped, so a PR never claims a review that
// did not happen.
func reviewed(skipped []Step) string {
	if len(skipped) == 0 {
		return ". Claude reviewed the plan, the check and the finished work; any blockers it found were fixed before this PR.\n"
	}
	var did []string
	for _, g := range []struct {
		step Step
		what string
	}{{StepPlanReview, "the plan"}, {StepVerifyReview, "the check"}, {StepFinalReview, "the finished work"}} {
		if !slices.Contains(skipped, g.step) {
			did = append(did, g.what)
		}
	}
	var b strings.Builder
	b.WriteString(".")
	if len(did) > 0 {
		fmt.Fprintf(&b, " Claude reviewed %s.", joinAnd(did))
	}
	names := make([]string, len(skipped))
	for i, s := range skipped {
		names[i] = string(s)
	}
	fmt.Fprintf(&b, " The owner skipped Claude at %s, so Claude did not review what those gates check.", strings.Join(names, ", "))
	if !slices.Contains(skipped, StepFinalReview) {
		b.WriteString(" Any blockers the final review found were fixed before this PR.")
	}
	b.WriteString("\n")
	return b.String()
}

// joinAnd joins items as a sentence does: "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// IssueOf is the issue number in a home's issue:<owner>/<repo>/<n> label, or
// zero when the run was not asked for by an issue.
func IssueOf(labels []string) int {
	for _, l := range labels {
		rest, ok := strings.CutPrefix(l, "issue:")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(rest[strings.LastIndex(rest, "/")+1:]); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// rebrief takes up a changed brief when a run is resumed: the issue it came
// from was edited or commented on, which is what resumed it.
func (rn *Runner) rebrief(ctx context.Context, d Daemon, r *Run) error {
	full, err := d.Home(ctx, r.Home)
	if err != nil {
		return err
	}
	if full.Brief == "" || full.Brief == r.Brief {
		return nil
	}
	r.Brief = full.Brief
	if _, err := os.Stat(r.Worktree); err != nil {
		return nil // setup has not run; it will write the new brief
	}
	return rn.write(ctx, r, BriefFile, strings.TrimSpace(r.Brief)+"\n", "run: brief updated from the issue")
}

// report tells the run's issue how the run ended.
func (rn *Runner) report(ctx context.Context, r *Run) error {
	var body string
	if r.Step == StepDone {
		body = fmt.Sprintf("%s finished a run for this issue: %s", github.Signature, r.PRURL)
	} else {
		body = fmt.Sprintf("%s stopped working on this issue at the `%s` step: %s\n\nEdit this issue, or comment on it, to start again from there.",
			github.Signature, r.FailedAt, r.Why)
	}
	dir := r.Worktree
	if _, err := os.Stat(dir); err != nil {
		dir = r.Workspace
	}
	return rn.GH.CommentIssue(ctx, dir, r.Issue, body+"\n\n"+github.Marker)
}

package runs

import "fmt"

// The caps on a run (spec: Caps).
const (
	// MaxFixes is how many fix sessions a run gets before it fails.
	MaxFixes = 10
	// MaxRevisions is how many times Claude may be asked to revise verify.sh.
	MaxRevisions = 2
	// sessionTries is how many times a session step is tried: once, and once
	// more after a failure.
	sessionTries = 2
	// otherTries is how many times a Claude gate or the PR is tried before
	// the run fails, one poll apart.
	otherTries = 3
	// ciTries is how many polls of the checks may fail in a row: ci is a
	// poll, so one failed gh call is not much.
	ciTries = 10
	// MaxCIFixes is how many ci-fix sessions a run gets.
	MaxCIFixes = 5
)

// Outcome is what came of doing a run's current step.
type Outcome struct {
	// OK is whether the step did its job: a session that produced its file
	// and left verify.sh alone, a Claude answer that parsed, a script that
	// ran, a PR that opened. A check whose script fails is still OK; that is
	// CheckPassed.
	OK  bool
	Why string
	// CheckPassed is check's verdict.
	CheckPassed bool
	// Revision is a fix session asking Claude to revise verify.sh.
	Revision bool
	// TasksLeft is how many tasks are unchecked after a step.
	TasksLeft int
	// Blockers is how many blockers the final review found.
	Blockers int
	// CI is what a poll of the PR's checks found.
	CI string
	// Wait is a step with nothing to do yet, such as checks still running.
	// It changes nothing and costs no attempt.
	Wait bool
}

// Transition is the whole workflow: given where a run is and what came of
// its step, where it goes next. It is pure, so every arrow of the spec's
// table is a test row.
func Transition(r Run, o Outcome) Run {
	if r.Step.Over() || o.Wait {
		return r
	}
	if !o.OK {
		r.Attempt++
		tries := otherTries
		switch {
		case r.Step.Session():
			tries = sessionTries
		case r.Step == StepCI:
			tries = ciTries
		}
		if r.Attempt >= tries {
			return fail(r, r.Step, o.Why)
		}
		return enter(r, r.Step, false)
	}

	switch r.Step {
	case StepSetup:
		if r.Brief != "" {
			return enter(r, StepPlan, true)
		}
		return enter(r, StepBrief, true)
	case StepBrief:
		return enter(r, StepPlan, true)
	case StepPlan:
		return enter(r, StepPlanReview, true)
	// The check comes before the tasks, so every test it names can be given
	// to a task (docs/specs/2026-10-09-verify-before-tasks-design.md).
	case StepPlanReview:
		return enter(r, StepVerify, true)
	case StepVerify:
		return enter(r, StepVerifyReview, true)
	case StepVerifyReview:
		return enter(r, StepTasks, true)
	case StepTasks, StepWork:
		if o.TasksLeft > 0 {
			// Each task is a fresh start, with its own retry.
			return enter(r, StepWork, true)
		}
		return enter(r, StepCheck, true)
	case StepCheck:
		if o.CheckPassed {
			if r.PR != 0 {
				// A fix after the PR opened goes up to it.
				return enter(r, StepPush, true)
			}
			if r.FinalReviewed {
				return enter(r, StepPR, true)
			}
			return enter(r, StepFinalReview, true)
		}
		if r.Fixes >= MaxFixes {
			return fail(r, StepFix, fmt.Sprintf("verify.sh still fails after %d fixes", MaxFixes))
		}
		r.Fixes++
		return enter(r, StepFix, true)
	case StepFix, StepCIFix:
		if o.Revision {
			if r.Revisions >= MaxRevisions {
				return fail(r, StepRevise, fmt.Sprintf("a fix asked for a revision of verify.sh after %d already", MaxRevisions))
			}
			return enter(r, StepRevise, true)
		}
		return enter(r, StepCheck, true)
	case StepRevise:
		r.Revisions++
		return enter(r, StepCheck, true)
	case StepFinalReview:
		r.FinalReviewed = true
		if o.Blockers > 0 {
			return enter(r, StepWork, true)
		}
		return enter(r, StepPR, true)
	case StepPR, StepPush:
		return enter(r, StepCI, true)
	case StepMerge:
		return enter(r, StepDone, true)
	case StepCI:
		switch o.CI {
		case CIPass:
			if r.Goal != "" {
				return enter(r, StepMerge, true)
			}
			return enter(r, StepDone, true)
		case CIMerged:
			return enter(r, StepDone, true)
		case CIClosed:
			return fail(r, StepCI, "the PR was closed")
		case CIFail:
			if r.CIFixes >= MaxCIFixes {
				return fail(r, StepCIFix, fmt.Sprintf("CI still fails after %d fixes", MaxCIFixes))
			}
			r.CIFixes++
			return enter(r, StepCIFix, true)
		}
		return r
	}
	return r
}

// enter puts a run at a step. A new step (or task) starts its attempts
// again; a retry keeps them. Either way the last session is finished with,
// and a session step waits for a slot.
func enter(r Run, s Step, fresh bool) Run {
	r.Step = s
	if fresh {
		r.Attempt = 0
	}
	r.Session, r.Start, r.Prompted, r.Stopped, r.NudgedAt = "", "", false, false, 0
	r.Waiting = s.Session()
	return r
}

// fail ends a run at a step, with the reason.
func fail(r Run, at Step, why string) Run {
	r = enter(r, StepFailed, true)
	r.FailedAt, r.Why = at, why
	return r
}

// Resume puts a failed run back at the step it failed at, with that step's
// counters cleared: asking again with /run is asking for another go.
func Resume(r Run) Run {
	if r.Step != StepFailed {
		return r
	}
	switch r.FailedAt {
	case StepFix:
		r.Fixes = 0
		// It failed on a check, so it resumes by checking again.
		r = enter(r, StepCheck, true)
	case StepRevise:
		r.Revisions = 0
		r = enter(r, StepRevise, true)
	case StepCIFix:
		// It failed on CI, so it resumes by looking at CI again.
		r.CIFixes = 0
		r = enter(r, StepCI, true)
	default:
		r = enter(r, r.FailedAt, true)
	}
	r.FailedAt, r.Why = "", ""
	return r
}

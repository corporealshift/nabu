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
	// MaxReplans is how many times a run goes back to plan because its plan
	// cannot work, and MaxRefusals how many times Claude may disagree that
	// it cannot. Past either, the run fails for Kyle
	// (docs/specs/2026-10-12-run-replan-design.md).
	MaxReplans  = 2
	MaxRefusals = 2
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
	// Note is what the step found worth keeping in the run's history.
	Note string
	// ReplanRequest is a session's claim that the plan cannot work, left for
	// Claude to judge. Replan is a finding Claude agreed with, or reached
	// itself; Refused is Claude's advice when it disagreed.
	ReplanRequest bool
	Replan        string
	Refused       string
}

// claims reports whether a step's session may say the plan cannot work: the
// steps that carry it out, not the ones that write it.
func claims(s Step) bool { return s == StepWork || s == StepFix || s == StepCIFix }

// Transition is the whole workflow: given where a run is and what came of
// its step, where it goes next. It is pure, so every arrow of the spec's
// table is a test row.
func Transition(r Run, o Outcome) Run {
	if r.Step.Over() || o.Wait {
		return r
	}
	if r.Step == StepReflect {
		// Whatever came of it: the reflection never decides how a run ends,
		// and is not worth a second try.
		return finish(r)
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

	if claims(r.Step) {
		switch {
		case o.Replan != "":
			return replan(r, o.Replan)
		case o.ReplanRequest:
			r.ReplanFrom = r.Step
			return enter(r, StepReplanReview, true)
		}
		// The step did its job, so the advice it was given is spent.
		r.ReplanAdvice = ""
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
	case StepReplanReview:
		if o.Replan != "" {
			return replan(r, o.Replan)
		}
		r.Refusals++
		if r.Refusals > MaxRefusals {
			// Failed at the step that claimed, so a resume tries it again.
			return fail(r, r.ReplanFrom, fmt.Sprintf("the session said the plan cannot work %d times, and Claude disagreed each time: %s", r.Refusals, o.Refused))
		}
		r.ReplanAdvice = o.Refused
		return enter(r, r.ReplanFrom, true)
	case StepFinalReview:
		r.FinalReviewed = true
		if o.Blockers > 0 {
			return enter(r, StepWork, true)
		}
		return enter(r, StepPR, true)
	case StepPR, StepPush:
		return enter(r, StepCI, true)
	case StepMerge:
		return finish(r)
	case StepCI:
		switch o.CI {
		case CIPass:
			if r.Goal != "" {
				return enter(r, StepMerge, true)
			}
			return finish(r)
		case CIMerged:
			return finish(r)
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
	r.Seen, r.Finding = "", ""
	r.Waiting = s.Session()
	return r
}

// replan sends a run back to plan, with what was found to be impossible
// already in findings.md (docs/specs/2026-10-12-run-replan-design.md). The
// check is written again from the new plan, so the fixes and revisions
// counted against the old one start again.
func replan(r Run, finding string) Run {
	r.Replans++
	if r.Replans > MaxReplans {
		// Failed at plan, so a resume plans again.
		return fail(r, StepPlan, fmt.Sprintf("the plan was found unworkable %d times; the last finding: %s", r.Replans, finding))
	}
	r.FinalReviewed, r.Fixes, r.Revisions = false, 0, 0
	r.Advice, r.ReplanAdvice = "", ""
	return enter(r, StepPlan, true)
}

// fail ends a run at a step, with the reason. The reason is recorded first,
// so a reflection in between hands it on unchanged.
func fail(r Run, at Step, why string) Run {
	r.FailedAt, r.Why = at, why
	return finish(r)
}

// finish ends a run, done or failed as FailedAt says. A run a session worked
// on reflects first, so what it taught is saved before it closes
// (docs/specs/2026-10-11-run-reflection-design.md); one no session touched,
// such as a failed setup, has nothing to reflect on.
func finish(r Run) Run {
	if r.Step != StepReflect && worked(r) {
		return enter(r, StepReflect, true)
	}
	if r.FailedAt != "" {
		return enter(r, StepFailed, true)
	}
	return enter(r, StepDone, true)
}

// worked reports whether any of a run's steps was done by a session.
func worked(r Run) bool {
	for _, rec := range r.History {
		if rec.Session != "" {
			return true
		}
	}
	return false
}

// Resume puts a failed run back at the step it failed at, with that step's
// counters cleared: asking again with /run is asking for another go.
func Resume(r Run) Run {
	if r.Step != StepFailed {
		return r
	}
	// Kyle asking again is a fresh judgment of whether the plan can work.
	r.Replans, r.Refusals = 0, 0
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

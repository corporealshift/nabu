package runs

import "fmt"

// maxFailures is how many of a goal's runs may fail in a row before it stops
// for Kyle. The first failure gets a re-plan; a second means re-planning is
// not getting anywhere.
const maxFailures = 2

// How the current brief's run ended, for GoalOutcome.Run.
const (
	RunEnded  = "done"
	RunFailed = "failed"
	// RoundOver is a round with no brief left to run.
	RoundOver = "round-over"
)

// GoalOutcome is what came of doing a goal's current step.
type GoalOutcome struct {
	// OK is whether the step did its job: Claude's answer parsed, git and gh
	// worked, a run ended either way.
	OK  bool
	Why string
	// Wait is a step with nothing to do yet: a run still going, checks
	// still running. It changes nothing and costs no attempt.
	Wait bool

	// DoneWhen and Briefs are the breakdown's answer; Briefs is also an
	// unmet check's next round.
	DoneWhen []string
	Briefs   []GoalBrief
	// Met and Reason are the check's verdict.
	Met    bool
	Reason string

	// Run is how the current brief's run ended, with what it left: its PR
	// and review notes, or where and why it failed.
	Run      string
	PRURL    string
	Notes    []string
	FailedAt Step
	RunWhy   string

	// CI is what a poll of the goal's PR found.
	CI string
}

// GoalTransition is a goal's whole workflow: given where it is and what came
// of its step, where it goes next. It is pure, as Transition is for a run.
func GoalTransition(g Goal, o GoalOutcome, maxRounds int) Goal {
	if g.Step.Over() || o.Wait {
		return g
	}
	if !o.OK {
		g.Attempt++
		tries := otherTries
		if g.Step == GoalCI {
			tries = ciTries
		}
		if g.Attempt >= tries {
			return block(g, g.Step, o.Why)
		}
		return g
	}
	g.Attempt = 0

	switch g.Step {
	case GoalSetup:
		g.Step = GoalBreakdown
	case GoalBreakdown:
		g.DoneWhen = o.DoneWhen
		g.Round, g.RoundsFrom = 1, 1
		g = addRound(g, o.Briefs)
		g.Step = GoalRuns
	case GoalRuns:
		i := g.Current()
		switch o.Run {
		case RunEnded:
			if i >= 0 {
				g.Briefs[i].State, g.Briefs[i].PRURL, g.Briefs[i].Notes = BriefDone, o.PRURL, o.Notes
			}
			g.Failures = 0
			if g.Current() < 0 {
				g.Step = GoalCheck
			}
		case RunFailed:
			if i >= 0 {
				g.Briefs[i].State, g.Briefs[i].FailedAt, g.Briefs[i].Why = BriefFailed, o.FailedAt, o.RunWhy
			}
			// The rest of the round was planned on this run working.
			for j := range g.Briefs {
				if g.Briefs[j].Round == g.Round && g.Briefs[j].State == BriefPending {
					g.Briefs[j].State = BriefDropped
				}
			}
			g.Failures++
			if g.Failures >= maxFailures {
				return block(g, GoalRuns, fmt.Sprintf("%d runs in a row failed; the last at its %s step: %s", g.Failures, o.FailedAt, o.RunWhy))
			}
			g.Step = GoalCheck
		default:
			g.Step = GoalCheck
		}
	case GoalCheck:
		g.Verdicts = append(g.Verdicts, Verdict{Round: g.Round, Met: o.Met, Reason: o.Reason})
		g.CIFailure = ""
		if o.Met {
			g.Step = GoalPR
			return g
		}
		if g.Round-g.RoundsFrom+1 >= maxRounds {
			return block(g, GoalCheck, fmt.Sprintf("not met after %d rounds: %s", maxRounds, o.Reason))
		}
		g.Round++
		g = addRound(g, o.Briefs)
		g.Step = GoalRuns
	case GoalPR:
		g.Step = GoalCI
	case GoalCI:
		switch o.CI {
		case CIPass, CIMerged:
			g.Step = GoalDone
		case CIClosed:
			return block(g, GoalCI, "the pull request was closed")
		case CIFail:
			// The check sees the failure, and its briefs fix it on the goal's
			// branch, which the open PR follows.
			g.Step = GoalCheck
		}
	}
	return g
}

// addRound appends a round's briefs, waiting to run.
func addRound(g Goal, briefs []GoalBrief) Goal {
	for _, b := range briefs {
		b.Round, b.State = g.Round, BriefPending
		g.Briefs = append(g.Briefs, b)
	}
	return g
}

// block stops a goal for Kyle, with the reason.
func block(g Goal, at GoalStep, why string) Goal {
	g.Step, g.BlockedAt, g.Why, g.Attempt = GoalBlocked, at, why, 0
	return g
}

// ResumeGoal takes up a blocked goal again, when Kyle asks. A goal blocked by
// its runs or its rounds goes to the check with a fresh allowance of both;
// one blocked at any other step tries that step again.
func ResumeGoal(g Goal) Goal {
	if g.Step != GoalBlocked {
		return g
	}
	at := g.BlockedAt
	g.BlockedAt, g.Why, g.Attempt = "", "", 0
	switch at {
	case GoalRuns, GoalCheck:
		g.Failures = 0
		g.RoundsFrom = g.Round + 1
		g.Step = GoalCheck
	case GoalCI:
		// Blocked at ci means the PR was closed, or its checks could not be
		// read. pr opens a new one, or takes up the one still open.
		g.Step = GoalPR
	default:
		g.Step = at
	}
	return g
}

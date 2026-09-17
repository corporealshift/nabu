package bench

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"time"
)

// Outcome is how one run ended. They are distinct because they mean different
// things: a harness that ran out of time has not failed in the same way as one
// that produced a change that does not work.
type Outcome string

const (
	Passed    Outcome = "passed"     // verification succeeded and the rules were kept
	Failed    Outcome = "failed"     // it ran, and what it produced does not pass
	Violated  Outcome = "violated"   // it passed by changing what it was told not to
	TimedOut  Outcome = "timed_out"  // it ran out of wall clock
	Errored   Outcome = "errored"    // the harness could not be run
	Unusable  Outcome = "unusable"   // the fixture is broken: verify would not run
	Unscored  Outcome = "unscored"   // reserved for craft, never a run outcome
	OutcomeNA Outcome = "not_scored" // excluded, because its task is suspect
)

// verify runs a task's check in the workspace. It reports whether the check
// passed, whether it could be run at all, and why not when it could not: a
// broken fixture with no reason attached is a dead end to diagnose.
func verify(ctx context.Context, ws *Workspace, task Task) (ok bool, runnable bool, why string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, task.Verify[0], task.Verify[1:]...)
	cmd.Dir = ws.Dir
	out, err := cmd.CombinedOutput()
	switch {
	case err == nil:
		return true, true, ""
	case isExitError(err):
		// It ran and disagreed, which is a result about the harness.
		return false, true, ""
	default:
		return false, false, fmt.Sprintf("%v: %s", err, tail(string(out), 200))
	}
}

func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

// violations is the forbidden files this run touched.
func violations(changed []string, unchanged []string) []string {
	if len(unchanged) == 0 {
		return nil
	}
	touched := map[string]bool{}
	for _, f := range changed {
		touched[f] = true
	}

	var found []string
	for _, f := range unchanged {
		if touched[f] {
			found = append(found, f)
		}
	}
	sort.Strings(found)
	return found
}

// outcome decides how a run ended, given what happened to it.
func outcome(a Attempt, ranAtAll bool, verified, runnable bool, broke []string) Outcome {
	switch {
	case !ranAtAll:
		return Errored
	case a.TimedOut:
		return TimedOut
	case !runnable:
		return Unusable
	case len(broke) > 0:
		// Breaking the rule is its own outcome whether or not the check passed:
		// a harness that edits the test always passes, and has done nothing.
		return Violated
	case verified:
		return Passed
	default:
		return Failed
	}
}

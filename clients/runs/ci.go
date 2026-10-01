package runs

import (
	"regexp"
	"strings"
)

// What a poll of the PR's checks found.
const (
	CIPass    = "pass"
	CIPending = "pending"
	CIFail    = "fail"
	CINone    = "none"
	CIMerged  = "merged"
	CIClosed  = "closed"
)

// Check is one of a PR's checks: a check run or a commit status.
type Check struct {
	Name string
	// State is a check run's conclusion once it has completed, else its
	// status; or a commit status's state. Upper case, as GitHub writes them.
	State string
	Link  string
}

// The states that mean a check has not finished, and those that mean it
// failed. Anything else that finished passed: SUCCESS, SKIPPED, NEUTRAL.
var (
	pendingStates = []string{"QUEUED", "IN_PROGRESS", "PENDING", "EXPECTED", "WAITING", "REQUESTED", ""}
	failedStates  = []string{"FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE"}
)

// Classify is what a PR's checks add up to. Pending wins over failed, so one
// fix session sees every failure at once rather than the first to finish.
func Classify(checks []Check) string {
	if len(checks) == 0 {
		return CINone
	}
	failed := false
	for _, c := range checks {
		state := strings.ToUpper(c.State)
		for _, p := range pendingStates {
			if state == p {
				return CIPending
			}
		}
		for _, f := range failedStates {
			if state == f {
				failed = true
			}
		}
	}
	if failed {
		return CIFail
	}
	return CIPass
}

// Failed is the checks that failed.
func Failed(checks []Check) []Check {
	var out []Check
	for _, c := range checks {
		state := strings.ToUpper(c.State)
		for _, f := range failedStates {
			if state == f {
				out = append(out, c)
			}
		}
	}
	return out
}

var actionsRun = regexp.MustCompile(`/actions/runs/(\d+)`)

// RunID is the Actions run a check's link points into, or "" for a check
// that is not an Actions run.
func RunID(link string) string {
	if m := actionsRun.FindStringSubmatch(link); m != nil {
		return m[1]
	}
	return ""
}

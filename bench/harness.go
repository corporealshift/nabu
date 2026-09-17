package bench

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Cost is what one run consumed. Harnesses report different amounts of this;
// a zero field means the harness did not say, not that it was free.
type Cost struct {
	// Nanoseconds, because that is what time.Duration is; the report does the
	// conversion for people.
	Duration     time.Duration `json:"duration_ns"`
	Turns        int           `json:"turns,omitempty"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
	USD          float64       `json:"usd,omitempty"`
}

// Attempt is what a harness did, before anything has been verified.
type Attempt struct {
	Cost     Cost   `json:"cost"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
	Output   string `json:"-"`
}

// Harness is one coding agent, driven headlessly.
type Harness interface {
	// Name is how it appears in the report.
	Name() string
	// Run gives the harness the task in the workspace. An error means the
	// harness could not be run at all; a harness that ran and failed reports
	// that through Attempt, because "it tried and did not manage it" is a
	// result rather than a fault.
	Run(ctx context.Context, ws *Workspace, task Task) (Attempt, error)
	// Preflight is called before each run. It is where a harness says it is
	// not usable, or cleans up after its own last run.
	Preflight(ctx context.Context) error
}

// run executes argv in a directory with a wall-clock cap.
//
// Never through a shell: prompts hold quotes, backslashes and newlines, and
// three different shells would mangle them three different ways, which would
// mean the harnesses were not given the same task.
func run(ctx context.Context, dir string, timeout time.Duration, argv []string, env []string) (Attempt, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = env
	}

	out, err := cmd.CombinedOutput()
	a := Attempt{
		Cost:     Cost{Duration: time.Since(started)},
		Output:   string(out),
		ExitCode: cmd.ProcessState.ExitCode(),
		TimedOut: ctx.Err() == context.DeadlineExceeded,
	}

	// A non-zero exit is the harness's business, not a failure to run it.
	var ee *exec.ExitError
	if err != nil && !asExitError(err, &ee) && !a.TimedOut {
		return a, err
	}
	return a, nil
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// lastJSONObject returns the last line that parses as a JSON object. Every one
// of these CLIs prints progress before its result, and the result is last.
func lastJSONObject(out string) string {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			return line
		}
	}
	return ""
}

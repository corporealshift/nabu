package bench

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Options is one invocation of the suite.
type Options struct {
	Tasks     []Task
	Harnesses []Harness
	Judge     Judge
	Repeat    int
	// Reference is the harness expected to pass everything. A task it fails is
	// treated as a broken task rather than a hard one.
	Reference string
	Models    Models
	// Progress is written as the suite goes, because a run takes hours and a
	// silent terminal is indistinguishable from a hang.
	Progress io.Writer
}

// Runner executes the suite. Strictly sequential: the local provider allows one
// request in flight, so parallel runs would queue anyway and share a workspace
// parent for no gain.
type Runner struct{ opts Options }

func NewRunner(opts Options) *Runner {
	if opts.Repeat <= 0 {
		opts.Repeat = 3
	}
	if opts.Judge == nil {
		opts.Judge = NoJudge{}
	}
	if opts.Progress == nil {
		opts.Progress = io.Discard
	}
	return &Runner{opts: opts}
}

// Run executes every task against every harness, repeat times.
func (r *Runner) Run(ctx context.Context) (Results, error) {
	parent, err := os.MkdirTemp("", "nabu-bench-")
	if err != nil {
		return Results{}, err
	}
	defer os.RemoveAll(parent)

	res := Results{
		Started: time.Now().UTC().Format(time.RFC3339),
		Repeat:  r.opts.Repeat,
		Judge:   r.opts.Judge.Name(),
		Models:  r.opts.Models,
	}
	res.Tiers = map[string]string{}
	for _, t := range r.opts.Tasks {
		res.Tasks = append(res.Tasks, t.ID)
		res.Tiers[t.ID] = t.Tier
	}
	for _, h := range r.opts.Harnesses {
		res.Harness = append(res.Harness, h.Name())
	}

	for _, task := range r.opts.Tasks {
		for _, h := range r.opts.Harnesses {
			for i := 1; i <= r.opts.Repeat; i++ {
				if ctx.Err() != nil {
					return res, ctx.Err()
				}
				fmt.Fprintf(r.opts.Progress, "%-22s %-8s %d/%d ", task.ID, h.Name(), i, r.opts.Repeat)

				run := r.one(ctx, parent, task, h, i)
				res.Runs = append(res.Runs, run)

				fmt.Fprintf(r.opts.Progress, "%-10s %6.0fs\n", run.Outcome, run.Cost.Duration.Seconds())
			}
		}
	}

	res.Suspects = suspectTasks(res.Runs, r.opts.Reference)
	return res, nil
}

// one is a single attempt, and never returns an error: every way a run can go
// wrong is one of the outcomes, because a suite that stops three hours in
// because one harness was missing has wasted the three hours.
func (r *Runner) one(ctx context.Context, parent string, task Task, h Harness, repeat int) Run {
	out := Run{
		Task:     task.ID,
		Harness:  h.Name(),
		Repeat:   repeat,
		Finished: time.Now().UTC().Format(time.RFC3339),
	}

	if err := h.Preflight(ctx); err != nil {
		out.Outcome, out.Note = Errored, err.Error()
		return out
	}

	ws, err := NewWorkspace(ctx, task.Fixture(), parent)
	if err != nil {
		out.Outcome, out.Note = Unusable, err.Error()
		return out
	}
	defer ws.Remove()

	attempt, runErr := h.Run(ctx, ws, task)
	out.Cost = attempt.Cost
	if runErr != nil {
		out.Note = runErr.Error()
	}
	// Whatever the harness last said is the only evidence of why a run went
	// wrong. Without it a bad result is indistinguishable from a bad harness.
	out.Tail = tail(attempt.Output, 400)

	changed, err := ws.Changed(ctx)
	if err != nil {
		out.Outcome, out.Note = Unusable, err.Error()
		return out
	}
	out.Changed = changed
	out.Broke = violations(changed, task.Unchanged)

	verified, runnable, why := verify(ctx, ws, task)
	if why != "" {
		out.Note = "the check could not be run — " + why
	}
	out.Outcome = outcome(attempt, runErr == nil, verified, runnable, out.Broke)
	if out.Outcome == Passed {
		out.Tail = "" // nothing to explain
	}

	// Craft is only worth scoring on work that runs. Grading the elegance of a
	// change that does not pass rewards tidy failure.
	if out.Outcome == Passed {
		out.Craft = r.craft(ctx, task, ws, h)
	}
	return out
}

func (r *Runner) craft(ctx context.Context, task Task, ws *Workspace, h Harness) Craft {
	diff, err := ws.Diff(ctx)
	if err != nil {
		return Craft{Reason: "diff unavailable"}
	}
	c, err := r.opts.Judge.Judge(ctx, task, diff)
	if err != nil && c.Reason == "" {
		c.Reason = err.Error()
	}
	// The judge and this harness are the same company; say so rather than
	// presenting the number as neutral.
	c.SelfJudged = judgeIsRelated(r.opts.Judge.Name(), h.Name())
	return c
}

func judgeIsRelated(judge, harness string) bool {
	return harness == "claude" && len(judge) >= 6 && judge[:6] == "claude"
}

// tail is the last n characters, which is where a CLI puts its reason.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

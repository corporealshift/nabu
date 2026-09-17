package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Craft is what the judge thought of a change, on three axes from 1 to 5.
// Scored is false when there was no judge, or it could not be read.
type Craft struct {
	Scored  bool   `json:"scored"`
	Minimal int    `json:"minimal,omitempty"`
	Tested  int    `json:"tested,omitempty"`
	Focused int    `json:"focused,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// SelfJudged marks a run the judge is a relative of. The tool does not
	// pretend Claude grading Claude is neutral.
	SelfJudged bool `json:"self_judged,omitempty"`
}

// Judge reads a diff and scores the craft in it.
type Judge interface {
	Judge(ctx context.Context, task Task, diff string) (Craft, error)
	// Name is the judging model, recorded so a later run knows what graded it.
	Name() string
}

const judgeRubric = `You are grading one change a coding agent made, against the task it was given.

Score three things from 1 to 5, where 3 is ordinary and acceptable:

- minimal: is the change no larger than the task required? 5 = nothing spare. 1 = sprawling.
- tested: were tests added or extended where the change warranted it? 5 = well covered. 1 = none where some were clearly needed. If the task forbade touching tests, score 3.
- focused: is the diff free of unrelated churn — reformatting, renames, drive-by edits? 5 = nothing unrelated. 1 = mostly noise.

Reply with one line of JSON and nothing else:
{"minimal":N,"tested":N,"focused":N,"reason":"one short sentence"}`

// ClaudeJudge scores with Claude, one call per run rather than an agent loop.
type ClaudeJudge struct {
	Exe   string
	Model string
}

func (c *ClaudeJudge) Name() string { return "claude/" + c.Model }

func (c *ClaudeJudge) Judge(ctx context.Context, task Task, diff string) (Craft, error) {
	if strings.TrimSpace(diff) == "" {
		return Craft{Scored: false, Reason: "no change to judge"}, nil
	}

	exe := c.Exe
	if exe == "" {
		exe = "claude"
	}
	prompt := fmt.Sprintf("%s\n\n## The task\n\n%s\n\n## The change\n\n```diff\n%s\n```",
		judgeRubric, task.Prompt, clip(diff, 60_000))

	// The judge reads a diff and answers; it is given no tools and no
	// workspace, so it cannot wander into the repository it is grading.
	dir, err := os.MkdirTemp("", "judge-")
	if err != nil {
		return Craft{}, err
	}
	defer os.RemoveAll(dir)

	argv := []string{exe, "-p", prompt, "--output-format", "json", "--model", c.Model}
	a, err := run(ctx, dir, 5*time.Minute, argv, nil)
	if err != nil {
		return Craft{Scored: false, Reason: "judge could not be run"}, err
	}
	return parseCraft(a.Output), nil
}

// parseCraft reads the judge's reply. An unreadable answer leaves craft
// unscored: it must never fail the run, because the objective half of the
// score stands on its own.
func parseCraft(out string) Craft {
	line := lastJSONObject(out)
	if line == "" {
		return Craft{Scored: false, Reason: "judge said nothing readable"}
	}

	// claude -p wraps its answer; the scores may be in the envelope's result.
	var envelope struct {
		Result string `json:"result"`
	}
	if json.Unmarshal([]byte(line), &envelope) == nil && envelope.Result != "" {
		if inner := lastJSONObject(envelope.Result); inner != "" {
			line = inner
		}
	}

	var wire struct {
		Minimal int    `json:"minimal"`
		Tested  int    `json:"tested"`
		Focused int    `json:"focused"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal([]byte(line), &wire) != nil {
		return Craft{Scored: false, Reason: "judge reply was not the shape asked for"}
	}
	if wire.Minimal == 0 && wire.Tested == 0 && wire.Focused == 0 {
		return Craft{Scored: false, Reason: "judge returned no scores"}
	}

	return Craft{
		Scored:  true,
		Minimal: clamp(wire.Minimal),
		Tested:  clamp(wire.Tested),
		Focused: clamp(wire.Focused),
		Reason:  wire.Reason,
	}
}

func clamp(n int) int {
	switch {
	case n < 1:
		return 1
	case n > 5:
		return 5
	default:
		return n
	}
}

// clip keeps a diff inside what is worth sending. The top of a diff is the
// part that shows what was done.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n… diff truncated"
}

// NoJudge scores nothing, for a run with --no-judge.
type NoJudge struct{}

func (NoJudge) Name() string { return "none" }

func (NoJudge) Judge(context.Context, Task, string) (Craft, error) {
	return Craft{Scored: false}, nil
}

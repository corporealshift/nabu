package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LabelGoalRequested is the label that asks the runner for a goal, or asks
// it to take up a blocked one again.
const LabelGoalRequested = "goal:requested"

// goalPrefix starts every label the runner reads or sets on a goal's home.
const goalPrefix = "goal:"

// pickUpGoals turns each home labeled goal:requested into a goal, or resumes
// the blocked goal it already has.
func (rn *Runner) pickUpGoals(ctx context.Context, d Daemon) error {
	homes, err := d.Requested(ctx, LabelGoalRequested)
	if err != nil {
		return err
	}
	for _, h := range homes {
		g, known := rn.goals[h.ID]
		switch {
		case known && g.Step == GoalBlocked:
			full, err := d.Home(ctx, h.ID)
			if err != nil {
				return err
			}
			if text := strings.TrimSpace(full.Brief); text != "" && text != g.Text {
				// Kyle edited the goal: that is his guidance, and the goal
				// from here on.
				g.Text = text
				if err := rn.publish(ctx, g, GoalFile, text+"\n", "goal: updated by the owner"); err != nil {
					rn.logf("goal %s: recording the new text: %v", g.Home, err)
				}
			}
			*g = ResumeGoal(*g)
			rn.logf("goal %s resumed at %s", g.Home, g.Step)
		case known && g.Step == GoalDone:
			rn.logf("goal %s is already done (%s)", h.ID, g.PRURL)
		case known:
			// Asked again while it goes: nothing to do but relabel.
		default:
			full, err := d.Home(ctx, h.ID)
			if err != nil {
				return err
			}
			text := strings.TrimSpace(full.Brief)
			if text == "" || !rn.Claude.Available() {
				why := "a goal needs its text as the session's description"
				if text != "" {
					why = "the claude CLI is not installed, and a goal is planned and judged by it"
				}
				rn.logf("goal %s not started: %s", h.ID, why)
				if err := d.SetLabels(ctx, h.ID, withPrefix(h.Labels, goalPrefix, goalPrefix+string(GoalBlocked))); err != nil {
					return err
				}
				continue
			}
			slug := "goal-" + Slug(firstLine(text), h.ID)
			g = &Goal{Home: h.ID, Workspace: full.Workspace, Text: text, Slug: slug, Branch: "nabu/" + slug,
				Worktree: filepath.Join(rn.Root, "runner", "worktrees", slug), Step: GoalSetup, Started: rn.Now()}
			rn.goals[h.ID] = g
			rn.logf("goal %s: %s", h.ID, g.Branch)
		}
		g.Labelled = "" // the home was relabeled to ask, so label it again
		if err := rn.saveGoal(g); err != nil {
			return err
		}
		if err := rn.labelGoal(ctx, d, g, h.Labels); err != nil {
			return err
		}
	}
	return nil
}

func (rn *Runner) saveGoal(g *Goal) error {
	g.Updated = rn.Now()
	return g.Save(rn.Root)
}

// advanceGoals moves every goal as far as it can go this tick, in a fixed
// order.
func (rn *Runner) advanceGoals(ctx context.Context, d Daemon) error {
	homes := make([]string, 0, len(rn.goals))
	for h := range rn.goals {
		homes = append(homes, h)
	}
	sort.Strings(homes)
	var errs []error
	for _, h := range homes {
		if err := rn.advanceGoal(ctx, d, rn.goals[h]); err != nil {
			errs = append(errs, fmt.Errorf("runner: goal %s: %w", h, err))
		}
	}
	return errors.Join(errs...)
}

func (rn *Runner) advanceGoal(ctx context.Context, d Daemon, g *Goal) error {
	for range maxSteps {
		if g.Step.Over() {
			return rn.showGoal(ctx, d, g)
		}
		o, err := rn.performGoal(ctx, d, g)
		if err != nil || o.Wait {
			return errors.Join(err, rn.showGoal(ctx, d, g))
		}
		from := g.Step
		*g = GoalTransition(*g, o, rn.Cfg.GoalRounds)
		switch {
		case !o.OK:
			rn.logf("goal %s: %s did not work: %s", g.Home, from, o.Why)
		case from == GoalBreakdown || from == GoalCheck:
			// The roadmap on the branch is a record for the PR; the home's
			// task list is what Kyle watches. A roadmap that fails to land
			// is written whole at the next check.
			if err := rn.publish(ctx, g, RoadmapFile, Roadmap(*g), fmt.Sprintf("goal: round %d %s", g.Round, from)); err != nil {
				rn.logf("goal %s: recording the roadmap: %v", g.Home, err)
			}
		}
		if g.Step == GoalBlocked {
			rn.logf("goal %s blocked at %s: %s", g.Home, g.BlockedAt, g.Why)
		}
		if err := rn.saveGoal(g); err != nil {
			return err
		}
		if err := rn.showGoal(ctx, d, g); err != nil {
			return err
		}
		if !o.OK {
			return nil
		}
	}
	return nil
}

// performGoal does a goal's current step.
func (rn *Runner) performGoal(ctx context.Context, d Daemon, g *Goal) (GoalOutcome, error) {
	switch g.Step {
	case GoalSetup:
		return rn.setupGoal(ctx, g), nil
	case GoalBreakdown:
		answer, err := rn.Claude.Ask(ctx, g.Worktree, BreakdownPrompt(*g))
		if err != nil {
			return GoalOutcome{Why: err.Error()}, nil
		}
		doneWhen, briefs, ok := ParseBreakdown(answer)
		if !ok {
			return GoalOutcome{Why: "claude's breakdown was not in the form asked for"}, nil
		}
		rn.logf("goal %s: %d briefs", g.Home, len(briefs))
		return GoalOutcome{OK: true, DoneWhen: doneWhen, Briefs: briefs}, nil
	case GoalRuns:
		return rn.goalRun(ctx, d, g)
	case GoalCheck:
		if err := rn.sync(ctx, g); err != nil {
			return GoalOutcome{Why: err.Error()}, nil
		}
		changed, err := rn.Git.Diffed(ctx, g.Worktree, g.Base)
		if err != nil {
			return GoalOutcome{Why: err.Error()}, nil
		}
		answer, err := rn.Claude.Ask(ctx, g.Worktree, CheckPrompt(*g, changed))
		if err != nil {
			return GoalOutcome{Why: err.Error()}, nil
		}
		met, reason, briefs, ok := ParseCheck(answer)
		if !ok {
			return GoalOutcome{Why: "claude's check was not in the form asked for"}, nil
		}
		rn.logf("goal %s: round %d check: met %v", g.Home, g.Round, met)
		return GoalOutcome{OK: true, Met: met, Reason: reason, Briefs: briefs}, nil
	case GoalPR:
		title, body := prTitle(g.Text), GoalPRBody(*g)
		n, url, found, err := rn.GH.OpenPRFor(ctx, g.Worktree, g.Branch)
		if err != nil {
			return GoalOutcome{Why: err.Error()}, nil
		}
		if found {
			if err := rn.GH.EditPR(ctx, g.Worktree, n, title, body, rn.Cfg.Label); err != nil {
				return GoalOutcome{Why: err.Error()}, nil
			}
		} else if n, url, err = rn.GH.CreatePR(ctx, g.Worktree, g.Base, g.Branch, title, body, rn.Cfg.Label); err != nil {
			return GoalOutcome{Why: err.Error()}, nil
		}
		rn.logf("goal %s: %s", g.Home, url)
		g.PR, g.PRURL, g.CISince = n, url, rn.Now()
		return GoalOutcome{OK: true}, nil
	case GoalCI:
		result, failed, err := rn.pollCI(ctx, g.Worktree, g.PR, g.CISince)
		switch {
		case err != nil:
			return GoalOutcome{Why: err.Error()}, nil
		case result == CIPending:
			return GoalOutcome{Wait: true}, nil
		case result == CIFail:
			g.CIFailure = rn.failures(ctx, g.Worktree, failed)
			rn.logf("goal %s: CI failed", g.Home)
		}
		return GoalOutcome{OK: true, CI: result}, nil
	}
	return GoalOutcome{}, fmt.Errorf("%s is not a step a goal performs", g.Step)
}

// setupGoal makes the goal's branch and worktree, commits goal.md, and
// pushes the branch, which every run of the goal starts from.
func (rn *Runner) setupGoal(ctx context.Context, g *Goal) GoalOutcome {
	if err := rn.Git.Fetch(ctx, g.Workspace); err != nil {
		return GoalOutcome{Why: err.Error()}
	}
	base, err := rn.Git.DefaultBranch(ctx, g.Workspace)
	if err != nil {
		return GoalOutcome{Why: err.Error()}
	}
	g.Base = base
	if _, err := os.Stat(g.Worktree); err != nil {
		if err := rn.Git.AddBranchWorktree(ctx, g.Workspace, g.Worktree, g.Branch, base); err != nil {
			return GoalOutcome{Why: err.Error()}
		}
	}
	if err := rn.writeGoalFile(ctx, g, GoalFile, strings.TrimSpace(g.Text)+"\n", "goal: "+firstLine(g.Text)); err != nil {
		return GoalOutcome{Why: err.Error()}
	}
	if err := rn.Git.Push(ctx, g.Worktree, g.Branch); err != nil {
		return GoalOutcome{Why: err.Error()}
	}
	return GoalOutcome{OK: true}
}

// goalRun starts the current brief's run, or sees how it is going.
func (rn *Runner) goalRun(ctx context.Context, d Daemon, g *Goal) (GoalOutcome, error) {
	i := g.Current()
	if i < 0 {
		return GoalOutcome{OK: true, Run: RoundOver}, nil
	}
	br := &g.Briefs[i]
	if br.Run == "" {
		// The run's brief opens with its title, which names its branch and
		// its PR.
		brief := "# " + br.Title + "\n\n" + strings.TrimSpace(br.Brief)
		id, err := d.CreateHome(ctx, g.Workspace, g.Home, brief, rn.contextOf(ctx, d, g.Home))
		if err != nil {
			return GoalOutcome{}, err
		}
		slug := Slug(br.Title, id)
		r := &Run{Home: id, Workspace: g.Workspace, Brief: brief, Slug: slug, Branch: "nabu/" + slug, Base: g.Branch, Goal: g.Home,
			Worktree: filepath.Join(rn.Root, "runner", "worktrees", slug), Step: StepSetup, Started: rn.Now()}
		rn.runs[id] = r
		br.Run, br.State = id, BriefRunning
		if err := errors.Join(rn.save(r), rn.saveGoal(g)); err != nil {
			return GoalOutcome{}, err
		}
		rn.logf("goal %s: round %d, run %s: %s", g.Home, g.Round, id, br.Title)
		return GoalOutcome{Wait: true}, nil
	}
	r := rn.runs[br.Run]
	switch {
	case r == nil:
		return GoalOutcome{OK: true, Run: RunFailed, FailedAt: StepSetup, RunWhy: "the run's state is missing"}, nil
	case r.Step == StepDone:
		return GoalOutcome{OK: true, Run: RunEnded, PRURL: r.PRURL, Notes: r.Notes}, nil
	case r.Step == StepFailed:
		return GoalOutcome{OK: true, Run: RunFailed, FailedAt: r.FailedAt, RunWhy: r.Why}, nil
	}
	return GoalOutcome{Wait: true}, nil
}

// sync brings the goal's worktree up to its branch on origin, where the
// runs merge.
func (rn *Runner) sync(ctx context.Context, g *Goal) error {
	if err := rn.Git.Fetch(ctx, g.Worktree); err != nil {
		return err
	}
	return rn.resetHard(ctx, g.Worktree, "origin/"+g.Branch)
}

// publish puts one of the goal's files on its branch: up to date with origin
// first, since runs merge there, then committed and pushed.
func (rn *Runner) publish(ctx context.Context, g *Goal, name, text, msg string) error {
	if _, err := os.Stat(g.Worktree); err != nil {
		return nil // setup has not run; it writes goal.md itself
	}
	if err := rn.sync(ctx, g); err != nil {
		return err
	}
	if err := rn.writeGoalFile(ctx, g, name, text, msg); err != nil {
		return err
	}
	return rn.Git.Push(ctx, g.Worktree, g.Branch)
}

// writeGoalFile replaces one of the goal's files and commits it, if it
// changed.
func (rn *Runner) writeGoalFile(ctx context.Context, g *Goal, name, text, msg string) error {
	p := filepath.Join(g.Worktree, filepath.FromSlash(g.File(name)))
	if old, err := os.ReadFile(p); err == nil && string(old) == text {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		return err
	}
	return rn.Git.Commit(ctx, g.Worktree, msg, g.File(name))
}

// showGoal puts the goal's step and round on its home's labels, and its
// roadmap in its home's task list, each only when it changed.
func (rn *Runner) showGoal(ctx context.Context, d Daemon, g *Goal) error {
	if err := rn.labelGoal(ctx, d, g, nil); err != nil {
		return err
	}
	tasks := GoalTasks(*g)
	if len(tasks) == 0 {
		return nil
	}
	b, err := json.Marshal(tasks)
	if err != nil {
		return err
	}
	if string(b) == g.Tasked {
		return nil
	}
	if err := d.UpdateTasks(ctx, g.Home, tasks); err != nil {
		return err
	}
	g.Tasked = string(b)
	return rn.saveGoal(g)
}

// labelGoal sets goal:<step> and goal:round:<n> on the home, keeping its
// other labels.
func (rn *Runner) labelGoal(ctx context.Context, d Daemon, g *Goal, current []string) error {
	want := []string{goalPrefix + string(g.Step)}
	if g.Round > 0 {
		want = append(want, fmt.Sprintf("%sround:%d", goalPrefix, g.Round))
	}
	key := strings.Join(want, " ")
	if g.Labelled == key {
		return nil
	}
	if current == nil {
		h, err := d.Home(ctx, g.Home)
		if err != nil {
			return err
		}
		current = h.Labels
	}
	if err := d.SetLabels(ctx, g.Home, withPrefix(current, goalPrefix, want...)); err != nil {
		return err
	}
	g.Labelled = key
	return rn.saveGoal(g)
}

// withPrefix is a home's labels with every label under prefix replaced.
func withPrefix(current []string, prefix string, labels ...string) []string {
	out := []string{}
	for _, l := range current {
		if !strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return append(out, labels...)
}

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// Watcher polls GitHub, runs a session for each job, and posts what a
// finished session produced.
type Watcher struct {
	Cfg  Config
	Root string
	GH   GitHub
	Git  Git
	// Dial connects to the daemon, once per poll. A daemon that is down costs
	// one poll, not the watcher.
	Dial func(ctx context.Context) (Daemon, error)
	Now  func() time.Time
	// DryRun runs sessions for real and prints what it would post instead of
	// posting it, keeping its own state file.
	DryRun bool
	// Log gets one line per thing the watcher does; Out gets what a dry run
	// would have posted.
	Log, Out io.Writer
	// Wait is how often RunOnce checks on running jobs. Default 10s.
	Wait time.Duration

	state *State
}

func (w *Watcher) statePath() string { return StatePath(w.Root, w.DryRun) }

func (w *Watcher) logf(format string, args ...any) {
	if w.Log != nil {
		fmt.Fprintf(w.Log, "github: "+format+"\n", args...)
	}
}

func (w *Watcher) save() error { return w.state.Save(w.statePath()) }

// Poll does one round: moves every running job along, looks at each
// repository's open PRs, and starts what is due. It returns how many jobs it
// started.
func (w *Watcher) Poll(ctx context.Context) (int, error) {
	if w.state == nil {
		st, err := LoadState(w.statePath())
		if err != nil {
			return 0, err
		}
		w.state = st
	}
	d, err := w.Dial(ctx)
	if err != nil {
		return 0, fmt.Errorf("github: reaching the daemon: %w", err)
	}
	defer d.Close()

	var errs []error
	type candidate struct {
		repo Repo
		pr   PR
	}
	var due []candidate
	for _, repo := range w.Cfg.Repos {
		rs := w.state.Repo(repo.Name)
		prs, prErr := w.GH.OpenPRs(ctx, repo.Name)
		open := make(map[int]PR, len(prs))
		for _, p := range prs {
			open[p.Number] = p
		}
		for _, job := range append([]Job(nil), rs.Running...) {
			if err := w.advance(ctx, d, repo, job, open[job.PR]); err != nil {
				errs = append(errs, err)
			}
		}
		if prErr != nil {
			errs = append(errs, fmt.Errorf("github: %s: %w", repo.Name, prErr))
			continue
		}
		Observe(rs, prs, w.Now())
		for _, p := range ReviewJobs(w.Cfg, rs, prs, w.Now()) {
			due = append(due, candidate{repo, p})
		}
	}
	if err := w.save(); err != nil {
		return 0, err
	}

	started := 0
	for _, c := range Admit(w.Cfg.MaxJobs, w.state.RunningCount(), due) {
		if err := w.startReview(ctx, d, c.repo, c.pr); err != nil {
			errs = append(errs, err)
			continue
		}
		started++
	}
	return started, errors.Join(errs...)
}

// Run polls every Cfg.Poll until ctx is done.
func (w *Watcher) Run(ctx context.Context) error {
	t := time.NewTicker(time.Duration(w.Cfg.Poll))
	defer t.Stop()
	for {
		if _, err := w.Poll(ctx); err != nil {
			w.logf("%v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// RunOnce polls, then keeps polling until no job is running and a poll
// starts nothing new. It is for testing and for a scheduler.
func (w *Watcher) RunOnce(ctx context.Context) error {
	wait := w.Wait
	if wait <= 0 {
		wait = 10 * time.Second
	}
	var last error
	for {
		started, err := w.Poll(ctx)
		if err != nil {
			w.logf("%v", err)
			last = err
		}
		if w.state != nil && w.state.RunningCount() == 0 && started == 0 {
			return last
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// worktreePath is where a review job's worktree goes.
func (w *Watcher) worktreePath(repo string, pr PR) string {
	return filepath.Join(Dir(w.Root), "worktrees", strings.ReplaceAll(repo, "/", "-"),
		fmt.Sprintf("review-%d-%s", pr.Number, short(pr.HeadSHA)))
}

// startReview makes the worktree, records the job, and starts its session.
// The job is saved before the session exists, so a crash in between is seen
// on restart as a job with no session, and given one.
func (w *Watcher) startReview(ctx context.Context, d Daemon, repo Repo, pr PR) error {
	path := w.worktreePath(repo.Name, pr)
	if err := w.Git.FetchPR(ctx, repo.Clone, pr.Number, pr.BaseRef); err != nil {
		return fmt.Errorf("github: %s#%d: %w", repo.Name, pr.Number, err)
	}
	if _, err := os.Stat(path); err == nil {
		// Left by a crash before the job was saved; nothing else uses it.
		_ = w.Git.RemoveWorktree(ctx, repo.Clone, path)
	}
	if err := w.Git.AddWorktree(ctx, repo.Clone, path, pr.HeadSHA); err != nil {
		return fmt.Errorf("github: %s#%d: %w", repo.Name, pr.Number, err)
	}
	rs := w.state.Repo(repo.Name)
	job := Job{Kind: KindReview, PR: pr.Number, SHA: pr.HeadSHA, Base: pr.BaseRef, Worktree: path, Started: w.Now()}
	rs.Running = append(rs.Running, job)
	if err := w.save(); err != nil {
		return err
	}
	w.logf("%s#%d %s: review starting in %s", repo.Name, pr.Number, short(pr.HeadSHA), path)
	return w.begin(ctx, d, repo, pr, job)
}

// begin creates a job's session and sends its prompt, saving after each so
// neither is done twice.
func (w *Watcher) begin(ctx context.Context, d Daemon, repo Repo, pr PR, job Job) error {
	rs := w.state.Repo(repo.Name)
	if job.SessionID == "" {
		id, err := d.Create(ctx, job.Worktree, w.Cfg.Review.MaxTurns)
		if err != nil {
			return fmt.Errorf("github: %s#%d: creating a session: %w", repo.Name, job.PR, err)
		}
		job.SessionID = id
		update(rs, job)
		if err := w.save(); err != nil {
			return err
		}
	}
	var prev *Reviewed
	if r, ok := rs.Reviewed[job.PR]; ok {
		prev = &r
	}
	if err := d.SendPrompt(ctx, job.SessionID, ReviewPrompt(repo.Name, pr, prev)); err != nil {
		return fmt.Errorf("github: %s#%d: session %s: %w", repo.Name, job.PR, job.SessionID, err)
	}
	job.Prompted = true
	update(rs, job)
	if err := w.save(); err != nil {
		return err
	}
	w.logf("%s#%d %s: session %s running", repo.Name, job.PR, short(job.SHA), job.SessionID)
	return nil
}

// update replaces a running job with a newer copy of itself.
func update(rs *RepoState, job Job) {
	for i := range rs.Running {
		if rs.Running[i].PR == job.PR && rs.Running[i].Kind == job.Kind {
			rs.Running[i] = job
		}
	}
}

// drop removes a job from the running list.
func drop(rs *RepoState, job Job) {
	out := rs.Running[:0]
	for _, j := range rs.Running {
		if !(j.PR == job.PR && j.Kind == job.Kind) {
			out = append(out, j)
		}
	}
	rs.Running = out
}

// advance moves one running job along: gives it a session and a prompt if a
// crash left it without, finishes it if its session is done, fails it if its
// session ended any other way, and otherwise leaves it running. pr is the
// PR as the poll saw it, or zero if the poll could not see it.
func (w *Watcher) advance(ctx context.Context, d Daemon, repo Repo, job Job, pr PR) error {
	if job.SessionID == "" || !job.Prompted {
		// The prompt is for the head the job recorded, whatever the PR's
		// head is now.
		pr.Number, pr.HeadSHA, pr.BaseRef = job.PR, job.SHA, job.Base
		return w.begin(ctx, d, repo, pr, job)
	}
	st, err := d.State(ctx, job.SessionID)
	if err != nil {
		return fmt.Errorf("github: %s#%d: session %s: %w", repo.Name, job.PR, job.SessionID, err)
	}
	events, err := d.Events(ctx, job.SessionID)
	if err != nil {
		return fmt.Errorf("github: %s#%d: session %s: %w", repo.Name, job.PR, job.SessionID, err)
	}
	final, answered := finalMessage(events)

	switch st.State {
	case protocol.StateRunning:
		return nil
	case protocol.StateIdle:
		if !answered {
			// Created and prompted, but the loop has not started yet.
			return nil
		}
		if !job.Stopped {
			job.Stopped = true
			update(w.state.Repo(repo.Name), job)
			if err := w.save(); err != nil {
				return err
			}
		}
		// The watcher owns the session, so it ends it; that is also what
		// writes the report.
		if err := d.Stop(ctx, job.SessionID); err != nil {
			w.logf("%s#%d: stopping session %s: %v", repo.Name, job.PR, job.SessionID, err)
		}
	case protocol.StateCompleted:
		if !job.Stopped {
			return w.fail(ctx, repo, job, "the session was stopped before it finished")
		}
	default:
		return w.fail(ctx, repo, job, "the session ended "+string(st.State))
	}
	return w.post(ctx, repo, job, final)
}

// finalMessage is the last assistant message in a log.
func finalMessage(events []protocol.Event) (string, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != protocol.EventMessage {
			continue
		}
		var m protocol.MessageData
		if json.Unmarshal(events[i].Data, &m) == nil && m.Role == "assistant" {
			return m.Content, true
		}
	}
	return "", false
}

// post builds the review from a finished session and posts it. Only a
// successful post marks the head reviewed; a failed one leaves the job for
// the next poll, which posts again from the same session.
func (w *Watcher) post(ctx context.Context, repo Repo, job Job, final string) error {
	diff, err := w.Git.Diff(ctx, job.Worktree, job.Base, job.SHA)
	if err != nil {
		w.logf("%s#%d: no diff, so every comment goes in the body: %v", repo.Name, job.PR, err)
	}
	r, ok := ParseReview(final)
	post := BuildReview(r, ok, final, diff, job.SHA)

	if w.DryRun {
		b, _ := json.MarshalIndent(post, "", "  ")
		fmt.Fprintf(w.Out, "would post a review on %s#%d at %s:\n%s\n", repo.Name, job.PR, short(job.SHA), b)
	} else if err := w.GH.PostReview(ctx, repo.Name, job.PR, post); err != nil {
		return fmt.Errorf("github: %s#%d: posting the review from session %s: %w", repo.Name, job.PR, job.SessionID, err)
	}

	summary := r.Summary
	if !ok {
		summary = strings.TrimSpace(final)
	}
	if len(summary) > 2000 {
		summary = summary[:2000]
	}
	rs := w.state.Repo(repo.Name)
	rs.Reviewed[job.PR] = Reviewed{SHA: job.SHA, Summary: summary}
	drop(rs, job)
	if err := w.save(); err != nil {
		return err
	}
	w.logf("%s#%d %s: review posted from session %s", repo.Name, job.PR, short(job.SHA), job.SessionID)
	if err := w.Git.RemoveWorktree(ctx, repo.Clone, job.Worktree); err != nil {
		w.logf("%s#%d: removing %s: %v", repo.Name, job.PR, job.Worktree, err)
	}
	return nil
}

// fail records a job as failed on its head, which is not tried again until
// the PR moves. The worktree is kept for a look.
func (w *Watcher) fail(_ context.Context, repo Repo, job Job, why string) error {
	rs := w.state.Repo(repo.Name)
	rs.Failed[job.PR] = job.SHA
	drop(rs, job)
	if err := w.save(); err != nil {
		return err
	}
	w.logf("%s#%d %s: review failed: %s; session %s, worktree %s", repo.Name, job.PR, short(job.SHA), why, job.SessionID, job.Worktree)
	return nil
}

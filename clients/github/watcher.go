package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	// DryRun runs sessions for real and prints what it would push and post
	// instead of doing it, keeping its own state file.
	DryRun bool
	// Log gets one line per thing the watcher does; Out gets what a dry run
	// would have posted.
	Log, Out io.Writer
	// Wait is how often RunOnce checks on running jobs. Default 10s.
	Wait time.Duration

	state *State
	// moved is the PRs this poll pushed to, as "repo#n". The poll's list of
	// PRs was read before the push, so it shows a head that is already gone,
	// and reviewing that head would only be done again on the next poll.
	moved map[string]bool

	// Runs, when set, shares the slots: see Poll.
	Runs Runs

	// handed is how many issues the last poll handed to the runner.
	handed int
}

// Handed is how many issues the last poll handed to the runner, new or asked
// for again, so the caller can let the runner take them up in the same tick.
func (w *Watcher) Handed() int { return w.handed }

func (w *Watcher) statePath() string { return StatePath(w.Root, w.DryRun) }

func (w *Watcher) logf(format string, args ...any) {
	if w.Log != nil {
		fmt.Fprintf(w.Log, "github: "+format+"\n", args...)
	}
}

func (w *Watcher) save() error { return w.state.Save(w.statePath()) }

// candidate is a job that is due and not yet started.
type candidate struct {
	kind string
	repo Repo
	pr   PR
	// due and all are a comments job's comments, and every comment on the PR.
	due, all []Comment
}

// Poll does one round: moves every running job along, looks at each
// repository's open PRs, and starts what is due. Comment jobs go ahead of
// reviews, since a person is waiting on a reply. It returns how many jobs it
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

	w.moved = map[string]bool{}
	w.handed = 0
	var errs []error
	var comments, reviews []candidate
	for _, repo := range w.Cfg.Repos {
		rs := w.state.Repo(repo.Name)
		prs, prErr := w.GH.OpenPRs(ctx, repo.Name)
		open := make(map[int]PR, len(prs))
		for _, p := range prs {
			open[p.Number] = p
		}
		for _, job := range slices.Clone(rs.Running) {
			if err := w.advance(ctx, d, repo, job, open[job.PR]); err != nil {
				errs = append(errs, err)
			}
		}
		if prErr != nil {
			errs = append(errs, fmt.Errorf("github: %s: %w", repo.Name, prErr))
			continue
		}
		Observe(rs, prs, w.Now())
		if w.Cfg.IssuesEnabled() {
			if err := w.issues(ctx, d, repo, rs); err != nil {
				errs = append(errs, err)
			}
		}
		for _, p := range ReviewJobs(w.Cfg, rs, prs, w.Now()) {
			if w.moved[fmt.Sprintf("%s#%d", repo.Name, p.Number)] {
				continue
			}
			reviews = append(reviews, candidate{kind: KindReview, repo: repo, pr: p})
		}
		for _, p := range prs {
			if !w.Cfg.CommentsEnabled() || p.Fork || !p.HasLabel(w.Cfg.Label) || running(rs, KindComments, p.Number) {
				continue
			}
			all, err := w.GH.PRComments(ctx, repo.Name, p.Number)
			if err != nil {
				errs = append(errs, fmt.Errorf("github: %s#%d: reading comments: %w", repo.Name, p.Number, err))
				continue
			}
			if due := CommentsDue(w.Cfg, rs, p, all, w.Now()); len(due) > 0 {
				comments = append(comments, candidate{kind: KindComments, repo: repo, pr: p, due: due, all: all})
			}
		}
	}
	if err := w.save(); err != nil {
		return 0, err
	}

	// Slots go to comment jobs first, since someone is waiting on a reply,
	// then to runs, then to reviews.
	busy := w.state.RunningCount()
	if w.Runs != nil {
		busy += w.Runs.Busy()
	}
	started := 0
	for _, c := range Admit(w.Cfg.MaxJobs, busy, comments) {
		if err := w.start(ctx, d, c); err != nil {
			errs = append(errs, err)
			continue
		}
		started++
	}
	if w.Runs != nil {
		if free := w.Cfg.MaxJobs - busy - started; free > 0 {
			n, err := w.Runs.Start(ctx, free)
			if err != nil {
				errs = append(errs, err)
			}
			started += n
		}
	}
	for _, c := range Admit(w.Cfg.MaxJobs, busy+started, reviews) {
		if err := w.start(ctx, d, c); err != nil {
			errs = append(errs, err)
			continue
		}
		started++
	}
	return started, errors.Join(errs...)
}

// Running is how many of the watcher's own jobs are running.
func (w *Watcher) Running() int {
	if w.state == nil {
		return 0
	}
	return w.state.RunningCount()
}

// Runs is the orchestrated-runs runner, as the watcher sees it when both run
// in one process: something else that holds slots and wants more.
type Runs interface {
	// Busy is how many slots runs hold.
	Busy() int
	// Start begins up to n waiting run sessions and says how many.
	Start(ctx context.Context, n int) (int, error)
}

// running reports whether a job of a kind is running on a PR.
func running(rs *RepoState, kind string, pr int) bool {
	for _, j := range rs.Running {
		if j.Kind == kind && j.PR == pr {
			return true
		}
	}
	return false
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

// worktreePath is where a job's worktree goes.
func (w *Watcher) worktreePath(repo, name string) string {
	return filepath.Join(Dir(w.Root), "worktrees", strings.ReplaceAll(repo, "/", "-"), name)
}

// start makes a job's worktree, records the job, and starts its session. The
// job is saved before the session exists, so a crash in between is seen on
// restart as a job with no session, and given one.
func (w *Watcher) start(ctx context.Context, d Daemon, c candidate) error {
	repo, pr := c.repo, c.pr
	rs := w.state.Repo(repo.Name)
	job := Job{Kind: c.kind, PR: pr.Number, SHA: pr.HeadSHA, Base: pr.BaseRef, Started: w.Now()}
	var fetch func() error
	switch c.kind {
	case KindReview:
		var prev *Reviewed
		if r, ok := rs.Reviewed[pr.Number]; ok {
			prev = &r
		}
		job.Through = rs.Handled[pr.Number]
		var asked []Comment
		if prev != nil && prev.Handled != job.Through {
			all, err := w.GH.PRComments(ctx, repo.Name, pr.Number)
			if err != nil {
				w.logf("%s#%d: reading comments for the review, which goes ahead without them: %v", repo.Name, pr.Number, err)
			}
			asked = Answered(prev.Handled, job.Through, all)
		}
		job.Worktree = w.worktreePath(repo.Name, fmt.Sprintf("review-%d-%s", pr.Number, short(pr.HeadSHA)))
		job.Prompt = ReviewPrompt(repo.Name, pr, prev, asked)
		job.MaxTurns = w.Cfg.Review.MaxTurns
		fetch = func() error { return w.Git.FetchPR(ctx, repo.Clone, pr.Number, pr.BaseRef) }
	case KindComments:
		job.HeadRef = pr.HeadRef
		job.Due = c.due
		for _, cm := range c.due {
			job.Through.Raise(cm)
		}
		job.Worktree = w.worktreePath(repo.Name, fmt.Sprintf("comments-%d-%d", pr.Number, c.due[0].ID))
		job.Prompt = CommentsPrompt(repo.Name, pr, c.due, c.all)
		job.Goal = CommentsGoal
		job.MaxTurns = w.Cfg.Comments.MaxTurns
		fetch = func() error { return w.Git.FetchBranch(ctx, repo.Clone, pr.HeadRef) }
	}

	if err := fetch(); err != nil {
		return fmt.Errorf("github: %s#%d: %w", repo.Name, pr.Number, err)
	}
	if _, err := os.Stat(job.Worktree); err == nil {
		// Left by a crash before the job was saved, or by an earlier failed
		// job of the same name, which a new attempt supersedes.
		_ = w.Git.RemoveWorktree(ctx, repo.Clone, job.Worktree)
	}
	if err := w.Git.AddWorktree(ctx, repo.Clone, job.Worktree, pr.HeadSHA); err != nil {
		return fmt.Errorf("github: %s#%d: %w", repo.Name, pr.Number, err)
	}
	rs.Running = append(rs.Running, job)
	if err := w.save(); err != nil {
		return err
	}
	w.logf("%s#%d %s: %s starting in %s", repo.Name, pr.Number, short(pr.HeadSHA), c.kind, job.Worktree)
	return w.begin(ctx, d, repo, job)
}

// begin creates a job's session, gives it its goal and sends its prompt,
// saving after each step so a restart picks up where this left off.
func (w *Watcher) begin(ctx context.Context, d Daemon, repo Repo, job Job) error {
	rs := w.state.Repo(repo.Name)
	if job.SessionID == "" {
		id, err := d.Create(ctx, job.Worktree, job.MaxTurns)
		if err != nil {
			return fmt.Errorf("github: %s#%d: creating a session: %w", repo.Name, job.PR, err)
		}
		job.SessionID = id
		update(rs, job)
		if err := w.save(); err != nil {
			return err
		}
	}
	// Setting the same goal again after a restart only repeats it.
	if job.Goal != "" {
		if err := d.SetGoal(ctx, job.SessionID, job.Goal); err != nil {
			return fmt.Errorf("github: %s#%d: session %s: setting the goal: %w", repo.Name, job.PR, job.SessionID, err)
		}
	}
	if err := d.SendPrompt(ctx, job.SessionID, job.Prompt); err != nil {
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
	rs.Running = slices.DeleteFunc(rs.Running, func(j Job) bool { return j.PR == job.PR && j.Kind == job.Kind })
}

// advance moves one running job along: gives it a session and a prompt if a
// crash left it without, finishes it if its session is done, fails it if its
// session ended any other way, and otherwise leaves it running. pr is the
// PR as the poll saw it, or zero if the poll could not see it.
func (w *Watcher) advance(ctx context.Context, d Daemon, repo Repo, job Job, pr PR) error {
	if job.SessionID == "" || !job.Prompted {
		if job.Prompt == "" && job.Kind == KindReview {
			// Recorded before jobs kept their prompt: rebuild it for the head
			// the job recorded, whatever the PR's head is now.
			pr.Number, pr.HeadSHA, pr.BaseRef = job.PR, job.SHA, job.Base
			job.Prompt, job.MaxTurns = ReviewPrompt(repo.Name, pr, nil, nil), w.Cfg.Review.MaxTurns
		}
		return w.begin(ctx, d, repo, job)
	}
	st, err := d.State(ctx, job.SessionID)
	if err != nil {
		return fmt.Errorf("github: %s#%d: session %s: %w", repo.Name, job.PR, job.SessionID, err)
	}
	events, err := d.Events(ctx, job.SessionID)
	if err != nil {
		return fmt.Errorf("github: %s#%d: session %s: %w", repo.Name, job.PR, job.SessionID, err)
	}
	answers, answered := assistantMessages(events)

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
			return w.fail(repo, job, "the session was stopped before it finished")
		}
	default:
		return w.fail(repo, job, "the session ended "+string(st.State))
	}
	if job.Kind == KindComments {
		return w.postComments(ctx, repo, job, answers)
	}
	return w.postReview(ctx, repo, job, answers)
}

// assistantMessages is what the model said, newest first, leaving out the
// empty messages that only carry tool calls. The second result is whether it
// said anything at all, empty or not: whether the loop has started.
func assistantMessages(events []protocol.Event) ([]string, bool) {
	var out []string
	started := false
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != protocol.EventMessage {
			continue
		}
		var m protocol.MessageData
		if json.Unmarshal(events[i].Data, &m) != nil || m.Role != "assistant" {
			continue
		}
		started = true
		if strings.TrimSpace(m.Content) != "" {
			out = append(out, m.Content)
		}
	}
	return out, started
}

// newest is the most recent answer to carry a block parse accepts, and
// whether there was one. The block is not always in the last message: the
// stop gate can send the model round again after it wrote the block, and
// its last word is then a summary without one.
func newest[T any](answers []string, parse func(string) (T, bool)) (T, bool) {
	for _, a := range answers {
		if v, ok := parse(a); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// last is the final thing the model said, which is posted as it stands when
// no answer carries a block.
func last(answers []string) string {
	if len(answers) == 0 {
		return ""
	}
	return answers[0]
}

// postReview builds the review from a finished session and posts it. Only a
// successful post marks the head reviewed; a failed one leaves the job for
// the next poll, which posts again from the same session.
func (w *Watcher) postReview(ctx context.Context, repo Repo, job Job, answers []string) error {
	diff, err := w.Git.Diff(ctx, job.Worktree, job.Base, job.SHA)
	if err != nil {
		w.logf("%s#%d: no diff, so every comment goes in the body: %v", repo.Name, job.PR, err)
	}
	final := last(answers)
	r, ok := newest(answers, ParseReview)
	post := BuildReview(r, ok, final, diff, job.SHA)

	if w.DryRun {
		fmt.Fprintf(w.Out, "would post a review on %s#%d at %s:\n%s\n", repo.Name, job.PR, short(job.SHA), post.JSON(true))
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
	rs.Reviewed[job.PR] = Reviewed{SHA: job.SHA, Summary: summary, Handled: job.Through}
	return w.done(ctx, repo, job, "review")
}

// postComments pushes a finished comments session's commits and posts its
// replies. Each step is recorded as it lands, so a retry after a failure
// neither pushes nor posts anything twice. A push the remote refuses means
// the branch moved while the session worked: nothing is posted, and the job
// fails.
func (w *Watcher) postComments(ctx context.Context, repo Repo, job Job, answers []string) error {
	rs := w.state.Repo(repo.Name)
	if !job.Pushed {
		head, err := w.Git.Head(ctx, job.Worktree)
		if err != nil {
			return fmt.Errorf("github: %s#%d: reading the worktree's head: %w", repo.Name, job.PR, err)
		}
		if head != job.SHA {
			if w.DryRun {
				fmt.Fprintf(w.Out, "would push %s to %s on %s#%d\n", short(head), job.HeadRef, repo.Name, job.PR)
			} else if err := w.Git.Push(ctx, job.Worktree, job.HeadRef); err != nil {
				return w.fail(repo, job, fmt.Sprintf("pushing to %s: %v", job.HeadRef, err))
			} else {
				w.logf("%s#%d: pushed %s to %s", repo.Name, job.PR, short(head), job.HeadRef)
				w.moved[fmt.Sprintf("%s#%d", repo.Name, job.PR)] = true
			}
		}
		job.Pushed = true
		update(rs, job)
		if err := w.save(); err != nil {
			return err
		}
	}

	replies, ok := newest(answers, ParseReplies)
	out := BuildReplies(job.Due, replies, ok, last(answers))
	type post struct {
		key  string
		root int64
		body string
	}
	var posts []post
	for _, t := range out.Threads {
		posts = append(posts, post{t.Key, t.Root, t.Body})
	}
	if out.Conversation != "" {
		posts = append(posts, post{ConversationKey, 0, out.Conversation})
	}
	for _, p := range posts {
		if slices.Contains(job.Posted, p.key) {
			continue
		}
		var err error
		switch {
		case w.DryRun && p.root != 0:
			fmt.Fprintf(w.Out, "would reply in thread %d on %s#%d:\n%s\n", p.root, repo.Name, job.PR, p.body)
		case w.DryRun:
			fmt.Fprintf(w.Out, "would comment on %s#%d:\n%s\n", repo.Name, job.PR, p.body)
		case p.root != 0:
			err = w.GH.ReplyTo(ctx, repo.Name, job.PR, p.root, p.body)
		default:
			err = w.GH.Comment(ctx, repo.Name, job.PR, p.body)
		}
		if err != nil {
			return fmt.Errorf("github: %s#%d: posting replies from session %s: %w", repo.Name, job.PR, job.SessionID, err)
		}
		job.Posted = append(job.Posted, p.key)
		update(rs, job)
		if err := w.save(); err != nil {
			return err
		}
	}

	h := rs.Handled[job.PR]
	h.Merge(job.Through)
	rs.Handled[job.PR] = h
	delete(rs.FailedThrough, job.PR)
	return w.done(ctx, repo, job, "replies")
}

// done drops a finished job, saves, and removes its worktree.
func (w *Watcher) done(ctx context.Context, repo Repo, job Job, what string) error {
	drop(w.state.Repo(repo.Name), job)
	if err := w.save(); err != nil {
		return err
	}
	verb := "posted"
	if w.DryRun {
		verb = "printed (dry run)"
	}
	w.logf("%s#%d %s: %s %s from session %s", repo.Name, job.PR, short(job.SHA), what, verb, job.SessionID)
	if err := w.Git.RemoveWorktree(ctx, repo.Clone, job.Worktree); err != nil {
		w.logf("%s#%d: removing %s: %v", repo.Name, job.PR, job.Worktree, err)
	}
	return nil
}

// fail records a job as failed and keeps its worktree for a look. A failed
// review is not tried again until the PR has a new head; a failed comments
// job, until a comment arrives past the ones it had.
func (w *Watcher) fail(repo Repo, job Job, why string) error {
	rs := w.state.Repo(repo.Name)
	switch job.Kind {
	case KindComments:
		f := rs.FailedThrough[job.PR]
		f.Merge(job.Through)
		rs.FailedThrough[job.PR] = f
	default:
		rs.Failed[job.PR] = job.SHA
	}
	drop(rs, job)
	if err := w.save(); err != nil {
		return err
	}
	w.logf("%s#%d %s: %s failed: %s; session %s, worktree %s", repo.Name, job.PR, short(job.SHA), job.Kind, why, job.SessionID, job.Worktree)
	return nil
}

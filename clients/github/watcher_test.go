package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

type fakeGH struct {
	prs           map[string][]PR
	listErr       error
	posts         []ReviewPost
	postErr       error
	comments      map[int][]Comment
	replies       []string // "root: body"
	convo         []string
	replyErr      error
	convoErr      error
	issues        []Issue
	issueComments map[int][]Comment
}

func (g *fakeGH) OpenIssues(context.Context, string, string) ([]Issue, error) { return g.issues, nil }

func (g *fakeGH) IssueComments(_ context.Context, _ string, n int) ([]Comment, error) {
	return g.issueComments[n], nil
}

func (g *fakeGH) PRComments(_ context.Context, _ string, n int) ([]Comment, error) {
	return g.comments[n], nil
}

func (g *fakeGH) ReplyTo(_ context.Context, _ string, _ int, root int64, body string) error {
	if g.replyErr != nil {
		return g.replyErr
	}
	g.replies = append(g.replies, fmt.Sprintf("%d: %s", root, body))
	return nil
}

func (g *fakeGH) Comment(_ context.Context, _ string, _ int, body string) error {
	if g.convoErr != nil {
		return g.convoErr
	}
	g.convo = append(g.convo, body)
	return nil
}

func (g *fakeGH) OpenPRs(_ context.Context, repo string) ([]PR, error) {
	return g.prs[repo], g.listErr
}

func (g *fakeGH) PostReview(_ context.Context, _ string, _ int, post ReviewPost) error {
	if g.postErr != nil {
		return g.postErr
	}
	g.posts = append(g.posts, post)
	return nil
}

type fakeGit struct {
	calls   []string
	diff    string
	heads   map[string]string // worktree path → HEAD; absent means where it was added
	added   map[string]string
	pushErr error
}

func (g *fakeGit) FetchBranch(_ context.Context, clone, ref string) error {
	g.calls = append(g.calls, "fetch-branch "+clone+" "+ref)
	return nil
}

func (g *fakeGit) Head(_ context.Context, dir string) (string, error) {
	if h, ok := g.heads[dir]; ok {
		return h, nil
	}
	return g.added[dir], nil
}

func (g *fakeGit) Push(_ context.Context, dir, ref string) error {
	if g.pushErr != nil {
		return g.pushErr
	}
	g.calls = append(g.calls, "push "+dir+" "+ref)
	return nil
}

func (g *fakeGit) FetchPR(_ context.Context, clone string, n int, base string) error {
	g.calls = append(g.calls, fmt.Sprintf("fetch %s %d %s", clone, n, base))
	return nil
}

func (g *fakeGit) AddWorktree(_ context.Context, _, path, sha string) error {
	g.calls = append(g.calls, "add "+path+" "+sha)
	if g.added == nil {
		g.added = map[string]string{}
	}
	g.added[path] = sha
	return nil
}

func (g *fakeGit) Diff(_ context.Context, _, base, sha string) (string, error) {
	return g.diff, nil
}

func (g *fakeGit) RemoveWorktree(_ context.Context, _, path string) error {
	g.calls = append(g.calls, "remove "+path)
	return nil
}

func (g *fakeGit) did(prefix string) int {
	n := 0
	for _, c := range g.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

type fakeSession struct {
	workspace string
	maxTurns  int
	goals     []string
	prompts   []string
	state     protocol.SessionState
	// earlier are assistant messages before the final one.
	earlier []string
	final   string
}

type fakeDaemon struct {
	sessions  map[string]*fakeSession
	creates   int
	stops     int
	promptErr error
	homes     map[string]*fakeHome
	reruns    int
}

type fakeHome struct {
	workspace, description string
	labels                 []string
}

func (d *fakeDaemon) CreateHome(_ context.Context, ws, description string, labels []string) (string, error) {
	if d.homes == nil {
		d.homes = map[string]*fakeHome{}
	}
	id := fmt.Sprintf("H%d", len(d.homes)+1)
	d.homes[id] = &fakeHome{workspace: ws, description: description, labels: labels}
	return id, nil
}

func (d *fakeDaemon) Labels(_ context.Context, id string) ([]string, error) {
	return d.homes[id].labels, nil
}

func (d *fakeDaemon) Rerun(_ context.Context, id, description string, labels []string) error {
	d.reruns++
	d.homes[id].description, d.homes[id].labels = description, labels
	return nil
}

func newFakeDaemon() *fakeDaemon { return &fakeDaemon{sessions: map[string]*fakeSession{}} }

func (d *fakeDaemon) Create(_ context.Context, ws string, maxTurns int) (string, error) {
	d.creates++
	id := fmt.Sprintf("S%d", d.creates)
	d.sessions[id] = &fakeSession{workspace: ws, maxTurns: maxTurns, state: protocol.StateIdle}
	return id, nil
}

func (d *fakeDaemon) SetGoal(_ context.Context, id, condition string) error {
	d.sessions[id].goals = append(d.sessions[id].goals, condition)
	return nil
}

func (d *fakeDaemon) SendPrompt(_ context.Context, id, text string) error {
	if d.promptErr != nil {
		return d.promptErr
	}
	s := d.sessions[id]
	s.prompts = append(s.prompts, text)
	s.state = protocol.StateRunning
	return nil
}

func (d *fakeDaemon) State(_ context.Context, id string) (protocol.State, error) {
	s, ok := d.sessions[id]
	if !ok {
		return protocol.State{}, errors.New("no such session")
	}
	return protocol.State{State: s.state}, nil
}

func (d *fakeDaemon) Stop(_ context.Context, id string) error {
	d.stops++
	d.sessions[id].state = protocol.StateCompleted
	return nil
}

func (d *fakeDaemon) Events(_ context.Context, id string) ([]protocol.Event, error) {
	s := d.sessions[id]
	var evs []protocol.Event
	for _, p := range s.prompts {
		b, _ := json.Marshal(protocol.MessageData{Role: "user", Content: p})
		evs = append(evs, protocol.Event{Type: protocol.EventMessage, Data: b})
	}
	for _, m := range append(slices.Clone(s.earlier), s.final) {
		if m == "" && s.final == "" {
			continue
		}
		b, _ := json.Marshal(protocol.MessageData{Role: "assistant", Content: m})
		evs = append(evs, protocol.Event{Type: protocol.EventMessage, Data: b})
	}
	return evs, nil
}

func (d *fakeDaemon) Close() {}

// finish makes a session end its turn with a final message.
func (d *fakeDaemon) finish(id, final string, state protocol.SessionState) {
	d.sessions[id].final = final
	d.sessions[id].state = state
}

type rig struct {
	w   *Watcher
	gh  *fakeGH
	git *fakeGit
	d   *fakeDaemon
	now time.Time
	log bytes.Buffer
	out bytes.Buffer
}

const repoName = "kyle/breezeway"

func newRig(t *testing.T, prs ...PR) *rig {
	t.Helper()
	cfg := Config{Repos: []Repo{{Name: repoName, Clone: "C:/src/breezeway"}}}
	cfg.withDefaults()
	r := &rig{
		gh:  &fakeGH{prs: map[string][]PR{repoName: prs}},
		git: &fakeGit{diff: readDiff(t)},
		d:   newFakeDaemon(),
		now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
	}
	r.w = r.watcher(cfg, t.TempDir())
	return r
}

// watcher builds a Watcher over the rig's fakes; a second one over the same
// root is a restart.
func (r *rig) watcher(cfg Config, root string) *Watcher {
	return &Watcher{
		Cfg: cfg, Root: root, GH: r.gh, Git: r.git,
		Dial: func(context.Context) (Daemon, error) { return r.d, nil },
		Now:  func() time.Time { return r.now },
		Log:  &r.log, Out: &r.out,
	}
}

func (r *rig) poll(t *testing.T) int {
	t.Helper()
	n, err := r.w.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v\nlog:\n%s", err, r.log.String())
	}
	return n
}

// started polls once to see the PRs, then again past the quiet period.
func (r *rig) started(t *testing.T) {
	t.Helper()
	r.poll(t)
	r.now = r.now.Add(6 * time.Minute)
	r.poll(t)
}

func (r *rig) repo() *RepoState { return r.w.state.Repo(repoName) }

var pr7 = PR{Number: 7, HeadSHA: "abc1234def", HeadRef: "feat", BaseRef: "main", Title: "Add the thing", Body: "It adds the thing."}

const goodFinal = "Looked.\n```json\n{\"summary\":\"One bug.\",\"comments\":[{\"path\":\"main.go\",\"line\":3,\"body\":\"off by one\"}]}\n```"

func TestWatcherStartsAReviewAfterTheQuietPeriod(t *testing.T) {
	r := newRig(t, pr7)
	if n := r.poll(t); n != 0 || r.d.creates != 0 {
		t.Fatalf("a PR seen for the first time started %d jobs", n)
	}
	r.now = r.now.Add(6 * time.Minute)
	if n := r.poll(t); n != 1 {
		t.Fatalf("started %d jobs, want 1", n)
	}
	s := r.d.sessions["S1"]
	if !strings.Contains(s.workspace, "review-7-abc1234") || s.maxTurns != 100 {
		t.Errorf("session in %q with %d turns", s.workspace, s.maxTurns)
	}
	if len(s.prompts) != 1 || !strings.Contains(s.prompts[0], "Add the thing") {
		t.Errorf("prompts = %q", s.prompts)
	}
	if r.git.did("fetch C:/src/breezeway 7 main") != 1 || r.git.did("add "+s.workspace+" abc1234def") != 1 {
		t.Errorf("git calls = %q", r.git.calls)
	}
	if j := r.repo().Running; len(j) != 1 || j[0].SessionID != "S1" || !j[0].Prompted {
		t.Errorf("running = %+v", j)
	}
}

func TestWatcherPostsAFinishedReview(t *testing.T) {
	r := newRig(t, pr7)
	r.started(t)
	r.d.finish("S1", goodFinal, protocol.StateIdle)
	r.poll(t)

	if r.d.stops != 1 {
		t.Errorf("stops = %d, want 1", r.d.stops)
	}
	if len(r.gh.posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(r.gh.posts))
	}
	p := r.gh.posts[0]
	if p.CommitID != "abc1234def" || p.Event != "COMMENT" || !strings.HasSuffix(p.Body, Marker) || len(p.Comments) != 1 {
		t.Errorf("post = %+v", p)
	}
	rs := r.repo()
	if rs.Reviewed[7] != (Reviewed{SHA: "abc1234def", Summary: "One bug."}) || len(rs.Running) != 0 {
		t.Errorf("state: reviewed %+v, running %+v", rs.Reviewed, rs.Running)
	}
	if r.git.did("remove ") != 1 {
		t.Errorf("worktree not removed: %q", r.git.calls)
	}
	if r.poll(t) != 0 || len(r.gh.posts) != 1 {
		t.Error("a reviewed head was reviewed again")
	}
}

func TestWatcherLeavesARunningSessionAlone(t *testing.T) {
	for _, state := range []protocol.SessionState{protocol.StateRunning, protocol.StateIdle} {
		t.Run(string(state), func(t *testing.T) {
			r := newRig(t, pr7)
			r.started(t)
			// Idle with no answer yet is a session whose loop has not started.
			r.d.sessions["S1"].state = state
			r.poll(t)
			if r.d.stops != 0 || len(r.gh.posts) != 0 || len(r.repo().Running) != 1 {
				t.Errorf("stops %d, posts %d, running %d", r.d.stops, len(r.gh.posts), len(r.repo().Running))
			}
		})
	}
}

func TestWatcherRecordsAFailedSession(t *testing.T) {
	for _, state := range []protocol.SessionState{protocol.StateBlocked, protocol.StatePaused, protocol.StateError, protocol.StateCompleted} {
		t.Run(string(state), func(t *testing.T) {
			r := newRig(t, pr7)
			r.started(t)
			r.d.finish("S1", goodFinal, state)
			r.poll(t)
			rs := r.repo()
			if rs.Failed[7] != "abc1234def" || len(rs.Running) != 0 || len(r.gh.posts) != 0 {
				t.Errorf("failed %v, running %v, posts %d", rs.Failed, rs.Running, len(r.gh.posts))
			}
			if r.git.did("remove ") != 0 {
				t.Error("a failed job's worktree was removed")
			}
			if !strings.Contains(r.log.String(), "S1") {
				t.Errorf("the log does not name the session:\n%s", r.log.String())
			}
			if r.poll(t) != 0 {
				t.Error("a failed head was tried again")
			}
		})
	}
}

func TestWatcherRetriesAFailedPostFromTheSameSession(t *testing.T) {
	r := newRig(t, pr7)
	r.started(t)
	r.d.finish("S1", goodFinal, protocol.StateIdle)
	r.gh.postErr = errors.New("502")
	if _, err := r.w.Poll(context.Background()); err == nil {
		t.Fatal("a failed post was not reported")
	}
	if _, ok := r.repo().Reviewed[7]; ok {
		t.Fatal("a failed post was recorded as reviewed")
	}
	r.gh.postErr = nil
	r.poll(t)
	if len(r.gh.posts) != 1 || r.d.creates != 1 || r.d.stops != 1 {
		t.Errorf("posts %d, creates %d, stops %d; want 1, 1, 1", len(r.gh.posts), r.d.creates, r.d.stops)
	}
}

func TestWatcherReattachesAfterARestart(t *testing.T) {
	r := newRig(t, pr7)
	r.started(t)
	restarted := r.watcher(r.w.Cfg, r.w.Root)
	r.w = restarted
	r.d.finish("S1", goodFinal, protocol.StateIdle)
	r.poll(t)
	if len(r.gh.posts) != 1 || r.d.creates != 1 {
		t.Errorf("posts %d, creates %d after a restart", len(r.gh.posts), r.d.creates)
	}
}

func TestWatcherPromptsASessionLeftWithoutOne(t *testing.T) {
	r := newRig(t, pr7)
	r.d.promptErr = errors.New("connection lost")
	r.poll(t)
	r.now = r.now.Add(6 * time.Minute)
	if _, err := r.w.Poll(context.Background()); err == nil {
		t.Fatal("a failed prompt was not reported")
	}
	if j := r.repo().Running; len(j) != 1 || j[0].SessionID != "S1" || j[0].Prompted {
		t.Fatalf("running = %+v", j)
	}
	r.d.promptErr = nil
	r.poll(t)
	if s := r.d.sessions["S1"]; len(s.prompts) != 1 || r.d.creates != 1 {
		t.Errorf("prompts %d, creates %d", len(s.prompts), r.d.creates)
	}
}

func TestWatcherDryRun(t *testing.T) {
	r := newRig(t, pr7)
	r.w.DryRun = true
	r.started(t)
	r.d.finish("S1", goodFinal, protocol.StateIdle)
	r.poll(t)
	if len(r.gh.posts) != 0 {
		t.Error("a dry run posted")
	}
	if !strings.Contains(r.out.String(), "would post a review on kyle/breezeway#7") || !strings.Contains(r.out.String(), "off by one") ||
		!strings.Contains(r.out.String(), Marker) {
		t.Errorf("dry run printed:\n%s", r.out.String())
	}
	if strings.Contains(r.log.String(), "review posted") {
		t.Errorf("a dry run logged a post:\n%s", r.log.String())
	}
	if _, err := os.Stat(StatePath(r.w.Root, true)); err != nil {
		t.Errorf("no dry-run state: %v", err)
	}
	if _, err := os.Stat(StatePath(r.w.Root, false)); err == nil {
		t.Error("a dry run wrote the real state")
	}
}

func TestWatcherKeepsToMaxJobs(t *testing.T) {
	r := newRig(t, pr7, PR{Number: 8, HeadSHA: "fff0000", BaseRef: "main"})
	r.started(t)
	if r.d.creates != 1 {
		t.Fatalf("creates = %d with max_jobs 1", r.d.creates)
	}
	r.d.finish("S1", goodFinal, protocol.StateIdle)
	r.poll(t)
	if r.d.creates != 2 || !strings.Contains(r.d.sessions["S2"].workspace, "review-8-") {
		t.Errorf("the second PR did not start once the first finished: creates %d", r.d.creates)
	}
}

func TestWatcherSavesNothingWhenTheDaemonIsDown(t *testing.T) {
	r := newRig(t, pr7)
	r.w.Dial = func(context.Context) (Daemon, error) { return nil, errors.New("refused") }
	if _, err := r.w.Poll(context.Background()); err == nil {
		t.Fatal("no error")
	}
	if _, err := os.Stat(StatePath(r.w.Root, false)); err == nil {
		t.Error("state written without a daemon")
	}
}

func TestRunOnceWaitsForItsJobs(t *testing.T) {
	r := newRig(t, pr7)
	r.w.Cfg.Quiet = new(Duration)
	r.w.Wait = time.Millisecond
	r.w.Dial = func(context.Context) (Daemon, error) {
		// The session finishes as soon as it has been prompted.
		for id, s := range r.d.sessions {
			if len(s.prompts) > 0 && s.final == "" {
				r.d.finish(id, goodFinal, protocol.StateIdle)
			}
		}
		return r.d, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.gh.posts) != 1 {
		t.Errorf("posts = %d, want 1", len(r.gh.posts))
	}
}

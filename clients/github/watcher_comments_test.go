package github

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

var labeledPR = PR{Number: 9, HeadSHA: "headsha1234", HeadRef: "feat/x", BaseRef: "main", Title: "Parse cursors", Labels: []string{"nabu"}}

const repliesFinal = "Done.\n```json\n{\"replies\":[{\"id\":10,\"body\":\"Renamed in a1b2c3d.\"},{\"id\":900,\"body\":\"README updated.\"}]}\n```"

// commentsRig is a rig whose one PR is labeled and has a line comment and a
// conversation comment from an hour ago. Reviewing is off, so only comment
// jobs run.
func commentsRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, labeledPR)
	off := false
	r.w.Cfg.Review.Enabled = &off
	old := r.now.Add(-time.Hour)
	r.gh.comments = map[int][]Comment{9: {
		{Kind: CommentLine, ID: 10, Author: "kyle", Body: "Rename to parseCursor.", Path: "a.go", Line: 3, Created: old, URL: "u10"},
		{Kind: CommentIssue, ID: 900, Author: "kyle", Body: "Update the README.", Created: old, URL: "u900"},
	}}
	return r
}

func TestWatcherStartsACommentsJob(t *testing.T) {
	r := commentsRig(t)
	if n := r.poll(t); n != 1 {
		t.Fatalf("started %d jobs, want 1", n)
	}
	s := r.d.sessions["S1"]
	if !strings.Contains(s.workspace, "comments-9-10") || s.maxTurns != 60 {
		t.Errorf("session in %q with %d turns", s.workspace, s.maxTurns)
	}
	if len(s.goals) != 1 || s.goals[0] != CommentsGoal {
		t.Errorf("goals = %q", s.goals)
	}
	if len(s.prompts) != 1 || !strings.Contains(s.prompts[0], "Rename to parseCursor.") || !strings.Contains(s.prompts[0], "Update the README.") {
		t.Errorf("prompt = %q", s.prompts)
	}
	if r.git.did("fetch-branch C:/src/breezeway feat/x") != 1 || r.git.did("add "+s.workspace+" headsha1234") != 1 {
		t.Errorf("git calls = %q", r.git.calls)
	}
	if n := r.poll(t); n != 0 {
		t.Error("a second job started on a PR with one running")
	}
}

func TestWatcherPushesAndReplies(t *testing.T) {
	r := commentsRig(t)
	r.poll(t)
	ws := r.d.sessions["S1"].workspace
	r.git.heads = map[string]string{ws: "newcommit99"}
	r.d.finish("S1", repliesFinal, protocol.StateIdle)
	r.poll(t)

	if r.git.did("push "+ws+" feat/x") != 1 {
		t.Errorf("git calls = %q", r.git.calls)
	}
	if len(r.gh.replies) != 1 || !strings.HasPrefix(r.gh.replies[0], "10: "+Signature+": Renamed in a1b2c3d.") {
		t.Errorf("thread replies = %q", r.gh.replies)
	}
	if len(r.gh.convo) != 1 || !strings.Contains(r.gh.convo[0], "README updated.") || !strings.HasSuffix(r.gh.convo[0], Marker) {
		t.Errorf("conversation = %q", r.gh.convo)
	}
	rs := r.repo()
	if rs.Handled[9] != (Marks{Line: 10, Issue: 900}) || len(rs.Running) != 0 || r.git.did("remove ") != 1 {
		t.Errorf("handled %+v, running %+v, calls %q", rs.Handled, rs.Running, r.git.calls)
	}

	// nabu's own replies come back as comments; they start nothing.
	r.gh.comments[9] = append(r.gh.comments[9],
		Comment{Kind: CommentLine, ID: 11, Body: r.gh.replies[0][4:], InReplyTo: 10, Created: r.now},
		Comment{Kind: CommentIssue, ID: 901, Body: r.gh.convo[0], Created: r.now})
	r.now = r.now.Add(time.Hour)
	if n := r.poll(t); n != 0 {
		t.Error("nabu's own replies started a job")
	}
}

func TestWatcherPushesNothingWhenNothingWasCommitted(t *testing.T) {
	r := commentsRig(t)
	r.poll(t)
	r.d.finish("S1", repliesFinal, protocol.StateIdle)
	r.poll(t)
	if r.git.did("push ") != 0 {
		t.Errorf("pushed without a new commit: %q", r.git.calls)
	}
	if len(r.gh.replies) != 1 || len(r.gh.convo) != 1 {
		t.Errorf("replies %q, conversation %q", r.gh.replies, r.gh.convo)
	}
}

func TestWatcherARejectedPushPostsNothing(t *testing.T) {
	r := commentsRig(t)
	r.poll(t)
	r.git.heads = map[string]string{r.d.sessions["S1"].workspace: "newcommit99"}
	r.git.pushErr = errors.New("! [rejected] (fetch first)")
	r.d.finish("S1", repliesFinal, protocol.StateIdle)
	r.poll(t)

	rs := r.repo()
	if len(r.gh.replies)+len(r.gh.convo) != 0 {
		t.Error("posted after a rejected push")
	}
	if rs.FailedThrough[9] != (Marks{Line: 10, Issue: 900}) || len(rs.Running) != 0 || len(rs.Handled) != 0 {
		t.Errorf("failed-through %+v, running %+v, handled %+v", rs.FailedThrough, rs.Running, rs.Handled)
	}
	if !strings.Contains(r.log.String(), "rejected") {
		t.Errorf("the log does not say why:\n%s", r.log.String())
	}

	r.git.pushErr = nil
	if n := r.poll(t); n != 0 {
		t.Error("a failed job was tried again with no new comment")
	}
	r.gh.comments[9] = append(r.gh.comments[9], Comment{Kind: CommentIssue, ID: 950, Body: "try again", Created: r.now.Add(-time.Hour)})
	if n := r.poll(t); n != 1 {
		t.Fatal("a newer comment did not start a job after a failure")
	}
	if p := r.d.sessions["S2"].prompts[0]; !strings.Contains(p, "Rename to parseCursor.") || !strings.Contains(p, "try again") {
		t.Errorf("the retry should answer everything unhandled:\n%s", p)
	}
}

func TestWatcherResumesAFailedPostWithoutRepeating(t *testing.T) {
	r := commentsRig(t)
	r.poll(t)
	r.git.heads = map[string]string{r.d.sessions["S1"].workspace: "newcommit99"}
	r.d.finish("S1", repliesFinal, protocol.StateIdle)
	// The thread reply lands, then the conversation comment fails.
	r.gh.convoErr = errors.New("502")
	if _, err := r.w.Poll(context.Background()); err == nil {
		t.Fatal("a failed post was not reported")
	}
	if len(r.gh.replies) != 1 {
		t.Fatalf("replies before the retry = %q", r.gh.replies)
	}
	r.gh.convoErr = nil
	r.poll(t)
	if r.git.did("push ") != 1 || len(r.gh.replies) != 1 || len(r.gh.convo) != 1 {
		t.Errorf("pushes %d, replies %q, conversation %q", r.git.did("push "), r.gh.replies, r.gh.convo)
	}
}

func TestWatcherPutsCommentsBeforeReviews(t *testing.T) {
	r := commentsRig(t)
	on := true
	r.w.Cfg.Review.Enabled = &on
	r.w.Cfg.Quiet = new(Duration)
	// PR 8 would be reviewed and sorts first; the comments job still wins.
	r.gh.prs[repoName] = []PR{{Number: 8, HeadSHA: "eee", BaseRef: "main"}, labeledPR}
	r.poll(t)
	if r.d.creates != 1 || !strings.Contains(r.d.sessions["S1"].workspace, "comments-9-") {
		t.Errorf("first job in %q", r.d.sessions["S1"].workspace)
	}
}

func TestWatcherDryRunNeitherPushesNorPosts(t *testing.T) {
	r := commentsRig(t)
	r.w.DryRun = true
	r.poll(t)
	r.git.heads = map[string]string{r.d.sessions["S1"].workspace: "newcommit99"}
	r.d.finish("S1", repliesFinal, protocol.StateIdle)
	r.poll(t)
	if r.git.did("push ") != 0 || len(r.gh.replies)+len(r.gh.convo) != 0 {
		t.Error("a dry run pushed or posted")
	}
	for _, s := range []string{"would push newcomm to feat/x", "would reply in thread 10", "would comment on kyle/breezeway#9"} {
		if !strings.Contains(r.out.String(), s) {
			t.Errorf("dry run output lacks %q:\n%s", s, r.out.String())
		}
	}
}

// Seen live: the model wrote its replies block, the stop gate sent it round
// again, and its last message was a summary without the block. The replies
// must still be found, or every thread goes unanswered.
func TestWatcherFindsRepliesBeforeTheLastMessage(t *testing.T) {
	r := commentsRig(t)
	r.poll(t)
	s := r.d.sessions["S1"]
	s.earlier = []string{"", repliesFinal, ""}
	r.d.finish("S1", "All three comments have been addressed. Committed as a1b2c3d.", protocol.StateIdle)
	r.poll(t)
	if len(r.gh.replies) != 1 || !strings.Contains(r.gh.replies[0], "Renamed in a1b2c3d.") {
		t.Errorf("thread replies = %q", r.gh.replies)
	}
	if len(r.gh.convo) != 1 || strings.Contains(r.gh.convo[0], "All three comments") {
		t.Errorf("the summary was posted instead of the replies: %q", r.gh.convo)
	}
}

func TestWatcherFindsAReviewBeforeTheLastMessage(t *testing.T) {
	r := newRig(t, pr7)
	r.started(t)
	r.d.sessions["S1"].earlier = []string{goodFinal}
	r.d.finish("S1", "That's my review.", protocol.StateIdle)
	r.poll(t)
	if len(r.gh.posts) != 1 || len(r.gh.posts[0].Comments) != 1 || !strings.Contains(r.gh.posts[0].Body, "One bug.") {
		t.Errorf("posts = %+v", r.gh.posts)
	}
}

// Seen live: the poll that pushed a comments job's commit started a review
// of the head the push had just replaced, because it listed the PRs first.
func TestWatcherDoesNotReviewAHeadItJustPushedOver(t *testing.T) {
	r := commentsRig(t)
	on := true
	r.w.Cfg.Review.Enabled = &on
	r.w.Cfg.Quiet = new(Duration)
	r.poll(t) // the comments job starts first; the review waits on max_jobs
	r.git.heads = map[string]string{r.d.sessions["S1"].workspace: "newcommit99"}
	r.d.finish("S1", repliesFinal, protocol.StateIdle)
	if n := r.poll(t); n != 0 {
		t.Fatalf("started %d jobs in the poll that pushed; the head it listed is gone", n)
	}
	moved := labeledPR
	moved.HeadSHA = "newcommit99"
	r.gh.prs[repoName] = []PR{moved}
	r.poll(t)
	if r.d.creates != 2 || !strings.Contains(r.d.sessions["S2"].workspace, "review-9-newcomm") {
		t.Errorf("the new head was not reviewed: creates %d", r.d.creates)
	}
}

type fakeRuns struct {
	busy    int
	waiting int
	asked   []int
}

func (f *fakeRuns) Busy() int { return f.busy }

func (f *fakeRuns) Start(_ context.Context, n int) (int, error) {
	f.asked = append(f.asked, n)
	s := min(n, f.waiting)
	f.waiting -= s
	f.busy += s
	return s, nil
}

// Slots go to comment jobs, then runs, then reviews.
func TestRunsShareTheSlots(t *testing.T) {
	setup := func(t *testing.T, comments bool, runsWaiting int) (*rig, *fakeRuns) {
		r := commentsRig(t)
		on := true
		r.w.Cfg.Review.Enabled = &on
		r.w.Cfg.Quiet = new(Duration)
		prs := []PR{{Number: 8, HeadSHA: "eee", BaseRef: "main"}}
		if comments {
			prs = append(prs, labeledPR)
		}
		r.gh.prs[repoName] = prs
		runs := &fakeRuns{waiting: runsWaiting}
		r.w.Runs = runs
		return r, runs
	}

	t.Run("a comment job first", func(t *testing.T) {
		r, runs := setup(t, true, 1)
		r.poll(t)
		if r.d.creates != 1 || !strings.Contains(r.d.sessions["S1"].workspace, "comments-9-") || len(runs.asked) != 0 {
			t.Errorf("creates %d, runs asked %v", r.d.creates, runs.asked)
		}
	})
	t.Run("then a run", func(t *testing.T) {
		r, runs := setup(t, false, 1)
		r.poll(t)
		if r.d.creates != 0 || len(runs.asked) != 1 || runs.asked[0] != 1 {
			t.Errorf("creates %d, runs asked %v", r.d.creates, runs.asked)
		}
	})
	t.Run("then a review", func(t *testing.T) {
		r, runs := setup(t, false, 0)
		r.poll(t)
		if r.d.creates != 1 || !strings.Contains(r.d.sessions["S1"].workspace, "review-8-") || len(runs.asked) != 1 {
			t.Errorf("creates %d, runs asked %v", r.d.creates, runs.asked)
		}
	})
	t.Run("a busy run holds its slot", func(t *testing.T) {
		r, runs := setup(t, true, 0)
		runs.busy = 1
		r.poll(t)
		if r.d.creates != 0 || len(runs.asked) != 0 {
			t.Errorf("creates %d, runs asked %v", r.d.creates, runs.asked)
		}
	})
}

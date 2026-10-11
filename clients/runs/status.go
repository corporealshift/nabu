package runs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// A run's and a goal's status: a snapshot of where it is and how it got
// there, set on its home for the phone's Run and Goal pages
// (docs/specs/2026-10-11-run-page-design.md). The shapes are this package's
// own; the daemon stores them without reading them. testdata/status holds an
// example of each, which the Android parser's tests decode too.

// StatusVersion is the version of both shapes. A reader that does not know a
// version says so instead of guessing.
const StatusVersion = 1

// statusText bounds each reason and result in a snapshot. The history keeps
// longer notes for the reflection; a page needs one line.
const statusText = 300

// RunStatus is a run's snapshot. Task numbers count from 1; 0 is no task.
type RunStatus struct {
	Version   int         `json:"version"`
	Title     string      `json:"title"`
	Step      Step        `json:"step"`
	Task      int         `json:"task,omitempty"`
	Tasks     []string    `json:"tasks"`
	Attempt   int         `json:"attempt"`
	Since     time.Time   `json:"since,omitzero"`
	Session   string      `json:"session,omitempty"`
	Waiting   bool        `json:"waiting"`
	Goal      string      `json:"goal,omitempty"`
	GoalTitle string      `json:"goal_title,omitempty"`
	PR        StatusPR    `json:"pr"`
	Fixes     int         `json:"fixes"`
	CIFixes   int         `json:"ci_fixes"`
	Revisions int         `json:"revisions"`
	FailedAt  Step        `json:"failed_at,omitempty"`
	Why       string      `json:"why,omitempty"`
	History   []StatusRow `json:"history"`
	Brief     string      `json:"brief"`
	Started   time.Time   `json:"started"`
	Updated   time.Time   `json:"updated"`
}

// StatusPR is a pull request, or none when Number is 0.
type StatusPR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// StatusRow is one finished step: a Record without its note.
type StatusRow struct {
	Step    Step      `json:"step"`
	Task    int       `json:"task,omitempty"`
	Session string    `json:"session,omitempty"`
	OK      bool      `json:"ok"`
	Result  string    `json:"result,omitempty"`
	Why     string    `json:"why,omitempty"`
	At      time.Time `json:"at"`
}

// GoalStatus is a goal's snapshot. Its briefs carry titles, not their text:
// the text is on each run's own status.
type GoalStatus struct {
	Version  int               `json:"version"`
	Text     string            `json:"text"`
	Step     GoalStep          `json:"step"`
	Round    int               `json:"round"`
	Rounds   int               `json:"rounds"`
	Since    time.Time         `json:"since,omitzero"`
	Briefs   []GoalStatusBrief `json:"briefs"`
	Verdicts []Verdict         `json:"verdicts"`
	PR       StatusPR          `json:"pr"`
	Why      string            `json:"why,omitempty"`
	Started  time.Time         `json:"started"`
	Updated  time.Time         `json:"updated"`
}

// GoalStatusBrief is one of a goal's briefs, without its text.
type GoalStatusBrief struct {
	Title    string `json:"title"`
	Round    int    `json:"round"`
	State    string `json:"state"`
	Run      string `json:"run,omitempty"`
	PRURL    string `json:"pr_url,omitempty"`
	FailedAt Step   `json:"failed_at,omitempty"`
	Why      string `json:"why,omitempty"`
}

// NewRunStatus builds a run's snapshot. brief is the brief's text, tasks the
// task titles once tasks.md exists, and goalTitle the goal's first line when
// the run is one of a goal's.
func NewRunStatus(r Run, brief string, tasks []string, goalTitle string) RunStatus {
	if tasks == nil {
		tasks = []string{}
	}
	s := RunStatus{
		Version: StatusVersion, Title: titleOf(brief), Step: r.Step, Tasks: tasks,
		Attempt: r.Attempt, Since: r.Since, Session: r.Session, Waiting: r.Waiting,
		Goal: r.Goal, GoalTitle: goalTitle, PR: StatusPR{Number: r.PR, URL: r.PRURL},
		Fixes: r.Fixes, CIFixes: r.CIFixes, Revisions: r.Revisions,
		FailedAt: r.FailedAt, Why: cutText(r.Why, statusText),
		History: []StatusRow{}, Brief: brief, Started: r.Started, Updated: r.Updated,
	}
	if r.Step == StepWork {
		s.Task = r.Task + 1
	}
	for _, rec := range r.History {
		row := StatusRow{Step: rec.Step, Session: rec.Session, OK: rec.OK,
			Result: cutText(rec.Result, statusText), Why: cutText(rec.Why, statusText), At: rec.At}
		if rec.Step == StepWork {
			row.Task = rec.Task + 1
		}
		s.History = append(s.History, row)
	}
	return s
}

// NewGoalStatus builds a goal's snapshot. rounds is the goal's allowance:
// RoundsFrom plus the configured rounds, less one.
func NewGoalStatus(g Goal, rounds int) GoalStatus {
	s := GoalStatus{
		Version: StatusVersion, Text: g.Text, Step: g.Step, Round: g.Round, Rounds: rounds,
		Since: g.Since, Briefs: []GoalStatusBrief{}, Verdicts: []Verdict{},
		PR: StatusPR{Number: g.PR, URL: g.PRURL}, Why: cutText(g.Why, statusText),
		Started: g.Started, Updated: g.Updated,
	}
	for _, b := range g.Briefs {
		s.Briefs = append(s.Briefs, GoalStatusBrief{Title: b.Title, Round: b.Round, State: b.State,
			Run: b.Run, PRURL: b.PRURL, FailedAt: b.FailedAt, Why: cutText(b.Why, statusText)})
	}
	for _, v := range g.Verdicts {
		v.Reason = cutText(v.Reason, statusText)
		s.Verdicts = append(s.Verdicts, v)
	}
	return s
}

// encodeRunStatus is the snapshot encoded under protocol.MaxStatusBytes. One
// that is too big loses its oldest history rows first, since the current step
// and the latest results are what a page is opened for.
func encodeRunStatus(s RunStatus) ([]byte, error) {
	for {
		b, err := json.Marshal(s)
		if err != nil || len(b) <= protocol.MaxStatusBytes {
			return b, err
		}
		if len(s.History) == 0 {
			return nil, fmt.Errorf("run status is %d bytes with no history left to drop", len(b))
		}
		s.History = s.History[min(len(s.History), 1+len(s.History)/8):]
	}
}

// encodeGoalStatus is the snapshot encoded under protocol.MaxStatusBytes. One
// that is too big loses its oldest verdicts' reasons first.
func encodeGoalStatus(s GoalStatus) ([]byte, error) {
	for i := 0; ; i++ {
		b, err := json.Marshal(s)
		if err != nil || len(b) <= protocol.MaxStatusBytes {
			return b, err
		}
		if i >= len(s.Verdicts) {
			return nil, fmt.Errorf("goal status is %d bytes with no reasons left to drop", len(b))
		}
		s.Verdicts[i].Reason = ""
	}
}

// titleOf is a brief's first heading, or its first line.
func titleOf(brief string) string {
	for line := range strings.SplitSeq(brief, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimLeft(t, "#"))
		}
	}
	return firstLine(brief)
}

// cutText bounds s to n bytes on a rune boundary, marking the cut.
func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n - len("…")
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}

// homeGone reports an error that says the home is not there to set a status
// on: archived, most likely, once its run was done. The snapshot is then
// taken as set, since asking again at every poll would only fail again.
func homeGone(err error) bool {
	var rpc *goclient.RPCError
	return errors.As(err, &rpc) && rpc.Code == protocol.CodeSessionNotFound
}

// fingerprint names a snapshot, so the same one is not set twice.
func fingerprint(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// show sets the run's status on its home when it has changed. A status is for
// people to read, so failing to set one is logged and never stops the run.
func (rn *Runner) show(ctx context.Context, d Daemon, r *Run) {
	brief := r.Brief
	if text, ok := rn.read(r, BriefFile); ok && strings.TrimSpace(text) != "" {
		brief = text
	}
	var titles []string
	if tasks, err := rn.tasks(r); err == nil {
		for _, t := range tasks {
			titles = append(titles, t.Title)
		}
	}
	goalTitle := ""
	if g := rn.goals[r.Goal]; g != nil {
		goalTitle = firstLine(g.Text)
	}
	b, err := encodeRunStatus(NewRunStatus(*r, brief, titles, goalTitle))
	if err != nil {
		rn.logf("run %s: status: %v", r.Home, err)
		return
	}
	if fp := fingerprint(b); fp != r.Statused {
		if err := d.SetStatus(ctx, r.Home, protocol.StatusRun, b); err != nil && !homeGone(err) {
			rn.logf("run %s: setting its status: %v", r.Home, err)
			return
		}
		r.Statused = fp
		if err := r.Save(rn.Root); err != nil {
			rn.logf("run %s: %v", r.Home, err)
		}
	}
}

// showGoalStatus sets the goal's status on its home when it has changed, as
// show does for a run.
func (rn *Runner) showGoalStatus(ctx context.Context, d Daemon, g *Goal) {
	rounds := max(g.RoundsFrom, 1) + rn.Cfg.GoalRounds - 1
	b, err := encodeGoalStatus(NewGoalStatus(*g, rounds))
	if err != nil {
		rn.logf("goal %s: status: %v", g.Home, err)
		return
	}
	if fp := fingerprint(b); fp != g.Statused {
		if err := d.SetStatus(ctx, g.Home, protocol.StatusGoal, b); err != nil && !homeGone(err) {
			rn.logf("goal %s: setting its status: %v", g.Home, err)
			return
		}
		g.Statused = fp
		if err := g.Save(rn.Root); err != nil {
			rn.logf("goal %s: %v", g.Home, err)
		}
	}
}

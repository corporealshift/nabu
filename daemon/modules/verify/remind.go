package verify

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// One reminder at the stop, for a session the person is watching.
//
// Repeated vetoes pushed a model past what it was asked for: three of them
// buried "don't add extra things. just hello world is fine for now", and the
// model credited its own earlier plan to the person and started on it. Here
// the turn gets one account of what nabu can see and one question, and then
// it may stop. See docs/specs/2026-09-26-one-reminder-stop-design.md.

// remindOnce is the stop gate without a goal.
func (m *Module) remindOnce(ctx context.Context, s module.Session, info module.StopInfo) module.StopVerdict {
	allow := module.StopVerdict{Allow: true}
	log, err := s.Events(nil)
	if err != nil || !module.ChangedSinceUser(log) || remindedThisTurn(log) {
		return allow
	}
	facts := m.turnFacts(ctx, s, info)
	if len(facts) == 0 {
		return allow
	}
	return module.StopVerdict{Reason: reminder(lastPersonMessage(log), facts)}
}

// remindedThisTurn reports whether verify has already spoken since the
// person's last message. It is read from the log, so a restart does not
// remind twice.
func remindedThisTurn(log []protocol.Event) bool {
	for i := len(log) - 1; i >= 0; i-- {
		switch log[i].Type {
		case protocol.EventMessage:
			if protocol.MustData[protocol.MessageData](log[i]).Role == "user" {
				return false
			}
		case protocol.EventStopVeto:
			if protocol.MustData[protocol.StopVetoData](log[i]).Module == "verify" {
				return true
			}
		}
	}
	return false
}

// lastPersonMessage is what the person last asked for.
func lastPersonMessage(log []protocol.Event) string {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		if d := protocol.MustData[protocol.MessageData](log[i]); d.Role == "user" {
			return d.Content
		}
	}
	return ""
}

// maxQuoted bounds the person's message in a reminder. The start of a long
// request says what it was for.
const maxQuoted = 400

// reminder frames the facts. It says whose it is, because a veto reaches the
// model as a user-role message and the model took each one for the person.
// It quotes the person, because the request is what the facts must not
// replace. And it says the model may stop, because it may.
func reminder(asked string, facts []string) string {
	var b strings.Builder
	b.WriteString("This is nabu's end-of-turn check, sent once. It is not from the person, " +
		"and you may stop after answering it.\n")
	if asked = strings.TrimSpace(asked); asked != "" {
		fmt.Fprintf(&b, "The person's last message was: %q\nStay within that: start nothing it did not ask for.\n",
			truncate(asked, maxQuoted))
	}
	b.WriteString("What nabu sees:\n")
	for _, f := range facts {
		b.WriteString("  - " + strings.ReplaceAll(strings.TrimSpace(f), "\n", "\n    ") + "\n")
	}
	b.WriteString("If the work they asked for is finished: mark the finished tasks done, commit on a " +
		"branch (not main), and open a pull request. If it is not finished: do not push; say what " +
		"is left and why.")
	return b.String()
}

// turnFacts is what nabu can check about the turn's work, each as one line.
func (m *Module) turnFacts(ctx context.Context, s module.Session, info module.StopInfo) []string {
	var facts []string

	gateFailed := false
	if reason := m.gateVeto(ctx, s); reason != "" {
		gateFailed = true
		facts = append(facts, reason)
	}
	for _, f := range failedChecks(s) {
		if gateFailed && strings.HasPrefix(f, "verify.command:") {
			continue // the gate's own failure is already said, with its output
		}
		facts = append(facts, "a check is failing: "+f)
	}

	untracked, changed := m.sessionChanges(s)
	if src, build := splitBuild(append(untracked, changed...)); len(src)+len(build) > 0 {
		if len(src) > 0 {
			facts = append(facts, "not committed: "+listSome(src, maxListed))
		}
		if len(build) > 0 {
			files := "files"
			if len(build) == 1 {
				files = "file"
			}
			facts = append(facts, fmt.Sprintf("build output that git does not ignore: %d %s under %s. "+
				"Ignore it in the .gitignore at the repository root, not in a nested one.",
				len(build), files, strings.Join(buildDirs(build), ", ")))
		}
	}
	if reason := m.committedBuildVeto(s); reason != "" {
		facts = append(facts, reason)
	}
	facts = append(facts, m.branchFacts(s, len(untracked)+len(changed) > 0)...)

	if open := openTaskLines(info.Tasks); len(open) > 0 {
		facts = append(facts, "still open on your task list (mark the finished ones done): "+
			strings.Join(open, "; "))
	}
	return facts
}

// splitBuild separates build output from everything else.
func splitBuild(paths []string) (source, build []string) {
	for _, p := range paths {
		if buildPrefix(p) != "" {
			build = append(build, p)
		} else {
			source = append(source, p)
		}
	}
	return source, build
}

// openTaskLines are the tasks still pending, in progress, or blocked without
// a note saying why.
func openTaskLines(tasks []protocol.Task) []string {
	var out []string
	for _, t := range tasks {
		switch {
		case t.Status == protocol.TaskPending || t.Status == protocol.TaskInProgress:
			out = append(out, fmt.Sprintf("%s %s (%s)", t.ID, t.Title, t.Status))
		case t.Status == protocol.TaskBlocked && strings.TrimSpace(t.Note) == "":
			out = append(out, fmt.Sprintf("%s %s (blocked, with no note saying why)", t.ID, t.Title))
		}
	}
	return out
}

// branchFacts says where the session's commits are: on main, on a branch with
// no pull request, or ahead of the branch's upstream. dirty says whether work
// is still uncommitted, which on main needs a branch before it needs a commit.
func (m *Module) branchFacts(s module.Session, dirty bool) []string {
	dir := s.Workspace().Path
	branch, err := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil
	}
	branch = strings.TrimSpace(branch)
	onMain := branch == "main" || branch == "master"

	m.mu.Lock()
	before, known := m.baselines[s.ID()]
	m.mu.Unlock()
	head, ok := headCommit(dir)
	committed := known && ok && before.head != "" && head != before.head

	var facts []string
	switch {
	case onMain && committed:
		facts = append(facts, "this session committed on "+branch+". Move that work to a branch.")
	case onMain && dirty:
		facts = append(facts, "you are on "+branch+": commit on a new branch.")
	case committed:
		switch pr, known := pullRequest(dir); {
		case known && pr == "":
			facts = append(facts, "branch "+branch+" has this session's commits and no pull request.")
		case known:
			if n := unpushed(dir); n > 0 {
				facts = append(facts, fmt.Sprintf("%d commits on %s are not pushed to its pull request %s.", n, branch, pr))
			}
		}
	}
	return facts
}

// pullRequest is the URL of the current branch's pull request, "" when it has
// none. known is false when gh cannot say (not installed, not signed in, not
// GitHub), and then nothing is claimed either way.
func pullRequest(dir string) (url string, known bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", "--json", "url", "--jq", ".url")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(out)), true
	}
	if strings.Contains(string(out), "no pull requests found") {
		return "", true
	}
	return "", false
}

// unpushed counts commits ahead of the upstream branch, 0 when there is none.
func unpushed(dir string) int {
	out, err := git(dir, "rev-list", "--count", "@{upstream}..HEAD")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

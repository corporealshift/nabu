package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Defaults. Spec 10 fixes the behaviour; these fix the numbers.
const (
	// DefaultCommandTimeout bounds a mechanical check or the workspace gate.
	DefaultCommandTimeout = 5 * time.Minute
	// DefaultJudgeTurns is how much transcript the judge sees. It is a model
	// call on every stop attempt, so this is a recurring cost.
	DefaultJudgeTurns = 10
	// maxCheckOutput is how much command output reaches a check event.
	maxCheckOutput = 2000
	// maxSummary is how much reaches a one-line summary.
	maxSummary = 200
)

// Module is the verification policy: it refuses to let the loop stop while
// work is demonstrably unfinished, and judges the run goal in a fresh context
// rather than trusting the model that did the work.
type Module struct {
	// RequireDoneWhen forces every new task to carry a done_when. Spec 10.2
	// puts it on under a headless run and whenever a goal is set, and off for
	// casual interactive use unless configured on.
	RequireDoneWhen *bool
	// Command is the project's canonical gate, run at the stop gate.
	Command string
	// Commands are gates for particular workspaces, keyed by workspaceKey. One
	// replaces Command in its workspace; an empty one turns the gate off there.
	Commands map[string]string
	// RequireCleanTree vetoes a stop while the tree has uncommitted changes.
	RequireCleanTree bool
	// JudgeModel overrides the model used for the goal judge.
	JudgeModel string
	// CommandTimeout bounds a check or gate command.
	CommandTimeout time.Duration
	// JudgeTurns is how many turns of transcript the judge sees.
	JudgeTurns int

	host module.Host

	// baselines is the tree each session started with, by session id. The
	// gate answers for what a session changed, never for what it found.
	mu        sync.Mutex
	baselines map[string]baseline
}

// baseline is the repository as a session found it.
type baseline struct {
	// dirty is every path git reported as changed or untracked, with its
	// two-letter status.
	dirty map[string]string
	// head is the commit checked out, empty when there is none yet.
	head string
}

// SessionStart implements module.SessionStarter. It records the tree the
// session inherited. Nothing is injected; this hook exists for the baseline.
func (m *Module) SessionStart(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	m.baseline(s)
	return nil, nil
}

// SessionResume implements module.ResumeHook. Resuming re-baselines: whatever
// is on disk when a human picks the session back up is the new start point.
func (m *Module) SessionResume(_ context.Context, s module.Session) error {
	m.baseline(s)
	return nil
}

// baseline records the dirty paths and the commit a session starts from.
func (m *Module) baseline(s module.Session) {
	if s == nil {
		return
	}
	paths, ok := dirtyPaths(s.Workspace().Path)
	if !ok {
		return
	}
	head, _ := headCommit(s.Workspace().Path)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.baselines == nil {
		m.baselines = map[string]baseline{}
	}
	m.baselines[s.ID()] = baseline{dirty: paths, head: head}
}

// Name implements module.Module.
func (m *Module) Name() string { return "verify" }

// Init implements module.Module.
func (m *Module) Init(h module.Host, cfg module.Config) error {
	m.host = h
	m.Command = cfg.String("command", "")
	if err := m.readCommands(cfg["commands"]); err != nil {
		return err
	}
	m.JudgeModel = cfg.String("judge_model", "")
	m.RequireCleanTree = cfg.Bool("require_clean_tree", true)

	if raw, ok := cfg["require_done_when"]; ok {
		b, ok := raw.(bool)
		if !ok {
			return fmt.Errorf("verify: require_done_when must be a boolean, got %T", raw)
		}
		m.RequireDoneWhen = &b
	}

	secs := cfg.Int("command_timeout", 0)
	if secs > 0 {
		m.CommandTimeout = time.Duration(secs) * time.Second
	} else {
		m.CommandTimeout = DefaultCommandTimeout
	}

	turns := cfg.Int("judge_turns", 0)
	if turns > 0 {
		m.JudgeTurns = turns
	} else {
		m.JudgeTurns = DefaultJudgeTurns
	}
	return nil
}

// ---------------------------------------------------------------------------
// ToolGate: require_done_when, and mechanical checks on the way to done.

// GateTool implements module.ToolGate. It only ever speaks about task.update;
// everything else is guard's business.
func (m *Module) GateTool(ctx context.Context, s module.Session, call protocol.ToolCallData) module.Verdict {
	if call.Tool != "task.update" || s == nil {
		return module.Verdict{Decision: module.Allow}
	}

	incoming, ok := tasksFrom(call.Arguments)
	if !ok {
		return module.Verdict{Decision: module.Allow}
	}
	state := s.State()
	existing := byID(state.Tasks)

	if m.requireDoneWhen(state) {
		for _, t := range incoming {
			if _, known := existing[t.ID]; known {
				continue
			}
			if strings.TrimSpace(t.DoneWhen) == "" {
				return module.Verdict{
					Decision: module.Deny,
					Reason: "every task needs a done_when: the observable condition " +
						"that proves it finished. Task " + t.ID + " has none.",
				}
			}
		}
	}

	// A task carrying a command may only reach done if that command passes.
	for _, t := range incoming {
		if t.Status != protocol.TaskDone || strings.TrimSpace(t.Check) == "" {
			continue
		}
		if was, known := existing[t.ID]; known && was.Status == protocol.TaskDone {
			continue // already done; not a transition
		}
		out, err := m.run(ctx, s.Workspace().Path, t.Check)
		if err != nil {
			_, _ = s.Append(protocol.EventCheck, protocol.CheckData{
				Name: "task:" + t.ID, Kind: "command", TaskID: t.ID,
				Status: "fail", Summary: truncate(err.Error(), maxSummary),
				Output: tail(out, maxCheckOutput),
			})
			return module.Verdict{
				Decision: module.Deny,
				Reason: fmt.Sprintf("task %s is not done: its check %q failed (%v)\n%s",
					t.ID, t.Check, err, tail(out, maxCheckOutput)),
			}
		}
		_, _ = s.Append(protocol.EventCheck, protocol.CheckData{
			Name: "task:" + t.ID, Kind: "command", TaskID: t.ID,
			Status: "pass", Summary: "exit 0", Output: tail(out, maxCheckOutput),
		})
	}

	return module.Verdict{Decision: module.Allow}
}

// requireDoneWhen reports whether the rule is on: configured explicitly, or
// implied by a goal being set (spec 10.2).
func (m *Module) requireDoneWhen(state protocol.State) bool {
	if m.RequireDoneWhen != nil {
		return *m.RequireDoneWhen
	}
	return state.GoalActive()
}

// tasksFrom pulls the task list out of a task.update call.
func tasksFrom(args json.RawMessage) ([]protocol.Task, bool) {
	if len(args) == 0 {
		return nil, false
	}
	var p struct {
		Tasks []protocol.Task `json:"tasks"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, false
	}
	return p.Tasks, p.Tasks != nil
}

func byID(tasks []protocol.Task) map[string]protocol.Task {
	out := make(map[string]protocol.Task, len(tasks))
	for _, t := range tasks {
		out[t.ID] = t
	}
	return out
}

// ---------------------------------------------------------------------------
// StopGate: deterministic checks first, then the judge.

// BeforeStop implements module.StopGate. Without a goal it sends at most one
// reminder a turn (remindOnce). With a goal, spec 10.3 requires the cheap
// checks to run before paying for a judge call, so the order here is
// load-bearing: open tasks, undocumented blocks, failed checks, the gate, the
// tree, and only then the judge.
func (m *Module) BeforeStop(ctx context.Context, s module.Session, info module.StopInfo) module.StopVerdict {
	if s == nil {
		return module.StopVerdict{Allow: true}
	}

	// A question, answered, is a finished turn. Sending the agent back to open
	// tasks or a tree it did not touch this turn is how "what's the status?"
	// became a reason to resume work, and "commit them" a reason to commit to
	// main (issue 76). Once the turn has changed something, it is work again
	// and every check below applies.
	if answeredOnly(s) {
		return module.StopVerdict{Allow: true}
	}

	// With the person watching, one reminder and then the stop. The vetoes
	// below are for a run with a goal, where nobody is there to say "carry on"
	// (docs/specs/2026-09-26-one-reminder-stop-design.md).
	if !s.State().GoalActive() {
		return m.remindOnce(ctx, s, info)
	}

	if reason := openTaskVeto(info.Tasks); reason != "" {
		return module.StopVerdict{Reason: reason}
	}
	if reason := failedCheckVeto(s); reason != "" {
		return module.StopVerdict{Reason: reason}
	}
	if reason := m.gateVeto(ctx, s); reason != "" {
		return module.StopVerdict{Reason: reason}
	}
	if reason := m.committedBuildVeto(s); reason != "" {
		return module.StopVerdict{Reason: reason}
	}
	if reason := m.treeVeto(s); reason != "" {
		return module.StopVerdict{Reason: reason}
	}
	if reason := m.judgeVeto(ctx, s, info); reason != "" {
		return module.StopVerdict{Reason: reason}
	}
	return module.StopVerdict{Allow: true}
}

// openTaskVeto refuses a stop while work is outstanding. A blocked task must
// carry a note; one without is undocumented and vetoes too.
func openTaskVeto(tasks []protocol.Task) string {
	var open, undocumented, blocked []string
	for _, t := range tasks {
		switch t.Status {
		case protocol.TaskPending, protocol.TaskInProgress:
			open = append(open, t.ID+" "+t.Title)
		case protocol.TaskBlocked:
			if strings.TrimSpace(t.Note) == "" {
				undocumented = append(undocumented, t.ID+" "+t.Title)
			} else {
				blocked = append(blocked, t.ID+" "+t.Title+" — "+t.Note)
			}
		}
	}
	if len(open) == 0 && len(undocumented) == 0 {
		return ""
	}

	var b strings.Builder
	if len(open) > 0 {
		b.WriteString("these tasks are not finished:\n")
		for _, t := range open {
			b.WriteString("  - " + t + "\n")
		}
	}
	if len(undocumented) > 0 {
		b.WriteString("these tasks are blocked with no note saying why:\n")
		for _, t := range undocumented {
			b.WriteString("  - " + t + "\n")
		}
	}
	if len(blocked) > 0 {
		b.WriteString("(blocked and documented, not counted against you:\n")
		for _, t := range blocked {
			b.WriteString("  - " + t + "\n")
		}
		b.WriteString(")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// failedCheckVeto refuses a stop while a mechanical check last failed.
func failedCheckVeto(s module.Session) string {
	failed := failedChecks(s)
	if len(failed) == 0 {
		return ""
	}
	return "these checks are failing:\n  - " + strings.Join(failed, "\n  - ")
}

// failedChecks names each check whose latest result is a failure, with its
// summary, in the order the checks first appeared.
func failedChecks(s module.Session) []string {
	events, err := s.Events(nil)
	if err != nil {
		return nil
	}
	// Last status per check name wins: a later pass clears an earlier failure.
	status := map[string]protocol.CheckData{}
	var order []string
	for _, e := range events {
		if e.Type != protocol.EventCheck {
			continue
		}
		var d protocol.CheckData
		if json.Unmarshal(e.Data, &d) != nil {
			continue
		}
		if _, seen := status[d.Name]; !seen {
			order = append(order, d.Name)
		}
		status[d.Name] = d
	}

	var failed []string
	for _, name := range order {
		if d := status[name]; d.Status == "fail" {
			failed = append(failed, name+": "+d.Summary)
		}
	}
	return failed
}

// readCommands reads the per-workspace gates: an object of workspace path to
// command.
//
// They live here because the workspace overlay (<workspace>/.nabu/config.json)
// is never applied: module config is read once, for the whole daemon, so one
// global "command" ran in every repository or in none. A gate is the one
// setting that is plainly per project, and the module can pick it per session.
func (m *Module) readCommands(raw any) error {
	if raw == nil {
		return nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("verify: commands must be an object of workspace path to command, got %T", raw)
	}
	m.Commands = make(map[string]string, len(obj))
	for path, v := range obj {
		cmd, ok := v.(string)
		if !ok {
			return fmt.Errorf("verify: the command for %s must be a string, got %T", path, v)
		}
		m.Commands[workspaceKey(path)] = cmd
	}
	return nil
}

// workspaceKey puts a workspace path in one comparable form: absolute, clean,
// forward slashes, and lower case, since Windows paths are case-insensitive
// and the same directory is written both ways.
func workspaceKey(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(p)))
}

// commandFor is the gate for a session's workspace.
func (m *Module) commandFor(s module.Session) string {
	if cmd, ok := m.Commands[workspaceKey(s.Workspace().Path)]; ok {
		return cmd
	}
	return m.Command
}

// gateVeto runs the project's canonical gate and refuses a stop if it fails.
//
// Only a turn that changed the workspace is gated, as with the tree veto: the
// gate answers for work, not for what the session walked into. Asked for a
// README, a model replied without calling a tool, the gate failed on an
// Android build it had never touched, and the session blocked. The follow-up
// prompt then met the same recorded failure. A failure the session did cause
// still holds a later turn, through failedCheckVeto.
func (m *Module) gateVeto(ctx context.Context, s module.Session) string {
	command := m.commandFor(s)
	if strings.TrimSpace(command) == "" {
		return ""
	}
	if log, err := s.Events(nil); err == nil && !module.ChangedSinceUser(log) {
		return ""
	}
	out, err := m.run(ctx, s.Workspace().Path, command)
	status, summary := "pass", "exit 0"
	if err != nil {
		status, summary = "fail", truncate(err.Error(), maxSummary)
	}
	_, _ = s.Append(protocol.EventCheck, protocol.CheckData{
		Name: "verify.command", Kind: "module", Status: status,
		Summary: summary, Output: tail(out, maxCheckOutput),
	})
	if err == nil {
		return ""
	}
	return fmt.Sprintf("the project gate failed: %s (%v)\n%s",
		command, err, tail(out, maxCheckOutput))
}

// treeVeto targets the failure this whole module exists for: the agent
// reporting that tests pass while the fix sits uncommitted.
//
// Only what the session changed counts. A session that walks into a dirty
// tree and is asked a read-only question has nothing to answer for, and
// vetoing there blocks work that was already finished.
func (m *Module) treeVeto(s module.Session) string {
	untracked, changed := m.sessionChanges(s)
	if len(untracked)+len(changed) == 0 {
		return ""
	}
	return treeReason(untracked, changed)
}

// sessionChanges are the paths git lists as untracked or changed that were not
// already so when the session started. Both are empty with the check off.
func (m *Module) sessionChanges(s module.Session) (untracked, changed []string) {
	if !m.RequireCleanTree {
		return nil, nil
	}
	now, ok := dirtyPaths(s.Workspace().Path)
	if !ok || len(now) == 0 {
		return nil, nil
	}

	m.mu.Lock()
	before, known := m.baselines[s.ID()]
	m.mu.Unlock()
	if !known {
		// No baseline, so there is no way to tell whose changes these are.
		// Staying quiet is the safe direction: the alternative blames the
		// session for the whole tree.
		return nil, nil
	}

	for p, code := range now {
		if _, was := before.dirty[p]; was {
			continue
		}
		if code == "??" {
			untracked = append(untracked, p)
		} else {
			changed = append(changed, p)
		}
	}
	return untracked, changed
}

// treeReason says what git reports and the only things that clear it.
//
// It used to end "commit them or say why they should stay uncommitted". Saying
// why cleared nothing, so a model explained itself fourteen times to a veto
// that kept coming back, and the session ended blocked twice. It had also
// convinced itself the files were ignored, from a check-ignore run in the
// wrong directory; git status is what settles that, so the reason says so.
func treeReason(untracked, changed []string) string {
	// Build output is summarised by directory rather than listed: a build
	// leaves hundreds of files, and listing them buried the source among them.
	var build []string
	source := func(paths []string) []string {
		var out []string
		for _, p := range paths {
			if buildPrefix(p) != "" {
				build = append(build, p)
			} else {
				out = append(out, p)
			}
		}
		sort.Strings(out)
		return out
	}
	untracked, changed = source(untracked), source(changed)

	var b strings.Builder
	b.WriteString("this session left uncommitted changes that git status still lists:\n")
	if len(untracked) > 0 {
		b.WriteString("  untracked, and not ignored: " + listSome(untracked, maxListed) + "\n")
	}
	if len(changed) > 0 {
		b.WriteString("  changed: " + listSome(changed, maxListed) + "\n")
	}
	if len(build) > 0 {
		fmt.Fprintf(&b, "  build output, not ignored: %d files under %s\n",
			len(build), strings.Join(buildDirs(build), ", "))
		b.WriteString("Ignore build output in the .gitignore at the repository root: a pattern " +
			"in a nested .gitignore is relative to that file's own directory. A tracked file " +
			"is never ignored, whatever .gitignore says; untrack it with `git rm -r --cached <dir>`.\n")
	}
	b.WriteString("Commit what belongs in the repository. This clears only when git status " +
		"stops listing these paths; explaining them does not clear it. If they should " +
		"stay as they are, say so once and stop: the session ends blocked, for the person to decide.")
	return b.String()
}

// maxListed bounds each list in a veto. A build can leave hundreds of paths,
// and the first few say what they are.
const maxListed = 15

// listSome joins up to n items and counts the rest.
func listSome(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(", and %d more", len(items)-n)
}

// committedBuildVeto refuses a stop when the session's own commits added build
// output. An agent that staged a whole directory committed 866 Gradle files
// that way, and nothing noticed until they were in a pull request.
//
// It compares the commit the session started on with HEAD, so it answers only
// for commits made on top of that start, and a later commit that untracks the
// files clears it.
func (m *Module) committedBuildVeto(s module.Session) string {
	if !m.RequireCleanTree {
		return ""
	}
	m.mu.Lock()
	before, known := m.baselines[s.ID()]
	m.mu.Unlock()
	if !known || before.head == "" {
		return ""
	}
	dir := s.Workspace().Path
	head, ok := headCommit(dir)
	if !ok || head == before.head {
		return ""
	}
	// Switching to another branch is not this session committing.
	if _, err := git(dir, "merge-base", "--is-ancestor", before.head, head); err != nil {
		return ""
	}
	out, err := git(dir, "diff", "--name-only", "--diff-filter=A", before.head, head)
	if err != nil {
		return ""
	}
	var added []string
	for _, p := range strings.Split(out, "\n") {
		if p = strings.TrimSpace(p); p != "" && buildPrefix(p) != "" {
			added = append(added, p)
		}
	}
	if len(added) == 0 {
		return ""
	}
	dirs := buildDirs(added)
	return fmt.Sprintf("this session committed %d files of build output under %s. "+
		"Build output does not belong in the repository: untrack it with "+
		"`git rm -r --cached %s`, ignore it in the .gitignore at the repository root, "+
		"and commit that.", len(added), strings.Join(dirs, ", "), strings.Join(dirs, " "))
}

// buildDotDirs are the dotted caches a build writes. module.NoiseDir leaves
// dotted directories to its callers, since they disagree about them.
var buildDotDirs = map[string]bool{
	".gradle": true, ".kotlin": true, ".next": true, ".venv": true,
	".pytest_cache": true, ".mypy_cache": true,
}

// buildPrefix is a repository path up to and including its first build
// directory, or "" when it is not under one. A file's own name is not a
// directory, so a file called "build" is not build output; git names an
// untracked directory with a trailing slash, and that last name is.
func buildPrefix(p string) string {
	dir := strings.HasSuffix(p, "/")
	parts := strings.Split(strings.TrimSuffix(p, "/"), "/")
	for i, seg := range parts {
		if i == len(parts)-1 && !dir {
			break
		}
		if module.NoiseDir(seg) || buildDotDirs[strings.ToLower(seg)] {
			return strings.Join(parts[:i+1], "/") + "/"
		}
	}
	return ""
}

// buildDirs is the distinct build directories the paths lie under, sorted.
func buildDirs(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if d := buildPrefix(p); d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// headCommit is the commit checked out. The second result is false outside a
// repository or before its first commit.
func headCommit(dir string) (string, bool) {
	out, err := git(dir, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// dirtyPaths is every path git reports as changed or untracked, with its
// two-letter status ("??" for untracked). The second result is false when the
// question cannot be answered.
//
// Untracked files are listed one by one. By default git folds a new directory
// into one line, and "android/" hid the build output inside it.
func dirtyPaths(dir string) (map[string]string, bool) {
	out, err := git(dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, false
	}
	paths := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		// "XY <path>", and a rename is "XY <old> -> <new>".
		p := strings.TrimSpace(line[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		if p = strings.Trim(strings.TrimSpace(p), `"`); p != "" {
			paths[p] = line[:2]
		}
	}
	return paths, true
}

// judgeVeto asks the judge whether the goal is met. Unmet vetoes; met and
// impossible append a goal event and allow.
func (m *Module) judgeVeto(ctx context.Context, s module.Session, info module.StopInfo) string {
	if info.Goal == nil || !s.State().GoalActive() {
		return ""
	}
	if m.host == nil || m.host.Model() == nil {
		// Without a model there is no way to judge. Say so rather than
		// allowing a stop that was never actually judged.
		return "the run goal could not be judged: this daemon has no model available"
	}

	verdict, reason := m.judge(ctx, s, info)
	switch verdict {
	case "met", "impossible":
		_, _ = s.Append(protocol.EventGoal, protocol.GoalData{
			Condition: info.Goal.Condition, State: verdict,
			Reason: reason, Source: "module:verify",
		})
		return ""
	default:
		if reason == "" {
			reason = "the goal is not met"
		}
		return "the run goal is not met: " + reason
	}
}

// judge runs the goal judge in a fresh context and returns its verdict and
// reason. A failed or unparseable call is deliberately reported as unmet: a
// judge that fails open would make this whole mechanism theatre.
func (m *Module) judge(ctx context.Context, s module.Session, info module.StopInfo) (string, string) {
	prompt := m.judgePrompt(s, info)
	resp, err := m.host.Model().Complete(ctx, s, module.CompletionRequest{
		Model:  m.JudgeModel,
		System: judgeSystem,
		// A fresh context: the condition, the tasks, and a transcript window.
		// The judge never sees the loop's own message history.
		Messages:  []module.Message{{Role: "user", Content: prompt}},
		MaxTokens: 400,
	})
	if err != nil {
		return "unmet", "judge call failed: " + err.Error()
	}
	verdict, reason, ok := parseVerdict(resp.Content)
	if !ok {
		return "unmet", "the judge did not answer in the required form"
	}
	return verdict, reason
}

const judgeSystem = "You are an impartial judge. You did not do the work and you " +
	"have no stake in it being finished. Decide only whether the stated condition is met."

// judgePrompt builds the judge's single user message.
func (m *Module) judgePrompt(s module.Session, info module.StopInfo) string {
	var b strings.Builder
	b.WriteString("The agent believes its work is done. Decide whether the goal is met.\n\n")
	b.WriteString("## Condition\n")
	b.WriteString(info.Goal.Condition + "\n\n")

	b.WriteString("## Tasks\n")
	if len(info.Tasks) == 0 {
		b.WriteString("(none)\n")
	}
	for _, t := range info.Tasks {
		evidence := t.Evidence
		if evidence == "" {
			evidence = "none"
		}
		fmt.Fprintf(&b, "- %s: %s — status: %s, evidence: %s\n", t.ID, t.Title, t.Status, evidence)
	}

	b.WriteString("\n## Recent transcript\n")
	b.WriteString(m.transcript(s))

	b.WriteString("\n## Verdict\nRespond with exactly one of these three lines, nothing else:\n")
	b.WriteString("VERDICT: met\n")
	b.WriteString("VERDICT: unmet - <one sentence reason>\n")
	b.WriteString("VERDICT: impossible - <one sentence reason>\n")
	return b.String()
}

// transcript renders the last JudgeTurns messages. It is a recurring cost, so
// it is bounded rather than complete.
func (m *Module) transcript(s module.Session) string {
	events, err := s.Events(nil)
	if err != nil {
		return "(unavailable)\n"
	}
	var lines []string
	for _, e := range events {
		if e.Type != protocol.EventMessage {
			continue
		}
		var d protocol.MessageData
		if json.Unmarshal(e.Data, &d) != nil {
			continue
		}
		lines = append(lines, d.Role+": "+truncate(strings.TrimSpace(d.Content), 1000))
	}
	if len(lines) == 0 {
		return "(no messages)\n"
	}
	if n := m.JudgeTurns; n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n"
}

// parseVerdict finds the first verdict line. The reason is whatever follows
// the separator, which may be a hyphen or an em dash depending on what the
// model produced.
func parseVerdict(content string) (string, string, bool) {
	lower := strings.ToLower(content)
	best, at := "", -1
	for _, v := range []string{"met", "unmet", "impossible"} {
		i := strings.Index(lower, "verdict: "+v)
		if i < 0 {
			continue
		}
		// "met" also matches inside "unmet"; prefer the longest match at the
		// same position and the earliest match overall.
		if at < 0 || i < at || (i == at && len(v) > len(best)) {
			best, at = v, i
		}
	}
	if at < 0 {
		return "", "", false
	}

	rest := content[at+len("verdict: ")+len(best):]
	if i := strings.IndexAny(rest, "\r\n"); i >= 0 {
		rest = rest[:i]
	}
	reason := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rest), "-—–:"))
	return best, strings.TrimSpace(reason), true
}

// ---------------------------------------------------------------------------
// Reporter: the checks half of the run report.

// Report implements module.Reporter. verify owns tree_dirty because it is the
// module that vetoes on it; report reflects the same truth rather than
// asserting a second one.
func (m *Module) Report(_ context.Context, s module.Session) (module.ReportFields, error) {
	var out module.ReportFields
	if s == nil {
		return out, nil
	}
	if dirty, ok := TreeDirty(s.Workspace().Path); ok {
		out.TreeDirty = &dirty
	}

	events, err := s.Events(nil)
	if err != nil {
		return out, nil
	}
	latest := map[string]protocol.CheckData{}
	var order []string
	for _, e := range events {
		if e.Type != protocol.EventCheck {
			continue
		}
		var d protocol.CheckData
		if json.Unmarshal(e.Data, &d) != nil {
			continue
		}
		if _, seen := latest[d.Name]; !seen {
			order = append(order, d.Name)
		}
		latest[d.Name] = d
	}
	for _, name := range order {
		d := latest[name]
		out.Checks = append(out.Checks, protocol.ReportCheck{
			Name: d.Name, Status: d.Status, Summary: d.Summary,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Running commands and reading the tree.

// run executes a command in the workspace, returning its combined output. A
// timeout is a failure like any other, named so the model can tell.
func (m *Module) run(ctx context.Context, dir, command string) (string, error) {
	timeout := m.CommandTimeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name, flag := shell()
	cmd := exec.CommandContext(cctx, name, flag, command)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("command timed out after %s", timeout)
	}
	return string(out), err
}

// shell picks an interpreter. A configured gate must work on both platforms,
// so this prefers a POSIX shell where one exists and falls back to cmd.
func shell() (string, string) {
	for _, sh := range []string{"bash", "sh"} {
		if p, err := exec.LookPath(sh); err == nil {
			return p, "-c"
		}
	}
	if runtime.GOOS == "windows" {
		return "cmd", "/C"
	}
	return "sh", "-c"
}

// TreeDirty reports whether dir has uncommitted changes. The second result is
// false when the question cannot be answered — no git, or not a repository —
// which is a normal case rather than an error.
func TreeDirty(dir string) (bool, bool) {
	out, err := git(dir, "status", "--porcelain")
	if err != nil {
		return false, false
	}
	return strings.TrimSpace(out) != "", true
}

// git runs one git command in dir.
func git(dir string, args ...string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no workspace")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// tail keeps the end of command output, which is where a failure explains
// itself.
func tail(s string, n int) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// answeredOnly reports whether this turn asked a question and changed nothing
// while answering it.
func answeredOnly(s module.Session) bool {
	log, err := s.Events(nil)
	if err != nil {
		return false
	}
	return module.QuestionTurn(log, s.State()) && !module.ChangedSinceUser(log)
}

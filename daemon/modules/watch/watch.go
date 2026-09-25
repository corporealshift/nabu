// Package watch tells the model when the workspace changed underneath it.
//
// The owner drives the same workspace from other terminals, so what the model
// read three turns ago may no longer be on disk. Without this it guesses, or
// re-reads defensively, or is wrong.
//
// # Why this is a context event and not a notification
//
// Invariant 3: request = f(log). Two daemons with the same modules and the same
// log build the same request byte for byte. A watcher that whispered to the
// model out of band would break that, so what it observes is appended as a
// `context` event and reaches the model the way every other injection does.
// Replay stays deterministic because the event is in the log; the
// non-determinism lives in when the daemon noticed a change, which is the same
// category as when a tool result arrived.
//
// # Why polling, and not a filesystem watcher
//
// Three reasons, in order of weight. Coalescing is free: the diff between two
// snapshots taken at turn boundaries *is* the coalesced change set, where an
// event stream would have to be debounced and drained. A turn is never
// interrupted, because nothing runs between the boundaries. And it needs no
// dependency, which CLAUDE.md asks for where it is practical.
//
// The cost is a walk per turn, which is why the scan is bounded and a workspace
// too large to scan cheaply turns the module off for that session rather than
// paying the cost silently on every request.
package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

const (
	// defaultMaxFiles bounds the walk. A workspace larger than this is not
	// scanned at all: a per-turn walk of a huge tree would cost more than the
	// answer is worth, and doing it silently would be worse.
	defaultMaxFiles = 20000
	// defaultMaxReported caps the list in one block. A hundred changed files is
	// a branch switch, and naming them all would cost more context than saying
	// so.
	defaultMaxReported = 20
)

// skipDir reports a directory this module never walks.
//
// The generated and vendored ones come from module.NoiseDir, so the file tools
// and this module agree about what the workspace contains — a claim the two
// separate lists here used to make and not keep.
//
// Dotted directories go too, which the file tools do not do. They are where
// build scratch lives (.gradle, .kotlin, .venv, .pytest_cache) and it changes
// constantly, so reporting it would mean a change list on every turn in any
// repository being built. The cost is that a change to .github goes unmentioned;
// that is rare, and not something the model needs to know mid-turn.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || module.NoiseDir(name)
}

// skipFile reports a file this module never reports: any dotted one. The
// directory rule above left them in, so an .env edited in another terminal,
// an editor's swap file or a tool's .lock landed in front of the model (issue
// 66). They are configuration and scratch, not the code being worked on.
func skipFile(name string) bool {
	return strings.HasPrefix(name, ".")
}

// stamp is what identifies a file version without reading it.
type stamp struct {
	mod  time.Time
	size int64
}

// snapshot is the workspace as it was at one turn boundary.
type snapshot map[string]stamp

// sessionState is one session's view. Guarded by Module.mu.
type sessionState struct {
	// last is the snapshot taken at the previous boundary. nil means the
	// session has not been scanned yet.
	last snapshot
	// mine are paths this agent wrote since the last scan. Reporting the
	// model's own edits back to it is noise, and noise in every request is
	// worse than a gap in an unlikely one.
	mine map[string]bool
	// before holds the snapshot taken just before a shell command ran, by
	// call id, so what the command changed can be counted as the agent's.
	before map[string]snapshot
	// off stops scanning this session after the workspace proved too large.
	off bool
}

// Module watches each session's workspace between turns.
type Module struct {
	enabled     bool
	maxFiles    int
	maxReported int
	log         interface{ Warn(string, ...any) }

	mu    sync.Mutex
	state map[string]*sessionState
}

func (m *Module) Name() string { return "watch" }

func (m *Module) Init(h module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	if m.maxFiles = cfg.Int("max_files", 0); m.maxFiles <= 0 {
		m.maxFiles = defaultMaxFiles
	}
	if m.maxReported = cfg.Int("max_reported", 0); m.maxReported <= 0 {
		m.maxReported = defaultMaxReported
	}
	m.state = map[string]*sessionState{}
	if h != nil {
		m.log = h.Log()
	}
	return nil
}

// SessionStart takes the first snapshot and says nothing.
//
// Everything already on disk when a session opens is the starting state, not a
// change: reporting it would open every session by telling the model that the
// whole repository had just changed.
func (m *Module) SessionStart(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	if !m.enabled {
		return nil, nil
	}
	st := m.stateFor(s.ID())
	snap, ok := m.scan(s.Workspace().Path)

	m.mu.Lock()
	defer m.mu.Unlock()
	if !ok {
		st.off = true
		return nil, nil
	}
	st.last = snap
	st.mine = map[string]bool{}
	return nil, nil
}

// BeforeRequest reports what changed since the previous turn.
//
// A suffix block, never a prefix one: the prefix must not change between
// compactions (invariant 6), and this is the most volatile thing in the
// session.
func (m *Module) BeforeRequest(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	if !m.enabled {
		return nil, nil
	}
	st := m.stateFor(s.ID())

	m.mu.Lock()
	if st.off {
		m.mu.Unlock()
		return nil, nil
	}
	previous, mine := st.last, st.mine
	m.mu.Unlock()

	// The first request of a session that was loaded rather than created has no
	// snapshot yet. Take one and report nothing, for the same reason
	// SessionStart does.
	snap, ok := m.scan(s.Workspace().Path)
	if !ok {
		m.mu.Lock()
		st.off = true
		m.mu.Unlock()
		m.warn("watch: %s has more than %d files; not scanning it", s.Workspace().Path, m.maxFiles)
		return nil, nil
	}

	m.mu.Lock()
	st.last = snap
	st.mine = map[string]bool{}
	m.mu.Unlock()

	if previous == nil {
		return nil, nil
	}
	changes := diff(previous, snap, mine)
	if len(changes) == 0 {
		return nil, nil
	}
	return []module.ContextBlock{{Slot: "suffix", Content: render(s.Workspace().Path, changes, m.maxReported)}}, nil
}

// shellTools run arbitrary commands, so what they change can only be learned by
// looking before and after.
var shellTools = map[string]bool{"bash": true, "wait": true}

// GateTool implements module.ToolGate, only to look at the workspace before a
// shell command runs. It never objects to anything.
//
// write and edit name their path, but a shell command does not: an `rm` of the
// model's own test files was reported to it as a change "not because of your
// own edits", and it concluded the person had deleted them. A snapshot taken
// here, compared with one taken when the command returns, is what the command
// did.
func (m *Module) GateTool(_ context.Context, s module.Session, call protocol.ToolCallData) module.Verdict {
	allow := module.Verdict{Decision: module.Allow}
	if !m.enabled || s == nil || !shellTools[call.Tool] {
		return allow
	}
	st := m.stateFor(s.ID())
	m.mu.Lock()
	off := st.off
	m.mu.Unlock()
	if off {
		return allow
	}
	snap, ok := m.scan(s.Workspace().Path)
	if !ok {
		return allow
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if st.before == nil {
		st.before = map[string]snapshot{}
	}
	st.before[call.CallID] = snap
	return allow
}

// ToolResult records what the agent itself wrote, so its own edits are not
// reported back to it next turn.
//
// A file the agent wrote *and* someone else changed in the same window is
// missed once; it is caught on the following turn, because the snapshot taken
// here is what the next diff compares against. Suppressing the model's own
// edits every turn is worth that.
func (m *Module) ToolResult(_ context.Context, s module.Session, call protocol.ToolCallData, res protocol.ToolResultData) {
	if !m.enabled {
		return
	}
	if shellTools[call.Tool] {
		m.claimShellChanges(s, call, res)
		return
	}
	if res.Status != "ok" {
		return
	}
	if call.Tool != "write" && call.Tool != "edit" {
		return
	}
	path := pathArg(call.Arguments)
	if path == "" {
		return
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.Workspace().Path, path)
	}
	st := m.stateFor(s.ID())
	m.mu.Lock()
	defer m.mu.Unlock()
	if st.mine == nil {
		st.mine = map[string]bool{}
	}
	st.mine[filepath.Clean(abs)] = true
}

// claimShellChanges counts what a shell command changed as the agent's own.
//
// A command that failed may still have changed files, so the exit status does
// not matter. A command that never ran did not, so a denied one claims
// nothing.
func (m *Module) claimShellChanges(s module.Session, call protocol.ToolCallData, res protocol.ToolResultData) {
	st := m.stateFor(s.ID())
	m.mu.Lock()
	before, ok := st.before[call.CallID]
	delete(st.before, call.CallID)
	m.mu.Unlock()
	if !ok || res.Kind == protocol.ToolErrorDenied {
		return
	}
	after, ok := m.scan(s.Workspace().Path)
	if !ok {
		return
	}
	changes := diff(before, after, nil)
	m.mu.Lock()
	defer m.mu.Unlock()
	if st.mine == nil {
		st.mine = map[string]bool{}
	}
	for _, c := range changes {
		st.mine[c.Path] = true
	}
}

// SessionEnd drops the session's snapshot. A daemon that runs for weeks should
// not hold a file list for every session it ever opened.
func (m *Module) SessionEnd(_ context.Context, s module.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.state, s.ID())
}

func (m *Module) stateFor(id string) *sessionState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.state[id]; ok {
		return st
	}
	st := &sessionState{mine: map[string]bool{}}
	if m.state == nil {
		m.state = map[string]*sessionState{}
	}
	m.state[id] = st
	return st
}

func (m *Module) warn(format string, args ...any) {
	if m.log != nil {
		m.log.Warn(fmt.Sprintf(format, args...))
	}
}

// scan walks the workspace. ok is false when it holds more files than the
// budget allows, which turns the module off for that session.
func (m *Module) scan(root string) (snapshot, bool) {
	if root == "" {
		return nil, false
	}
	snap := snapshot{}
	over := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable entry is skipped, not fatal
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || skipFile(d.Name()) {
			return nil
		}
		if len(snap) >= m.maxFiles {
			over = true
			return fs.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		snap[filepath.Clean(p)] = stamp{mod: info.ModTime(), size: info.Size()}
		return nil
	})
	if over {
		return nil, false
	}
	return snap, true
}

// Change is one path and what happened to it.
type Change struct {
	Path string
	Kind string // "added" | "modified" | "deleted"
}

// diff compares two snapshots, leaving out paths the agent wrote itself.
func diff(before, after snapshot, mine map[string]bool) []Change {
	var out []Change
	for p, now := range after {
		if mine[p] {
			continue
		}
		was, existed := before[p]
		switch {
		case !existed:
			out = append(out, Change{Path: p, Kind: "added"})
		case was.size != now.size || !was.mod.Equal(now.mod):
			out = append(out, Change{Path: p, Kind: "modified"})
		}
	}
	for p := range before {
		if mine[p] {
			continue
		}
		if _, still := after[p]; !still {
			out = append(out, Change{Path: p, Kind: "deleted"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// render writes the block the model sees.
//
// Paths and kinds only, never contents: the model re-reads what it cares about,
// and inlining the files would put the whole change set in every request for
// the sake of the one line it might need.
//
// Paths are named relative to the workspace, the way every tool names them. An
// absolute path here would repeat the workspace prefix on every changed file in
// every request, and would not match what the model would have to type to read
// one back.
func render(root string, changes []Change, max int) string {
	var b strings.Builder
	b.WriteString("## Workspace changes\n")
	b.WriteString("These files changed on disk since your last turn, and not because of your own edits:\n")

	shown := changes
	if len(shown) > max {
		shown = shown[:max]
	}
	for _, c := range shown {
		fmt.Fprintf(&b, "- %s: %s\n", c.Kind, relTo(root, c.Path))
	}
	if len(changes) > len(shown) {
		fmt.Fprintf(&b, "- … and %d more\n", len(changes)-len(shown))
	}
	b.WriteString("Anything you read earlier and are still relying on may be stale. Re-read it before acting on it.\n")
	return b.String()
}

// relTo names a path the way the file tools do: relative to the workspace, with
// forward slashes. A path that somehow lies outside the workspace keeps its
// absolute form rather than being reported as a string of "..".
func relTo(root, abs string) string {
	if root == "" {
		return filepath.ToSlash(abs)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// pathArg pulls the path out of a write or edit call without caring about the
// rest of the arguments.
func pathArg(raw []byte) string {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return ""
	}
	return a.Path
}

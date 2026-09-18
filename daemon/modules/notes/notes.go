// Package notes gives the agent somewhere to keep what it works out while
// doing a job: dead ends, why X has to happen before Y, how far through a
// refactor it got. Prose, not checkboxes.
//
// # Why this is neither the task list nor memory
//
// The task list holds what to do, each item with a done_when that makes it
// checkable. Nothing held what was learned doing it, and a summarize compaction
// is where that knowledge went to die: everything the model figured out
// squeezed into one summary written in a single pass.
//
// memory is for facts the owner stated that the code does not record, and is
// meant to be true indefinitely. "I am halfway through the SimpleFIN sync" is
// false within a week, so it is the wrong shape for memory.
//
// # Scope, and why nothing new is invented
//
// Notes belong to a workspace, keyed by the identity daemon/workspace already
// computes: the same key across worktrees and subdirectories of one repository.
// The unit that actually matters is a multi-session task inside a repository,
// which nothing in the system names — so the model names its own notes instead.
// An effort keeps its own note; while the work continues the model rewrites it,
// which refreshes its clock, so it lives as long as the work does and ages out
// when the work stops.
package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

const (
	// defaultExpireDays is longer than the "few days" that prompted it, because
	// a multi-session task can run past a week.
	defaultExpireDays = 14
	// defaultMaxBytes bounds what the notes cost in one request. Past it, the
	// rest are listed by name so the model knows they exist.
	defaultMaxBytes = 4 << 10
	// maxBody stops one note eating the whole budget on its own.
	maxBody = 8 << 10
)

// Module keeps per-workspace notes.
type Module struct {
	// Root is the notes directory. Empty means <host data dir>.
	Root string

	enabled    bool
	expire     time.Duration
	maxBytes   int
	log        interface{ Warn(string, ...any) }
	now        func() time.Time // swappable for tests
	sweptMu    sync.Mutex
	sweptKeys  map[string]bool
	hostDataFn func(string) (string, error)
}

func (m *Module) Name() string { return "notes" }

func (m *Module) Init(h module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	days := cfg.Int("expire_days", defaultExpireDays)
	if days <= 0 {
		days = defaultExpireDays
	}
	m.expire = time.Duration(days) * 24 * time.Hour
	if m.maxBytes = cfg.Int("max_bytes", 0); m.maxBytes <= 0 {
		m.maxBytes = defaultMaxBytes
	}
	if m.now == nil {
		m.now = time.Now
	}
	m.sweptKeys = map[string]bool{}
	if h != nil {
		m.log = h.Log()
		m.hostDataFn = h.DataDir
	}
	if m.Root == "" && m.hostDataFn != nil {
		dir, err := m.hostDataFn(m.Name())
		if err != nil {
			return fmt.Errorf("notes: data directory: %w", err)
		}
		m.Root = dir
	}
	return nil
}

// store is the store for one session's workspace, or nil when notes have
// nowhere to live.
func (m *Module) store(s module.Session) *Store {
	if m.Root == "" {
		return nil
	}
	key := s.Workspace().Key
	if key == "" {
		return nil
	}
	return NewStore(filepath.Join(m.Root, "ws", key))
}

// ---------------------------------------------------------------- hooks

// SessionStart sweeps expired notes once per workspace per daemon run.
//
// At session start rather than on a timer: there is no background work in this
// daemon, and a sweep that runs when a session opens is a sweep that runs
// often enough without anything having to wake up.
func (m *Module) SessionStart(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	if !m.enabled {
		return nil, nil
	}
	m.sweep(s)
	return nil, nil
}

func (m *Module) sweep(s module.Session) {
	st := m.store(s)
	if st == nil {
		return
	}
	key := s.Workspace().Key

	m.sweptMu.Lock()
	if m.sweptKeys[key] {
		m.sweptMu.Unlock()
		return
	}
	m.sweptKeys[key] = true
	m.sweptMu.Unlock()

	if gone := st.Sweep(m.now().Add(-m.expire)); len(gone) > 0 {
		m.warn("notes: expired %d note(s) in %s: %s", len(gone), key, strings.Join(gone, ", "))
	}
}

// BeforeRequest puts the live notes in front of the model.
//
// A suffix block, never prefix: the prefix must not change between compactions
// and notes change constantly. Assembly keeps only suffix contexts after the
// last assistant message, so this replaces itself each turn rather than
// accumulating.
func (m *Module) BeforeRequest(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	if !m.enabled {
		return nil, nil
	}
	st := m.store(s)
	if st == nil {
		return nil, nil
	}
	live := st.List()
	if len(live) == 0 {
		// A block saying there are no notes is context spent to say nothing.
		return nil, nil
	}
	return []module.ContextBlock{{Slot: "suffix", Content: Render(live, m.now(), m.maxBytes)}}, nil
}

// BeforeCompaction hands the notes to the summariser as text it must preserve.
//
// The notes are on disk, so a compaction cannot destroy one. This is about the
// summary staying coherent with what they say.
func (m *Module) BeforeCompaction(_ context.Context, s module.Session, _ module.Range) []string {
	if !m.enabled {
		return nil
	}
	st := m.store(s)
	if st == nil {
		return nil
	}
	var out []string
	for _, n := range st.List() {
		out = append(out, fmt.Sprintf("Working note %q: %s", n.Name, n.Body))
	}
	return out
}

// AfterCompaction re-injects nothing: BeforeRequest runs on the very next
// request and puts the notes back by itself.
func (m *Module) AfterCompaction(_ context.Context, _ module.Session) ([]module.ContextBlock, error) {
	return nil, nil
}

func (m *Module) warn(format string, args ...any) {
	if m.log != nil {
		m.log.Warn(fmt.Sprintf(format, args...))
	}
}

// ---------------------------------------------------------------- rendering

// Render writes the block the model sees: every note newest first, each with
// its age, up to maxBytes.
//
// Past the cap the rest are named rather than dropped, so a note never vanishes
// without the model being able to tell that it did.
func Render(live []Note, now time.Time, maxBytes int) string {
	var b strings.Builder
	b.WriteString("## Working notes\n")
	b.WriteString("What you worked out earlier in this repository. Keep them current with " +
		"`notes.write`, which replaces a note whole; drop one you are done with using " +
		"`notes.delete`. They expire on their own, so a note you still need should be rewritten.\n\n")

	// The newest note is always rendered, however big it is. Measuring it
	// against the budget like the rest would let the header alone push every
	// note out, leaving a block that names notes and shows none — and the
	// newest is the one most likely to be the work in hand. One note cannot run
	// away with this, because a note is capped at maxBody when it is written.
	var listed []Note
	shown := 0
	for _, n := range live {
		entry := fmt.Sprintf("### %s (%s)\n%s\n\n", n.Name, humanAge(n.Age(now)), n.Body)
		if shown > 0 && b.Len()+len(entry) > maxBytes {
			listed = append(listed, n)
			continue
		}
		b.WriteString(entry)
		shown++
	}
	if len(listed) > 0 {
		b.WriteString("Not shown here, but still saved — rewrite one to bring it back into view:\n")
		for _, n := range listed {
			fmt.Fprintf(&b, "- %s (%s)\n", n.Name, humanAge(n.Age(now)))
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// humanAge is the coarse age a reader actually wants. Minutes matter for a note
// written this session; days matter for one about to expire; nothing in between
// is worth the characters.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// ---------------------------------------------------------------- tools

func (m *Module) Tools() []module.Tool {
	if !m.enabled {
		return nil
	}
	return []module.Tool{
		{
			Name: "notes.write",
			Description: "Save what you have worked out in this repository under a name, so it " +
				"survives compaction and the end of this session. Use it for findings, dead ends, " +
				"and orderings that were not obvious: the things you would have to rediscover. " +
				"Writing a name that already exists replaces that note, so keep one note per " +
				"piece of work and rewrite it as you learn more. Not for facts the owner told " +
				"you, which belong in memory.save, and not for a plan, which belongs in task.update.",
			Schema: schema(`{"type":"object","required":["name","body"],"properties":{
				"name":{"type":"string","description":"what this note is about, e.g. simplefin-sync-refactor"},
				"body":{"type":"string","description":"what you worked out, in prose"}}}`),
			Run: m.runWrite,
		},
		{
			Name:        "notes.delete",
			Description: "Delete a note by name, for work that is finished or a note that turned out to be wrong.",
			Schema: schema(`{"type":"object","required":["name"],"properties":{
				"name":{"type":"string"}}}`),
			Run: m.runDelete,
		},
	}
}

func (m *Module) runWrite(_ context.Context, s module.Session, raw json.RawMessage) (string, error) {
	var a struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := decode(raw, &a); err != nil {
		return "", module.Fail(protocol.ToolErrorInvalidArgs, "invalid arguments: %s", err)
	}
	st := m.store(s)
	if st == nil {
		return "", module.Fail(protocol.ToolErrorIO, "notes have nowhere to live for this workspace")
	}
	if len(a.Body) > maxBody {
		return "", module.Fail(protocol.ToolErrorInvalidArgs,
			"a note is limited to %d bytes; this one is %d, so shorten it to what you would not want to rediscover",
			maxBody, len(a.Body))
	}
	n, err := st.Write(a.Name, a.Body, m.now())
	if err != nil {
		return "", module.Fail(protocol.ToolErrorInvalidArgs, "%s", err)
	}
	return fmt.Sprintf("saved note %q (%d bytes)", n.Name, len(n.Body)), nil
}

func (m *Module) runDelete(_ context.Context, s module.Session, raw json.RawMessage) (string, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := decode(raw, &a); err != nil {
		return "", module.Fail(protocol.ToolErrorInvalidArgs, "invalid arguments: %s", err)
	}
	if strings.TrimSpace(a.Name) == "" {
		return "", module.Fail(protocol.ToolErrorInvalidArgs, "name is required")
	}
	st := m.store(s)
	if st == nil {
		return "", module.Fail(protocol.ToolErrorIO, "notes have nowhere to live for this workspace")
	}
	existed, err := st.Delete(a.Name)
	if err != nil {
		return "", module.FailWith(protocol.ToolErrorIO, err)
	}
	if !existed {
		return "", module.Fail(protocol.ToolErrorNotFound, "no note named %q", a.Name)
	}
	return fmt.Sprintf("deleted note %q", a.Name), nil
}

// ---------------------------------------------------------------- reading, for the CLI

// ForWorkspace reads one workspace's notes from a notes root.
func ForWorkspace(root, key string) []Note {
	return NewStore(filepath.Join(root, "ws", key)).List()
}

// AllWorkspaces reads every workspace's notes, keyed by workspace key.
func AllWorkspaces(root string) map[string][]Note {
	entries, err := os.ReadDir(filepath.Join(root, "ws"))
	if err != nil {
		return nil
	}
	out := map[string][]Note{}
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			keys = append(keys, e.Name())
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if live := ForWorkspace(root, k); len(live) > 0 {
			out[k] = live
		}
	}
	return out
}

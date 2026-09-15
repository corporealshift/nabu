package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// SaveInstructions governs when the model saves. A vaguer wording fills memory
// with descriptions of the code, which go stale while the code does not.
const SaveInstructions = "Save a memory when the user corrects you, confirms an " +
	"approach, or tells you something about this project you could not have read from " +
	"the code or the git history. Save a pointer to anything outside the repository " +
	"you had to be told about. Do not save architecture, file layout, or anything the " +
	"repository already records: that goes stale and the code does not."

// DefaultCuratorMaxPerPass is spec 11.4's cap: at most three memories per
// pass, so a pass that judges badly does so in a small way.
const DefaultCuratorMaxPerPass = 3

// Module is the memory module.
type Module struct {
	// Root is the memory directory. Empty means the host's data directory.
	Root string
	// ImportDirs are other tools' memory directories, read but never written.
	ImportDirs []string
	// NoGit disables versioning. Tests set it; so does a machine without git.
	NoGit bool
	// Curator turns the automatic passes on. Default on.
	Curator bool
	// CuratorModel overrides the model a pass uses.
	CuratorModel string
	// CuratorMaxPerPass caps how many memories one pass may write.
	CuratorMaxPerPass int

	enabled bool
	host    module.Host
	global  *Store
	imports []*Store
	log     logger

	// wrote counts what each session changed, for the commit at session end.
	// cursors is the last event each session's curator pass saw.
	mu      sync.Mutex
	wrote   map[string]*writes
	cursors map[string]string
}

// writes is one session's changes to memory.
type writes struct{ saved, forgotten int }

// logger is the slice of the host this module needs, so tests need no host.
type logger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

type discardLog struct{}

func (discardLog) Warn(string, ...any) {}
func (discardLog) Info(string, ...any) {}

// Name implements module.Module.
func (m *Module) Name() string { return "memory" }

// Init implements module.Module. Stores are read on demand, so a memory saved
// by one session reaches the next without restarting the daemon.
func (m *Module) Init(h module.Host, cfg module.Config) error {
	m.log = discardLog{}
	if h != nil && h.Log() != nil {
		m.log = h.Log()
	}
	m.enabled = cfg.Enabled()
	m.host = h
	m.wrote = map[string]*writes{}
	m.cursors = map[string]string{}
	m.Curator = cfg.Bool("curator", true)
	m.CuratorModel = cfg.String("curator_model", "")
	if m.CuratorMaxPerPass = cfg.Int("curator_max_per_pass", 0); m.CuratorMaxPerPass <= 0 {
		m.CuratorMaxPerPass = DefaultCuratorMaxPerPass
	}

	if m.Root == "" {
		// ~/.nabu/memory, per spec 11.2.
		if h == nil {
			return nil
		}
		dir, err := h.DataDir(m.Name())
		if err != nil {
			return err
		}
		m.Root = dir
	}

	m.global = NewStore(filepath.Join(m.Root, "global"), ScopeGlobal, false)

	for _, dir := range append(append([]string(nil), m.ImportDirs...), configImports(cfg)...) {
		dir = expandHome(dir)
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			// A stale import is configuration that no longer applies, not a failure.
			continue
		}
		m.imports = append(m.imports, NewStore(dir, ScopeGlobal, true))
	}

	if err := m.initGit(); err != nil {
		m.log.Warn("memory: versioning is off", "error", err)
	}
	return nil
}

// configImports reads memory.import_dirs.
func configImports(cfg module.Config) []string {
	return cfg.Strings("import_dirs", nil)
}

// expandHome resolves a leading ~ so a configured path can be written the way
// a person would type it.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimLeft(p[1:], `/\`))
}

// GlobalStore is user-level memory: who the owner is, how they like to work.
func (m *Module) GlobalStore() *Store { return m.global }

// WorkspaceStore is the memory of one repository. The workspace key is what
// separates two repositories and joins two worktrees of one.
func (m *Module) WorkspaceStore(s module.Session) *Store {
	key := "unknown"
	if s != nil && s.Workspace().Key != "" {
		key = s.Workspace().Key
	}
	return NewStore(filepath.Join(m.Root, "ws", key), ScopeWorkspace, false)
}

// globalCorpus is global memory plus imports, with nabu's own copy winning.
// Shadowing an import beats editing it, which would be nabu writing into
// another tool's state.
func (m *Module) globalCorpus() []Memory {
	var out []Memory
	seen := map[string]bool{}
	if m.global != nil {
		mems, err := m.global.Load()
		if err != nil {
			m.log.Warn("memory: cannot read global memory", "error", err)
		}
		for _, mem := range mems {
			seen[mem.Name] = true
			out = append(out, mem)
		}
	}
	for _, imp := range m.imports {
		mems, err := imp.Load()
		if err != nil {
			continue
		}
		for _, mem := range mems {
			if seen[mem.Name] {
				continue
			}
			seen[mem.Name] = true
			out = append(out, mem)
		}
	}
	return out
}

// workspaceCorpus is the memories of one repository.
func (m *Module) workspaceCorpus(s module.Session) []Memory {
	mems, err := m.WorkspaceStore(s).Load()
	if err != nil {
		m.log.Warn("memory: cannot read workspace memory", "error", err)
	}
	return mems
}

// SessionStart implements module.SessionStarter: the indexes go in front of
// the model as a prefix block.
func (m *Module) SessionStart(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	return m.blocks(s), nil
}

// AfterCompaction implements module.CompactionHook. A summarize retires the
// prefix, so the index has to go back or the model forgets what it knows.
func (m *Module) AfterCompaction(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	return m.blocks(s), nil
}

// BeforeCompaction implements module.CompactionHook. A compaction is about to
// retire events, so this is the last chance to learn anything from them.
func (m *Module) BeforeCompaction(ctx context.Context, s module.Session, _ module.Range) []string {
	m.curate(ctx, s)
	return nil
}

// blocks renders the memory block, or nothing when there are no memories: a
// block saying so is context spent to say nothing.
func (m *Module) blocks(s module.Session) []module.ContextBlock {
	if !m.enabled || m.global == nil {
		return nil
	}
	global, ws := m.globalCorpus(), m.workspaceCorpus(s)
	if len(global) == 0 && len(ws) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("## Memory\n\n")
	b.WriteString(SaveInstructions + "\n\n")
	b.WriteString("To read one of these in full, call `memory.recall` with what you " +
		"want to know. The list below is descriptions only.\n")

	// Global first, then workspace, per spec 11.3.
	m.section(&b, s, "What you know about the owner and how they work", global)
	m.section(&b, s, "What you know about this project", ws)

	return []module.ContextBlock{{Slot: "prefix", Content: b.String()}}
}

// section renders one index, reporting a cap breach to the session so the debt
// reaches a human and not only the model.
func (m *Module) section(b *strings.Builder, s module.Session, heading string, mems []Memory) {
	if len(mems) == 0 {
		return
	}
	content, notice := BuildIndex(mems)
	b.WriteString("\n### " + heading + "\n\n")
	b.WriteString(content)

	if notice != "" && s != nil {
		if _, err := s.Append(protocol.EventNotice, protocol.NoticeData{
			Source: "module:" + m.Name(), Level: "warn", Message: notice,
		}); err != nil {
			m.log.Warn("memory: cannot record the index notice", "error", err)
		}
	}
}

// recordWrite counts a change for the commit at session end.
func (m *Module) recordWrite(s module.Session, saved, forgotten int) {
	if s == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.wrote[s.ID()]
	if w == nil {
		w = &writes{}
		m.wrote[s.ID()] = w
	}
	w.saved += saved
	w.forgotten += forgotten
}

// takeWrites returns and clears what a session changed.
func (m *Module) takeWrites(id string) writes {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.wrote[id]
	delete(m.wrote, id)
	if w == nil {
		return writes{}
	}
	return *w
}

// Tools implements module.ToolProvider. A disabled module offers none.
func (m *Module) Tools() []module.Tool {
	if !m.enabled {
		return nil
	}
	return m.toolSet()
}

// SessionEnd implements module.SessionEnder.
func (m *Module) SessionEnd(ctx context.Context, s module.Session) {
	if s == nil {
		return
	}
	// Curating first puts the pass's own writes inside this session's commit
	// rather than trailing them into the next one.
	m.curate(ctx, s)
	m.commitSession(ctx, s, m.takeWrites(s.ID()))
}

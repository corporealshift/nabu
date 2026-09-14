// Package memory gives nabu a memory the model can read and write, so a
// session on a repository starts already knowing what earlier sessions
// learned.
//
// Memory is markdown files under the nabu root, global and per workspace, with
// a one-line-per-memory index injected as a prefix context block. Recall is
// BM25, which needs no model and no embeddings. Nothing in the agent, the
// session or the protocol knows memory exists.
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

// SaveInstructions is the wording that governs when the model saves. It is
// fixed here rather than paraphrased per injection, because the wording is the
// whole mechanism: a vaguer version produces a memory full of descriptions of
// the code, which go stale while the code does not.
const SaveInstructions = "Save a memory when the user corrects you, confirms an " +
	"approach, or tells you something about this project you could not have read from " +
	"the code or the git history. Save a pointer to anything outside the repository " +
	"you had to be told about. Do not save architecture, file layout, or anything the " +
	"repository already records: that goes stale and the code does not."

// Module is the memory module.
type Module struct {
	// Root is the memory directory. Empty means the host's data directory.
	Root string
	// ImportDirs are other tools' memory directories, read but never written.
	ImportDirs []string
	// NoGit disables versioning. Tests set it; so does a machine without git.
	NoGit bool

	enabled bool
	global  *Store
	imports []*Store
	log     logger

	// wrote counts what each session changed, for the commit at session end.
	mu    sync.Mutex
	wrote map[string]*writes
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

// Init implements module.Module. It resolves the roots and opens the import
// directories; the stores themselves are read on demand, so a memory saved by
// one session is visible to the next without restarting the daemon.
func (m *Module) Init(h module.Host, cfg module.Config) error {
	m.log = discardLog{}
	if h != nil && h.Log() != nil {
		m.log = h.Log()
	}
	m.enabled = cfg.Enabled()
	m.wrote = map[string]*writes{}

	if m.Root == "" {
		// The host only exposes DataDir, and the module boundary is worth more
		// than the spec's literal path: memory lives under the module's own
		// data directory rather than reaching around for the nabu root.
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
			// An import that is not there is a configuration that no longer
			// applies, not a failure: memory works without it.
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

// WorkspaceStore is the memory of one repository. Keying by the workspace key
// is what keeps two repositories from sharing memory, and what lets worktrees
// of one repository share it.
func (m *Module) WorkspaceStore(s module.Session) *Store {
	key := "unknown"
	if s != nil && s.Workspace().Key != "" {
		key = s.Workspace().Key
	}
	return NewStore(filepath.Join(m.Root, "ws", key), ScopeWorkspace, false)
}

// globalCorpus is global memory plus imports, with nabu's own copy winning.
// A save naming an imported memory writes nabu's version, and from then on the
// import is shadowed rather than edited: editing it would be nabu writing into
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

// AfterCompaction implements module.CompactionHook. A prefix block is fixed
// between compactions, so a summarize retires the index and it has to be put
// back or the model forgets what it knows.
func (m *Module) AfterCompaction(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	return m.blocks(s), nil
}

// BeforeCompaction implements module.CompactionHook. Nothing needs preserving
// in the summary: the index is re-injected afterwards instead.
func (m *Module) BeforeCompaction(context.Context, module.Session, module.Range) []string {
	return nil
}

// blocks renders the memory block, or nothing when there is nothing to say. A
// block announcing that there are no memories is context spent to say nothing.
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

// section renders one index under a heading and reports any cap breach to the
// session, so the debt is visible to a human rather than only to the model.
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

// Tools implements module.ToolProvider. A disabled module offers none: the
// model must not be shown a tool that will not work.
func (m *Module) Tools() []module.Tool {
	if !m.enabled {
		return nil
	}
	return m.toolSet()
}

// SessionEnd implements module.SessionEnder: one commit per session that
// wrote, rather than one per fact.
func (m *Module) SessionEnd(ctx context.Context, s module.Session) {
	if s == nil {
		return
	}
	m.commitSession(ctx, s, m.takeWrites(s.ID()))
}

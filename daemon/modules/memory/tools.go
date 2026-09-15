package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// maxRecall is how many memories one recall returns. A few hundred lines is
// affordable on demand in a way that injecting the whole corpus is not.
const maxRecall = 5

// toolSet is the memory tools: recall reads, save and forget write.
func (m *Module) toolSet() []module.Tool {
	return []module.Tool{
		{
			Name: "memory.recall",
			Description: "Search memory and read the matching memories in full. " +
				"Use it when the index suggests something relevant, or when you are " +
				"about to guess at a preference or a decision you may have been told.",
			Schema: json.RawMessage(`{"type":"object","required":["query"],"properties":{` +
				`"query":{"type":"string","description":"what you want to know, in words"},` +
				`"scope":{"type":"string","enum":["global","workspace","all"],` +
				`"description":"which memories to search; defaults to all"}}}`),
			Run: m.runRecall,
		},
		{
			Name: "memory.save",
			Description: "Write one memory. " + SaveInstructions +
				" Saving an existing name replaces that memory rather than adding a second one.",
			Schema: json.RawMessage(`{"type":"object",` +
				`"required":["scope","name","type","description","body"],"properties":{` +
				`"scope":{"type":"string","enum":["global","workspace"],` +
				`"description":"global for the owner and how they work, workspace for this repository"},` +
				`"name":{"type":"string","description":"kebab-case, naming the subject of the fact"},` +
				`"type":{"type":"string","enum":["user","feedback","project","reference"]},` +
				`"description":{"type":"string","description":"one line: the fact, and the hook that makes it worth opening"},` +
				`"body":{"type":"string","description":"the fact, then a **Why:** line and a **How to apply:** line"}}}`),
			Run: m.runSave,
		},
		{
			Name: "memory.forget",
			Description: "Delete one memory by name, for a fact that turned out to be " +
				"wrong or no longer holds. Git keeps the history.",
			Schema: json.RawMessage(`{"type":"object","required":["name"],"properties":{` +
				`"name":{"type":"string","description":"the memory name as listed in the index"}}}`),
			Run: m.runForget,
		},
	}
}

// runRecall is memory.recall.
func (m *Module) runRecall(_ context.Context, s module.Session, args json.RawMessage) (string, error) {
	var p struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
	}
	if err := decode(args, &p); err != nil {
		return "", fmt.Errorf("memory.recall: %w", err)
	}
	if strings.TrimSpace(p.Query) == "" {
		return "", fmt.Errorf("memory.recall: query is required")
	}

	// A caller asking about a subject does not care which store answers, and
	// every hit names its scope.
	scope := Scope(strings.TrimSpace(p.Scope))
	if scope == "" {
		scope = ScopeAll
	}
	var corpus []Memory
	switch scope {
	case ScopeAll:
		corpus = append(m.globalCorpus(), m.workspaceCorpus(s)...)
	case ScopeGlobal:
		corpus = m.globalCorpus()
	case ScopeWorkspace:
		corpus = m.workspaceCorpus(s)
	default:
		return "", fmt.Errorf("memory.recall: unknown scope %q (global|workspace|all)", p.Scope)
	}

	hits := Rank(p.Query, corpus, maxRecall)
	if len(hits) == 0 {
		return fmt.Sprintf("No memories match %q. Nothing has been remembered about this yet.", p.Query), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d memories match %q.\n", len(hits), p.Query)
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n--- memory %d of %d ---\n", i+1, len(hits))
		fmt.Fprintf(&b, "name: %s\n", hit.Name)
		fmt.Fprintf(&b, "scope: %s\n", hit.Scope)
		if hit.Type != "" {
			fmt.Fprintf(&b, "type: %s\n", hit.Type)
		}
		if hit.Imported {
			b.WriteString("imported: yes (saving this name writes a nabu copy instead)\n")
		}
		if hit.Description != "" {
			fmt.Fprintf(&b, "description: %s\n", hit.Description)
		}
		b.WriteString("\n" + strings.TrimRight(hit.Body, "\n") + "\n")
	}
	return b.String(), nil
}

// runSave is memory.save.
func (m *Module) runSave(_ context.Context, s module.Session, args json.RawMessage) (string, error) {
	var p struct {
		Scope       string `json:"scope"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Body        string `json:"body"`
	}
	if err := decode(args, &p); err != nil {
		return "", fmt.Errorf("memory.save: %w", err)
	}

	// A save always targets one store: a fact belongs either to the owner or
	// to this repository.
	store, err := m.writableStore(s, p.Scope)
	if err != nil {
		return "", err
	}

	mem := Memory{
		Name:        p.Name,
		Description: strings.TrimSpace(p.Description),
		Type:        strings.TrimSpace(p.Type),
		Body:        strings.TrimSpace(p.Body),
	}
	if mem.Type == "" {
		return "", fmt.Errorf("memory.save: type is required (user|feedback|project|reference)")
	}
	if err := store.Save(mem); err != nil {
		return "", fmt.Errorf("memory.save: %w", err)
	}
	m.recordWrite(s, 1, 0)
	m.reindex(s, store)

	name := Slug(mem.Name)
	return fmt.Sprintf("Saved %s memory %q. It will be in front of you at the start of the next session.",
		store.scope, name), nil
}

// runForget is memory.forget.
func (m *Module) runForget(_ context.Context, s module.Session, args json.RawMessage) (string, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := decode(args, &p); err != nil {
		return "", fmt.Errorf("memory.forget: %w", err)
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return "", fmt.Errorf("memory.forget: name is required")
	}

	// Workspace first: a name in both is likely the one this repository wrote.
	for _, store := range []*Store{m.WorkspaceStore(s), m.global} {
		if store == nil {
			continue
		}
		if err := store.Forget(name); err == nil {
			m.recordWrite(s, 0, 1)
			m.reindex(s, store)
			return fmt.Sprintf("Forgot %s memory %q.", store.scope, Slug(name)), nil
		}
	}
	return "", fmt.Errorf("memory.forget: no memory named %q that nabu can delete "+
		"(imported memories belong to another tool; save over the name instead)", name)
}

// writableStore resolves a save's scope to the store it writes to.
func (m *Module) writableStore(s module.Session, scope string) (*Store, error) {
	switch Scope(strings.TrimSpace(scope)) {
	case ScopeGlobal:
		if m.global == nil {
			return nil, fmt.Errorf("memory.save: memory has no root")
		}
		return m.global, nil
	case ScopeWorkspace:
		return m.WorkspaceStore(s), nil
	default:
		return nil, fmt.Errorf("memory.save: unknown scope %q (global|workspace)", scope)
	}
}

// reindex rewrites a store's MEMORY.md so it matches the files beside it.
func (m *Module) reindex(s module.Session, store *Store) {
	notice, err := WriteIndex(store)
	if err != nil {
		m.log.Warn("memory: cannot write the index", "dir", store.Dir(), "error", err)
		return
	}
	if notice == "" || s == nil {
		return
	}
	if _, err := s.Append(protocol.EventNotice, protocol.NoticeData{
		Source: "module:" + m.Name(), Level: "warn", Message: notice,
	}); err != nil {
		m.log.Warn("memory: cannot record the index notice", "error", err)
	}
}

// decode unmarshals tool arguments, treating no arguments as an empty object.
func decode(args json.RawMessage, into any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, into); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

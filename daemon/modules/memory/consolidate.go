package memory

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
)

// maxConsolidationPerPass caps merges and deletions separately. A pass that
// judges badly should do so in a small way, and git keeps whatever it removed.
const maxConsolidationPerPass = 3

// consolidateSystem frames the over-cap pass.
const consolidateSystem = "You are consolidating the memory of a coding agent. The " +
	"index is over its size cap. You are shown every memory by name and description.\n\n" +
	"Find memories that say the same thing and should be one, and memories that no " +
	"longer hold and should go. Be conservative: losing a fact is worse than an index " +
	"that is slightly too long, and an empty list is an acceptable answer.\n\n" +
	"Reply with JSON only: {\"merge\": [{\"into\": name, \"absorb\": [names], " +
	"\"description\": text, \"body\": text}], \"forget\": [names]}. `into` must be one " +
	"of the existing names; it is rewritten with your description and body, and the " +
	"memories it absorbs are deleted."

// merge is one proposed consolidation.
type merge struct {
	Into        string   `json:"into"`
	Absorb      []string `json:"absorb"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
}

// consolidationPlan is what the model proposed.
type consolidationPlan struct {
	Merge  []merge  `json:"merge"`
	Forget []string `json:"forget"`
}

// wouldEmpty reports whether applying the plan would leave nothing behind.
// Consolidation exists to shorten an index, never to clear it.
func (p consolidationPlan) wouldEmpty(total int) bool {
	gone := map[string]bool{}
	for _, mg := range p.Merge {
		for _, a := range mg.Absorb {
			gone[Slug(a)] = true
		}
	}
	for _, f := range p.Forget {
		gone[Slug(f)] = true
	}
	return total > 0 && len(gone) >= total
}

// consolidate runs the over-cap pass. It is a no-op while the index fits,
// which is the common case and the one that must stay free.
func (m *Module) consolidate(ctx context.Context, s module.Session) {
	if !m.enabled || !m.Curator || s == nil || m.host == nil {
		return
	}
	if m.host.Model() == nil || m.host.Tools() == nil {
		return
	}

	// Only nabu's own memories are eligible: an imported one belongs to
	// another tool and is not nabu's to merge or delete.
	own, err := m.global.Load()
	if err != nil || len(own) == 0 {
		return
	}
	if _, notice := BuildIndex(own); notice == "" {
		return // still inside the cap
	}

	resp, err := m.host.Model().Complete(ctx, s, module.CompletionRequest{
		Model:     m.CuratorModel,
		System:    consolidateSystem,
		Messages:  []module.Message{{Role: "user", Content: consolidatePrompt(own)}},
		MaxTokens: 1500,
	})
	if err != nil {
		m.log.Warn("memory: the consolidation pass failed", "error", err)
		return
	}

	plan := parseConsolidation(resp.Content)
	if plan.wouldEmpty(len(own)) {
		m.log.Warn("memory: refusing a consolidation that would remove every memory")
		return
	}
	m.applyConsolidation(ctx, s, plan, own)
}

// consolidatePrompt lists the memories by name and description. Bodies are not
// sent: they are most of the bytes and the decision is about subjects.
func consolidatePrompt(mems []Memory) string {
	var b strings.Builder
	b.WriteString("These memories are over the index cap.\n\n")
	for _, m := range mems {
		b.WriteString("- " + m.Name)
		if m.Description != "" {
			b.WriteString(": " + m.Description)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// parseConsolidation reads the plan, tolerating prose or a fence around it.
func parseConsolidation(reply string) consolidationPlan {
	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return consolidationPlan{}
	}
	var p consolidationPlan
	if err := json.Unmarshal([]byte(reply[start:end+1]), &p); err != nil {
		return consolidationPlan{}
	}
	return p
}

// applyConsolidation writes the survivors and removes what they absorbed,
// through the tool API so the whole pass is visible and git records it.
func (m *Module) applyConsolidation(ctx context.Context, s module.Session, p consolidationPlan, own []Memory) {
	exists := map[string]Memory{}
	for _, mem := range own {
		exists[mem.Name] = mem
	}

	merged := 0
	for _, mg := range p.Merge {
		if merged >= maxConsolidationPerPass {
			break
		}
		target, ok := exists[Slug(mg.Into)]
		if !ok || strings.TrimSpace(mg.Body) == "" {
			continue // a merge into something that is not there is not a merge
		}
		args, err := json.Marshal(proposal{
			Scope: string(ScopeGlobal), Name: target.Name, Type: typeOr(target.Type, "reference"),
			Description: firstNonEmpty(mg.Description, target.Description),
			Body:        mg.Body,
		})
		if err != nil {
			continue
		}
		if _, err := m.host.Tools().Call(ctx, s, "memory.save", args); err != nil {
			m.log.Warn("memory: cannot write a merged memory", "name", target.Name, "error", err)
			continue
		}
		for _, a := range mg.Absorb {
			if Slug(a) == target.Name {
				continue // absorbing itself would delete the survivor
			}
			m.forgetIfPresent(ctx, s, exists, Slug(a))
		}
		merged++
	}

	dropped := 0
	for _, f := range p.Forget {
		if dropped >= maxConsolidationPerPass {
			break
		}
		if m.forgetIfPresent(ctx, s, exists, Slug(f)) {
			dropped++
		}
	}
}

// forgetIfPresent removes a memory nabu owns, and reports whether it did. A
// name that is not there, or belongs to an import, is skipped rather than
// treated as an error.
func (m *Module) forgetIfPresent(ctx context.Context, s module.Session, own map[string]Memory, name string) bool {
	if _, ok := own[name]; !ok {
		return false
	}
	args, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return false
	}
	if _, err := m.host.Tools().Call(ctx, s, "memory.forget", args); err != nil {
		m.log.Warn("memory: cannot forget during consolidation", "name", name, "error", err)
		return false
	}
	delete(own, name)
	return true
}

func typeOr(t, def string) string {
	if validTypes[t] {
		return t
	}
	return def
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

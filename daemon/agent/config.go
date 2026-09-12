package agent

import "github.com/corporealshift/nabu/daemon/module"

// CompactionConfig sets when and how the loop shrinks the request (spec §6).
type CompactionConfig struct {
	// ClearAt is the fraction of the context window at which stage 0
	// (tool-result clearing) runs. Default 0.70.
	ClearAt float64
	// SummarizeAt is the fraction at which stage 1 (summarize) runs.
	// Default 0.85.
	SummarizeAt float64
	// KeepTurns is how many recent assistant turns keep their tool results
	// intact through stage 0. Default 4.
	KeepTurns int
}

// Modules holds per-module config sections, keyed by module name. The daemon
// fills it from config.toml.
type Modules map[string]map[string]any

// Config tunes the loop. Zero values take the defaults below.
type Config struct {
	SystemPrompt string
	// DefaultModel is used when a session is created without one.
	DefaultModel string
	// MaxConsecutiveVetoes bounds veto rounds before the session blocks.
	// Default 5.
	MaxConsecutiveVetoes int
	// NoProgressTurns is how many identical veto rounds with no tool use end
	// the loop. Default 3.
	NoProgressTurns int
	// MaxTokens caps each response; 0 uses the provider default.
	MaxTokens  int
	Compaction CompactionConfig
	// TasksEnabled offers task.update to the model. Default true.
	TasksEnabled *bool
	// ModuleConfigs are the [modules.<name>] sections.
	ModuleConfigs Modules
}

func (c Config) withDefaults() Config {
	if c.SystemPrompt == "" {
		c.SystemPrompt = DefaultSystemPrompt
	}
	if c.DefaultModel == "" {
		c.DefaultModel = "default"
	}
	if c.MaxConsecutiveVetoes == 0 {
		c.MaxConsecutiveVetoes = 5
	}
	if c.NoProgressTurns == 0 {
		c.NoProgressTurns = 3
	}
	if c.Compaction.ClearAt == 0 {
		c.Compaction.ClearAt = 0.70
	}
	if c.Compaction.SummarizeAt == 0 {
		c.Compaction.SummarizeAt = 0.85
	}
	if c.Compaction.KeepTurns == 0 {
		c.Compaction.KeepTurns = 4
	}
	if c.TasksEnabled == nil {
		t := true
		c.TasksEnabled = &t
	}
	return c
}

// ModuleConfig returns the section for a module; a missing section is empty,
// which means "on, with defaults".
func (c Config) ModuleConfig(name string) module.Config {
	if c.ModuleConfigs == nil {
		return module.Config{}
	}
	return module.Config(c.ModuleConfigs[name])
}

// DefaultSystemPrompt is the static prefix of every request. It states
// mechanism, not policy: how tools work and how a turn ends. Policy (what to
// gate, when to require tasks) arrives from modules as context blocks.
const DefaultSystemPrompt = `You are nabu, a coding agent running inside the user's workspace.

Rules:
- Use the tools to inspect and change the workspace. Paths are relative to the workspace unless absolute.
- For work with more than one step, call task.update first with every step, each with a done_when: the observable condition that proves that step is finished. Keep the list current as you go.
- Verify before you claim: run the relevant test or command and read its output before marking a task done or saying the work is finished.
- When the work is finished, reply with a short summary and no tool calls. If you cannot proceed without the user, say exactly what you need.`

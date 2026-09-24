// Package config loads nabu's configuration: the base file under the nabu
// root, plus an optional overlay from a trusted workspace.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DaemonConfig holds the "daemon" object.
type DaemonConfig struct {
	Bind     string `json:"bind"`
	Token    string `json:"token"`
	LogFile  string `json:"log_file"`
	LogLevel string `json:"log_level"`
	// DefaultModel is what a session uses when it names none. Either
	// "provider/model" or a bare model name, which goes to the default
	// provider. Without it every request carries a placeholder name.
	DefaultModel string `json:"default_model"`
	// BrowseRoots bound what nabu.workspace.browse may list, so a client
	// picking a directory cannot enumerate the whole machine. Empty means the
	// user's home directory. Point them at where the projects actually are and
	// the picker stops being a tapping exercise.
	BrowseRoots []string `json:"browse_roots"`
	// ArchiveAfterDays archives a session nothing has happened in for this
	// many days (issue 56). A pointer because 0 is a real setting, meaning
	// never; unset means DefaultArchiveAfterDays.
	ArchiveAfterDays *int `json:"archive_after_days"`
}

// DefaultArchiveAfterDays is how long a session sits untouched before it is
// archived, when the config does not say.
const DefaultArchiveAfterDays = 3

// ArchiveAfter is the configured idle period, and false when archiving by age
// is off.
func (c DaemonConfig) ArchiveAfter() (time.Duration, bool) {
	days := DefaultArchiveAfterDays
	if c.ArchiveAfterDays != nil {
		days = *c.ArchiveAfterDays
	}
	if days <= 0 {
		return 0, false
	}
	return time.Duration(days) * 24 * time.Hour, true
}

func (c *DaemonConfig) withDefaults() {
	if c.Bind == "" {
		c.Bind = "127.0.0.1:8737"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}

// ProviderConfig holds one entry of the "providers" object, keyed by provider name.
type ProviderConfig struct {
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	MaxInFlight int    `json:"max_in_flight"`
	// ContextWindow is the model's context size in tokens. Zero means unknown,
	// which turns size-based compaction off and lets a session grow until the
	// provider refuses it.
	ContextWindow int   `json:"context_window"`
	TasksEnabled  *bool `json:"tasks_enabled"`
	// MaxTokens caps one reply. Zero means the daemon's default, 16384. A
	// hosted API that allows less output than that needs it set lower, or it
	// refuses the request.
	MaxTokens int `json:"max_tokens"`
}

func (c *ProviderConfig) withDefaults() {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.openai.com/v1"
	}
	if c.MaxInFlight < 1 {
		c.MaxInFlight = 1
	}
	if c.TasksEnabled == nil {
		t := true
		c.TasksEnabled = &t
	}
}

// BudgetConfig holds the "budget" object. Spec 12 makes turns the unit and
// tokens and dollars optional caps, enforced only for providers that report
// them. The two loop bounds live here too, because they bound the same loop.
type BudgetConfig struct {
	// MaxTurns is the interactive default. 0 means unlimited; "nabu run"
	// overrides it to 60 at session creation, per spec 12.
	MaxTurns  int     `json:"max_turns"`
	MaxTokens int     `json:"max_tokens"`
	MaxUSD    float64 `json:"max_usd"`

	// MaxConsecutiveVetoes bounds veto rounds before the session blocks.
	// Default 5.
	MaxConsecutiveVetoes int `json:"max_consecutive_vetoes"`
	// NoProgressTurns is how many identical veto rounds with no tool use end
	// the loop. Default 3.
	NoProgressTurns int `json:"no_progress_turns"`
}

// RunDefaultMaxTurns is the turn cap "nabu run" applies when config leaves
// MaxTurns unlimited. Spec 12: unlimited interactively, 60 for a headless run.
const RunDefaultMaxTurns = 60

func (c *BudgetConfig) withDefaults() {
	if c.MaxConsecutiveVetoes == 0 {
		c.MaxConsecutiveVetoes = 5
	}
	if c.NoProgressTurns == 0 {
		c.NoProgressTurns = 3
	}
}

// Config is the top-level configuration.
type Config struct {
	Daemon            DaemonConfig              `json:"daemon"`
	Providers         map[string]ProviderConfig `json:"providers"`
	Budget            BudgetConfig              `json:"budget"`
	Modules           map[string]map[string]any `json:"modules"`
	TrustedWorkspaces map[string]bool           `json:"-"`
}

// Load reads the config JSON from root/config.json.
// If the file does not exist, returns a Config with all defaults.
func Load(root string) (*Config, error) {
	cfg := &Config{
		TrustedWorkspaces: make(map[string]bool),
	}

	cfgPath := filepath.Join(root, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.applyDefaults()
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", cfgPath, err)
	}

	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("config: decode %s: %w", cfgPath, err)
	}

	cfg.applyDefaults()
	return cfg, nil
}

// applyDefaults fills every unset value. Providers are written back into the
// map by key: ranging a map of values yields copies, so mutating the loop
// variable alone would discard the defaults.
func (c *Config) applyDefaults() {
	c.Daemon.withDefaults()
	c.Budget.withDefaults()
	for name, p := range c.Providers {
		p.withDefaults()
		c.Providers[name] = p
	}
}

// TrustWorkspace marks workspacePath as trusted.
func (c *Config) TrustWorkspace(workspacePath string) {
	if c.TrustedWorkspaces == nil {
		c.TrustedWorkspaces = make(map[string]bool)
	}
	c.TrustedWorkspaces[absOr(workspacePath)] = true
}

// IsWorkspaceTrusted reports whether workspacePath is trusted.
func (c *Config) IsWorkspaceTrusted(workspacePath string) bool {
	if c.TrustedWorkspaces == nil {
		return false
	}
	return c.TrustedWorkspaces[absOr(workspacePath)]
}

// absOr returns the absolute form of p, or p itself if that fails.
func absOr(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// workspaceOf returns the workspace directory an overlay file belongs to.
// The spec puts the overlay at <workspace>/.nabu/config.json, so a parent
// directory named ".nabu" is skipped; otherwise the containing directory is
// the workspace.
func workspaceOf(overlayPath string) string {
	dir := filepath.Dir(overlayPath)
	if filepath.Base(dir) == ".nabu" {
		return filepath.Dir(dir)
	}
	return dir
}

// ApplyOverlay merges a workspace overlay into this Config. Overlay providers
// and modules override matching base keys and add new ones. The overlay is
// ignored unless its workspace is trusted.
func (c *Config) ApplyOverlay(overlayPath string) error {
	if !c.IsWorkspaceTrusted(workspaceOf(overlayPath)) {
		return nil
	}

	overlayData, err := os.ReadFile(overlayPath)
	if err != nil {
		return fmt.Errorf("config: read overlay %s: %w", overlayPath, err)
	}

	var overlay struct {
		Providers map[string]ProviderConfig `json:"providers"`
		Modules   map[string]map[string]any `json:"modules"`
	}
	dec := json.NewDecoder(strings.NewReader(string(overlayData)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&overlay); err != nil {
		return fmt.Errorf("config: decode overlay %s: %w", overlayPath, err)
	}

	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	for name, p := range overlay.Providers {
		c.Providers[name] = p
	}
	if c.Modules == nil {
		c.Modules = make(map[string]map[string]any)
	}
	for name, m := range overlay.Modules {
		c.Modules[name] = m
	}

	// Overlay entries arrive raw, so defaults must be reapplied.
	c.applyDefaults()
	return nil
}

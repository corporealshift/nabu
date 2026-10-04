package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/corporealshift/nabu/clients/github"
)

// ClaudeConfig is how the runner asks Claude.
type ClaudeConfig struct {
	Path    string          `json:"path"`
	Model   string          `json:"model"`
	Timeout github.Duration `json:"timeout"`
}

// Config is <root>/runner/config.json. Every field has a default, so the
// file is optional.
type Config struct {
	// Poll and MaxJobs govern the whole runner process, the GitHub watcher's
	// jobs included when it runs alongside.
	Poll    github.Duration `json:"poll"`
	MaxJobs int             `json:"max_jobs"`
	// Label goes on every PR a run opens, so the comments job answers
	// review comments on it.
	Label         string          `json:"label"`
	VerifyTimeout github.Duration `json:"verify_timeout"`
	Claude        ClaudeConfig    `json:"claude"`
	// PlanTurns caps brief, plan, tasks and verify sessions' turns, and
	// WorkTurns caps work, fix and ci-fix sessions'. Zero, the default, is no
	// cap: caps of 50 and 100 failed a run whose sessions were still working.
	PlanTurns int `json:"plan_turns"`
	WorkTurns int `json:"work_turns"`
}

// ConfigPath is where LoadConfig looks.
func ConfigPath(root string) string { return filepath.Join(root, "runner", "config.json") }

// LoadConfig reads the config, or gives the defaults when there is none.
func LoadConfig(root string) (Config, error) {
	var c Config
	b, err := os.ReadFile(ConfigPath(root))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Config{}, fmt.Errorf("runs: %w", err)
	default:
		if err := json.Unmarshal(b, &c); err != nil {
			return Config{}, fmt.Errorf("runs: %s: %w", ConfigPath(root), err)
		}
	}
	c.withDefaults()
	return c, nil
}

func (c *Config) withDefaults() {
	if c.Poll <= 0 {
		c.Poll = github.Duration(2 * time.Minute)
	}
	if c.MaxJobs < 1 {
		c.MaxJobs = 1
	}
	if c.Label == "" {
		c.Label = "nabu"
	}
	if c.VerifyTimeout <= 0 {
		c.VerifyTimeout = github.Duration(30 * time.Minute)
	}
	if c.Claude.Timeout <= 0 {
		c.Claude.Timeout = github.Duration(15 * time.Minute)
	}
	if c.PlanTurns < 0 {
		c.PlanTurns = 0
	}
	if c.WorkTurns < 0 {
		c.WorkTurns = 0
	}
}

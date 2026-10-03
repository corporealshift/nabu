// Package github is the GitHub watcher behind `nabu github`: a client that
// polls GitHub with gh, does its work by starting ordinary daemon sessions in
// git worktrees, and posts the results only after a session has finished
// (docs/specs/2026-09-28-github-review-design.md).
//
// It is a client rather than a module because nothing inside the daemon may
// start a session, and GitHub is an opinion the core should not hold. It
// imports goclient and protocol, never the daemon; boundary_test.go enforces
// that.
package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Repo is one repository the watcher looks after.
type Repo struct {
	// Name is owner/repo, as gh takes it.
	Name string `json:"name"`
	// Clone is an existing checkout. The watcher only fetches into it and adds
	// worktrees beside it; it never checks anything out there.
	Clone string `json:"clone"`
}

// ReviewConfig is the review job's section.
type ReviewConfig struct {
	Enabled *bool `json:"enabled"`
	// Pushes reviews each new head of a PR; false reviews only the first head
	// a PR has.
	Pushes *bool `json:"pushes"`
	// MaxTurns caps a review session's turns: 100 by default. The first
	// default, 30, cut real reviews off before they finished.
	MaxTurns int `json:"max_turns"`
}

// CommentsConfig is the comments job's section.
type CommentsConfig struct {
	Enabled  *bool `json:"enabled"`
	MaxTurns int   `json:"max_turns"`
}

// IssuesConfig is the issues job's section: labeled issues become runs.
type IssuesConfig struct {
	Enabled *bool `json:"enabled"`
}

// Config is <root>/github/config.json.
type Config struct {
	Repos []Repo   `json:"repos"`
	Label string   `json:"label"`
	Poll  Duration `json:"poll"`
	// Quiet is how long a head must stay the head before it is reviewed, so a
	// burst of pushes gets one review. A pointer because 0 is a real setting.
	Quiet    *Duration      `json:"quiet"`
	MaxJobs  int            `json:"max_jobs"`
	Review   ReviewConfig   `json:"review"`
	Comments CommentsConfig `json:"comments"`
	Issues   IssuesConfig   `json:"issues"`
}

// QuietFor is the quiet period.
func (c Config) QuietFor() time.Duration {
	if c.Quiet == nil {
		return 0
	}
	return time.Duration(*c.Quiet)
}

// ReviewEnabled reports whether review jobs run.
func (c Config) ReviewEnabled() bool { return c.Review.Enabled == nil || *c.Review.Enabled }

// CommentsEnabled reports whether comment jobs run.
func (c Config) CommentsEnabled() bool { return c.Comments.Enabled == nil || *c.Comments.Enabled }

// IssuesEnabled reports whether labeled issues become runs.
func (c Config) IssuesEnabled() bool { return c.Issues.Enabled == nil || *c.Issues.Enabled }

// ReviewPushes reports whether each new head gets its own review.
func (c Config) ReviewPushes() bool { return c.Review.Pushes == nil || *c.Review.Pushes }

// Duration is a time.Duration written as "2m" in JSON.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("a duration is a string such as \"2m\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// Dir is where the watcher keeps its config, state and worktrees.
func Dir(root string) string { return filepath.Join(root, "github") }

// ConfigPath is where Load looks.
func ConfigPath(root string) string { return filepath.Join(Dir(root), "config.json") }

const configExample = `{"repos": [{"name": "owner/repo", "clone": "C:/path/to/checkout"}]}`

// Load reads the config and fills the defaults. A missing file is an error
// that says where it goes, because the watcher has nothing to do without
// repos.
func Load(root string) (Config, error) {
	path := ConfigPath(root)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("github: no config at %s; it needs at least %s", path, configExample)
	}
	if err != nil {
		return Config{}, fmt.Errorf("github: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("github: %s: %w", path, err)
	}
	if len(c.Repos) == 0 {
		return Config{}, fmt.Errorf("github: %s lists no repos; it needs at least %s", path, configExample)
	}
	for _, r := range c.Repos {
		owner, name, ok := strings.Cut(r.Name, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			return Config{}, fmt.Errorf("github: repo name %q is not owner/repo", r.Name)
		}
		if strings.TrimSpace(r.Clone) == "" {
			return Config{}, fmt.Errorf("github: repo %s has no clone path", r.Name)
		}
	}
	c.withDefaults()
	return c, nil
}

func (c *Config) withDefaults() {
	if c.Label == "" {
		c.Label = "nabu"
	}
	if c.Poll <= 0 {
		c.Poll = Duration(2 * time.Minute)
	}
	if c.Quiet == nil || *c.Quiet < 0 {
		q := Duration(5 * time.Minute)
		c.Quiet = &q
	}
	if c.MaxJobs < 1 {
		c.MaxJobs = 1
	}
	if c.Review.MaxTurns <= 0 {
		c.Review.MaxTurns = 100
	}
	if c.Comments.MaxTurns <= 0 {
		c.Comments.MaxTurns = 100
	}
}

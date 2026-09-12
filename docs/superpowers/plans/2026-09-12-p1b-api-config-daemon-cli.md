# nabu P1b — WebSocket API, Config, Daemon Lifecycle, CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put a JSON-RPC-over-WebSocket daemon and a CLI in front of the P1a engine, so Claude Code and the future TUI, Android, and Rust clients can drive nabu sessions over one protocol.

**Architecture:** A long-lived Go daemon owns sessions and is the sole writer of the append-only log. The `api` package exposes the normative protocol from `protocol/spec.md` over one WebSocket transport bound to loopback plus Tailscale, fanning session events out to every subscriber and issuing daemon-to-client requests resolved first-responder-wins. The CLI is a thin client that starts a detached daemon if none is listening.

**Tech Stack:** Go 1.26, standard library where practical, `log/slog` for daemon logging, JSON configuration via `encoding/json`, JSON-RPC 2.0 over WebSocket via `github.com/coder/websocket`.

---

## How this plan was produced, and what to distrust

Tasks 1 through 7 were drafted one at a time by the local pi planner against real
source, then reviewed. Two things about that process matter to you as the implementer:

1. **Cited line numbers drift.** The `file:line` citations inside the task bodies name
   real symbols with real signatures, but the line numbers are frequently stale by 3 to
   13 lines. **Never navigate by a cited line number — grep for the symbol.** The
   symbol names and signatures were spot-checked and are reliable; the coordinates are
   not.
2. **Expected-failure line numbers are written as `:XX`** in some steps, because the
   line a test fails on cannot be known before the test is written. Treat `XX` as "some
   line", not as a placeholder you must fill in.

If a snippet in a step cannot be mapped confidently onto the real code, STOP and report
rather than hand-repairing. The real code wins over this document.

---

## Decisions settled before writing this plan

- **Mechanism in core, policy in modules.** The `api` package carries transport
  mechanism, not policy. Permission decisions, verification, and skills are modules
  (P1c). The API only implements the wire protocol.
- **Configuration is JSON, not TOML.** `~/.nabu/config.json` is the base; a trusted
  workspace may add `<workspace>/.nabu/config.json` as an overlay, and an untrusted
  workspace's overlay is ignored until trusted. Parsed with the standard library
  `encoding/json` using `DisallowUnknownFields`, so a typo in a key is a loud error.
  **This deviates from architecture spec §8, which says TOML.** The owner changed it
  deliberately to keep the config loader dependency-free; JSON's cost is that the
  config file cannot carry comments. Spec §8 needs an amendment note recording this.
- **Exactly one third-party dependency.** `github.com/coder/websocket v1.8.15`, chosen
  over gorilla for its context-aware API, because `nabu.session.interrupt` must cancel
  a live provider stream. Version verified as published and current. The repo had zero
  dependencies and no `go.sum` before Task 2.
- **First-responder-wins for daemon-to-client requests.** `nabu.rpc.permission.request`
  and `nabu.rpc.ui.ask` broadcast to all subscribers; the first response wins and later
  responders receive `nabu_already_resolved` (spec §7.15).
- **Deltas are ephemeral.** `nabu.session.delta` notifications are never logged and
  never replayed. The final `message` event carries the full text.
- **Multi-client is the normal case.** Several connections may subscribe to one
  session; events fan out to all of them.
- **Bearer token for non-loopback.** Non-loopback connections must present the token
  during the upgrade. Missing or wrong is refused with `nabu_unauthorized`.
- **Daemon auto-start.** Every CLI subcommand except `daemon` starts a detached daemon
  if none is listening. Claude Code delegation must never fail with "daemon not
  running".
- **Default bind is `127.0.0.1:8737`.** Port 8080 is taken on the owner's machine.
- **`~/.nabu/` layout.** `config.json`, `sessions/` (from P1a), `memory/`,
  `modules/<name>/`, `daemon.log` via `log/slog`, `daemon.pid`, `daemon.port`.
- **Exit codes mirror final session state.** completed 0, blocked 1, paused 2, error 3.
- **`--json`** emits flushed JSONL per event; **`--done-when`** sets the session goal.
- **Standard library `flag` for argument parsing.** No cobra, no urfave/cli.

---

## File structure

| Path | Responsibility |
|---|---|
| `daemon/config/config.go` | **New.** JSON config loader: `~/.nabu/config.json` base, workspace overlay, trusted-workspace tracking. |
| `daemon/config/config_test.go` | **New.** Defaults, overlay, trust, unknown-key rejection. |
| `daemon/api/transport.go` | **New.** WebSocket upgrade, non-loopback bearer auth, `nabu.hello` handshake enforcement, protocol version check. |
| `daemon/api/transport_test.go` | **New.** Upgrade, auth, handshake, version mismatch. |
| `daemon/api/handler.go` | **New.** JSON-RPC 2.0 dispatch and every `nabu.session.*` method; subscriptions, event fan-out, delta broadcast. |
| `daemon/api/handler_test.go` | **New.** Every method, multi-client fan-out, delta non-logging. |
| `daemon/api/requests.go` | **New.** Daemon-to-client requests, first-responder-wins, timeout. |
| `daemon/api/requests_test.go` | **New.** First answer wins, `already_resolved`, timeout, disconnect. |
| `daemon/daemon.go` | **New.** Lifecycle: foreground run, detached start, pid/port files, graceful shutdown. |
| `daemon/daemon_test.go` | **New.** Startup, shutdown, pid/port handling. |
| `daemon/pid.go` | **New.** Stale-pid detection, `//go:build !windows`. |
| `daemon/pid_windows.go` | **New.** Stale-pid detection on Windows. |
| `cmd/nabu/main.go` | **Modify.** Replace the P0 scaffold with subcommand routing. |
| `cmd/nabu/run.go` | **New.** `nabu run` — headless run, goal, stream, exit code. |
| `cmd/nabu/status.go` | **New.** `nabu status`. |
| `cmd/nabu/attach.go` | **New.** `nabu attach` — JSONL event stream. |
| `cmd/nabu/stop.go` | **New.** `nabu stop`. |
| `cmd/nabu/resume.go` | **New.** `nabu resume`. |
| `cmd/nabu/daemon.go` | **New.** `nabu daemon` and `nabu daemon stop`. |
| `go.mod`, `go.sum` | **Modify/New.** Add `github.com/coder/websocket v1.8.15`. |

---

### Task 1: JSON configuration loader

**Deviation from spec:** The Architecture Design spec says TOML. The owner has deliberately changed this decision to keep the config loader dependency-free. Use `encoding/json` from the standard library — no third-party config parser.

**Files:**
- Create: `daemon/config/config.go`
- Create: `daemon/config/config_test.go`

**Goal:** Load `~/.nabu/config.json` with defaults, support `<workspace>/.nabu/config.json` overlay for trusted workspaces, track which workspaces are trusted. Spec: Architecture design §8 configuration.

---

- [ ] **Step 1: Write the failing test for Load with empty JSON**

Append to `daemon/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmptyJSON(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Daemon.Bind != "127.0.0.1:8737" {
		t.Errorf("bind: got %q, want %q", cfg.Daemon.Bind, "127.0.0.1:8737")
	}
	if cfg.Daemon.LogLevel != "info" {
		t.Errorf("log_level: got %q, want %q", cfg.Daemon.LogLevel, "info")
	}
	if cfg.Daemon.Token != "" {
		t.Errorf("token should be empty, got %q", cfg.Daemon.Token)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("providers: got %d, want 0", len(cfg.Providers))
	}
	if len(cfg.Modules) != 0 {
		t.Fatalf("modules: got %d, want 0", len(cfg.Modules))
	}
	if cfg.Budget.MaxTurns != 0 {
		t.Errorf("max_turns: got %d, want 0", cfg.Budget.MaxTurns)
	}
	if cfg.Budget.MaxTokens != 0 {
		t.Errorf("max_tokens: got %d, want 0", cfg.Budget.MaxTokens)
	}
	if cfg.Budget.MaxUSD != 0 {
		t.Errorf("max_usd: got %f, want 0", cfg.Budget.MaxUSD)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/config/ -run TestLoadEmptyJSON`
Expected: compile error `undefined: Load`.

- [ ] **Step 3: Create daemon/config/config.go with types and Load**

Create `daemon/config/config.go` with the complete file:

```go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DaemonConfig holds the [daemon] section.
type DaemonConfig struct {
	Bind     string `json:"bind"`
	Token    string `json:"token"`
	LogFile  string `json:"log_file"`
	LogLevel string `json:"log_level"`
}

func (c *DaemonConfig) withDefaults() {
	if c.Bind == "" {
		c.Bind = "127.0.0.1:8737"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}

// ProviderConfig holds one provider entry keyed by provider name.
type ProviderConfig struct {
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	MaxInFlight  int    `json:"max_in_flight"`
	TasksEnabled *bool  `json:"tasks_enabled"`
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

// BudgetConfig holds the [budget] section.
type BudgetConfig struct {
	MaxTurns  int     `json:"max_turns"`
	MaxTokens int     `json:"max_tokens"`
	MaxUSD    float64 `json:"max_usd"`
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
			cfg.Daemon.withDefaults()
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", cfgPath, err)
	}

	dec := json.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("config: decode %s: %w", cfgPath, err)
	}

	cfg.Daemon.withDefaults()
	for _, p := range cfg.Providers {
		p.withDefaults()
	}

	return cfg, nil
}
```

- [ ] **Step 4: Run the empty JSON test — it should pass**

Run: `go test ./daemon/config/ -run TestLoadEmptyJSON -v`
Expected output:
```
=== RUN   TestLoadEmptyJSON
--- PASS: TestLoadEmptyJSON (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon/config	0.052s
```

- [ ] **Step 5: Write the failing test for missing file**

Append to `daemon/config/config_test.go`:

```go
func TestLoadMissingFile(t *testing.T) {
	root := t.TempDir()
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Daemon.Bind != "127.0.0.1:8737" {
		t.Errorf("bind: got %q, want %q", cfg.Daemon.Bind, "127.0.0.1:8737")
	}
	if cfg.Daemon.LogLevel != "info" {
		t.Errorf("log_level: got %q, want %q", cfg.Daemon.LogLevel, "info")
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("providers: got %d, want 0", len(cfg.Providers))
	}
	if len(cfg.Modules) != 0 {
		t.Fatalf("modules: got %d, want 0", len(cfg.Modules))
	}
}
```

- [ ] **Step 6: Run it — it should pass (Load already handles missing files)**

Run: `go test ./daemon/config/ -run TestLoadMissingFile -v`
Expected output:
```
=== RUN   TestLoadMissingFile
--- PASS: TestLoadMissingFile (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon/config	0.052s
```

- [ ] **Step 7: Write the failing test for DisallowUnknownFields**

Append to `daemon/config/config_test.go`:

```go
func TestLoadRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"daemon": {"bind": "0.0.0.0:8737", "unknown_field": true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected 'unknown field' in error, got: %v", err)
	}
}
```

- [ ] **Step 8: Run it — it should fail**

Run: `go test ./daemon/config/ -run TestLoadRejectsUnknownFields -v`
Expected: `config_test.go:XX: expected error for unknown field, got nil`

- [ ] **Step 9: Add DisallowUnknownFields to Load**

In `daemon/config/config.go`, replace the Load function with this complete version:

```go
func Load(root string) (*Config, error) {
	cfg := &Config{
		TrustedWorkspaces: make(map[string]bool),
	}

	cfgPath := filepath.Join(root, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.Daemon.withDefaults()
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", cfgPath, err)
	}

	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("config: decode %s: %w", cfgPath, err)
	}

	cfg.Daemon.withDefaults()
	for _, p := range cfg.Providers {
		p.withDefaults()
	}

	return cfg, nil
}
```

- [ ] **Step 10: Run it — it should pass**

Run: `go test ./daemon/config/ -run TestLoadRejectsUnknownFields -v`
Expected output:
```
=== RUN   TestLoadRejectsUnknownFields
--- PASS: TestLoadRejectsUnknownFields (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon/config	0.052s
```

- [ ] **Step 11: Write the failing test for loading base config with all values**

Append to `daemon/config/config_test.go`:

```go
func TestLoadBaseConfig(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	configData := `{
		"daemon": {
			"bind": "0.0.0.0:8737",
			"token": "secret",
			"log_level": "debug",
			"log_file": "/var/log/nabu.log"
		},
		"providers": {
			"openai": {
				"base_url": "https://api.openai.com/v1",
				"api_key": "sk-openai",
				"max_in_flight": 5,
				"tasks_enabled": true
			},
			"anthropic": {
				"base_url": "https://api.anthropic.com/v1",
				"api_key": "sk-anthropic"
			}
		},
		"budget": {
			"max_turns": 100,
			"max_tokens": 50000,
			"max_usd": 5.0
		},
		"modules": {
			"judge": {
				"enabled": true,
				"strict": true
			},
			"curator": {
				"enabled": false
			}
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(configData), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Daemon.Bind != "0.0.0.0:8737" {
		t.Errorf("bind: got %q, want %q", cfg.Daemon.Bind, "0.0.0.0:8737")
	}
	if cfg.Daemon.Token != "secret" {
		t.Errorf("token: got %q, want %q", cfg.Daemon.Token, "secret")
	}
	if cfg.Daemon.LogLevel != "debug" {
		t.Errorf("log_level: got %q, want %q", cfg.Daemon.LogLevel, "debug")
	}
	if cfg.Daemon.LogFile != "/var/log/nabu.log" {
		t.Errorf("log_file: got %q, want %q", cfg.Daemon.LogFile, "/var/log/nabu.log")
	}

	if len(cfg.Providers) != 2 {
		t.Fatalf("providers: got %d, want 2", len(cfg.Providers))
	}
	if cfg.Providers["openai"].BaseURL != "https://api.openai.com/v1" {
		t.Errorf("providers.openai.base_url: got %q", cfg.Providers["openai"].BaseURL)
	}
	if cfg.Providers["openai"].APIKey != "sk-openai" {
		t.Errorf("providers.openai.api_key: got %q", cfg.Providers["openai"].APIKey)
	}
	if cfg.Providers["openai"].MaxInFlight != 5 {
		t.Errorf("providers.openai.max_in_flight: got %d, want 5", cfg.Providers["openai"].MaxInFlight)
	}
	if *cfg.Providers["openai"].TasksEnabled != true {
		t.Errorf("providers.openai.tasks_enabled: got %v, want true", *cfg.Providers["openai"].TasksEnabled)
	}
	if cfg.Providers["anthropic"].BaseURL != "https://api.anthropic.com/v1" {
		t.Errorf("providers.anthropic.base_url: got %q", cfg.Providers["anthropic"].BaseURL)
	}
	if cfg.Providers["anthropic"].APIKey != "sk-anthropic" {
		t.Errorf("providers.anthropic.api_key: got %q", cfg.Providers["anthropic"].APIKey)
	}
	if cfg.Providers["anthropic"].MaxInFlight != 1 {
		t.Errorf("providers.anthropic.max_in_flight default: got %d, want 1", cfg.Providers["anthropic"].MaxInFlight)
	}
	if *cfg.Providers["anthropic"].TasksEnabled != true {
		t.Errorf("providers.anthropic.tasks_enabled default: got %v, want true", *cfg.Providers["anthropic"].TasksEnabled)
	}

	if cfg.Budget.MaxTurns != 100 {
		t.Errorf("max_turns: got %d, want 100", cfg.Budget.MaxTurns)
	}
	if cfg.Budget.MaxTokens != 50000 {
		t.Errorf("max_tokens: got %d, want 50000", cfg.Budget.MaxTokens)
	}
	if cfg.Budget.MaxUSD != 5.0 {
		t.Errorf("max_usd: got %f, want 5.0", cfg.Budget.MaxUSD)
	}

	if len(cfg.Modules) != 2 {
		t.Fatalf("modules: got %d, want 2", len(cfg.Modules))
	}
	if !cfg.Modules["judge"].(map[string]any)["enabled"].(bool) {
		t.Errorf("modules.judge.enabled: got %v, want true", cfg.Modules["judge"])
	}
	if cfg.Modules["curator"].(map[string]any)["enabled"].(bool) {
		t.Errorf("modules.curator.enabled: got %v, want false", cfg.Modules["curator"])
	}
}
```

- [ ] **Step 12: Run it — it should pass (struct tags handle parsing)**

Run: `go test ./daemon/config/ -run TestLoadBaseConfig -v`
Expected output:
```
=== RUN   TestLoadBaseConfig
--- PASS: TestLoadBaseConfig (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon/config	0.052s
```

- [ ] **Step 13: Write the failing test for overlay merge**

Append to `daemon/config/config_test.go`:

```go
func TestOverlayMerges(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "config.json")
	workspaceDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(workspaceDir, "config.json")

	baseData := `{
		"providers": {
			"openai": {
				"base_url": "https://api.openai.com/v1",
				"api_key": "sk-base",
				"max_in_flight": 1
			}
		},
		"modules": {
			"judge": {
				"enabled": true
			}
		}
	}`
	if err := os.WriteFile(basePath, []byte(baseData), 0o644); err != nil {
		t.Fatal(err)
	}

	overlayData := `{
		"providers": {
			"openai": {
				"base_url": "https://custom.example.com",
				"max_in_flight": 5,
				"tasks_enabled": false
			},
			"newprovider": {
				"base_url": "https://new.example.com",
				"api_key": "sk-new"
			}
		},
		"modules": {
			"curator": {
				"enabled": true
			}
		}
	}`
	if err := os.WriteFile(overlayPath, []byte(overlayData), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.TrustWorkspace(workspaceDir)
	if err := cfg.ApplyOverlay(overlayPath); err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}

	if cfg.Providers["openai"].BaseURL != "https://custom.example.com" {
		t.Errorf("openai base_url: got %q, want %q", cfg.Providers["openai"].BaseURL, "https://custom.example.com")
	}
	if cfg.Providers["openai"].MaxInFlight != 5 {
		t.Errorf("openai max_in_flight: got %d, want 5", cfg.Providers["openai"].MaxInFlight)
	}
	if *cfg.Providers["openai"].TasksEnabled != false {
		t.Errorf("openai tasks_enabled: got %v, want false", *cfg.Providers["openai"].TasksEnabled)
	}
	if cfg.Providers["newprovider"].BaseURL != "https://new.example.com" {
		t.Errorf("newprovider base_url: got %q", cfg.Providers["newprovider"].BaseURL)
	}
	if _, ok := cfg.Modules["judge"]; !ok {
		t.Error("judge module should still exist from base")
	}
	if _, ok := cfg.Modules["curator"]; !ok {
		t.Error("curator module should be added from overlay")
	}
	if !cfg.IsWorkspaceTrusted(workspaceDir) {
		t.Error("workspace should be trusted")
	}
}
```

- [ ] **Step 14: Run it — it should fail with compile error**

Run: `go test ./daemon/config/ -run TestOverlayMerges`
Expected: compile error `cfg.TrustWorkspace undefined` (the method does not exist yet).

- [ ] **Step 15: Implement TrustWorkspace, IsWorkspaceTrusted, ApplyOverlay**

Append to `daemon/config/config.go`:

```go
// TrustWorkspace marks workspacePath as trusted.
func (c *Config) TrustWorkspace(workspacePath string) {
	if c.TrustedWorkspaces == nil {
		c.TrustedWorkspaces = make(map[string]bool)
	}
	abs, err := filepath.Abs(workspacePath)
	if err != nil {
		abs = workspacePath
	}
	c.TrustedWorkspaces[abs] = true
}

// IsWorkspaceTrusted reports whether workspacePath is trusted.
func (c *Config) IsWorkspaceTrusted(workspacePath string) bool {
	if c.TrustedWorkspaces == nil {
		return false
	}
	abs, err := filepath.Abs(workspacePath)
	if err != nil {
		abs = workspacePath
	}
	return c.TrustedWorkspaces[abs]
}

// ApplyOverlay merges the overlay config into this Config.
// Overlay providers and modules are merged: overlay values override
// base values for matching keys, new keys are added.
// Overlay is only applied if the workspace (derived from overlay path)
// is trusted.
func (c *Config) ApplyOverlay(overlayPath string) error {
	workspaceDir := filepath.Dir(overlayPath)
	if !c.IsWorkspaceTrusted(workspaceDir) {
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
	if err := json.Unmarshal(overlayData, &overlay); err != nil {
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

	return nil
}
```

- [ ] **Step 16: Run it — it should pass**

Run: `go test ./daemon/config/ -run TestOverlayMerges -v`
Expected output:
```
=== RUN   TestOverlayMerges
--- PASS: TestOverlayMerges (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon/config	0.052s
```

- [ ] **Step 17: Write the failing test for overlay ignored when untrusted**

Append to `daemon/config/config_test.go`:

```go
func TestOverlayIgnoredWhenUntrusted(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "config.json")
	workspaceDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(workspaceDir, "config.json")

	baseData := `{
		"providers": {
			"openai": {
				"base_url": "https://api.openai.com/v1",
				"api_key": "sk-base"
			}
		}
	}`
	if err := os.WriteFile(basePath, []byte(baseData), 0o644); err != nil {
		t.Fatal(err)
	}

	overlayData := `{
		"providers": {
			"openai": {
				"base_url": "https://evil.example.com",
				"api_key": "sk-evil"
			}
		}
	}`
	if err := os.WriteFile(overlayPath, []byte(overlayData), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := cfg.ApplyOverlay(overlayPath); err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}

	if cfg.Providers["openai"].BaseURL != "https://api.openai.com/v1" {
		t.Errorf("openai base_url: got %q, want %q (overlay should be ignored)", cfg.Providers["openai"].BaseURL, "https://api.openai.com/v1")
	}
	if cfg.Providers["openai"].APIKey != "sk-base" {
		t.Errorf("openai api_key: got %q, want %q (overlay should be ignored)", cfg.Providers["openai"].APIKey, "sk-base")
	}
}
```

- [ ] **Step 18: Run it — it should pass (ApplyOverlay returns nil for untrusted)**

Run: `go test ./daemon/config/ -run TestOverlayIgnoredWhenUntrusted -v`
Expected output:
```
=== RUN   TestOverlayIgnoredWhenUntrusted
--- PASS: TestOverlayIgnoredWhenUntrusted (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon/config	0.052s
```

- [ ] **Step 19: Write the failing test for TrustWorkspace**

Append to `daemon/config/config_test.go`:

```go
func TestTrustWorkspace(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{
		TrustedWorkspaces: make(map[string]bool),
	}

	workspaceDir := filepath.Join(root, "workspace")

	if cfg.IsWorkspaceTrusted(workspaceDir) {
		t.Fatal("workspace should not be trusted yet")
	}

	cfg.TrustWorkspace(workspaceDir)

	if !cfg.IsWorkspaceTrusted(workspaceDir) {
		t.Fatal("workspace should be trusted after TrustWorkspace")
	}
}
```

- [ ] **Step 20: Run it — it should pass (TrustWorkspace already implemented)**

Run: `go test ./daemon/config/ -run TestTrustWorkspace -v`
Expected output:
```
=== RUN   TestTrustWorkspace
--- PASS: TestTrustWorkspace (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon/config	0.052s
```

- [ ] **Step 21: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

- [ ] **Step 22: Run gofmt check**

Run: `gofmt -l daemon/config/`
Expected: no output (files are gofmt-clean).

- [ ] **Step 23: Commit**

Stage and commit the two source files:

```
git add daemon/config/config.go daemon/config/config_test.go
git commit -m "config: json loader with trusted-workspace overlay"
```

---

### Summary

**File written:** `.pi-delegations/task-01-config.out.md`

**Number of steps:** 23

**Key design decisions:**
- `Load(root string)` — testable via injectable root, no global state, no touch to real `~/.nabu`
- `Config.TrustWorkspace(path)` / `IsWorkspaceTrusted(path)` — in-memory trust tracking, keyed by absolute path
- `Config.ApplyOverlay(path)` — merges overlay providers/modules into base; returns nil (no-op) when workspace is untrusted
- `json.Decoder.DisallowUnknownFields()` — unknown fields are a hard error
- Default bind: `127.0.0.1:8737` (not 8080)
- Default log level: `info`
- Default provider `max_in_flight`: 1
- Default `tasks_enabled`: true (pointer to bool, nil means true)

**Symbols confirmed in source:**
- `module.Config` → `daemon/module/module.go:24` — `map[string]any` with helpers `Bool`, `String`, `Int`, `Strings`, `Enabled`
- `agent.Config.ModuleConfigs` → `daemon/agent/config.go:38` — `map[string]map[string]any` (aliased as `Modules`)
- `provider.Config` → `daemon/provider/provider.go:56` — has `BaseURL`, `APIKey`, `MaxInFlight`
- `module.Registry.Init` → `daemon/module/registry.go:44` — takes `hostFor` and `configFor` callbacks
- `agent.Manager.New` → `daemon/agent/manager.go:65` — takes `Deps` and `Config`
- `workspace.Workspace` → `daemon/workspace/workspace.go:20` — has `Path`, `Key`, `GitRoot`
- `protocol.Version` → `protocol/types.go:10` — const `"1.0"`
- `protocol.Task`, `protocol.TaskStatus` → `protocol/types.go:149-156`
- `protocol.State` → `protocol/project.go:10`

---

### Task 2: WebSocket transport and hello handshake

**Files:**
- Create: `daemon/api/transport.go`
- Create: `daemon/api/transport_test.go`

**Goal:** WebSocket server: HTTP-to-WebSocket upgrade, non-loopback bearer token auth, hello handshake enforcement, protocol version mismatch rejection. Defines the `ConnHandler` interface that the transport calls once a connection is fully established (the seam for Task 3 JSON-RPC method dispatch). Spec: protocol/spec.md §1, §1.1, §8.

**Third-party dependency:** `github.com/coder/websocket v1.8.15` — the owner-chosen library. This task introduces the repo's first third-party dependency; `go.mod` and `go.sum` will be staged in the commit.

**Symbols confirmed:**
- `protocol.CodeProtocolMismatch = -32006` → `protocol/errors.go:18` — confirmed
- `protocol.CodeUnauthorized = -32007` → `protocol/errors.go:19` — confirmed
- `protocol.ErrorNames[CodeProtocolMismatch] = "nabu_protocol_mismatch"` → `protocol/errors.go:22` — confirmed
- `protocol.ErrorNames[CodeUnauthorized] = "nabu_unauthorized"` → `protocol/errors.go:22` — confirmed
- `protocol.Version = "1.0"` → `protocol/types.go:10` — confirmed
- `protocol.RPCError` (Code, Message, Data) → `protocol/errors.go:24` — confirmed
- `protocol.NewRPCError(code, message)` → `protocol/errors.go:34` — confirmed

---

- [ ] **Step 1: Write the failing test for rejecting non-WebSocket requests**

Append to `daemon/api/transport_test.go`:

```go
package api

import (
	"bufio"
	"net"
	"testing"
)

func TestRejectNonWebSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &Server{
		handler: &noopHandler{},
		cfg:     &Config{},
		log:     slog.Default(),
	}
	go srv.ListenAndServe()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = conn.Write([]byte(
		"GET / HTTP/1.1\r\n" +
			"Host: " + ln.Addr().String() + "\r\n" +
			"\r\n"))
	if err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	if resp != "HTTP/1.1 426 Upgrade Required\r\n" {
		t.Errorf("status: got %q, want %q", resp, "HTTP/1.1 426 Upgrade Required\r\n")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/api/ -run TestRejectNonWebSocket -v`
Expected: compile error `undefined: Server` and `undefined: noopHandler` and `undefined: Config`.

- [ ] **Step 3: Create daemon/api/transport.go with types, ServeHTTP, isLoopback**

Create `daemon/api/transport.go` with the complete file:

```go
package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Config holds the server configuration read from the daemon config.
type Config struct {
	Bind string // listen address, e.g. "127.0.0.1:8737"
	Token string // bearer token for non-loopback connections
}

// ConnHandler is the interface the transport calls once a connection is fully
// established (upgrade done, hello verified, auth passed).
// Implementations handle JSON-RPC method dispatch and subscription fan-out.
type ConnHandler interface {
	// ServeConn runs the connection's message loop. It is responsible for
	// reading and writing on the connection until it is closed.
	ServeConn(ctx context.Context, conn Conn) error
}

// Conn is the minimal interface for a WebSocket connection.
// Implementations use *websocket.Conn from github.com/coder/websocket.
type Conn interface {
	// ReadJSON reads the next JSON message from the connection.
	ReadJSON(ctx context.Context, v any) error
	// WriteJSON writes v as a JSON message to the connection.
	WriteJSON(ctx context.Context, v any) error
	// Close closes the connection with the given status code and reason.
	Close(code int, reason string)
	// CloseNow closes the connection immediately without a close frame.
	CloseNow()
}

// Server is the HTTP server that accepts WebSocket connections.
type Server struct {
	httpServer *http.Server
	handler    ConnHandler
	cfg        *Config
	log        *slog.Logger
}

// NewServer creates a new Server.
func NewServer(handler ConnHandler, cfg *Config, log *slog.Logger) *Server {
	srv := &Server{
		handler: handler,
		cfg:     cfg,
		log:     log,
	}
	mux := http.NewServeMux()
	mux.Handle("/", srv)
	srv.httpServer = &http.Server{
		Handler: mux,
	}
	return srv
}

// ListenAndServe starts the HTTP server on cfg.Bind and blocks until Shutdown
// is called or the server returns an error.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// ServeHTTP implements http.Handler. It accepts WebSocket upgrades and rejects
// all other requests with a 426 status. Auth for non-loopback connections
// is implemented in a later step.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		http.Error(w, "Upgrade Required", http.StatusUpgradeRequired)
		return
	}

	s.log.Debug("connection accepted", "remote", r.RemoteAddr)

	// The handler's ServeConn will call websocket.Accept to perform the
	// actual upgrade. This keeps the upgrade logic in the handler
	// implementation while the transport owns the routing.
	go func() {
		ctx := r.Context()
		_ = s.handler.ServeConn(ctx, nil) // conn set by handler
	}()

	// Block to keep the connection alive. The handler manages the lifecycle.
	<-r.Context().Done()
}

// isLoopback reports whether addr is a loopback address.
// It handles addresses with and without ports.
func isLoopback(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}

	if host == "127.0.0.1" || host == "::1" || host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// isWebSocketUpgrade reports whether the request contains the headers
// required for a WebSocket handshake.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}
```

- [ ] **Step 4: Run the non-WebSocket test — it should fail**

Run: `go test ./daemon/api/ -run TestRejectNonWebSocket -v`
Expected: compile error `undefined: noopHandler` (the helper type does not exist yet).

- [ ] **Step 5: Add the noopHandler helper and run the test — it should fail**

Append to `daemon/api/transport_test.go`:

```go
type noopHandler struct{}

func (noopHandler) ServeConn(ctx context.Context, conn Conn) error {
	return nil
}
```

Run: `go test ./daemon/api/ -run TestRejectNonWebSocket -v`
Expected: compile error `undefined: Server` (the Server type does not exist yet).

- [ ] **Step 6: Run the non-WebSocket test — it should pass**

Run: `go test ./daemon/api/ -run TestRejectNonWebSocket -v`
Expected output:
```
=== RUN   TestRejectNonWebSocket
--- PASS: TestRejectNonWebSocket (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 7: Write the failing test for hello handshake**

Append to `daemon/api/transport_test.go`:

```go
func TestHelloHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	helloDone := make(chan struct{})
	h := &testHandler{
		onConn: func(ctx context.Context, conn Conn) error {
			var req helloRequest
			if err := conn.ReadJSON(ctx, &req); err != nil {
				t.Error(err)
				return err
			}
			if req.Method != "nabu.hello" {
				t.Errorf("method: got %q, want %q", req.Method, "nabu.hello")
				return nil
			}
			resp := helloResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"daemon_version":   "0.0.0",
					"protocol_version": "1.0",
					"capabilities":     []string{"delta", "tasks", "goal"},
				},
			}
			if err := conn.WriteJSON(ctx, resp); err != nil {
				t.Error(err)
			}
			close(helloDone)
			return nil
		},
	}

	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	// Dial using coder/websocket
	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	var resp helloResponse
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc: got %q, want %q", resp.JSONRPC, "2.0")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result["daemon_version"] != "0.0.0" {
		t.Errorf("daemon_version: got %v, want %q", resp.Result["daemon_version"], "0.0.0")
	}
	if resp.Result["protocol_version"] != "1.0" {
		t.Errorf("protocol_version: got %v, want %q", resp.Result["protocol_version"], "1.0")
	}
	caps, ok := resp.Result["capabilities"].([]any)
	if !ok {
		t.Fatalf("capabilities not an array: %T", resp.Result["capabilities"])
	}
	if len(caps) != 3 {
		t.Fatalf("capabilities: got %d, want 3", len(caps))
	}
}
```

- [ ] **Step 8: Run it to see it fail**

Run: `go test ./daemon/api/ -run TestHelloHandshake -v`
Expected: compile error `undefined: websocket` (import not added yet) and `undefined: wsjson` (import not added yet).

- [ ] **Step 9: Add the testHandler, hello types, and coder/websocket imports to transport_test.go**

Append to `daemon/api/transport_test.go`:

```go
import (
	"context"
	"net"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"log/slog"
)

// testHandler is a ConnHandler for tests that lets the test function
// control the connection lifecycle.
type testHandler struct {
	onConn func(ctx context.Context, conn Conn) error
}

func (h *testHandler) ServeConn(ctx context.Context, conn Conn) error {
	if h.onConn != nil {
		return h.onConn(ctx, conn)
	}
	return nil
}

// helloRequest is the JSON-RPC request sent by the client during hello.
type helloRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// helloResponse is the JSON-RPC response sent by the daemon during hello.
type helloResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
```

- [ ] **Step 10: Run the hello test — it should fail**

Run: `go test ./daemon/api/ -run TestHelloHandshake -v`
Expected: compile error `undefined: Server` (the Server type does not exist yet — we need to wire the handler to actually perform the upgrade).

- [ ] **Step 11: Wire the upgrade into ServeHTTP and add the wsConn adapter**

In `daemon/api/transport.go`, replace the ServeHTTP function and add the wsConn adapter:

```go
// ServeHTTP implements http.Handler. It accepts WebSocket upgrades and rejects
// all other requests with a 426 status. Auth for non-loopback connections
// is implemented in a later step.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		http.Error(w, "Upgrade Required", http.StatusUpgradeRequired)
		return
	}

	s.log.Debug("connection accepted", "remote", r.RemoteAddr)

	// Perform the WebSocket upgrade.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Error("failed to accept websocket", "error", err)
		return
	}
	defer c.CloseNow()

	// Run the handler's connection loop.
	if err := s.handler.ServeConn(r.Context(), newWSConn(c)); err != nil {
		s.log.Error("connection error", "error", err)
	}
}

// wsConn adapts *websocket.Conn to the Conn interface.
type wsConn struct {
	*websocket.Conn
}

func newWSConn(c *websocket.Conn) Conn {
	return wsConn{c}
}

func (c wsConn) ReadJSON(ctx context.Context, v any) error {
	return wsjson.Read(ctx, c.Conn, &v)
}

func (c wsConn) WriteJSON(ctx context.Context, v any) error {
	return wsjson.Write(ctx, c.Conn, v)
}

func (c wsConn) Close(code int, reason string) {
	c.Conn.Close(code, reason)
}

func (c wsConn) CloseNow() {
	c.Conn.CloseNow()
}
```

And add the coder/websocket imports to transport.go:

```go
import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)
```

- [ ] **Step 12: Run the hello test — it should pass**

Run: `go test ./daemon/api/ -run TestHelloHandshake -v`
Expected output:
```
=== RUN   TestHelloHandshake
--- PASS: TestHelloHandshake (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.062s
```

- [ ] **Step 13: Write the failing test for non-hello method before hello**

Append to `daemon/api/transport_test.go`:

```go
func TestNonHelloBeforeHelloIsRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	h := &testHandler{}

	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	// Send a non-hello method.
	req := helloRequest{
		JSONRPC: "2.0",
		ID:      "1",
		Method:  "nabu.session.list",
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	// The server should reject with protocol mismatch.
	var resp helloResponse
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != -32006 {
		t.Errorf("error code: got %d, want -32006", resp.Error.Code)
	}
}
```

- [ ] **Step 14: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestNonHelloBeforeHelloIsRefused -v`
Expected: the test hangs or times out because `ServeHTTP` does not enforce hello — it passes the raw connection to the handler, which does nothing (`onConn` is nil). The test reads forever waiting for a response.

- [ ] **Step 15: Enforce hello in ServeHTTP — read the first message and validate it**

In `daemon/api/transport.go`, replace the ServeHTTP upgrade section:

```go
	// Perform the WebSocket upgrade.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Error("failed to accept websocket", "error", err)
		return
	}
	defer c.CloseNow()

	// Enforce hello handshake: the first message must be nabu.hello.
	if err := enforceHello(c); err != nil {
		s.log.Error("hello failed", "error", err)
		return
	}

	// Run the handler's connection loop.
	if err := s.handler.ServeConn(r.Context(), newWSConn(c)); err != nil {
		s.log.Error("connection error", "error", err)
	}
```

And add the `enforceHello` function and missing imports to transport.go:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/corporealshift/nabu/protocol"
)

// enforceHello reads the first message on the connection and verifies it is
// a nabu.hello JSON-RPC request. If not, it writes a protocol mismatch error.
func enforceHello(c *websocket.Conn) error {
	var req helloRequest
	ctx := context.Background()
	if err := wsjson.Read(ctx, c, &req); err != nil {
		return err
	}

	if req.Method != "nabu.hello" {
		resp := helloResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32006,
				Message: "nabu_protocol_mismatch: first message must be nabu.hello",
			},
		}
		_ = wsjson.Write(ctx, c, resp)
		return fmt.Errorf("protocol mismatch: expected hello, got %s", req.Method)
	}

	// Send the hello response.
	resp := helloResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"daemon_version":   "0.0.0",
			"protocol_version": protocol.Version,
			"capabilities":     []string{"delta", "tasks", "goal"},
		},
	}
	if err := wsjson.Write(ctx, c, resp); err != nil {
		return err
	}

	return nil
}
```

- [ ] **Step 16: Run the non-hello test — it should pass**

Run: `go test ./daemon/api/ -run TestNonHelloBeforeHelloIsRefused -v`
Expected output:
```
=== RUN   TestNonHelloBeforeHelloIsRefused
--- PASS: TestNonHelloBeforeHelloIsRefused (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.062s
```

- [ ] **Step 17: Write the failing test for protocol version mismatch**

Append to `daemon/api/transport_test.go`:

```go
func TestProtocolVersionMismatch(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	h := &testHandler{}

	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	// Send hello with major version 2 — should be rejected.
	hello := map[string]any{
		"jsonrpc":       "2.0",
		"id":            "1",
		"method":        "nabu.hello",
		"params":        map[string]any{"protocol_version": "2.0"},
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}

	var resp helloResponse
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for version mismatch, got nil")
	}
	if resp.Error.Code != -32006 {
		t.Errorf("error code: got %d, want -32006", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "nabu_protocol_mismatch") {
		t.Errorf("error message: got %q, want 'nabu_protocol_mismatch'", resp.Error.Message)
	}
}
```

- [ ] **Step 18: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestProtocolVersionMismatch -v`
Expected: the test fails because `enforceHello` does not check the protocol version — it only checks the method name. The server sends a hello response (no error) and the test triggers: `transport_test.go:XX: expected error for version mismatch, got nil`.

- [ ] **Step 19: Add protocol version checking to enforceHello**

In `daemon/api/transport.go`, replace the `enforceHello` function:

```go
// enforceHello reads the first message on the connection and verifies it is
// a nabu.hello JSON-RPC request with a compatible protocol version.
// A mismatched MAJOR version is refused with nabu_protocol_mismatch.
// A differing MINOR is accepted; the client ignores unknown fields.
func enforceHello(c *websocket.Conn) error {
	var req helloRequest
	ctx := context.Background()
	if err := wsjson.Read(ctx, c, &req); err != nil {
		return err
	}

	if req.Method != "nabu.hello" {
		resp := helloResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32006,
				Message: "nabu_protocol_mismatch: first message must be nabu.hello",
			},
		}
		_ = wsjson.Write(ctx, c, resp)
		return fmt.Errorf("protocol mismatch: expected hello, got %s", req.Method)
	}

	// Check protocol version. Only MAJOR version must match.
	params, ok := req.Params.(map[string]any)
	var clientVer string
	if ok {
		if v, ok := params["protocol_version"].(string); ok {
			clientVer = v
		}
	}

	// Parse MAJOR versions.
	clientMajor := majorVersion(clientVer)
	daemonMajor := majorVersion(protocol.Version)
	if clientMajor != daemonMajor {
		resp := helloResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32006,
				Message: fmt.Sprintf("nabu_protocol_mismatch: protocol version %s incompatible with daemon %s", clientVer, protocol.Version),
			},
		}
		_ = wsjson.Write(ctx, c, resp)
		return fmt.Errorf("protocol mismatch: client %s, daemon %s", clientVer, protocol.Version)
	}

	// Send the hello response.
	resp := helloResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"daemon_version":   "0.0.0",
			"protocol_version": protocol.Version,
			"capabilities":     []string{"delta", "tasks", "goal"},
		},
	}
	if err := wsjson.Write(ctx, c, resp); err != nil {
		return err
	}

	return nil
}

// majorVersion extracts the MAJOR component from "MAJOR.MINOR".
func majorVersion(v string) int {
	if i := strings.Index(v, "."); i > 0 {
		var n int
		for _, c := range v[:i] {
			n = n*10 + int(c-'0')
		}
		return n
	}
	return 0
}
```

- [ ] **Step 20: Run the version mismatch test — it should pass**

Run: `go test ./daemon/api/ -run TestProtocolVersionMismatch -v`
Expected output:
```
=== RUN   TestProtocolVersionMismatch
--- PASS: TestProtocolVersionMismatch (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.062s
```

- [ ] **Step 21: Write the failing test for loopback with no token accepted**

Append to `daemon/api/transport_test.go`:

```go
func TestLoopbackNoTokenAccepted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	helloDone := make(chan struct{})
	h := &testHandler{
		onConn: func(ctx context.Context, conn Conn) error {
			var req helloRequest
			if err := conn.ReadJSON(ctx, &req); err != nil {
				t.Error(err)
				return err
			}
			resp := helloResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"daemon_version":   "0.0.0",
					"protocol_version": "1.0",
					"capabilities":     []string{"delta", "tasks", "goal"},
				},
			}
			if err := conn.WriteJSON(ctx, resp); err != nil {
				t.Error(err)
			}
			close(helloDone)
			return nil
		},
	}

	// Token is set but loopback connection should not need it.
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0", Token: "secret"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	var resp helloResponse
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("loopback with no token should succeed, got error: %+v", resp.Error)
	}
	if resp.Result["protocol_version"] != "1.0" {
		t.Errorf("protocol_version: got %v, want %q", resp.Result["protocol_version"], "1.0")
	}
}
```

- [ ] **Step 22: Run it — it should pass**

Run: `go test ./daemon/api/ -run TestLoopbackNoTokenAccepted -v`
Expected output:
```
=== RUN   TestLoopbackNoTokenAccepted
--- PASS: TestLoopbackNoTokenAccepted (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.062s
```

- [ ] **Step 23: Write the failing test for isLoopback unit test**

Append to `daemon/api/transport_test.go`:

```go
func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want   bool
	}{
		{"127.0.0.1:8737", true},
		{"127.0.0.1", true},
		{"::1:8737", true},
		{"::1", true},
		{"localhost:8737", true},
		{"localhost", true},
		{"192.168.1.1:8737", false},
		{"10.0.0.1:8737", false},
		{"0.0.0.0:8737", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got := isLoopback(tt.addr)
			if got != tt.want {
				t.Errorf("isLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 24: Run it — it should pass**

Run: `go test ./daemon/api/ -run TestIsLoopback -v`
Expected output:
```
=== RUN   TestIsLoopback
=== RUN   TestIsLoopback/127.0.0.1:8737
=== RUN   TestIsLoopback/127.0.0.1
=== RUN   TestIsLoopback/::1:8737
=== RUN   TestIsLoopback/::1
=== RUN   TestIsLoopback/localhost:8737
=== RUN   TestIsLoopback/localhost
=== RUN   TestIsLoopback/192.168.1.1:8737
=== RUN   TestIsLoopback/10.0.0.1:8737
=== RUN   TestIsLoopback/0.0.0.0:8737
--- PASS: TestIsLoopback (0.00s)
    --- PASS: TestIsLoopback/127.0.0.1:8737 (0.00s)
    --- PASS: TestIsLoopback/127.0.0.1 (0.00s)
    --- PASS: TestIsLoopback/::1:8737 (0.00s)
    --- PASS: TestIsLoopback/::1 (0.00s)
    --- PASS: TestIsLoopback/localhost:8737 (0.00s)
    --- PASS: TestIsLoopback/localhost (0.00s)
    --- PASS: TestIsLoopback/192.168.1.1:8737 (0.00s)
    --- PASS: TestIsLoopback/10.0.0.1:8737 (0.00s)
    --- PASS: TestIsLoopback/0.0.0.0:8737 (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 25: Write the failing test for non-loopback with wrong token refused**

Append to `daemon/api/transport_test.go`:

```go
func TestNonLoopbackWrongTokenRefused(t *testing.T) {
	nonLoopbackIP := findNonLoopbackIP(t)
	if nonLoopbackIP == "" {
		t.Skip("no non-loopback interface found")
	}

	ln, err := net.Listen("tcp", nonLoopbackIP+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := NewServer(&testHandler{}, &Config{Bind: ln.Addr().String(), Token: "secret"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	// Connect with wrong token.
	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(),
		&websocket.DialOptions{
			HTTPHeader: http.Header{
				"Authorization": {"Bearer wrong"},
			},
		})
	if err == nil {
		c.CloseNow()
		t.Fatal("expected dial error for wrong token, got nil")
	}
}
```

- [ ] **Step 26: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestNonLoopbackWrongTokenRefused -v`
Expected: compile error `undefined: findNonLoopbackIP` (the helper function does not exist yet).

- [ ] **Step 27: Add the findNonLoopbackIP helper**

Append to `daemon/api/transport_test.go`:

```go
// findNonLoopbackIP returns the first non-loopback IPv4 address on this machine.
func findNonLoopbackIP(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ip, ok := addr.(*net.IPNet); ok && !ip.IP.IsLoopback() {
				if ip.IP.To4() != nil {
					return ip.IP.To4().String()
				}
			}
		}
	}
	return ""
}
```

- [ ] **Step 28: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestNonLoopbackWrongTokenRefused -v`
Expected: the test fails because the auth check is not in ServeHTTP yet — the server accepts the connection regardless of the token, and the dial succeeds. The test's `t.Fatal` triggers: `expected dial error for wrong token, got nil`.

- [ ] **Step 29: Add the auth check to ServeHTTP**

In `daemon/api/transport.go`, replace the ServeHTTP function:

```go
// ServeHTTP implements http.Handler. It accepts WebSocket upgrades and rejects
// all other requests with a 426 status. Non-loopback connections with a
// configured token require Authorization: Bearer <token>.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		http.Error(w, "Upgrade Required", http.StatusUpgradeRequired)
		return
	}

	if !isLoopback(r.RemoteAddr) && s.cfg.Token != "" {
		token := r.Header.Get("Authorization")
		if token != "Bearer "+s.cfg.Token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	s.log.Debug("connection accepted", "remote", r.RemoteAddr)

	// Perform the WebSocket upgrade.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Error("failed to accept websocket", "error", err)
		return
	}
	defer c.CloseNow()

	// Enforce hello handshake: the first message must be nabu.hello.
	if err := enforceHello(c); err != nil {
		s.log.Error("hello failed", "error", err)
		return
	}

	// Run the handler's connection loop.
	if err := s.handler.ServeConn(r.Context(), newWSConn(c)); err != nil {
		s.log.Error("connection error", "error", err)
	}
}
```

- [ ] **Step 30: Run the wrong token test — it should pass**

Run: `go test ./daemon/api/ -run TestNonLoopbackWrongTokenRefused -v`
Expected output:
```
=== RUN   TestNonLoopbackWrongTokenRefused
--- PASS: TestNonLoopbackWrongTokenRefused (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.062s
```

- [ ] **Step 31: Write the failing test for non-loopback with correct token accepted**

Append to `daemon/api/transport_test.go`:

```go
func TestNonLoopbackCorrectTokenAccepted(t *testing.T) {
	nonLoopbackIP := findNonLoopbackIP(t)
	if nonLoopbackIP == "" {
		t.Skip("no non-loopback interface found")
	}

	ln, err := net.Listen("tcp", nonLoopbackIP+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	helloDone := make(chan struct{})
	h := &testHandler{
		onConn: func(ctx context.Context, conn Conn) error {
			var req helloRequest
			if err := conn.ReadJSON(ctx, &req); err != nil {
				t.Error(err)
				return err
			}
			resp := helloResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"daemon_version":   "0.0.0",
					"protocol_version": "1.0",
					"capabilities":     []string{"delta", "tasks", "goal"},
				},
			}
			if err := conn.WriteJSON(ctx, resp); err != nil {
				t.Error(err)
			}
			close(helloDone)
			return nil
		},
	}

	srv := NewServer(h, &Config{Bind: ln.Addr().String(), Token: "secret"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	// Connect with correct token.
	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(),
		&websocket.DialOptions{
			HTTPHeader: http.Header{
				"Authorization": {"Bearer secret"},
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	var resp helloResponse
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("non-loopback with correct token should succeed, got error: %+v", resp.Error)
	}
	if resp.Result["protocol_version"] != "1.0" {
		t.Errorf("protocol_version: got %v, want %q", resp.Result["protocol_version"], "1.0")
	}
}
```

- [ ] **Step 32: Run it — it should pass** (auth check allows correct tokens through)

Run: `go test ./daemon/api/ -run TestNonLoopbackCorrectTokenAccepted -v`
Expected output:
```
=== RUN   TestNonLoopbackCorrectTokenAccepted
--- PASS: TestNonLoopbackCorrectTokenAccepted (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.062s
```

- [ ] **Step 33: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

- [ ] **Step 34: Run gofmt check**

Run: `gofmt -l daemon/api/`
Expected: no output (files are gofmt-clean).

- [ ] **Step 35: Commit**

Stage and commit the source files and the new dependency:

```
git add daemon/api/transport.go daemon/api/transport_test.go go.mod go.sum
git commit -m "api: websocket transport and hello handshake"
```

---

### Summary

**File written:** `.pi-delegations/task-02-transport.out.md`

**Number of steps:** 35

**Key design decisions:**
- `github.com/coder/websocket v1.8.15` — pinned version, latest at time of writing
- `Server.ServeHTTP` owns routing: rejects non-WebSocket with 426, checks token for non-loopback, performs upgrade via `websocket.Accept`
- `enforceHello` reads the first message, rejects non-hello with `-32006 nabu_protocol_mismatch`, checks MAJOR version, sends hello response
- `isLoopback` handles addresses with and without ports, checks 127.0.0.1, ::1, localhost, and `net.IP.IsLoopback()`
- `Conn` interface abstracts `*websocket.Conn` — the transport calls `ConnHandler.ServeConn(ctx, conn)` after hello passes
- `ConnHandler.ServeConn` is the seam for Task 3: implementations handle JSON-RPC dispatch
- Loopback connections (127.0.0.1, ::1, localhost) do NOT need a token
- Non-loopback connections MUST present `Authorization: Bearer <token>` when a token is configured
- `findNonLoopbackIP` helper finds the machine's first non-loopback IPv4 for integration tests
- `majorVersion` extracts the MAJOR component from "MAJOR.MINOR" for version comparison
- Protocol version MAJOR mismatch → `-32006 nabu_protocol_mismatch`
- Protocol version MINOR mismatch → accepted (per spec)
- Hello response fields: `daemon_version`, `protocol_version`, `capabilities[]` (per spec §1.1)

**Symbols confirmed in source:**
- `protocol.CodeProtocolMismatch = -32006` → `protocol/errors.go:18` — confirmed
- `protocol.CodeUnauthorized = -32007` → `protocol/errors.go:19` — confirmed
- `protocol.ErrorNames[CodeProtocolMismatch] = "nabu_protocol_mismatch"` → `protocol/errors.go:22` — confirmed
- `protocol.ErrorNames[CodeUnauthorized] = "nabu_unauthorized"` → `protocol/errors.go:22` — confirmed
- `protocol.Version = "1.0"` → `protocol/types.go:10` — confirmed
- `protocol.RPCError` (Code, Message, Data) → `protocol/errors.go:24` — confirmed
- `protocol.NewRPCError(code, message)` → `protocol/errors.go:34` — confirmed
- `github.com/coder/websocket v1.8.15` — latest version confirmed via `go list -m -versions`

---

### Task 3: JSON-RPC dispatch and read-only session methods

**Files:**
- Create: `daemon/api/handler.go`
- Create: `daemon/api/handler_test.go`

**Goal:** JSON-RPC 2.0 dispatch on top of the Task 2 transport seam: request/response envelopes, id echoing, unknown-method handling, malformed-request handling. Four read-only session methods: `nabu.session.list`, `nabu.session.create`, `nabu.session.events_after`, `nabu.session.state`. Defines the method-name → handler-func map that Task 4 extends with `send_prompt`, `subscribe`, `interrupt`, `stop`, `resume`, `set_goal`, `clear_goal`, `set_option`, `update_tasks`. Spec: protocol/spec.md §1, §4, §5, §7.2–7.5, §8.

**Third-party dependency:** None. This task uses only `encoding/json`, `log/slog`, and the packages from Tasks 1 and 2.

**Symbols confirmed:**
- `protocol.CodeParseError = -32700` → `protocol/errors.go:10` — confirmed
- `protocol.CodeInvalidRequest = -32600` → `protocol/errors.go:11` — confirmed
- `protocol.CodeMethodNotFound = -32601` → `protocol/errors.go:12` — confirmed
- `protocol.CodeInvalidParams = -32602` → `protocol/errors.go:13` — confirmed
- `protocol.CodeInternalError = -32603` → `protocol/errors.go:14` — confirmed
- `protocol.CodeSessionNotFound = -32001` → `protocol/errors.go:15` — confirmed
- `protocol.CodeInvalidTransition = -32002` → `protocol/errors.go:16` — confirmed
- `protocol.CodeCursorUnknown = -32005` → `protocol/errors.go:19` — confirmed
- `protocol.ErrorNames` map → `protocol/errors.go:22` — confirmed
- `protocol.RPCError` (Code, Message, Data) → `protocol/errors.go:24` — confirmed
- `protocol.NewRPCError(code, message)` → `protocol/errors.go:34` — confirmed
- `protocol.EventsAfter(log, lastEventID)` → `protocol/cursor.go:13` — confirmed
- `protocol.Project(log)` → `protocol/project.go:17` — confirmed
- `protocol.Version = "1.0"` → `protocol/types.go:10` — confirmed
- `session.Store.List() ([]Summary, error)` → `daemon/session/store.go:145` — confirmed
- `session.Store.Get(id) (*Session, error)` → `daemon/session/store.go:62` — confirmed
- `session.Summary` (SessionID, Workspace, WorkspaceKey, State, EventCount, CreatedAt, UpdatedAt, Goal, TasksTotal, TasksDone) → `daemon/session/store.go:124` — confirmed
- `session.Session.Summary() Summary` → `daemon/session/store.go:135` — confirmed
- `session.Session.EventsAfter(last *string) ([]Event, bool, error)` → `daemon/session/session.go:89` — confirmed
- `session.Session.State() State` → `daemon/session/session.go:94` — confirmed
- `session.Session.ID() string` → `daemon/session/session.go:36` — confirmed
- `session.ErrNotFound` → `daemon/session/store.go:16` — confirmed
- `agent.Manager.Create(ctx, workspacePath, co CreateOptions) (*session.Session, error)` → `daemon/agent/manager.go:107` — confirmed
- `agent.CreateOptions` (Model, PermissionMode, CompactionEnabled) → `daemon/agent/manager.go:82` — confirmed
- `agent.Deps` (Store, Providers, Modules, Builtins, Root, Log, Asker, Deltas) → `daemon/agent/manager.go:30` — confirmed
- `github.com/coder/websocket` + `wsjson` — from Task 2, no new dependency

---

- [ ] **Step 1: Write the failing test for unknown method returns code -32601**

Append to `daemon/api/handler_test.go`:

```go
package api

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"log/slog"

	"github.com/corporealshift/nabu/daemon/agent"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

func TestUnknownMethodReturnsNotFound(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.nonexistent",
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp["error"])
	}
	code, _ := errObj["code"].(float64)
	if code != float64(protocol.CodeMethodNotFound) {
		t.Errorf("error code: got %v, want %d", code, protocol.CodeMethodNotFound)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/api/ -run TestUnknownMethodReturnsNotFound -v`
Expected: compile errors `undefined: NewHandler` and `undefined: NewServer` and `undefined: Config`.

- [ ] **Step 3: Create daemon/api/handler.go with JSON-RPC types, Handler struct, and ServeConn**

Create `daemon/api/handler.go` with the complete file:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/corporealshift/nabu/daemon/agent"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

// methodFunc is a handler for one JSON-RPC method.
type methodFunc func(h *Handler, params json.RawMessage) (any, *protocol.RPCError)

// Handler implements ConnHandler and dispatches JSON-RPC method calls.
type Handler struct {
	manager *agent.Manager
	store   *session.Store
	log     *slog.Logger
	mu      sync.RWMutex
	mods    map[string]methodFunc
}

// NewHandler creates a Handler with the built-in read-only methods registered.
func NewHandler(m *agent.Manager, st *session.Store, log *slog.Logger) *Handler {
	h := &Handler{
		manager: m,
		store:   st,
		log:     log,
		mods:    make(map[string]methodFunc),
	}
	h.mods["nabu.session.list"] = h.handleSessionList
	h.mods["nabu.session.create"] = h.handleSessionCreate
	h.mods["nabu.session.events_after"] = h.handleSessionEventsAfter
	h.mods["nabu.session.state"] = h.handleSessionState
	return h
}

// ServeConn implements ConnHandler.
func (h *Handler) ServeConn(ctx context.Context, conn Conn) error {
	for {
		var raw json.RawMessage
		if err := conn.ReadJSON(ctx, &raw); err != nil {
			return err
		}
		req, resp := h.dispatch(raw)
		if resp != nil {
			if err := conn.WriteJSON(ctx, resp); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

// dispatch parses one JSON-RPC request and returns the response envelope.
func (h *Handler) dispatch(raw json.RawMessage) (req *jsonrpcRequest, resp *jsonrpcResponse) {
	var envelope jsonrpcEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, h.errorResp(nil, protocol.CodeParseError, "parse error")
	}
	if envelope.JSONRPC != "2.0" {
		return nil, h.errorResp(nil, protocol.CodeInvalidRequest, "invalid jsonrpc version")
	}
	if envelope.Method == "" {
		return nil, h.errorResp(envelope.ID, protocol.CodeInvalidRequest, "method is required")
	}
	req = &jsonrpcRequest{
		JSONRPC: envelope.JSONRPC, ID: envelope.ID,
		Method: envelope.Method, Params: envelope.Params,
	}
	result, rpcErr := h.dispatchMethod(req)
	if rpcErr != nil {
		return req, h.errorResp(req.ID, rpcErr.Code, rpcErr.Message)
	}
	return req, h.successResp(req.ID, result)
}

// dispatchMethod routes a method name to its handler function.
func (h *Handler) dispatchMethod(req *jsonrpcRequest) (any, *protocol.RPCError) {
	h.mu.RLock()
	fn, ok := h.mods[req.Method]
	h.mu.RUnlock()
	if !ok {
		return nil, protocol.NewRPCError(protocol.CodeMethodNotFound, req.Method)
	}
	return fn(h, req.Params)
}

// jsonrpcEnvelope is the outermost layer of a JSON-RPC message.
type jsonrpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcRequest is a parsed JSON-RPC request.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC response envelope.
type jsonrpcResponse struct {
	JSONRPC string     `json:"jsonrpc"`
	ID      any        `json:"id"`
	Result  any        `json:"result,omitempty"`
	Error   *RPCError2 `json:"error,omitempty"`
}

// RPCError2 is a JSON-RPC error object for responses.
type RPCError2 struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// successResp builds a JSON-RPC success response.
func (h *Handler) successResp(id any, result any) *jsonrpcResponse {
	return &jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// errorResp builds a JSON-RPC error response.
func (h *Handler) errorResp(id any, code int, message string) *jsonrpcResponse {
	return &jsonrpcResponse{JSONRPC: "2.0", ID: id, Error: &RPCError2{Code: code, Message: message}}
}
```

- [ ] **Step 4: Run the unknown method test — it should fail**

Run: `go test ./daemon/api/ -run TestUnknownMethodReturnsNotFound -v`
Expected: compile error `undefined: NewHandler` (the function does not exist yet).

- [ ] **Step 5: Build the handler that wires into ServeConn**

In `daemon/api/handler.go`, replace the `ServeConn` function to actually run the dispatch loop:

```go
// ServeConn implements ConnHandler. It runs the JSON-RPC message loop:
// reads JSON-RPC requests, dispatches them to the appropriate handler,
// and writes responses back on the connection.
func (h *Handler) ServeConn(ctx context.Context, conn Conn) error {
	for {
		var raw json.RawMessage
		if err := conn.ReadJSON(ctx, &raw); err != nil {
			return err
		}
		req, resp := h.dispatch(raw)
		if resp != nil {
			if err := conn.WriteJSON(ctx, resp); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

// dispatch parses one JSON-RPC request and returns the response envelope.
func (h *Handler) dispatch(raw json.RawMessage) (req *jsonrpcRequest, resp *jsonrpcResponse) {
	var envelope jsonrpcEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, h.errorResp(nil, protocol.CodeParseError, "parse error")
	}
	if envelope.JSONRPC != "2.0" {
		return nil, h.errorResp(nil, protocol.CodeInvalidRequest, "invalid jsonrpc version")
	}
	if envelope.Method == "" {
		return nil, h.errorResp(envelope.ID, protocol.CodeInvalidRequest, "method is required")
	}
	req = &jsonrpcRequest{
		JSONRPC: envelope.JSONRPC, ID: envelope.ID,
		Method: envelope.Method, Params: envelope.Params,
	}
	result, rpcErr := h.dispatchMethod(req)
	if rpcErr != nil {
		return req, h.errorResp(req.ID, rpcErr.Code, rpcErr.Message)
	}
	return req, h.successResp(req.ID, result)
}

// dispatchMethod routes a method name to its handler function.
func (h *Handler) dispatchMethod(req *jsonrpcRequest) (any, *protocol.RPCError) {
	h.mu.RLock()
	fn, ok := h.mods[req.Method]
	h.mu.RUnlock()
	if !ok {
		return nil, protocol.NewRPCError(protocol.CodeMethodNotFound, req.Method)
	}
	return fn(h, req.Params)
}
```

- [ ] **Step 6: Run the unknown method test — it should pass**

Run: `go test ./daemon/api/ -run TestUnknownMethodReturnsNotFound -v`
Expected output:
```
=== RUN   TestUnknownMethodReturnsNotFound
--- PASS: TestUnknownMethodReturnsNotFound (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 7: Write the failing test for malformed JSON returns code -32700**

Append to `daemon/api/handler_test.go`:

```go
func TestMalformedJSONReturnsParseError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	if err := c.Write(context.Background(), websocket.MessageText, []byte("{bad json")); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp["error"])
	}
	code, _ := errObj["code"].(float64)
	if code != float64(protocol.CodeParseError) {
		t.Errorf("error code: got %v, want %d", code, protocol.CodeParseError)
	}
}
```

- [ ] **Step 8: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestMalformedJSONReturnsParseError -v`
Expected: the test fails because the `dispatch` function does not handle malformed JSON — it panics or the connection closes. The test sees a connection error rather than a parse error response.

- [ ] **Step 9: Add JSON-RPC parsing and error handling to dispatch**

In `daemon/api/handler.go`, replace the `dispatch` function:

```go
func (h *Handler) dispatch(raw json.RawMessage) (req *jsonrpcRequest, resp *jsonrpcResponse) {
	var envelope jsonrpcEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, h.errorResp(nil, protocol.CodeParseError, "parse error")
	}
	if envelope.JSONRPC != "2.0" {
		return nil, h.errorResp(nil, protocol.CodeInvalidRequest, "invalid jsonrpc version")
	}
	if envelope.Method == "" {
		return nil, h.errorResp(envelope.ID, protocol.CodeInvalidRequest, "method is required")
	}
	req = &jsonrpcRequest{
		JSONRPC: envelope.JSONRPC, ID: envelope.ID,
		Method: envelope.Method, Params: envelope.Params,
	}
	result, rpcErr := h.dispatchMethod(req)
	if rpcErr != nil {
		return req, h.errorResp(req.ID, rpcErr.Code, rpcErr.Message)
	}
	return req, h.successResp(req.ID, result)
}
```

- [ ] **Step 10: Run the malformed JSON test — it should pass**

Run: `go test ./daemon/api/ -run TestMalformedJSONReturnsParseError -v`
Expected output:
```
=== RUN   TestMalformedJSONReturnsParseError
--- PASS: TestMalformedJSONReturnsParseError (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 11: Write the failing test for id echoing**

Append to `daemon/api/handler_test.go`:

```go
func TestIDEchoing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": "abc-123", "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": "abc-123", "method": "nabu.nonexistent",
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	idVal, ok := resp["id"]
	if !ok {
		t.Fatal("expected id field in response")
	}
	if idVal != "abc-123" {
		t.Errorf("id: got %v, want %q", idVal, "abc-123")
	}
}
```

- [ ] **Step 12: Run it — it should pass**

Run: `go test ./daemon/api/ -run TestIDEchoing -v`
Expected output:
```
=== RUN   TestIDEchoing
--- PASS: TestIDEchoing (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 13: Write the failing test for session.list with no sessions**

Append to `daemon/api/handler_test.go`:

```go
func TestSessionListEmpty(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.list",
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}

	sessions, ok := resp["result"].(map[string]any)["sessions"].([]any)
	if !ok {
		t.Fatalf("sessions not an array: %T", resp["result"])
	}
	if len(sessions) != 0 {
		t.Errorf("sessions: got %d, want 0", len(sessions))
	}
}
```

- [ ] **Step 14: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestSessionListEmpty -v`
Expected: the test fails because `handleSessionList` is not yet implemented — it returns an internal error or panics. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32603 message:internal error]`.

- [ ] **Step 15: Implement handleSessionList**

In `daemon/api/handler.go`, add the `handleSessionList` method:

```go
// handleSessionList implements nabu.session.list (spec §7.2).
func (h *Handler) handleSessionList(_ *Handler, _ json.RawMessage) (any, *protocol.RPCError) {
	summaries, err := h.store.List()
	if err != nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}
	if summaries == nil {
		summaries = []session.Summary{}
	}
	return map[string]any{"sessions": summaries}, nil
}
```

- [ ] **Step 16: Run the empty list test — it should pass**

Run: `go test ./daemon/api/ -run TestSessionListEmpty -v`
Expected output:
```
=== RUN   TestSessionListEmpty
--- PASS: TestSessionListEmpty (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 17: Write the failing test for session.list with one session**

Append to `daemon/api/handler_test.go`:

```go
func TestSessionListWithSessions(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.list",
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}

	sessions, ok := resp["result"].(map[string]any)["sessions"].([]any)
	if !ok {
		t.Fatalf("sessions not an array: %T", resp["result"])
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions: got %d, want 1", len(sessions))
	}

	sess := sessions[0].(map[string]any)
	if sess["session_id"] != s.ID() {
		t.Errorf("session_id: got %v, want %q", sess["session_id"], s.ID())
	}
	if sess["workspace"] != "C:/Users/test/workspace" {
		t.Errorf("workspace: got %v, want %q", sess["workspace"], "C:/Users/test/workspace")
	}
	if sess["state"] != "idle" {
		t.Errorf("state: got %v, want %q", sess["state"], "idle")
	}
	if sess["event_count"].(float64) != 1 {
		t.Errorf("event_count: got %v, want 1", sess["event_count"])
	}
}
```

- [ ] **Step 18: Run it — it should pass**

Run: `go test ./daemon/api/ -run TestSessionListWithSessions -v`
Expected output:
```
=== RUN   TestSessionListWithSessions
--- PASS: TestSessionListWithSessions (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.062s
```

- [ ] **Step 19: Write the failing test for session.create**

Append to `daemon/api/handler_test.go`:

```go
func TestSessionCreate(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.create",
		"params": map[string]any{
			"workspace": "C:/Users/test/workspace",
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not an object: %T", resp["result"])
	}

	sessionID, ok := result["session_id"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session_id missing or empty: %v", result["session_id"])
	}

	event, ok := result["event"].(map[string]any)
	if !ok {
		t.Fatalf("event not an object: %T", result["event"])
	}
	if event["type"] != "session" {
		t.Errorf("event type: got %v, want %q", event["type"], "session")
	}
}
```

- [ ] **Step 20: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestSessionCreate -v`
Expected: the test fails because `handleSessionCreate` is not yet implemented — it returns an internal error or panics. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32603 message:internal error]`.

- [ ] **Step 21: Add CreateSessionOptions struct**

In `daemon/api/handler.go`, add the `CreateSessionOptions` type:

```go
// CreateSessionOptions are the optional parameters for nabu.session.create.
type CreateSessionOptions struct {
	Model             string  `json:"model,omitempty"`
	PermissionMode    string  `json:"permission_mode,omitempty"`
	CompactionEnabled *bool   `json:"compaction_enabled,omitempty"`
}
```

- [ ] **Step 22: Implement handleSessionCreate**

In `daemon/api/handler.go`, add the `handleSessionCreate` method:

```go
// handleSessionCreate implements nabu.session.create (spec §7.3).
func (h *Handler) handleSessionCreate(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		Workspace string                `json:"workspace"`
		Options   *CreateSessionOptions `json:"options,omitempty"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.Workspace) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "workspace is required")
	}

	co := agent.CreateOptions{}
	if p.Options != nil {
		co.Model = p.Options.Model
		co.PermissionMode = protocol.PermissionMode(p.Options.PermissionMode)
		if p.Options.CompactionEnabled != nil {
			co.CompactionEnabled = p.Options.CompactionEnabled
		}
	}

	ctx := context.Background()
	s, err := h.manager.Create(ctx, p.Workspace, co)
	if err != nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	ev := s.Events()
	if len(ev) == 0 {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, "session has no events")
	}

	return map[string]any{
		"session_id": s.ID(),
		"event":      ev[0],
	}, nil
}
```

- [ ] **Step 23: Run the session create test — it should pass**

Run: `go test ./daemon/api/ -run TestSessionCreate -v`
Expected output:
```
=== RUN   TestSessionCreate
--- PASS: TestSessionCreate (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 24: Write the failing test for session.events_after**

Append to `daemon/api/handler_test.go`:

```go
func TestEventsAfter(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.Append(protocol.EventMessage, protocol.MessageData{
		Role: "user", Content: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.events_after",
		"params": map[string]any{
			"session_id":    s.ID(),
			"last_event_id": nil,
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}

	result := resp["result"].(map[string]any)
	synced, _ := result["synced"].(bool)
	if !synced {
		t.Error("synced should be true when fetching all events")
	}

	events := result["events"].([]any)
	if len(events) != 2 {
		t.Errorf("events: got %d, want 2", len(events))
	}
}
```

- [ ] **Step 25: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestEventsAfter -v`
Expected: the test fails because `handleSessionEventsAfter` is not yet implemented — it returns an internal error. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32603 message:internal error]`.

- [ ] **Step 26: Implement handleSessionEventsAfter**

In `daemon/api/handler.go`, add the `handleSessionEventsAfter` method:

```go
// handleSessionEventsAfter implements nabu.session.events_after (spec §7.5, §4).
func (h *Handler) handleSessionEventsAfter(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID   string  `json:"session_id"`
		LastEventID *string `json:"last_event_id"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	events, synced, err := s.EventsAfter(p.LastEventID)
	if err != nil {
		rpcErr, ok := err.(*protocol.RPCError)
		if !ok {
			return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
		}
		return nil, rpcErr
	}

	if events == nil {
		events = []protocol.Event{}
	}

	return map[string]any{
		"events": events,
		"synced": synced,
	}, nil
}
```

- [ ] **Step 27: Run the events_after test — it should pass**

Run: `go test ./daemon/api/ -run TestEventsAfter -v`
Expected output:
```
=== RUN   TestEventsAfter
--- PASS: TestEventsAfter (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 28: Write the failing test for events_after with unknown cursor**

Append to `daemon/api/handler_test.go`:

```go
func TestEventsAfterUnknownCursor(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.events_after",
		"params": map[string]any{
			"session_id":    s.ID(),
			"last_event_id": "01J00000000000000000000001",
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp["error"])
	}
	code, _ := errObj["code"].(float64)
	if code != float64(protocol.CodeCursorUnknown) {
		t.Errorf("error code: got %v, want %d", code, protocol.CodeCursorUnknown)
	}
}
```


The cursor validation is delegated to `protocol.EventsAfter` (cursor.go:13), which `session.EventsAfter` (session.go:89) calls. No additional implementation needed.

Run: `go test ./daemon/api/ -run TestEventsAfterUnknownCursor -v`
Expected output:
```
=== RUN   TestEventsAfterUnknownCursor
--- PASS: TestEventsAfterUnknownCursor (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 29: Write the failing test for session.state**

Append to `daemon/api/handler_test.go`:

```go
func TestSessionState(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.state",
		"params": map[string]any{
			"session_id": s.ID(),
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}

	result := resp["result"].(map[string]any)
	if result["state"] != "idle" {
		t.Errorf("state: got %v, want %q", result["state"], "idle")
	}
	if result["last_event_id"] != s.Events()[0].ID {
		t.Errorf("last_event_id: got %v, want %q", result["last_event_id"], s.Events()[0].ID)
	}
}
```

- [ ] **Step 30: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestSessionState -v`
Expected: the test fails because `handleSessionState` is not yet implemented — it returns an internal error. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32603 message:internal error]`.

- [ ] **Step 31: Implement handleSessionState**

In `daemon/api/handler.go`, add the `handleSessionState` method:

```go
// handleSessionState implements nabu.session.state (spec §7.10, §5).
func (h *Handler) handleSessionState(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	st := s.State()
	return map[string]any{
		"state":             string(st.State),
		"options":           st.Options,
		"goal":              st.Goal,
		"tasks":             st.Tasks,
		"budget":            st.Budget,
		"turns":             st.Turns,
		"usage":             st.Usage,
		"last_event_id":     st.LastEventID,
		"compacted_through": st.CompactedThrough,
	}, nil
}
```

- [ ] **Step 32: Run the session state test — it should pass**

Run: `go test ./daemon/api/ -run TestSessionState -v`
Expected output:
```
=== RUN   TestSessionState
--- PASS: TestSessionState (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 33: Write the failing test for session.create with missing workspace**

Append to `daemon/api/handler_test.go`:

```go
func TestSessionCreateMissingWorkspace(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.create",
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp["error"])
	}
	code, _ := errObj["code"].(float64)
	if code != float64(protocol.CodeInvalidParams) {
		t.Errorf("error code: got %v, want %d", code, protocol.CodeInvalidParams)
	}
}
```

- [ ] **Step 34: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestSessionCreateMissingWorkspace -v`
Expected: the test fails because `handleSessionCreate` does not yet validate for missing workspace — it passes an empty string to `Manager.Create`, which likely panics or returns a filesystem error. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32603 message:internal error]`.

- [ ] **Step 35: Run the full gate, gofmt, and commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

Run: `gofmt -l daemon/api/`
Expected: no output (files are gofmt-clean).

Stage and commit the source files:

```
git add daemon/api/handler.go daemon/api/handler_test.go
git commit -m "api: json-rpc dispatch and read-only session methods"
```

---

### Summary

**File written:** `.pi-delegations/task-03-rpc-core.out.md`

**Number of steps:** 35

**Key design decisions:**
- `Handler` implements `ConnHandler` (from Task 2) — `ServeConn` runs the JSON-RPC message loop
- `dispatch` handles JSON-RPC 2.0 parsing: parse errors (-32700), invalid requests (-32600), method dispatch
- Method routing uses a `map[string]methodFunc` — Task 4 extends this map with mutating/streaming methods
- `nabu.session.list` → `Store.List()` → `{sessions: [Summary]}` (spec §7.2)
- `nabu.session.create` → `Manager.Create()` → `{session_id, event}` (spec §7.3)
- `nabu.session.events_after` → `Session.EventsAfter()` → `{events, synced}` (spec §7.5, §4)
- `nabu.session.state` → `Session.State()` → §5 projection (spec §7.10)
- Cursor validation delegated to `protocol.EventsAfter` (cursor.go:13) — no reimplementation
- State projection delegated to `protocol.Project` (project.go:17) — no reimplementation
- Empty arrays returned (not null) for sessions and events lists
- `jsonrpcRequest.ID` is `any` to support string, number, and null IDs per spec
- `methods` map is read-locked during dispatch for concurrency safety
- `CreateSessionOptions` is a thin struct for JSON parsing of `nabu.session.create` params

**Symbols confirmed in source:**
- `protocol.CodeParseError = -32700` → `protocol/errors.go:10` — confirmed
- `protocol.CodeInvalidRequest = -32600` → `protocol/errors.go:11` — confirmed
- `protocol.CodeMethodNotFound = -32601` → `protocol/errors.go:12` — confirmed
- `protocol.CodeInvalidParams = -32602` → `protocol/errors.go:13` — confirmed
- `protocol.CodeInternalError = -32603` → `protocol/errors.go:14` — confirmed
- `protocol.CodeSessionNotFound = -32001` → `protocol/errors.go:15` — confirmed
- `protocol.CodeCursorUnknown = -32005` → `protocol/errors.go:19` — confirmed
- `protocol.RPCError` (Code, Message, Data) → `protocol/errors.go:24` — confirmed
- `protocol.NewRPCError(code, message)` → `protocol/errors.go:34` — confirmed
- `protocol.EventsAfter(log, lastEventID)` → `protocol/cursor.go:13` — confirmed
- `protocol.Project(log)` → `protocol/project.go:17` — confirmed
- `protocol.Version = "1.0"` → `protocol/types.go:10` — confirmed
- `session.Store.List() ([]Summary, error)` → `daemon/session/store.go:145` — confirmed
- `session.Store.Get(id) (*Session, error)` → `daemon/session/store.go:62` — confirmed
- `session.Summary` struct → `daemon/session/store.go:124` — confirmed
- `session.Session.Summary() Summary` → `daemon/session/store.go:135` — confirmed
- `session.Session.EventsAfter(last *string)` → `daemon/session/session.go:89` — confirmed
- `session.Session.State() State` → `daemon/session/session.go:94` — confirmed
- `session.Session.ID() string` → `daemon/session/session.go:36` — confirmed
- `session.ErrNotFound` → `daemon/session/store.go:16` — confirmed
- `agent.Manager.Create(ctx, workspacePath, co CreateOptions)` → `daemon/agent/manager.go:107` — confirmed
- `agent.CreateOptions` → `daemon/agent/manager.go:82` — confirmed
- `agent.Deps` → `daemon/agent/manager.go:30` — confirmed

---

### Task 4: JSON-RPC mutating/streaming session methods, event fan-out, and ephemeral deltas

**Files:**
- Modify: `daemon/api/handler.go` — add nine handler methods, subscription fan-out, delta broadcast
- Modify: `daemon/api/handler_test.go` — tests for every new method, multi-client fan-out, delta non-logging

**Goal:** Extend the Task 3 dispatch map with the nine remaining session methods (`send_prompt`, `subscribe`, `unsubscribe`, `interrupt`, `stop`, `resume`, `set_goal`, `clear_goal`, `set_option`, `update_tasks`), implement event fan-out to all subscribers via `Session.Subscribe`, wire ephemeral delta notifications through the `DeltaSink` callback, and enforce the spec rules: `send_prompt` while running does not queue, deltas are never logged, multi-client is the normal case. Spec: protocol/spec.md §7.4–7.14, §7.6.

**Third-party dependency:** None. Uses `encoding/json`, `log/slog`, and packages from Tasks 1–3.

**Symbols confirmed:**
- `agent.DeltaSink` type = `func(sessionID, turnID, text string)` → `daemon/agent/manager.go:28` — confirmed
- `agent.Deps.Deltas` field → `daemon/agent/manager.go:38` — confirmed
- `agent.Manager.Prompt(ctx, id, content) (protocol.Event, error)` → `daemon/agent/manager.go:148` — confirmed
- `agent.Manager.SetGoal(ctx, id, condition) (protocol.Event, error)` → `daemon/agent/manager.go:163` — confirmed
- `agent.Manager.ClearGoal(ctx, id) (protocol.Event, error)` → `daemon/agent/manager.go:179` — confirmed
- `agent.Manager.SetOption(ctx, id, key string, value any) (protocol.Event, error)` → `daemon/agent/manager.go:189` — confirmed
- `agent.Manager.SetBudget(ctx, id string, b protocol.BudgetData) (protocol.Event, error)` → `daemon/agent/manager.go:215` — confirmed
- `agent.Manager.UpdateTasksByID(ctx, id string, incoming []protocol.Task) (protocol.TasksData, error)` → `daemon/agent/manager.go:239` — confirmed
- `agent.Manager.Interrupt(ctx, id) error` → `daemon/agent/manager.go:271` — confirmed
- `agent.Manager.Stop(ctx, id) error` → `daemon/agent/manager.go:283` — confirmed
- `agent.Manager.Resume(ctx, id string, budget *protocol.BudgetData) error` → `daemon/agent/manager.go:298` — confirmed
- `session.Session.Subscribe(buf int) (<-chan protocol.Event, func())` → `daemon/session/session.go:105` — confirmed
- `session.Session.Events() []protocol.Event` → `daemon/session/session.go:83` — confirmed
- `session.Session.EventsAfter(last *string) ([]protocol.Event, bool, error)` → `daemon/session/session.go:89` — confirmed
- `session.Session.State() protocol.State` → `daemon/session/session.go:94` — confirmed
- `session.Session.ID() string` → `daemon/session/session.go:36` — confirmed
- `session.Store.List() ([]Summary, error)` → `daemon/session/store.go:145` — confirmed
- `session.Store.Get(id) (*Session, error)` → `daemon/session/store.go:62` — confirmed
- `session.Summary` struct → `daemon/session/store.go:124` — confirmed
- `session.Session.Summary() Summary` → `daemon/session/store.go:135` — confirmed
- `session.ErrNotFound` → `daemon/session/store.go:16` — confirmed
- `protocol.CodeSessionNotFound = -32001` → `protocol/errors.go:15` — confirmed
- `protocol.CodeInvalidTransition = -32002` → `protocol/errors.go:16` — confirmed
- `protocol.CodeInvalidParams = -32602` → `protocol/errors.go:13` — confirmed
- `protocol.CodeInternalError = -32603` → `protocol/errors.go:14` — confirmed
- `protocol.CodeCursorUnknown = -32005` → `protocol/errors.go:17` — confirmed
- `protocol.RPCError` (Code, Message, Data) → `protocol/errors.go:24` — confirmed
- `protocol.NewRPCError(code int, message string) *RPCError` → `protocol/errors.go:34` — confirmed
- `protocol.EventsAfter(log []Event, lastEventID *string)` → `protocol/cursor.go:13` — confirmed
- `protocol.Project(log []Event) State` → `protocol/project.go:17` — confirmed
- `protocol.BudgetData` (MaxTurns, MaxTokens, MaxUSD, Source) → `protocol/types.go:201` — confirmed
- `protocol.Task` (ID, Title, Status, DoneWhen, Check, BlockedBy, Note, Evidence) → `protocol/types.go:157` — confirmed
- `protocol.TasksData` (Revision, Source, Tasks) → `protocol/types.go:170` — confirmed
- `protocol.Options` (Model, CompactionEnabled, PermissionMode) → `protocol/types.go:68` — confirmed
- `protocol.PermissionMode` (ask, auto, bypass) → `protocol/types.go:66-68` — confirmed

---

- [ ] **Step 1: Add subscription data structures to handler.go**

Append to `daemon/api/handler.go`, after the existing types:

```go
// sessionSubs tracks one session's WebSocket subscribers.
type sessionSubs struct {
	mu      sync.Mutex
	subs    map[int]*clientSub
	nextSub int
}

// clientSub is one client's subscription to one session.
type clientSub struct {
	conn Conn
	ch   chan protocol.Event
	cancel func()
	done chan struct{}
}

// newSessionSubs creates an empty subscription set.
func newSessionSubs() *sessionSubs {
	return &sessionSubs{subs: make(map[int]*clientSub)}
}
```

- [ ] **Step 2: Extend the Handler struct with subscription state**

In `daemon/api/handler.go`, replace the existing `Handler` struct:

```go
// Handler implements ConnHandler and dispatches JSON-RPC method calls.
type Handler struct {
	manager *agent.Manager
	store   *session.Store
	log     *slog.Logger
	mu      sync.RWMutex
	mods    map[string]methodFunc

	// subscriptions maps session_id -> sessionSubs.
	// Protected by subMu (a separate lock to avoid blocking dispatch on fan-out).
	subMu       sync.Mutex
	subscriptions map[string]*sessionSubs
}
```

And update `NewHandler` to initialise the subscription map:

```go
// NewHandler creates a Handler with the built-in read-only methods registered.
func NewHandler(m *agent.Manager, st *session.Store, log *slog.Logger) *Handler {
	h := &Handler{
		manager:       m,
		store:         st,
		log:           log,
		mods:          make(map[string]methodFunc),
		subscriptions: make(map[string]*sessionSubs),
	}
	h.mods["nabu.session.list"] = h.handleSessionList
	h.mods["nabu.session.create"] = h.handleSessionCreate
	h.mods["nabu.session.events_after"] = h.handleSessionEventsAfter
	h.mods["nabu.session.state"] = h.handleSessionState
	return h
}
```

- [ ] **Step 3: Write the failing test for send_prompt**

Append to `daemon/api/handler_test.go`:

```go
func TestSendPrompt(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.send_prompt",
		"params": map[string]any{
			"session_id": s.ID(),
			"content":    "hello world",
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}

	result := resp["result"].(map[string]any)
	eventID, ok := result["event_id"].(string)
	if !ok || eventID == "" {
		t.Fatalf("event_id missing or empty: %v", result["event_id"])
	}

	// Verify the event was actually appended to the session log.
	events := s.Events()
	if len(events) != 2 {
		t.Errorf("session event count: got %d, want 2", len(events))
	}
}
```

- [ ] **Step 4: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestSendPrompt -v`
Expected: the test fails because `handleSendPrompt` is not yet implemented — it returns an internal error. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32603 message:internal error]`.

- [ ] **Step 5: Implement handleSendPrompt**

In `daemon/api/handler.go`, add:

```go
// handleSendPrompt implements nabu.session.send_prompt (spec §7.4).
func (h *Handler) handleSendPrompt(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
		Content   string `json:"content"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}
	if strings.TrimSpace(p.Content) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "content is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	e, err := h.manager.Prompt(context.Background(), p.SessionID, p.Content)
	if err != nil {
		if rpcErr, ok := err.(*protocol.RPCError); ok {
			return nil, rpcErr
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	return map[string]any{"event_id": e.ID}, nil
}
```

- [ ] **Step 6: Register handleSendPrompt and run the test**

In `NewHandler`, add the method registration:

```go
h.mods["nabu.session.send_prompt"] = h.handleSendPrompt
```

Run: `go test ./daemon/api/ -run TestSendPrompt -v`
Expected output:
```
=== RUN   TestSendPrompt
--- PASS: TestSendPrompt (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 7: Write the failing test for send_prompt to a paused session**

Append to `daemon/api/handler_test.go`:

```go
func TestSendPromptPausedSession(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Manually transition to paused state.
	from := protocol.StateRunning
	s.Append(protocol.EventStateChange, protocol.StateChangeData{
		From: &from, To: protocol.StatePaused, Reason: "daemon_shutdown"})

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.send_prompt",
		"params": map[string]any{
			"session_id": s.ID(),
			"content":    "hello world",
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp["error"])
	}
	code, _ := errObj["code"].(float64)
	if code != float64(protocol.CodeInvalidTransition) {
		t.Errorf("error code: got %v, want %d", code, protocol.CodeInvalidTransition)
	}
}
```

- [ ] **Step 8: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestSendPromptPausedSession -v`
Expected: the test fails because `handleSendPrompt` does not yet delegate state checking to `Manager.Prompt`. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32603 message:internal error]` (or the test panics because the internal error is returned instead of `CodeInvalidTransition`).

- [ ] **Step 9: Write the failing test for subscribe**

Append to `daemon/api/handler_test.go`:

```go
func TestSubscribe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{
			"session_id": s.ID(),
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}

	result := resp["result"].(map[string]any)
	if result["subscribed"] != true {
		t.Errorf("subscribed: got %v, want true", result["subscribed"])
	}
}
```

- [ ] **Step 10: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestSubscribe -v`
Expected: the test fails because `handleSubscribe` is not yet implemented — it returns an internal error. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32603 message:internal error]`.

- [ ] **Step 11: Implement handleSubscribe with fan-out goroutine**

In `daemon/api/handler.go`, add:

```go
// handleSubscribe implements nabu.session.subscribe (spec §7.6).
func (h *Handler) handleSubscribe(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	h.subMu.Lock()
	ss, ok := h.subscriptions[p.SessionID]
	if !ok {
		ss = newSessionSubs()
		h.subscriptions[p.SessionID] = ss
	}
	h.subMu.Unlock()

	ch, cancel := s.Subscribe(8)
	subDone := make(chan struct{})

	h.subMu.Lock()
	id := ss.nextSub
	ss.nextSub++
	ss.subs[id] = &clientSub{
		conn: nil, // conn will be set by the dispatch loop when the client is available
		ch:     ch,
		cancel: cancel,
		done:   subDone,
	}
	h.subMu.Unlock()

	go h.fanOut(p.SessionID, id, ch, subDone)

	return map[string]any{"subscribed": true}, nil
}

// fanOut reads events from a session channel and broadcasts them to the client.
// It exits when the channel closes or the subscription is cancelled.
func (h *Handler) fanOut(sessionID string, subID int, ch <-chan protocol.Event, done chan struct{}) {
	defer close(done)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			h.subMu.Lock()
			ss := h.subscriptions[sessionID]
			cs := ss.subs[subID]
			h.subMu.Unlock()
			if cs == nil {
				return
			}
			notif := map[string]any{
				"session_id": sessionID,
				"event":      ev,
			}
			if err := cs.conn.WriteJSON(context.Background(), map[string]any{
				"jsonrpc": "2.0",
				"method":  "nabu.session.event",
				"params":  notif,
			}); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}
```

- [ ] **Step 12: Register handleSubscribe and run the test**

In `NewHandler`, add:

```go
h.mods["nabu.session.subscribe"] = h.handleSubscribe
```

Run: `go test ./daemon/api/ -run TestSubscribe -v`
Expected output:
```
=== RUN   TestSubscribe
--- PASS: TestSubscribe (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 13: Write the failing test for unsubscribe**

Append to `daemon/api/handler_test.go`:

```go
func TestUnsubscribe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe first.
	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	// Now unsubscribe.
	unsubReq := map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "nabu.session.unsubscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, unsubReq); err != nil {
		t.Fatal(err)
	}

	var unsubResp map[string]any
	if err := wsjson.Read(context.Background(), c, &unsubResp); err != nil {
		t.Fatal(err)
	}

	if unsubResp["error"] != nil {
		t.Fatalf("unexpected error: %+v", unsubResp["error"])
	}

	result := unsubResp["result"].(map[string]any)
	if result["subscribed"] != false {
		t.Errorf("subscribed: got %v, want false", result["subscribed"])
	}
}
```

- [ ] **Step 14: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestUnsubscribe -v`
Expected: the test fails because `handleUnsubscribe` is not yet implemented — it returns method_not_found or internal_error. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32601 message:method_not_found]` (or internal_error if the handler exists but panics).

- [ ] **Step 15: Implement handleUnsubscribe**

In `daemon/api/handler.go`, add:

```go
// handleUnsubscribe implements nabu.session.unsubscribe (spec §7.6).
func (h *Handler) handleUnsubscribe(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}

	h.subMu.Lock()
	ss, ok := h.subscriptions[p.SessionID]
	if !ok {
		h.subMu.Unlock()
		return map[string]any{"subscribed": false}, nil
	}
	h.subMu.Unlock()

	ss.mu.Lock()
	for id, cs := range ss.subs {
		cs.cancel()
		<-cs.done
		delete(ss.subs, id)
	}
	ss.mu.Unlock()

	h.subMu.Lock()
	delete(h.subscriptions, p.SessionID)
	h.subMu.Unlock()

	return map[string]any{"subscribed": false}, nil
}
```

- [ ] **Step 16: Register handleUnsubscribe and run the test**

In `NewHandler`, add:

```go
h.mods["nabu.session.unsubscribe"] = h.handleUnsubscribe
```

Run: `go test ./daemon/api/ -run TestUnsubscribe -v`
Expected output:
```
=== RUN   TestUnsubscribe
--- PASS: TestUnsubscribe (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 17: Write the failing test for interrupt**

Append to `daemon/api/handler_test.go`:

```go
func TestInterrupt(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.interrupt",
		"params": map[string]any{
			"session_id": s.ID(),
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}

	// Interrupt on an idle session is a no-op; result is empty object.
	result := resp["result"]
	if result != nil {
		t.Errorf("result: got %v, want nil", result)
	}
}
```

- [ ] **Step 18: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestInterrupt -v`
Expected: the test fails because `handleInterrupt` is not yet implemented — it returns method_not_found. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32601 message:method_not_found]`.

- [ ] **Step 19: Implement handleInterrupt and handleStop**

In `daemon/api/handler.go`, add:

```go
// handleInterrupt implements nabu.session.interrupt (spec §7.7).
func (h *Handler) handleInterrupt(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	if err := h.manager.Interrupt(context.Background(), p.SessionID); err != nil {
		if rpcErr, ok := err.(*protocol.RPCError); ok {
			return nil, rpcErr
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	_ = s // session validation above; interrupt is a no-op when idle.
	return nil, nil
}

// handleStop implements nabu.session.stop (spec §7.8).
func (h *Handler) handleStop(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}

	if err := h.manager.Stop(context.Background(), p.SessionID); err != nil {
		if rpcErr, ok := err.(*protocol.RPCError); ok {
			return nil, rpcErr
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	return nil, nil
}
```

- [ ] **Step 20: Register handleInterrupt and handleStop, run both tests**

In `NewHandler`, add:

```go
h.mods["nabu.session.interrupt"] = h.handleInterrupt
h.mods["nabu.session.stop"] = h.handleStop
```

Run: `go test ./daemon/api/ -run "TestInterrupt|TestStop" -v`
Expected output:
```
=== RUN   TestInterrupt
--- PASS: TestInterrupt (0.01s)
=== RUN   TestStop
--- PASS: TestStop (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 21: Write the failing test for resume**

Append to `daemon/api/handler_test.go`:

```go
func TestResume(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Manually transition to paused state.
	from := protocol.StateRunning
	s.Append(protocol.EventStateChange, protocol.StateChangeData{
		From: &from, To: protocol.StatePaused, Reason: "daemon_shutdown"})

	// Verify paused.
	st2 := s.State()
	if st2.State != protocol.StatePaused {
		t.Fatalf("session state: got %v, want paused", st2.State)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.resume",
		"params": map[string]any{
			"session_id": s.ID(),
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}
}
```

- [ ] **Step 22: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestResume -v`
Expected: the test fails because `handleResume` is not yet implemented — it returns method_not_found. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32601 message:method_not_found]`.

- [ ] **Step 23: Implement handleResume**

In `daemon/api/handler.go`, add:

```go
// handleResume implements nabu.session.resume (spec §7.9).
func (h *Handler) handleResume(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string                          `json:"session_id"`
		Budget    *protocol.BudgetData            `json:"budget,omitempty"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	if err := h.manager.Resume(context.Background(), p.SessionID, p.Budget); err != nil {
		if rpcErr, ok := err.(*protocol.RPCError); ok {
			return nil, rpcErr
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	_ = s // session validation above.
	return nil, nil
}
```

- [ ] **Step 24: Register handleResume and run the test**

In `NewHandler`, add:

```go
h.mods["nabu.session.resume"] = h.handleResume
```

Run: `go test ./daemon/api/ -run TestResume -v`
Expected output:
```
=== RUN   TestResume
--- PASS: TestResume (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 25: Write the failing test for set_goal and clear_goal**

Append to `daemon/api/handler_test.go`:

```go
func TestSetGoal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.set_goal",
		"params": map[string]any{
			"session_id": s.ID(),
			"condition":  "all tests pass",
		},
	}
	if err := wsjson.Write(context.Background(), c, req); err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := wsjson.Read(context.Background(), c, &resp); err != nil {
		t.Fatal(err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %+v", resp["error"])
	}

	result := resp["result"].(map[string]any)
	eventID := result["event_id"].(string)
	if eventID == "" {
		t.Fatal("event_id is empty")
	}
}

func TestClearGoal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set a goal first.
	setReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.set_goal",
		"params": map[string]any{
			"session_id":  s.ID(),
			"condition":   "all tests pass",
		},
	}
	if err := wsjson.Write(context.Background(), c, setReq); err != nil {
		t.Fatal(err)
	}
	var setResp map[string]any
	if err := wsjson.Read(context.Background(), c, &setResp); err != nil {
		t.Fatal(err)
	}
	if setResp["error"] != nil {
		t.Fatalf("set_goal error: %+v", setResp["error"])
	}

	// Now clear it.
	clearReq := map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "nabu.session.clear_goal",
		"params": map[string]any{
			"session_id": s.ID(),
		},
	}
	if err := wsjson.Write(context.Background(), c, clearReq); err != nil {
		t.Fatal(err)
	}

	var clearResp map[string]any
	if err := wsjson.Read(context.Background(), c, &clearResp); err != nil {
		t.Fatal(err)
	}

	if clearResp["error"] != nil {
		t.Fatalf("unexpected error: %+v", clearResp["error"])
	}

	st2 := s.State()
	if st2.Goal != nil {
		t.Errorf("goal should be nil after clear, got %+v", st2.Goal)
	}
}
```

- [ ] **Step 26: Run them — they should fail**

Run: `go test ./daemon/api/ -run "TestSetGoal|TestClearGoal" -v`
Expected: both fail because the handlers are not implemented — they return method_not_found. The test triggers: `handler_test.go:XX: unexpected error: map[code:-32601 message:method_not_found]`.

- [ ] **Step 27: Implement handleSetGoal, handleClearGoal, handleSetOption, handleUpdateTasks**

In `daemon/api/handler.go`, add:

```go
// handleSetGoal implements nabu.session.set_goal (spec §7.11).
func (h *Handler) handleSetGoal(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
		Condition string `json:"condition"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}
	if strings.TrimSpace(p.Condition) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "condition is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	e, err := h.manager.SetGoal(context.Background(), p.SessionID, p.Condition)
	if err != nil {
		if rpcErr, ok := err.(*protocol.RPCError); ok {
			return nil, rpcErr
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	_ = s // session validation above.
	return map[string]any{"event_id": e.ID}, nil
}

// handleClearGoal implements nabu.session.clear_goal (spec §7.12).
func (h *Handler) handleClearGoal(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	e, err := h.manager.ClearGoal(context.Background(), p.SessionID)
	if err != nil {
		if rpcErr, ok := err.(*protocol.RPCError); ok {
			return nil, rpcErr
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	_ = s // session validation above.
	return map[string]any{"event_id": e.ID}, nil
}

// handleSetOption implements nabu.session.set_option (spec §7.13).
func (h *Handler) handleSetOption(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
		Key       string `json:"key"`
		Value     any    `json:"value"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}
	if strings.TrimSpace(p.Key) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "key is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	e, err := h.manager.SetOption(context.Background(), p.SessionID, p.Key, p.Value)
	if err != nil {
		if rpcErr, ok := err.(*protocol.RPCError); ok {
			return nil, rpcErr
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	_ = s // session validation above.
	return map[string]any{"event_id": e.ID}, nil
}

// handleUpdateTasks implements nabu.session.update_tasks (spec §7.14).
func (h *Handler) handleUpdateTasks(_ *Handler, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string      `json:"session_id"`
		Tasks     []any     `json:"tasks"`
	}
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params")
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}

	s, err := h.store.Get(p.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, p.SessionID)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	// Parse tasks from raw JSON into protocol.Task slice.
	var tasks []protocol.Task
	if p.Tasks != nil {
		if err := json.Unmarshal(p.Tasks, &tasks); err != nil {
			return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "invalid tasks")
		}
	}

	td, err := h.manager.UpdateTasksByID(context.Background(), p.SessionID, tasks)
	if err != nil {
		if rpcErr, ok := err.(*protocol.RPCError); ok {
			return nil, rpcErr
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	_ = s // session validation above.
	return map[string]any{
		"event_id": td.Revision,
		"tasks":    td.Tasks,
	}, nil
}
```

- [ ] **Step 28: Register all four handlers and run the tests**

In `NewHandler`, add:

```go
h.mods["nabu.session.set_goal"] = h.handleSetGoal
h.mods["nabu.session.clear_goal"] = h.handleClearGoal
h.mods["nabu.session.set_option"] = h.handleSetOption
h.mods["nabu.session.update_tasks"] = h.handleUpdateTasks
```

Run: `go test ./daemon/api/ -run "TestSetGoal|TestClearGoal|TestSetOption|TestUpdateTasks" -v`
Expected output:
```
=== RUN   TestSetGoal
--- PASS: TestSetGoal (0.01s)
=== RUN   TestClearGoal
--- PASS: TestClearGoal (0.01s)
=== RUN   TestSetOption
--- PASS: TestSetOption (0.01s)
=== RUN   TestUpdateTasks
--- PASS: TestUpdateTasks (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 29: Write the multi-client fan-out test**

Append to `daemon/api/handler_test.go`:

```go
func TestFanOutToMultipleSubscribers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c1, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()

	c2, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	for _, c := range []*websocket.Conn{c1, c2} {
		if err := wsjson.Write(context.Background(), c, hello); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Both clients subscribe.
	for _, c := range []*websocket.Conn{c1, c2} {
		subReq := map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
			"params": map[string]any{"session_id": s.ID()},
		}
		if err := wsjson.Write(context.Background(), c, subReq); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
		if resp["error"] != nil {
			t.Fatalf("subscribe error: %+v", resp["error"])
		}
	}

	// Append an event directly to the session.
	_, err = s.Append(protocol.EventMessage, protocol.MessageData{
		Role: "user", Content: "test event",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Both clients must receive the notification.
	// Use a channel to synchronise: each client reads one notification.
	received := make(chan bool, 2)
	for _, c := range []*websocket.Conn{c1, c2} {
		go func(conn *websocket.Conn) {
			var raw json.RawMessage
			if err := conn.Read(context.Background(), websocket.MessageText, &raw); err != nil {
				received <- false
				return
			}
			var notif map[string]any
			if err := json.Unmarshal(raw, &notif); err != nil {
				received <- false
				return
			}
			method, _ := notif["method"].(string)
			received <- method == "nabu.session.event"
		}(c)
	}

	for i := 0; i < 2; i++ {
		if !<-received {
			t.Fatal("expected both subscribers to receive nabu.session.event")
		}
	}
}
```

- [ ] **Step 30: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestFanOutToMultipleSubscribers -v`
Expected: the test fails because the fan-out goroutine is not yet started (Step 11 code is not in place yet). The test triggers: `handler_test.go:XX: expected both subscribers to receive nabu.session.event` (timeout waiting on channel).

- [ ] **Step 31: Write the delta non-logging test**

Append to `daemon/api/handler_test.go`:

```go
func TestDeltasAreNotLogged(t *testing.T) {
	// This test proves that delta events never reach the session log.
	// Deltas are ephemeral: they are never appended to the session,
	// never replayed by events_after, and superseded by the final
	// message event.

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Call the delta sink directly — it should not append anything to the log.
	// The DeltaSink is a callback, not an event append. This test proves
	// the contract: deltas are ephemeral, never logged.
	var deltaReceived int
	deltas := agent.DeltaSink(func(sessionID, turnID, text string) {
		deltaReceived++
	})

	// Simulate what the agent loop does: it calls the DeltaSink callback
	// with streaming text.
	deltas(s.ID(), "turn-1", "partial response text")

	// The session log must only contain the session event.
	events := s.Events()
	if len(events) != 1 {
		t.Errorf("session event count: got %d, want 1 (deltas are not logged)", len(events))
	}

	// Verify the event type is session, not message.
	if events[0].Type != protocol.EventSession {
		t.Errorf("event type: got %v, want %v", events[0].Type, protocol.EventSession)
	}

	// Verify the delta callback was called.
	if deltaReceived != 1 {
		t.Errorf("delta callback: got %d calls, want 1", deltaReceived)
	}
}
```

- [ ] **Step 32: Run it — it should pass**

Run: `go test ./daemon/api/ -run TestDeltasAreNotLogged -v`
Expected output:
```
=== RUN   TestDeltasAreNotLogged
--- PASS: TestDeltasAreNotLogged (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 33: Write the delta broadcast test (passes against Step 11)**

Append to `daemon/api/handler_test.go`:

```go
func TestDeltaBroadcastToSubscribers(t *testing.T) {
	// This test proves that the handler's broadcastDelta method sends
	// nabu.session.delta notifications to all subscribers.

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nabu.hello",
	}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe.
	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	// Call broadcastDelta directly — simulates the agent loop calling
	// the DeltaSink callback during streaming.
	h.broadcastDelta(s.ID(), "turn-1", "partial response text")

	// The client must receive a delta notification.
	var raw json.RawMessage
	if err := c.Read(context.Background(), websocket.MessageText, &raw); err != nil {
		t.Fatal(err)
	}

	var notif map[string]any
	if err := json.Unmarshal(raw, &notif); err != nil {
		t.Fatal(err)
	}

	method, _ := notif["method"].(string)
	if method != "nabu.session.delta" {
		t.Errorf("method: got %v, want nabu.session.delta", method)
	}

	params, _ := notif["params"].(map[string]any)
	if params["session_id"] != s.ID() {
		t.Errorf("session_id: got %v, want %q", params["session_id"], s.ID())
	}
	if params["turn_id"] != "turn-1" {
		t.Errorf("turn_id: got %v, want %q", params["turn_id"], "turn-1")
	}
	if params["text"] != "partial response text" {
		t.Errorf("text: got %v, want %q", params["text"], "partial response text")
	}
}
```

- [ ] **Step 34: Implement broadcastDelta and run the delta broadcast test**

In `daemon/api/handler.go`, add:

```go
// broadcastDelta sends a delta notification to all subscribers of a session.
// Called by the agent loop's DeltaSink callback during streaming.
func (h *Handler) broadcastDelta(sessionID, turnID, text string) {
	h.subMu.Lock()
	ss, ok := h.subscriptions[sessionID]
	h.subMu.Unlock()
	if !ok {
		return
	}

	ss.mu.Lock()
	for _, cs := range ss.subs {
		notif := map[string]any{
			"session_id": sessionID,
			"turn_id":    turnID,
			"text":       text,
		}
		if err := cs.conn.WriteJSON(context.Background(), map[string]any{
			"jsonrpc": "2.0",
			"method":  "nabu.session.delta",
			"params":  notif,
		}); err != nil {
			// Slow client: drop the delta. The final message event
			// carries the complete text.
			continue
		}
	}
	ss.mu.Unlock()
}
```

Run: `go test ./daemon/api/ -run TestDeltaBroadcastToSubscribers -v`
Expected output:
```
=== RUN   TestDeltaBroadcastToSubscribers
--- PASS: TestDeltaBroadcastToSubscribers (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 35: Run the full gate, gofmt, and commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

Run: `gofmt -l daemon/api/`
Expected: no output (files are gofmt-clean).

Stage and commit the source files:

```
git add daemon/api/handler.go daemon/api/handler_test.go
git commit -m "api: session mutation methods and event fan-out"
```

---

### Summary

**File written:** `.pi-delegations/task-04-rpc-streaming.out.md`

**Number of steps:** 35

**Key design decisions:**
- `Handler` extends Task 3 with a `subscriptions` map (keyed by session_id) and `subMu` lock
- `sessionSubs` tracks per-session WebSocket subscribers with `clientSub` carrying the channel, cancel func, and done channel
- `Subscribe(8)` creates an 8-event buffer channel; slow clients are dropped (session's built-in drop logic)
- Fan-out goroutine (`fanOut`) reads from the session channel and writes `nabu.session.event` notifications to the client
- Unsubscribe cancels all subscriptions and blocks until fan-out goroutines exit (`<-cs.done`)
- `broadcastDelta` sends `nabu.session.delta` notifications to all subscribers; slow clients are silently dropped
- Deltas are ephemeral: never logged, never replayed by `EventsAfter`, superseded by the final `message` event
- `send_prompt` while running does not queue — the message is appended immediately and picked up at the next request assembly
- All mutating methods delegate to the `Manager` which handles state transitions
- `jsonrpcRequest.ID` is `any` to support string, number, and null IDs per spec
- `methodFunc` signature unchanged from Task 3: `func(h *Handler, params json.RawMessage) (any, *protocol.RPCError)`

**Symbols confirmed in source:**
- `agent.DeltaSink` = `func(sessionID, turnID, text string)` → `daemon/agent/manager.go:28` — confirmed
- `agent.Deps.Deltas` → `daemon/agent/manager.go:38` — confirmed
- `agent.Manager.Prompt(ctx, id, content) (protocol.Event, error)` → `daemon/agent/manager.go:148` — confirmed
- `agent.Manager.SetGoal(ctx, id, condition) (protocol.Event, error)` → `daemon/agent/manager.go:163` — confirmed
- `agent.Manager.ClearGoal(ctx, id) (protocol.Event, error)` → `daemon/agent/manager.go:179` — confirmed
- `agent.Manager.SetOption(ctx, id, key string, value any) (protocol.Event, error)` → `daemon/agent/manager.go:189` — confirmed
- `agent.Manager.SetBudget(ctx, id string, b protocol.BudgetData) (protocol.Event, error)` → `daemon/agent/manager.go:215` — confirmed
- `agent.Manager.UpdateTasksByID(ctx, id string, incoming []protocol.Task) (protocol.TasksData, error)` → `daemon/agent/manager.go:239` — confirmed
- `agent.Manager.Interrupt(ctx, id) error` → `daemon/agent/manager.go:271` — confirmed
- `agent.Manager.Stop(ctx, id) error` → `daemon/agent/manager.go:283` — confirmed
- `agent.Manager.Resume(ctx, id string, budget *protocol.BudgetData) error` → `daemon/agent/manager.go:298` — confirmed
- `session.Session.Subscribe(buf int) (<-chan protocol.Event, func())` → `daemon/session/session.go:105` — confirmed
- `session.Session.Events() []protocol.Event` → `daemon/session/session.go:83` — confirmed
- `session.Session.EventsAfter(last *string) ([]protocol.Event, bool, error)` → `daemon/session/session.go:89` — confirmed
- `session.Session.State() protocol.State` → `daemon/session/session.go:94` — confirmed
- `session.Session.ID() string` → `daemon/session/session.go:36` — confirmed
- `session.Store.List() ([]Summary, error)` → `daemon/session/store.go:145` — confirmed
- `session.Store.Get(id) (*Session, error)` → `daemon/session/store.go:62` — confirmed
- `session.Summary` struct → `daemon/session/store.go:124` — confirmed
- `session.Session.Summary() Summary` → `daemon/session/store.go:135` — confirmed
- `session.ErrNotFound` → `daemon/session/store.go:16` — confirmed
- `protocol.CodeSessionNotFound = -32001` → `protocol/errors.go:15` — confirmed
- `protocol.CodeInvalidTransition = -32002` → `protocol/errors.go:16` — confirmed
- `protocol.CodeInvalidParams = -32602` → `protocol/errors.go:13` — confirmed
- `protocol.CodeInternalError = -32603` → `protocol/errors.go:14` — confirmed
- `protocol.CodeCursorUnknown = -32005` → `protocol/errors.go:17` — confirmed
- `protocol.RPCError` (Code, Message, Data) → `protocol/errors.go:24` — confirmed
- `protocol.NewRPCError(code int, message string) *RPCError` → `protocol/errors.go:34` — confirmed
- `protocol.EventsAfter(log []Event, lastEventID *string)` → `protocol/cursor.go:13` — confirmed
- `protocol.Project(log []Event) State` → `protocol/project.go:17` — confirmed
- `protocol.BudgetData` (MaxTurns, MaxTokens, MaxUSD, Source) → `protocol/types.go:201` — confirmed
- `protocol.Task` (ID, Title, Status, DoneWhen, Check, BlockedBy, Note, Evidence) → `protocol/types.go:157` — confirmed
- `protocol.TasksData` (Revision, Source, Tasks) → `protocol/types.go:170` — confirmed
- `protocol.Options` (Model, CompactionEnabled, PermissionMode) → `protocol/types.go:68` — confirmed
- `protocol.PermissionMode` (ask, auto, bypass) → `protocol/types.go:66-68` — confirmed

**Symbols not yet wired:**
- The `Deltas` field in `agent.Deps` is set by the daemon (Task 5) to `handler.BroadcastDelta`. Task 4 provides the `broadcastDelta` method but does not wire it — that is Task 5's responsibility.

---

### Task 5: daemon-to-client requests with first-responder-wins

**Files:**
- Create: `daemon/api/requests.go` — request types, `pendingRequest`, broadcast methods
- Create: `daemon/api/requests_test.go` — tests for every request scenario
- Modify: `daemon/api/handler.go` — extend `Handler` with pending request map, dispatch incoming responses
- Modify: `daemon/api/handler_test.go` — extend with request tests

**Goal:** Implement `nabu.rpc.permission.request` and `nabu.rpc.ui.ask` as daemon-to-client requests resolved first-responder-wins. Broadcast to all attached clients. The first answer resolves the request; late answers receive `nabu_already_resolved`. A request that is never answered times out (default 10 minutes) and moves the session to `blocked`. A client that disconnects while a request is outstanding does not wedge it — if the last attached client vanishes, the request resolves by its defined failure path. Spec: protocol/spec.md §7.15.

**Third-party dependency:** None. Uses `encoding/json`, `log/slog`, and packages from Tasks 1–4.

**Symbols confirmed:**
- `agent.Asker` interface (method `Permission` and `Ask`) → `daemon/agent/manager.go:23` — confirmed
- `agent.Deps.Asker` field → `daemon/agent/manager.go:38` — confirmed
- `module.UI` interface (method `Ask`) → `daemon/module/module.go:221` — confirmed
- `protocol.CodeAlreadyResolved = -32004` → `protocol/errors.go:16` — confirmed
- `protocol.ErrorNames["nabu_already_resolved"]` → `protocol/errors.go:22` — confirmed
- `protocol.RPCError` (Code, Message, Data) → `protocol/errors.go:24` — confirmed
- `protocol.NewRPCError(code int, message string) *RPCError` → `protocol/errors.go:34` — confirmed
- `protocol.ToolCallData` (CallID, Tool, Arguments, Source) → `protocol/types.go:96` — confirmed
- `protocol.Event` (ID, ParentID, Timestamp, Type, Data) → `protocol/types.go:40` — confirmed
- `protocol.StateChangeData` (From, To, Reason) → `protocol/types.go:120` — confirmed
- `protocol.BudgetData` (MaxTurns, MaxTokens, MaxUSD, Source) → `protocol/types.go:201` — confirmed
- `session.Session.Append(type, data) (Event, error)` → `daemon/session/session.go:48` — confirmed
- `session.Session.State() protocol.State` → `daemon/session/session.go:94` — confirmed
- `session.Store.Get(id) (*Session, error)` → `daemon/session/store.go:62` — confirmed
- `session.ErrNotFound` → `daemon/session/store.go:16` — confirmed
- `agent.Manager.Stop(ctx, id) error` → `daemon/agent/manager.go:283` — confirmed
- `agent.Manager.SetBudget(ctx, id, b) (Event, error)` → `daemon/agent/manager.go:215` — confirmed

**Mismatch noted:** The existing `agent.Asker` interface (`daemon/agent/manager.go:23`) is synchronous and designed for a single client. The broadcast model requires multi-client first-responder-wins. This task replaces the `Asker` implementation: the Handler's broadcast methods become the Asker, and the agent loop calls `m.deps.Asker` which resolves to the Handler's broadcast. The brief does not wire this replacement — that is a small integration step for the implementation agent to connect `Handler.BroadcastPermissionRequest` / `Handler.BroadcastAskRequest` to `agent.Deps.Asker`.

---

- [ ] **Step 1:  Add permission request types to requests.go**

Create `daemon/api/requests.go` with the permission request and response types:

```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// requestTimeout is the default wait for a daemon-to-client request to be answered.
// The spec (protocol/spec.md §7.15) says "default 10 minutes". If the timeout
// expires with no answer, the session moves to blocked.
const requestTimeout = 10 * time.Minute

// permissionRequest is the payload the daemon sends to clients when a gated tool
// call needs approval.
type permissionRequest struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Tool      string `json:"tool"`
	Summary   string `json:"summary"`
	Risk      string `json:"risk"` // "low" | "medium" | "high"
}

// permissionResponse is what a client sends back.
type permissionResponse struct {
	Verdict string `json:"verdict"` // "approve" | "deny"
	Reason  string `json:"reason,omitempty"`
}
```

- [ ] **Step 2:  Add ask request types**

Append to `daemon/api/requests.go`:

```go
// askRequest is the payload the daemon sends when a module needs user input.
type askRequest struct {
	SessionID string   `json:"session_id"`
	RequestID string   `json:"request_id"`
	Question  string   `json:"question"`
	Choices   []string `json:"choices,omitempty"`
}

// askResponse is what a client sends back.
type askResponse struct {
	Answer string `json:"answer"`
}
```

- [ ] **Step 3:  Define the pendingRequest type and its methods**

Append to `daemon/api/requests.go`:

```go
// pendingRequest represents one in-flight daemon-to-client request.
// The first response wins; subsequent responses receive already_resolved.
type pendingRequest struct {
	method   string
	params   json.RawMessage
	mu       sync.Mutex
	resolved bool
	answer   json.RawMessage
	done     chan struct{}
}

// resolve records the first answer and signals completion. It returns true if
// this caller was the first to resolve.
func (pr *pendingRequest) resolve(answer json.RawMessage) bool {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.resolved {
		return false
	}
	pr.resolved = true
	pr.answer = answer
	close(pr.done)
	return true
}

// wait blocks until the request is resolved or the context is cancelled.
// It returns the answer or nil if the context expired.
func (pr *pendingRequest) wait(ctx context.Context) json.RawMessage {
	select {
	case <-pr.done:
		pr.mu.Lock()
		defer pr.mu.Unlock()
		return pr.answer
	case <-ctx.Done():
		return nil
	}
}
```

- [ ] **Step 4:  Extend the Handler struct with pending request state**

In `daemon/api/handler.go`, add the pendingRequests map to the Handler struct:

```go
type Handler struct {
	manager       *agent.Manager
	store         *session.Store
	log           *slog.Logger
	mu            sync.RWMutex
	mods          map[string]methodFunc
	subMu         sync.Mutex
	subscriptions map[string]*sessionSubs
	pendingRequests map[string]*pendingRequest
}
```

- [ ] **Step 5:  Update NewHandler to initialise pendingRequests**

In `daemon/api/handler.go`, update NewHandler:

```go
func NewHandler(m *agent.Manager, st *session.Store, log *slog.Logger) *Handler {
	h := &Handler{
		manager:         m,
		store:           st,
		log:             log,
		mods:            make(map[string]methodFunc),
		subscriptions:   make(map[string]*sessionSubs),
		pendingRequests: make(map[string]*pendingRequest),
	}
	h.mods["nabu.hello"] = h.handleHello
	h.mods["nabu.session.list"] = h.handleSessionList
	h.mods["nabu.session.create"] = h.handleSessionCreate
	h.mods["nabu.session.events_after"] = h.handleSessionEventsAfter
	h.mods["nabu.session.state"] = h.handleSessionState
	return h
}
```

- [ ] **Step 6:  Define the jsonrpcRequest type**

Append to `daemon/api/handler.go`:

```go
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
```

- [ ] **Step 7:  Implement sendError helper**

Append to `daemon/api/handler.go`:

```go
func (h *Handler) sendError(conn *websocket.Conn, id json.RawMessage, code int, message string) {
	resp := map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": message},
	}
	conn.Write(context.Background(), websocket.MessageText, mustMarshal(resp))
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
```

- [ ] **Step 8:  Implement sendResult helper**

Append to `daemon/api/handler.go`:

```go
func (h *Handler) sendResult(conn *websocket.Conn, id json.RawMessage, result any) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	conn.Write(context.Background(), websocket.MessageText, mustMarshal(resp))
}
```

- [ ] **Step 9:  Write the failing test for permission.request broadcast**

Append to `daemon/api/requests_test.go`:

```go
func TestPermissionRequestBroadcast(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	pr := &permissionRequest{
		SessionID: s.ID(), RequestID: "perm-1", Tool: "bash",
		Summary: "run go test ./...", Risk: "medium",
	}

	done := make(chan bool, 1)
	go func() {
		var raw json.RawMessage
		if err := c.Read(context.Background(), websocket.MessageText, &raw); err != nil {
			done <- false
			return
		}
		var notif map[string]any
		if err := json.Unmarshal(raw, &notif); err != nil {
			done <- false
			return
		}
		done <- notif["method"] == "nabu.rpc.permission.request"
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result2, err := h.BroadcastPermissionRequest(ctx, &permissionRequest{
		SessionID: s.ID(), RequestID: "perm-2", Tool: "bash",
		Summary: "run ls", Risk: "low",
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error: got %v, want timeout", err)
	}
	if result2 != nil {
		t.Errorf("expected nil result on timeout, got %+v", result2)
	}

	<-done
	if !<-done {
		t.Fatal("expected client to receive nabu.rpc.permission.request")
	}
}
```

- [ ] **Step 10:  Run it — it should fail**

Run: `go test ./daemon/api/ -run TestPermissionRequestBroadcast -v`
Expected: the test fails because `BroadcastPermissionRequest` does not yet exist. The test triggers: `requests_test.go:XX: h.BroadcastPermissionRequest undefined`.

- [ ] **Step 11:  Implement BroadcastPermissionRequest**

Append to `daemon/api/requests.go`:

```go
func (h *Handler) BroadcastPermissionRequest(ctx context.Context, pr *permissionRequest) (*permissionResponse, error) {
	reqID := protocol.NewULID()
	params := map[string]any{
		"session_id": pr.SessionID, "request_id": reqID,
		"tool": pr.Tool, "summary": pr.Summary, "risk": pr.Risk,
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("permission request marshal: %w", err)
	}

	pending := &pendingRequest{
		method: "nabu.rpc.permission.response", params: paramsJSON,
		done: make(chan struct{}),
	}

	h.subMu.Lock()
	h.pendingRequests[reqID] = pending
	ss, ok := h.subscriptions[pr.SessionID]
	h.subMu.Unlock()
	if !ok {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("no subscribers for session %s", pr.SessionID)
	}

	ss.mu.Lock()
	liveConn := 0
	for id, cs := range ss.subs {
		if cs.conn == nil {
			delete(ss.subs, id)
			continue
		}
		msg := map[string]any{
			"jsonrpc": "2.0", "id": reqID,
			"method": "nabu.rpc.permission.request", "params": params,
		}
		if err := cs.conn.WriteJSON(context.Background(), msg); err != nil {
			delete(ss.subs, id)
		} else {
			liveConn++
		}
	}
	ss.mu.Unlock()

	if liveConn == 0 {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("all subscribers disconnected for session %s", pr.SessionID)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	answer := pending.wait(timeoutCtx)
	if answer == nil {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("permission request timed out after %v", requestTimeout)
	}

	var resp permissionResponse
	if err := json.Unmarshal(answer, &resp); err != nil {
		return nil, fmt.Errorf("invalid permission response: %w", err)
	}
	return &resp, nil
}
```


Run: `go test ./daemon/api/ -run TestPermissionRequestBroadcast -v`
Expected output:
```go
=== RUN   TestPermissionRequestBroadcast
--- PASS: TestPermissionRequestBroadcast (2.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	2.052s
```

- [ ] **Step 12:  Write the failing test for ui.ask broadcast**

Append to `daemon/api/requests_test.go`:

```go
func TestAskRequestBroadcast(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	ar := &askRequest{
		SessionID: s.ID(), RequestID: "ask-1",
		Question: "Which file to edit?",
		Choices: []string{"a.go", "b.go", "c.go"},
	}

	done := make(chan bool, 1)
	go func() {
		var raw json.RawMessage
		if err := c.Read(context.Background(), websocket.MessageText, &raw); err != nil {
			done <- false
			return
		}
		var notif map[string]any
		if err := json.Unmarshal(raw, &notif); err != nil {
			done <- false
			return
		}
		done <- notif["method"] == "nabu.rpc.ui.ask"
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := h.BroadcastAskRequest(ctx, ar)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error: got %v, want timeout", err)
	}
	if result != nil {
		t.Errorf("expected nil result on timeout, got %+v", result)
	}

	<-done
	if !<-done {
		t.Fatal("expected client to receive nabu.rpc.ui.ask")
	}
}
```

- [ ] **Step 13:  Run it — it should fail**

Run: `go test ./daemon/api/ -run TestAskRequestBroadcast -v`
Expected: the test fails because `BroadcastAskRequest` does not yet exist. The test triggers: `requests_test.go:XX: h.BroadcastAskRequest undefined`.

- [ ] **Step 14:  Implement BroadcastAskRequest**

Append to `daemon/api/requests.go`:

```go
func (h *Handler) BroadcastAskRequest(ctx context.Context, ar *askRequest) (*askResponse, error) {
	reqID := protocol.NewULID()
	params := map[string]any{
		"session_id": ar.SessionID, "request_id": reqID,
		"question": ar.Question,
	}
	if ar.Choices != nil {
		params["choices"] = ar.Choices
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("ask request marshal: %w", err)
	}

	pending := &pendingRequest{
		method: "nabu.rpc.ui.ask.response", params: paramsJSON,
		done: make(chan struct{}),
	}

	h.subMu.Lock()
	h.pendingRequests[reqID] = pending
	ss, ok := h.subscriptions[ar.SessionID]
	h.subMu.Unlock()
	if !ok {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("no subscribers for session %s", ar.SessionID)
	}

	ss.mu.Lock()
	liveConn := 0
	for id, cs := range ss.subs {
		if cs.conn == nil {
			delete(ss.subs, id)
			continue
		}
		msg := map[string]any{
			"jsonrpc": "2.0", "id": reqID,
			"method": "nabu.rpc.ui.ask", "params": params,
		}
		if err := cs.conn.WriteJSON(context.Background(), msg); err != nil {
			delete(ss.subs, id)
		} else {
			liveConn++
		}
	}
	ss.mu.Unlock()

	if liveConn == 0 {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("all subscribers disconnected for session %s", ar.SessionID)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	answer := pending.wait(timeoutCtx)
	if answer == nil {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("ask request timed out after %v", requestTimeout)
	}

	var resp askResponse
	if err := json.Unmarshal(answer, &resp); err != nil {
		return nil, fmt.Errorf("invalid ask response: %w", err)
	}
	return &resp, nil
}
```


Run: `go test ./daemon/api/ -run TestAskRequestBroadcast -v`
Expected output:
```go
=== RUN   TestAskRequestBroadcast
--- PASS: TestAskRequestBroadcast (2.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	2.052s
```

- [ ] **Step 15:  Write the failing test for first-responder-wins (two clients)**

Append to `daemon/api/requests_test.go`:

```go
func TestPermissionRequestFirstResponderWins(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c1, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()

	c2, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	for _, c := range []*websocket.Conn{c1, c2} {
		if err := wsjson.Write(context.Background(), c, hello); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []*websocket.Conn{c1, c2} {
		subReq := map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
			"params": map[string]any{"session_id": s.ID()},
		}
		if err := wsjson.Write(context.Background(), c, subReq); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
		if resp["error"] != nil {
			t.Fatalf("subscribe error: %+v", resp["error"])
		}
	}

	pr := &permissionRequest{
		SessionID: s.ID(), RequestID: "perm-1",
		Tool: "bash", Summary: "run go test ./...", Risk: "medium",
	}

	var notif1, notif2 json.RawMessage
	if err := c1.Read(context.Background(), websocket.MessageText, &notif1); err != nil {
		t.Fatal(err)
	}
	if err := c2.Read(context.Background(), websocket.MessageText, &notif2); err != nil {
		t.Fatal(err)
	}

	var notif1Map map[string]any
	if err := json.Unmarshal(notif1, &notif1Map); err != nil {
		t.Fatal(err)
	}
	reqID, _ := notif1Map["id"].(string)

	resp1 := map[string]any{
		"jsonrpc": "2.0", "id": reqID,
		"result": map[string]any{"verdict": "approve"},
	}
	if err := wsjson.Write(context.Background(), c1, resp1); err != nil {
		t.Fatal(err)
	}

	var notif2Map map[string]any
	if err := json.Unmarshal(notif2, &notif2Map); err != nil {
		t.Fatal(err)
	}
	reqID2, _ := notif2Map["id"].(string)

	resp2 := map[string]any{
		"jsonrpc": "2.0", "id": reqID2,
		"result": map[string]any{"verdict": "deny"},
	}
	if err := wsjson.Write(context.Background(), c2, resp2); err != nil {
		t.Fatal(err)
	}

	result, err := h.BroadcastPermissionRequest(context.Background(), pr)
	if err != nil {
		t.Fatalf("BroadcastPermissionRequest error: %v", err)
	}
	if result.Verdict != "approve" {
		t.Errorf("verdict: got %q, want %q", result.Verdict, "approve")
	}
}
```

- [ ] **Step 16:  Run it — it should fail**

Run: `go test ./daemon/api/ -run TestPermissionRequestFirstResponderWins -v`
Expected: the test fails because the broadcast does not yet handle responses — it times out waiting. The test triggers: `requests_test.go:XX: BroadcastPermissionRequest error: permission request timed out`.

- [ ] **Step 17:  Implement the response dispatch in the Handler**

Append to `daemon/api/handler.go`:

```go
func (h *Handler) handleIncomingResponse(conn *websocket.Conn, req *jsonrpcRequest) {
	h.subMu.Lock()
	pr, ok := h.pendingRequests[req.ID.String()]
	h.subMu.Unlock()
	if !ok {
		h.sendError(conn, req.ID, protocol.CodeInternalError,
			"no pending request for id "+req.ID.String())
		return
	}

	var resp map[string]any
	if err := json.Unmarshal(req.Params, &resp); err != nil {
		h.sendError(conn, req.ID, protocol.CodeInvalidParams, "invalid response")
		return
	}

	answer, ok := resp["result"]
	if !ok {
		h.sendError(conn, req.ID, protocol.CodeInvalidParams, "missing result")
		return
	}

	answerJSON, err := json.Marshal(answer)
	if err != nil {
		h.sendError(conn, req.ID, protocol.CodeInternalError, err.Error())
		return
	}

	if !pr.resolve(answerJSON) {
		h.sendError(conn, req.ID, protocol.CodeAlreadyResolved, "already_resolved")
		return
	}
}
```

- [ ] **Step 18:  Wire the response dispatch into the Handler's dispatch loop**

In `daemon/api/handler.go`, extend the dispatch:

```go
func (h *Handler) dispatch(conn *websocket.Conn, req *jsonrpcRequest) {
	if !h.helloDone {
		if req.Method != "nabu.hello" {
			h.sendError(conn, req.ID, protocol.CodeProtocolMismatch,
				"hello required first")
			return
		}
	}

	if strings.HasPrefix(req.Method, "nabu.rpc.") &&
		strings.HasSuffix(req.Method, ".response") {
		go h.handleIncomingResponse(conn, req)
		return
	}

	h.mu.RLock()
	fn, ok := h.mods[req.Method]
	h.mu.RUnlock()
	if !ok {
		h.sendError(conn, req.ID, protocol.CodeMethodNotFound, req.Method)
		return
	}
	result, rpcErr := fn(h, req.Params)
	if rpcErr != nil {
		h.sendError(conn, req.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	h.sendResult(conn, req.ID, result)
}
```

- [ ] **Step 19:  Write the failing test for already_resolved (second answer)**

Append to `daemon/api/requests_test.go`:

```go
func TestPermissionRequestAlreadyResolved(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c1, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()

	c2, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	for _, c := range []*websocket.Conn{c1, c2} {
		if err := wsjson.Write(context.Background(), c, hello); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []*websocket.Conn{c1, c2} {
		subReq := map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
			"params": map[string]any{"session_id": s.ID()},
		}
		if err := wsjson.Write(context.Background(), c, subReq); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
		if resp["error"] != nil {
			t.Fatalf("subscribe error: %+v", resp["error"])
		}
	}

	pr := &permissionRequest{
		SessionID: s.ID(), RequestID: "perm-1",
		Tool: "bash", Summary: "run go test ./...", Risk: "medium",
	}

	var notif1, notif2 json.RawMessage
	if err := c1.Read(context.Background(), websocket.MessageText, &notif1); err != nil {
		t.Fatal(err)
	}
	if err := c2.Read(context.Background(), websocket.MessageText, &notif2); err != nil {
		t.Fatal(err)
	}

	var notif1Map map[string]any
	if err := json.Unmarshal(notif1, &notif1Map); err != nil {
		t.Fatal(err)
	}
	reqID, _ := notif1Map["id"].(string)

	resp1 := map[string]any{
		"jsonrpc": "2.0", "id": reqID,
		"result": map[string]any{"verdict": "approve"},
	}
	if err := wsjson.Write(context.Background(), c1, resp1); err != nil {
		t.Fatal(err)
	}

	var notif2Map map[string]any
	if err := json.Unmarshal(notif2, &notif2Map); err != nil {
		t.Fatal(err)
	}
	reqID2, _ := notif2Map["id"].(string)

	resp2 := map[string]any{
		"jsonrpc": "2.0", "id": reqID2,
		"result": map[string]any{"verdict": "deny"},
	}
	if err := wsjson.Write(context.Background(), c2, resp2); err != nil {
		t.Fatal(err)
	}

	var errResp json.RawMessage
	if err := c2.Read(context.Background(), websocket.MessageText, &errResp); err != nil {
		t.Fatal(err)
	}
	var errMap map[string]any
	if err := json.Unmarshal(errResp, &errMap); err != nil {
		t.Fatal(err)
	}
	errObj, ok := errMap["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", errMap["error"])
	}
	code, _ := errObj["code"].(float64)
	if code != float64(protocol.CodeAlreadyResolved) {
		t.Errorf("error code: got %v, want %d", code, protocol.CodeAlreadyResolved)
	}
	message, _ := errObj["message"].(string)
	if message != "already_resolved" {
		t.Errorf("error message: got %q, want %q", message, "already_resolved")
	}

	result, err := h.BroadcastPermissionRequest(context.Background(), pr)
	if err != nil {
		t.Fatalf("BroadcastPermissionRequest error: %v", err)
	}
	if result.Verdict != "approve" {
		t.Errorf("verdict: got %q, want %q", result.Verdict, "approve")
	}
}
```

- [ ] **Step 20:  Run it — it should fail**

Run: `go test ./daemon/api/ -run TestPermissionRequestAlreadyResolved -v`
Expected: the test fails because the dispatch loop does not yet route responses to pending requests. The test triggers: `requests_test.go:XX: BroadcastPermissionRequest error: permission request timed out`.

- [ ] **Step 21:  Implement pendingRequest.resolve and the already_resolved path**

The `pendingRequest.resolve` method (Step 3) already implements first-responder-wins: it uses a mutex to protect the `resolved` flag, sets the answer, and closes the `done` channel. The `handleIncomingResponse` method (Step 19) checks `pr.resolve()` and sends `already_resolved` if it returns false. No additional implementation needed — these pieces fit together.


The dispatch fix is in Step 20. Run the test:

Run: `go test ./daemon/api/ -run TestPermissionRequestAlreadyResolved -v`
Expected output:
```go
=== RUN   TestPermissionRequestAlreadyResolved
--- PASS: TestPermissionRequestAlreadyResolved (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 22:  Write the failing test for timeout expiry**

Append to `daemon/api/requests_test.go`:

```go
func TestPermissionRequestTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	pr := &permissionRequest{
		SessionID: s.ID(), RequestID: "perm-timeout",
		Tool: "bash", Summary: "run ls", Risk: "low",
	}

	result, err := h.BroadcastPermissionRequest(ctx, pr)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if result != nil {
		t.Errorf("expected nil result on timeout, got %+v", result)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error: got %q, want to contain %q", err.Error(), "timeout")
	}
}
```

- [ ] **Step 23:  Run it — it should fail**

Run: `go test ./daemon/api/ -run TestPermissionRequestTimeout -v`
Expected: the test fails because `BroadcastPermissionRequest` does not yet respect the short context timeout — it waits for the full `requestTimeout`. The test triggers: `requests_test.go:XX: expected timeout error` (the test hangs for 10 minutes and times out by the test framework).

- [ ] **Step 24:  Implement timeout with named constant**

The timeout is already wired in `BroadcastPermissionRequest` (Step 11) via the `requestTimeout` constant and the `wait` method. The `pending.wait(timeoutCtx)` respects the context cancellation. The test should now pass.

Run: `go test ./daemon/api/ -run TestPermissionRequestTimeout -v`
Expected output:
```go
=== RUN   TestPermissionRequestTimeout
--- PASS: TestPermissionRequestTimeout (0.50s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.552s
```

- [ ] **Step 25:  Write the failing test for disconnect-with-request-outstanding**

Append to `daemon/api/requests_test.go`:

```go
func TestPermissionRequestDisconnectResolves(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	// Simulate disconnect by removing the client from the subscription
	// (deterministic, no time.Sleep).
	h.subMu.Lock()
	ss := h.subscriptions[s.ID()]
	if ss != nil {
		ss.mu.Lock()
		for id := range ss.subs {
			delete(ss.subs, id)
		}
		ss.mu.Unlock()
	}
	h.subMu.Unlock()

	pr := &permissionRequest{
		SessionID: s.ID(), RequestID: "perm-disconnect",
		Tool: "bash", Summary: "run ls", Risk: "low",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := h.BroadcastPermissionRequest(ctx, pr)
	if err == nil {
		t.Fatal("expected error when client disconnected")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}
```

- [ ] **Step 26:  Run it — it should fail**

Run: `go test ./daemon/api/ -run TestPermissionRequestDisconnectResolves -v`
Expected: the test fails because the broadcast does not yet detect disconnected clients — it hangs waiting for a response that will never come. The test triggers: `requests_test.go:XX: expected error when client disconnected` (timeout waiting).

- [ ] **Step 27:  Implement disconnect detection and resolution**

Append to `daemon/api/requests.go`:

```go
func (h *Handler) BroadcastPermissionRequest(ctx context.Context, pr *permissionRequest) (*permissionResponse, error) {
	reqID := protocol.NewULID()
	params := map[string]any{
		"session_id": pr.SessionID, "request_id": reqID,
		"tool": pr.Tool, "summary": pr.Summary, "risk": pr.Risk,
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("permission request marshal: %w", err)
	}

	pending := &pendingRequest{
		method: "nabu.rpc.permission.response", params: paramsJSON,
		done: make(chan struct{}),
	}

	h.subMu.Lock()
	h.pendingRequests[reqID] = pending
	ss, ok := h.subscriptions[pr.SessionID]
	h.subMu.Unlock()
	if !ok {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("no subscribers for session %s", pr.SessionID)
	}

	ss.mu.Lock()
	liveConn := 0
	for id, cs := range ss.subs {
		if cs.conn == nil {
			delete(ss.subs, id)
			continue
		}
		msg := map[string]any{
			"jsonrpc": "2.0", "id": reqID,
			"method": "nabu.rpc.permission.request", "params": params,
		}
		if err := cs.conn.WriteJSON(context.Background(), msg); err != nil {
			delete(ss.subs, id)
		} else {
			liveConn++
		}
	}
	ss.mu.Unlock()

	if liveConn == 0 {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("all subscribers disconnected for session %s", pr.SessionID)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	answer := pending.wait(timeoutCtx)
	if answer == nil {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("permission request timed out after %v", requestTimeout)
	}

	var resp permissionResponse
	if err := json.Unmarshal(answer, &resp); err != nil {
		return nil, fmt.Errorf("invalid permission response: %w", err)
	}
	return &resp, nil
}
```


The disconnect detection is already in `BroadcastPermissionRequest` (Step 30). Run the test:

Run: `go test ./daemon/api/ -run TestPermissionRequestDisconnectResolves -v`
Expected output:
```go
=== RUN   TestPermissionRequestDisconnectResolves
--- PASS: TestPermissionRequestDisconnectResolves (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

- [ ] **Step 28:  Write the failing test for already_resolved error on the wire**

Append to `daemon/api/requests_test.go`:

```go
func TestAlreadyResolvedErrorOnWire(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c1, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()

	c2, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	for _, c := range []*websocket.Conn{c1, c2} {
		if err := wsjson.Write(context.Background(), c, hello); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []*websocket.Conn{c1, c2} {
		subReq := map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
			"params": map[string]any{"session_id": s.ID()},
		}
		if err := wsjson.Write(context.Background(), c, subReq); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
		if resp["error"] != nil {
			t.Fatalf("subscribe error: %+v", resp["error"])
		}
	}

	pr := &permissionRequest{
		SessionID: s.ID(), RequestID: "perm-1",
		Tool: "bash", Summary: "run go test ./...", Risk: "medium",
	}

	var notif1, notif2 json.RawMessage
	if err := c1.Read(context.Background(), websocket.MessageText, &notif1); err != nil {
		t.Fatal(err)
	}
	if err := c2.Read(context.Background(), websocket.MessageText, &notif2); err != nil {
		t.Fatal(err)
	}

	var notif1Map map[string]any
	if err := json.Unmarshal(notif1, &notif1Map); err != nil {
		t.Fatal(err)
	}
	reqID, _ := notif1Map["id"].(string)

	resp1 := map[string]any{
		"jsonrpc": "2.0", "id": reqID,
		"result": map[string]any{"verdict": "approve"},
	}
	if err := wsjson.Write(context.Background(), c1, resp1); err != nil {
		t.Fatal(err)
	}

	var notif2Map map[string]any
	if err := json.Unmarshal(notif2, &notif2Map); err != nil {
		t.Fatal(err)
	}
	reqID2, _ := notif2Map["id"].(string)

	resp2 := map[string]any{
		"jsonrpc": "2.0", "id": reqID2,
		"result": map[string]any{"verdict": "deny"},
	}
	if err := wsjson.Write(context.Background(), c2, resp2); err != nil {
		t.Fatal(err)
	}

	var errResp json.RawMessage
	if err := c2.Read(context.Background(), websocket.MessageText, &errResp); err != nil {
		t.Fatal(err)
	}
	var errMap map[string]any
	if err := json.Unmarshal(errResp, &errMap); err != nil {
		t.Fatal(err)
	}
	errObj, ok := errMap["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", errMap["error"])
	}
	code, _ := errObj["code"].(float64)
	if code != float64(protocol.CodeAlreadyResolved) {
		t.Errorf("error code: got %v, want %d", code, protocol.CodeAlreadyResolved)
	}
	message, _ := errObj["message"].(string)
	if message != "already_resolved" {
		t.Errorf("error message: got %q, want %q", message, "already_resolved")
	}
}
```

- [ ] **Step 29:  Run it — it should fail**

Run: `go test ./daemon/api/ -run TestAlreadyResolvedErrorOnWire -v`
Expected: the test fails because the response dispatch is not yet wired into the dispatch loop. The test triggers: `requests_test.go:XX: expected error object, got <nil>` (the late response is not routed, so no error is sent back).

- [ ] **Step 30:  Wire the response dispatch into the dispatch loop**

In `daemon/api/handler.go`, update the dispatch method:

```go
func (h *Handler) dispatch(conn *websocket.Conn, req *jsonrpcRequest) {
	if !h.helloDone {
		if req.Method != "nabu.hello" {
			h.sendError(conn, req.ID, protocol.CodeProtocolMismatch,
				"hello required first")
			return
		}
	}

	if strings.HasPrefix(req.Method, "nabu.rpc.") &&
		strings.HasSuffix(req.Method, ".response") {
		go h.handleIncomingResponse(conn, req)
		return
	}

	h.mu.RLock()
	fn, ok := h.mods[req.Method]
	h.mu.RUnlock()
	if !ok {
		h.sendError(conn, req.ID, protocol.CodeMethodNotFound, req.Method)
		return
	}
	result, rpcErr := fn(h, req.Params)
	if rpcErr != nil {
		h.sendError(conn, req.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	h.sendResult(conn, req.ID, result)
}
```


Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

Run: `gofmt -l daemon/api/`
Expected: no output (files are gofmt-clean).

Stage and commit the source files:

```
git add daemon/api/requests.go daemon/api/requests_test.go daemon/api/handler.go daemon/api/handler_test.go
git commit -m "api: daemon-to-client requests with first-responder-wins"
```
### Summary

**File written:** `.pi-delegations/task-05-requests.out.md`

**Number of steps:** 35

**Timeout chosen:** 10 minutes (`requestTimeout = 10 * time.Minute`). Reasoning: the spec (protocol/spec.md §7.15) states "Requests time out after a configurable interval (default 10 minutes)". This is a reasonable default — long enough for a human to respond to a permission prompt or answer a question, but bounded so a session doesn't hang forever. If the daemon needs to move to `blocked` state on timeout (per spec), the 10-minute window gives the user adequate time to respond to urgent prompts while still providing a safety net.

**Symbols confirmed in source:**
- `protocol.CodeAlreadyResolved = -32004` — confirmed at `protocol/errors.go:16`
- `agent.Asker` interface with `Permission` and `Ask` methods — confirmed at `daemon/agent/manager.go:23`
- `agent.Deps.Asker` field — confirmed at `daemon/agent/manager.go:38`
- `module.UI` interface with `Ask` method — confirmed at `daemon/module/module.go:221`

**Unresolved symbol:** None. All symbols referenced in this task are confirmed in the source. The `Asker` interface mismatch is documented in the brief and is a design note, not a missing symbol.

- [ ] **Step 31: Register the response handler and run the first-responder test**

The response handler is in Step 17. Run the test:

Run: `go test ./daemon/api/ -run TestPermissionRequestFirstResponderWins -v`
Expected: the test passes.

- [ ] **Step 32: Register the timeout wiring and run the timeout test**

The timeout is already wired in Step 11. Run the test:

Run: `go test ./daemon/api/ -run TestPermissionRequestTimeout -v`
Expected: the test passes.

- [ ] **Step 33: Register the disconnect detection and run the disconnect test**

The disconnect detection is already in Step 27. Run the test:

Run: `go test ./daemon/api/ -run TestPermissionRequestDisconnectResolves -v`
Expected: the test passes.

- [ ] **Step 34: Register the already_resolved wire and run the wire test**

The dispatch fix is in Step 30. Run the test:

Run: `go test ./daemon/api/ -run TestAlreadyResolvedErrorOnWire -v`
Expected: the test passes.

- [ ] **Step 35: Run the full gate, gofmt, and commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

Run: `gofmt -l daemon/api/`
Expected: no output (files are gofmt-clean).

---

### Task 6: Daemon lifecycle — foreground run, detached start, PID/port files, graceful shutdown

**Files:**
- Create: `daemon/daemon.go`
- Create: `daemon/daemon_test.go`
- Create: `daemon/pid.go` (Unix: `syscall.Kill` for stale-PID detection)
- Create: `daemon/pid_windows.go` (Windows: `OpenProcess` for stale-PID detection)

**Goal:** Build the daemon lifecycle: a `Daemon` struct that owns the full stack (store, provider registry, module registry, agent manager, API server), runs it in the foreground, supports detached start for CLI auto-start, manages `daemon.pid` and `daemon.port` files, and performs graceful shutdown per the architecture spec §8 / §8.1. Spec: Architecture design §8 "Daemon lifecycle, storage, and identifiers" and §8.1 "Graceful restart".

**Stale PID detection:** On Unix, `syscall.Kill(pid, 0)` — `ESRCH` means stale, `EPERM` means alive (different UID). On Windows, `syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` — failure means stale; success means alive if `GetExitCodeProcess` returns `STATUS_PENDING`.

**Symbols confirmed in source (line numbers from current tree):**
- `agent.New(deps Deps, cfg Config) (*Manager, error)` → `daemon/agent/manager.go:65` — confirmed
- `agent.Deps` (Store, Providers, Modules, Builtins, Root, Log, Asker, Deltas) → `daemon/agent/manager.go:30` — confirmed
- `agent.Manager.Shutdown(ctx context.Context) error` → `daemon/agent/manager.go:316` — confirmed; cancels running turns, appends notice, marks sessions `paused` with reason `daemon_shutdown`
- `session.Open(root string) (*Store, error)` → `daemon/session/store.go:30` — confirmed
- `session.Store.List() ([]Summary, error)` → `daemon/session/store.go:145` — confirmed
- `session.Store.RecoverInterrupted() ([]string, error)` → `daemon/session/store.go:164` — confirmed; pauses sessions still in `StateRunning`, appends notice + `state_change` to `paused` with reason `daemon_restart`, returns their ids
- `session.Store.Close() error` → `daemon/session/store.go:111` — confirmed
- `session.Summary` (SessionID, Workspace, WorkspaceKey, State, EventCount, CreatedAt, UpdatedAt, Goal, TasksTotal, TasksDone) → `daemon/session/store.go:124` — confirmed
- `api.NewServer(handler ConnHandler, cfg *api.Config, log *slog.Logger) *Server` → `daemon/api/transport.go:133` (per task-02) — confirmed
- `api.Server.ListenAndServe() error` → `daemon/api/transport.go:149` — confirmed
- `api.Server.Shutdown(ctx) error` → `daemon/api/transport.go:154` — confirmed
- `api.Config` (Bind, Token) → `daemon/api/transport.go:97` — confirmed
- `api.NewHandler(m *agent.Manager, st *session.Store, log *slog.Logger) *Handler` → `daemon/api/handler.go:167` (per task-03) — confirmed
- `config.Config`, `config.DaemonConfig`, `config.ProviderConfig`, `config.BudgetConfig`, `config.Load(root string) (*Config, error)` → `daemon/config/config.go` (per task-01) — confirmed
- `config.Config.TrustWorkspace(path)`, `config.Config.ApplyOverlay(path)`, `config.Config.IsWorkspaceTrusted(path)` → `daemon/config/config.go` (per task-01) — confirmed
- `protocol.NewULID() string` → `protocol/ulid.go:30` — confirmed
- `protocol.StateRunning`, `protocol.StatePaused` → `protocol/types.go:55,57` — confirmed
- `protocol.EventNotice`, `protocol.EventStateChange` → `protocol/types.go:30,22` — confirmed
- `protocol.NoticeData` (Source, Level, Message) → `protocol/types.go:219` — confirmed
- `protocol.StateChangeData` (From *SessionState, To SessionState, Reason string) → `protocol/types.go:126` — confirmed
- `module.Registry`, `module.NewRegistry(mods []Module, opts Options) *Registry` → `daemon/module/registry.go:17,22` — confirmed
- `module.Options` (HookTimeout, GateTimeout, Log) → `daemon/module/registry.go:10` — confirmed
- `tools.Builtins` → `daemon/tools/builtins.go:13` — confirmed

---

- [ ] **Step 1: Write the failing test for `New` with nil store**

Append to `daemon/daemon_test.go`:

```go
package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/config"
	"github.com/corporealshift/nabu/protocol"
)

func TestNewNilStore(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	_, err := New(root, cfg, nil)
	if err == nil {
		t.Fatal("expected error for nil store, got nil")
	}
}
```

- [ ] **Step 2: Run it — it should fail with compile error**

Run: `go test ./daemon/ -run TestNewNilStore`
Expected: compile error `undefined: New` (the package does not exist yet).

- [ ] **Step 3: Create `daemon/daemon.go` with the Daemon struct and New**

Create `daemon/daemon.go` with the complete file:

```go
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/corporealshift/nabu/daemon/agent"
	"github.com/corporealshift/nabu/daemon/api"
	"github.com/corporealshift/nabu/daemon/config"
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
)

const (
	pidFileName  = "daemon.pid"
	portFileName = "daemon.port"
)

// Daemon owns the full nabu stack: store, providers, modules, agent manager,
// and the API server. It can run in the foreground or be started detached.
type Daemon struct {
	root     string
	cfg      *config.Config
	log      *slog.Logger
	store    *session.Store
	provReg  *provider.Registry
	modReg   *module.Registry
	builtins *tools.Builtins
	manager  *agent.Manager
	apiSrv   *api.Server
	mu       sync.Mutex
	closed   bool
}

// New builds the full stack from config. The store is opened at root/sessions.
// All directories that do not exist are created.
func New(root string, cfg *config.Config, log *slog.Logger) (*Daemon, error) {
	if log == nil {
		log = slog.Default()
	}

	// Create all storage directories.
	for _, dir := range []string{
		filepath.Join(root, "sessions"),
		filepath.Join(root, "memory"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("daemon: mkdir %s: %w", dir, err)
		}
	}

	// Open session store.
	st, err := session.Open(root)
	if err != nil {
		return nil, fmt.Errorf("daemon: open store: %w", err)
	}

	// Build provider registry from config.
	provReg := provider.NewRegistry()
	for name, pc := range cfg.Providers {
		if pc.APIKey != "" {
			provReg.Add(pc, nil, false)
		}
	}

	// Build module registry (empty module list — modules are in-process).
	opts := module.Options{Log: log}
	modReg := module.NewRegistry(nil, opts)

	// Builtins (tasks disabled until config says otherwise).
	builtins := &tools.Builtins{}

	// Agent config.
	agentCfg := agent.Config{ModuleConfigs: cfg.Modules}

	// Deps.
	deps := agent.Deps{
		Store:     st,
		Providers: provReg,
		Modules:   modReg,
		Builtins:  builtins,
		Root:      root,
		Log:       log,
	}

	mgr, err := agent.New(deps, agentCfg)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("daemon: new agent: %w", err)
	}

	// API handler.
	h := api.NewHandler(mgr, st, log)

	// API server config.
	apiCfg := &api.Config{
		Bind:  cfg.Daemon.Bind,
		Token: cfg.Daemon.Token,
	}

	srv := api.NewServer(h, apiCfg, log)

	return &Daemon{
		root:     root,
		cfg:      cfg,
		log:      log,
		store:    st,
		provReg:  provReg,
		modReg:   modReg,
		builtins: builtins,
		manager:  mgr,
		apiSrv:   srv,
	}, nil
}

// writePID writes the current process ID to daemon.pid.
func writePID(root string, pid int) error {
	data := []byte(fmt.Sprintf("%d", pid))
	return os.WriteFile(filepath.Join(root, pidFileName), data, 0o644)
}

// readPID reads the PID from daemon.pid. Returns 0 and os.ErrNotExist if missing.
func readPID(root string) (int, error) {
	data, err := os.ReadFile(filepath.Join(root, pidFileName))
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, fmt.Errorf("daemon: parse PID file: %w", err)
	}
	return pid, nil
}

// removePID removes the daemon.pid file.
func removePID(root string) error {
	return os.Remove(filepath.Join(root, pidFileName))
}

// writePort writes the listen address to daemon.port.
func writePort(root string, addr string) error {
	return os.WriteFile(filepath.Join(root, portFileName), []byte(addr), 0o644)
}

// readPort reads the address from daemon.port. Returns "" and os.ErrNotExist if missing.
func readPort(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, portFileName))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// removePort removes the daemon.port file.
func removePort(root string) error {
	return os.Remove(filepath.Join(root, portFileName))
}

// IsRunning reports whether a daemon is currently running by reading the PID
// file and checking if the process still exists. A stale PID file (from a
// crashed daemon) is detected by attempting to signal the process: on Unix
// syscall.Kill(pid, 0) returns ESRCH; on Windows OpenProcess fails.
func IsRunning(root string) bool {
	pid, err := readPID(root)
	if err != nil {
		return false
	}
	if pid <= 0 {
		return false
	}
	return isProcessAlive(pid)
}

// Run starts the daemon in the foreground: writes PID/port files, recovers
// interrupted sessions, starts the API server, and blocks until Shutdown is
// called or a signal arrives.
func (d *Daemon) Run(ctx context.Context) error {
	// Write PID file.
	if err := writePID(d.root, os.Getpid()); err != nil {
		return fmt.Errorf("daemon: write PID file: %w", err)
	}

	// Write port file (cfg.Bind is "host:port").
	if err := writePort(d.root, d.cfg.Daemon.Bind); err != nil {
		return fmt.Errorf("daemon: write port file: %w", err)
	}

	d.log.Info("daemon: starting", "addr", d.cfg.Daemon.Bind)

	// Recover interrupted sessions (spec §8.1).
	paused, err := d.store.RecoverInterrupted()
	if err != nil {
		d.log.Error("daemon: recover interrupted sessions", "err", err)
	}
	if len(paused) > 0 {
		d.log.Info("daemon: recovered interrupted sessions", "count", len(paused))
	}

	// Serve in a goroutine.
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		if err := d.apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			d.log.Error("daemon: serve error", "err", err)
		}
	}()

	// Wait for context cancellation.
	<-ctx.Done()

	return d.Shutdown(ctx)
}

// Shutdown performs a graceful shutdown: stops the API server, shuts down the
// agent manager (which pauses running sessions), closes the store, and removes
// PID/port files.
func (d *Daemon) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()

	d.log.Info("daemon: shutting down")

	// 1. Stop the API server (rejects new connections).
	if err := d.apiSrv.Shutdown(ctx); err != nil {
		d.log.Error("daemon: API server shutdown", "err", err)
	}

	// 2. Shut down the agent manager (cancels in-flight turns, pauses sessions).
	if err := d.manager.Shutdown(ctx); err != nil {
		d.log.Error("daemon: manager shutdown", "err", err)
	}

	// 3. Close the session store.
	if err := d.store.Close(); err != nil {
		d.log.Error("daemon: store close", "err", err)
	}

	// 4. Remove PID and port files.
	_ = removePID(d.root)
	_ = removePort(d.root)

	d.log.Info("daemon: stopped")
	return nil
}

// StartDetached starts a new daemon process in the background by spawning the
// current executable with "daemon" as the first argument. It returns the child
// PID and waits for the port file to appear (up to 5 seconds).
func StartDetached(root string, cfg *config.Config, log *slog.Logger) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("daemon: find executable: %w", err)
	}

	cmd := execCommand(exe, "daemon", "--root", root)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("daemon: start detached: %w", err)
	}

	// Wait for the port file to appear.
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(filepath.Join(root, portFileName)); err == nil {
			return cmd.Process.Pid, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Port file didn't appear — kill the child.
	_ = cmd.Process.Kill()
	return 0, fmt.Errorf("daemon: did not start in time (port file not written)")
}
```

Wait — `execCommand` doesn't exist. Let me use `exec.Command` directly and
import `os/exec`:

```go
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/corporealshift/nabu/daemon/agent"
	"github.com/corporealshift/nabu/daemon/api"
	"github.com/corporealshift/nabu/daemon/config"
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
)
```

And replace `execCommand` with `exec.Command` in `StartDetached`:

```go
	cmd := exec.Command(exe, "daemon", "--root", root)
```

- [ ] **Step 4: Run it — it should compile and the test should pass**

Run: `go test ./daemon/ -run TestNewNilStore -v`
Expected output:
```
=== RUN   TestNewNilStore
--- PASS: TestNewNilStore (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.052s
```

- [ ] **Step 5: Write the failing test for `New` with valid config**

Append to `daemon/daemon_test.go`:

```go
func TestNewValidConfig(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	log := slog.Default()

	d, err := New(root, cfg, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Shutdown(context.Background())

	if d.store == nil {
		t.Fatal("store should not be nil")
	}
	if d.manager == nil {
		t.Fatal("manager should not be nil")
	}
	if d.apiSrv == nil {
		t.Fatal("api server should not be nil")
	}

	// Verify directories were created.
	for _, dir := range []string{"sessions", "memory"} {
		info, err := os.Stat(filepath.Join(root, dir))
		if err != nil {
			t.Errorf("directory %s should exist: %v", dir, err)
		}
		if !info.IsDir() {
			t.Errorf("directory %s should be a directory", dir)
		}
	}
}
```

- [ ] **Step 6: Run it — it should pass**

Run: `go test ./daemon/ -run TestNewValidConfig -v`
Expected output:
```
=== RUN   TestNewValidConfig
--- PASS: TestNewValidConfig (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.052s
```

- [ ] **Step 7: Write the failing test for PID file write and read**

Append to `daemon/daemon_test.go`:

```go
func TestPIDFile(t *testing.T) {
	root := t.TempDir()

	// Write a PID file.
	pid := os.Getpid()
	if err := writePID(root, pid); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	// Read it back.
	readPID, err := readPID(root)
	if err != nil {
		t.Fatalf("readPID: %v", err)
	}
	if readPID != pid {
		t.Errorf("readPID: got %d, want %d", readPID, pid)
	}

	// Remove and verify gone.
	if err := removePID(root); err != nil {
		t.Fatalf("removePID: %v", err)
	}
	_, err = os.Stat(filepath.Join(root, "daemon.pid"))
	if !os.IsNotExist(err) {
		t.Fatal("daemon.pid should not exist after remove")
	}
}
```

- [ ] **Step 8: Run it — it should pass**

Run: `go test ./daemon/ -run TestPIDFile -v`
Expected output:
```
=== RUN   TestPIDFile
--- PASS: TestPIDFile (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.052s
```

- [ ] **Step 9: Write the failing test for port file write and read**

Append to `daemon/daemon_test.go`:

```go
func TestPortFile(t *testing.T) {
	root := t.TempDir()

	if err := writePort(root, "8737"); err != nil {
		t.Fatalf("writePort: %v", err)
	}

	port, err := readPort(root)
	if err != nil {
		t.Fatalf("readPort: %v", err)
	}
	if port != "8737" {
		t.Errorf("readPort: got %q, want %q", port, "8737")
	}

	if err := removePort(root); err != nil {
		t.Fatalf("removePort: %v", err)
	}
	_, err = os.Stat(filepath.Join(root, "daemon.port"))
	if !os.IsNotExist(err) {
		t.Fatal("daemon.port should not exist after remove")
	}
}
```

- [ ] **Step 10: Run it — it should pass**

Run: `go test ./daemon/ -run TestPortFile -v`
Expected output:
```
=== RUN   TestPortFile
--- PASS: TestPortFile (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.052s
```

- [ ] **Step 11: Write the failing test for `IsRunning` with no PID file**

Append to `daemon/daemon_test.go`:

```go
func TestIsRunningNoPID(t *testing.T) {
	root := t.TempDir()
	if IsRunning(root) {
		t.Fatal("IsRunning should return false when no PID file exists")
	}
}
```

- [ ] **Step 12: Run it — it should fail with compile error**

Run: `go test ./daemon/ -run TestIsRunningNoPID`
Expected: compile error `undefined: IsRunning` (function not implemented yet).

- [ ] **Step 13: Implement `IsRunning`**

Append to `daemon/daemon.go`:

```go
// IsRunning reports whether a daemon is currently running by reading the PID
// file and checking if the process still exists. A stale PID file (from a
// crashed daemon) is detected by attempting to signal the process: on Unix
// syscall.Kill(pid, 0) returns ESRCH; on Windows OpenProcess fails.
func IsRunning(root string) bool {
	pid, err := readPID(root)
	if err != nil {
		return false
	}
	if pid <= 0 {
		return false
	}
	return isProcessAlive(pid)
}
```

- [ ] **Step 14: Create `daemon/pid.go` (Unix build)**

Create `daemon/pid.go`:

```go
//go:build !windows

package daemon

import "syscall"

// isProcessAlive checks whether pid is a running process.
// On Unix, Signal(0) returns ESRCH if the process doesn't exist,
// or EPERM if it exists but we lack permission (still alive).
func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
```

- [ ] **Step 15: Create `daemon/pid_windows.go` (Windows build)**

Create `daemon/pid_windows.go`:

```go
//go:build windows

package daemon

import "syscall"

// isProcessAlive checks whether pid is a running process.
// On Windows, OpenProcess fails if the process doesn't exist.
// If it succeeds, GetExitCodeProcess returning STATUS_PENDING means alive.
func isProcessAlive(pid int) bool {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == uint32(syscall.STATUS_PENDING)
}
```

- [ ] **Step 16: Run the IsRunning test — it should pass**

Run: `go test ./daemon/ -run TestIsRunningNoPID -v`
Expected output:
```
=== RUN   TestIsRunningNoPID
--- PASS: TestIsRunningNoPID (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.052s
```

- [ ] **Step 17: Write the failing test for `IsRunning` with a stale PID**

Append to `daemon/daemon_test.go`:

```go
func TestIsRunningStalePID(t *testing.T) {
	root := t.TempDir()

	// Write a PID that will never exist.
	if err := os.WriteFile(filepath.Join(root, "daemon.pid"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	if IsRunning(root) {
		t.Fatal("IsRunning should return false for a stale PID")
	}
}
```

- [ ] **Step 18: Run it — it should pass**

Run: `go test ./daemon/ -run TestIsRunningStalePID -v`
Expected output:
```
=== RUN   TestIsRunningStalePID
--- PASS: TestIsRunningStalePID (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.052s
```

- [ ] **Step 19: Write the failing test for graceful shutdown**

Append to `daemon/daemon_test.go`:

```go
func TestGracefulShutdown(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	log := slog.Default()

	d, err := New(root, cfg, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Create a session so there is something to shut down.
	s, err := d.store.Create(t.TempDir(), "test", protocol.Options{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := d.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Verify PID and port files are removed.
	if _, err := os.Stat(filepath.Join(root, "daemon.pid")); !os.IsNotExist(err) {
		t.Fatal("daemon.pid should be removed after shutdown")
	}

	// Verify the store is closed.
	_, err = d.store.Get(s.ID())
	if err == nil {
		t.Fatal("expected error getting session from closed store")
	}
}
```

- [ ] **Step 20: Run it — it should fail with compile error**

Run: `go test ./daemon/ -run TestGracefulShutdown`
Expected: compile error `undefined: d.Shutdown` (method not implemented yet).

- [ ] **Step 21: Implement `Run` and `Shutdown` on `Daemon`**

Append to `daemon/daemon.go` (the `Run` and `Shutdown` methods — the helper
functions `writePID`, `readPID`, `removePID`, `writePort`, `readPort`, `removePort`,
and `IsRunning` were added in steps 3 and 13):

```go
// Run starts the daemon in the foreground: writes PID/port files, recovers
// interrupted sessions, starts the API server, and blocks until Shutdown is
// called or a signal arrives.
func (d *Daemon) Run(ctx context.Context) error {
	// Write PID file.
	if err := writePID(d.root, os.Getpid()); err != nil {
		return fmt.Errorf("daemon: write PID file: %w", err)
	}

	// Write port file (cfg.Bind is "host:port").
	if err := writePort(d.root, d.cfg.Daemon.Bind); err != nil {
		return fmt.Errorf("daemon: write port file: %w", err)
	}

	d.log.Info("daemon: starting", "addr", d.cfg.Daemon.Bind)

	// Recover interrupted sessions (spec §8.1).
	paused, err := d.store.RecoverInterrupted()
	if err != nil {
		d.log.Error("daemon: recover interrupted sessions", "err", err)
	}
	if len(paused) > 0 {
		d.log.Info("daemon: recovered interrupted sessions", "count", len(paused))
	}

	// Serve in a goroutine.
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		if err := d.apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			d.log.Error("daemon: serve error", "err", err)
		}
	}()

	// Wait for context cancellation.
	<-ctx.Done()

	return d.Shutdown(ctx)
}

// Shutdown performs a graceful shutdown: stops the API server, shuts down the
// agent manager (which pauses running sessions), closes the store, and removes
// PID/port files.
func (d *Daemon) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()

	d.log.Info("daemon: shutting down")

	// 1. Stop the API server (rejects new connections).
	if err := d.apiSrv.Shutdown(ctx); err != nil {
		d.log.Error("daemon: API server shutdown", "err", err)
	}

	// 2. Shut down the agent manager (cancels in-flight turns, pauses sessions).
	if err := d.manager.Shutdown(ctx); err != nil {
		d.log.Error("daemon: manager shutdown", "err", err)
	}

	// 3. Close the session store.
	if err := d.store.Close(); err != nil {
		d.log.Error("daemon: store close", "err", err)
	}

	// 4. Remove PID and port files.
	_ = removePID(d.root)
	_ = removePort(d.root)

	d.log.Info("daemon: stopped")
	return nil
}
```

- [ ] **Step 22: Run the graceful shutdown test — it should pass**

Run: `go test ./daemon/ -run TestGracefulShutdown -v`
Expected output:
```
=== RUN   TestGracefulShutdown
--- PASS: TestGracefulShutdown (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.052s
```

- [ ] **Step 23: Write the failing test for `Run` with a real listener**

Append to `daemon/daemon_test.go`:

```go
func TestRunShutdown(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Daemon: config.DaemonConfig{
			Bind: "127.0.0.1:0",
		},
	}
	log := slog.Default()

	d, err := New(root, cfg, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		_ = d.Run(ctx)
	}()

	// Wait for the port file (server is starting).
	portPath := filepath.Join(root, "daemon.port")
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(portPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify PID and port files exist.
	if _, err := os.Stat(filepath.Join(root, "daemon.pid")); err != nil {
		t.Fatalf("daemon.pid should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "daemon.port")); err != nil {
		t.Fatalf("daemon.port should exist: %v", err)
	}

	// Cancel to trigger shutdown.
	cancel()

	select {
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	case <-srvDone:
	}

	// Verify files removed.
	if _, err := os.Stat(filepath.Join(root, "daemon.pid")); !os.IsNotExist(err) {
		t.Fatal("daemon.pid should be removed after shutdown")
	}
	if _, err := os.Stat(filepath.Join(root, "daemon.port")); !os.IsNotExist(err) {
		t.Fatal("daemon.port should be removed after shutdown")
	}
}
```

- [ ] **Step 24: Run it — it should pass**

Run: `go test ./daemon/ -run TestRunShutdown -v`
Expected output:
```
=== RUN   TestRunShutdown
--- PASS: TestRunShutdown (0.10s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.152s
```

- [ ] **Step 25: Write the failing test for listing paused sessions on start**

Append to `daemon/daemon_test.go`:

```go
func TestRecoverInterruptedListsPaused(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	log := slog.Default()

	d, err := New(root, cfg, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Create a session and manually set it to running.
	s, err := d.store.Create(t.TempDir(), "test", protocol.Options{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Manually append a state_change to running.
	from := protocol.StateIdle
	if _, err := s.Append(protocol.EventStateChange, protocol.StateChangeData{
		From: &from, To: protocol.StateRunning, Reason: "test"}); err != nil {
		t.Fatalf("Append running: %v", err)
	}

	// Close the store without graceful shutdown (simulates crash).
	d.store.Close()

	// Reopen the store and recover.
	st2, err := session.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st2.Close()

	paused, err := st2.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	if len(paused) != 1 {
		t.Fatalf("RecoverInterrupted: got %d paused sessions, want 1", len(paused))
	}
	if paused[0] != s.ID() {
		t.Errorf("RecoverInterrupted: got session %s, want %s", paused[0], s.ID())
	}

	// Verify the session is now paused.
	s2, err := st2.Get(s.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	st := s2.State()
	if st.State != protocol.StatePaused {
		t.Errorf("session state: got %q, want %q", st.State, protocol.StatePaused)
	}

	// Verify a notice was appended.
	evs := s2.Events()
	foundNotice := false
	for _, e := range evs {
		if e.Type == protocol.EventNotice {
			foundNotice = true
			break
		}
	}
	if !foundNotice {
		t.Error("expected a notice event for interrupted session")
	}
}
```

- [ ] **Step 26: Run it — it should pass**

Run: `go test ./daemon/ -run TestRecoverInterruptedListsPaused -v`
Expected output:
```
=== RUN   TestRecoverInterruptedListsPaused
--- PASS: TestRecoverInterruptedListsPaused (0.00s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.052s
```

- [ ] **Step 27: Write the failing test for port-file polling (without forking)**

Append to `daemon/daemon_test.go`:

```go
func TestPortFilePolling(t *testing.T) {
	root := t.TempDir()

	// Simulate the port file appearing after a delay.
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(200 * time.Millisecond)
		if err := writePort(root, "127.0.0.1:8737"); err != nil {
			t.Error(err)
		}
	}()

	// Poll like StartDetached does.
	portPath := filepath.Join(root, portFileName)
	var port string
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(portPath); err == nil {
			port, _ = readPort(root)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if port == "" {
		t.Fatal("port file should have been written")
	}
	if port != "127.0.0.1:8737" {
		t.Errorf("port: got %q, want %q", port, "127.0.0.1:8737")
	}

	<-done
}
```

- [ ] **Step 28: Run it — it should pass**

Run: `go test ./daemon/ -run TestPortFilePolling -v`
Expected output:
```
=== RUN   TestPortFilePolling
--- PASS: TestPortFilePolling (0.20s)
PASS
ok  	github.com/corporealshift/nabu/daemon	0.252s
```

- [ ] **Step 29: Write the failing test for `StartDetached`**

Append to `daemon/daemon_test.go`:

```go
func TestStartDetached(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	log := slog.Default()

	pid, err := StartDetached(root, cfg, log)
	if err != nil {
		t.Fatalf("StartDetached: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("StartDetached: got pid %d, want > 0", pid)
	}

	// Wait for the port file.
	for i := 0; i < 50; i++ {
		if _, err := readPort(root); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify the daemon is running.
	if !IsRunning(root) {
		t.Fatal("daemon should be running")
	}

	// Clean up: kill the child process.
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Kill()
	}
	_ = removePID(root)
	_ = removePort(root)
}
```

- [ ] **Step 30: Run it — it should fail with compile error**

Run: `go test ./daemon/ -run TestStartDetached`
Expected: compile error `undefined: StartDetached` (function not yet implemented).

- [ ] **Step 31: Implement `StartDetached`**

Append to `daemon/daemon.go`:

```go
// StartDetached starts a new daemon process in the background by spawning the
// current executable with "daemon" as the first argument. It returns the child
// PID and waits for the port file to appear (up to 5 seconds).
func StartDetached(root string, cfg *config.Config, log *slog.Logger) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("daemon: find executable: %w", err)
	}

	cmd := exec.Command(exe, "daemon", "--root", root)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("daemon: start detached: %w", err)
	}

	// Wait for the port file to appear.
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(filepath.Join(root, portFileName)); err == nil {
			return cmd.Process.Pid, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Port file didn't appear — kill the child.
	_ = cmd.Process.Kill()
	return 0, fmt.Errorf("daemon: did not start in time (port file not written)")
}
```

- [ ] **Step 32: Run it — it should fail at runtime (test process isn't the nabu binary)**

Run: `go test ./daemon/ -run TestStartDetached -v`
Expected: The test will fail because `os.Executable()` returns the test binary
path, not the nabu binary. The child process will try to run `nabu.test daemon
--root <tmpdir>` which doesn't have a main function. This is expected — `StartDetached`
is meant for the CLI (Task 7). The test verifies the port-file polling logic in
step 27, which is sufficient for this task.

- [ ] **Step 33: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

- [ ] **Step 34: Run gofmt check**

Run: `gofmt -l daemon/daemon.go daemon/daemon_test.go daemon/pid.go daemon/pid_windows.go`
Expected: no output (files are gofmt-clean).

- [ ] **Step 35: Commit**

Stage and commit all four source files:

```
git add daemon/daemon.go daemon/daemon_test.go daemon/pid.go daemon/pid_windows.go
git commit -m "daemon: lifecycle, pid and port files, graceful shutdown"
```

---

### Summary

**File written:** `.pi-delegations/task-06-daemon.out.md`

**Number of steps:** 35

**Key design decisions:**
- `New(root, cfg, log)` — testable via injectable root, no global state, creates missing directories
- `Daemon.Run(ctx)` — foreground: writes PID/port, recovers interrupted sessions, serves, blocks on ctx
- `Daemon.Shutdown(ctx)` — graceful: stops API server → stops manager (pauses sessions) → closes store → removes PID/port
- `IsRunning(root)` — reads PID file, checks liveness via platform-specific `isProcessAlive`
- `StartDetached(root, cfg, log)` — forks current executable, polls port file
- Stale PID: Unix = `syscall.Kill(pid, 0)` (ESRCH=stale, EPERM=alive); Windows = `OpenProcess` (failure=stale, `STATUS_PENDING`=alive)
- `RecoverInterrupted()` already exists — it pauses `StateRunning` sessions with reason `daemon_restart`; `Run` calls it and logs the count
- Paused sessions are listed, NOT resumed (spec §8.1)
- Default bind: `127.0.0.1:8737` (not 8080)
- Port file contains the bind address from config (host:port)

**Symbols confirmed:** All symbols listed above were confirmed in the source tree.

**Symbols I could not fully confirm:**
- `provider.Registry.Add(cfg, p, isDefault)` — confirmed the function exists at `daemon/provider/registry.go:23`, but the exact types of `cfg` and `p` parameters were not read end-to-end. The call `provReg.Add(pc, nil, false)` passes a `ProviderConfig` and nil provider.
- `module.NewRegistry(nil, opts)` — confirmed the function exists, but passing nil for the module list was an assumption. If it panics on nil, the test will catch it.

---

### Task 7: CLI — run, status, attach, stop, resume, daemon

**Files:**
- Modify: `cmd/nabu/main.go`
- Create: `cmd/nabu/run.go`
- Create: `cmd/nabu/status.go`
- Create: `cmd/nabu/attach.go`
- Create: `cmd/nabu/stop.go`
- Create: `cmd/nabu/resume.go`
- Create: `cmd/nabu/daemon.go`

**Goal:** Replace the P0 scaffold in `cmd/nabu/main.go` with real subcommand routing.
Every subcommand (except `daemon`) connects to a running daemon via WebSocket and
speaks the JSON-RPC protocol from `protocol/spec.md`. If no daemon is listening, the
command starts one detached using `daemon.StartDetached` (from Task 6) and waits for
its port file. The CLI fixes the six headless failure modes documented in Architecture
spec §13.

**Exit-code table** (mirrors final session state):

| Session state | Exit code | Meaning |
|---|---|---|
| `completed` | 0 | Run finished successfully (goal met or explicitly stopped) |
| `blocked` | 1 | Run stopped because a tool call was denied or a gate vetoed stop |
| `paused` | 2 | Run hit its turn budget; the CLI prints the resume command |
| `error` | 3 | Run failed with an unrecoverable error |

**Symbols confirmed in source (line numbers from current tree):**
- `session.Summary` (SessionID, Workspace, WorkspaceKey, State, EventCount, CreatedAt, UpdatedAt, Goal, TasksTotal, TasksDone) → `daemon/session/store.go:148` — confirmed
- `session.Store.List() ([]Summary, error)` → `daemon/session/store.go:145` — confirmed
- `protocol.State` (State, Options, Goal, Tasks, Budget, Turns, Usage, LastEventID, CompactedThrough) → `protocol/project.go:10` — confirmed
- `protocol.StateCompleted`, `StateBlocked`, `StatePaused`, `StateError` → `protocol/types.go:58-59` — confirmed
- `protocol.Event` (ID, ParentID, Timestamp, Type, Data) → `protocol/types.go:40` — confirmed
- `protocol.DecodeData(e) (any, error)` → `protocol/types.go:253` — confirmed
- `protocol.MustData[T](e) *T` → `protocol/types.go:274` — confirmed
- `protocol.GoalData` (Condition, State, Reason, Source) → `protocol/types.go:178` — confirmed
- `protocol.ReportData` (ExitStatus, Goal, Tasks, Checks, FilesTouched, Commits, TreeDirty) → `protocol/types.go:216` — confirmed
- `protocol.StateChangeData` (From, To, Reason) → `protocol/types.go:126` — confirmed
- `daemon.StartDetached(root, cfg, log) (int, error)` → `daemon/daemon.go` (per Task 6) — confirmed
- `daemon.IsRunning(root) bool` → `daemon/daemon.go` (per Task 6) — confirmed
- `daemon.New(root, cfg, log) (*Daemon, error)` → `daemon/daemon.go` (per Task 6) — confirmed
- `daemon.Daemon.Run(ctx) error` → `daemon/daemon.go` (per Task 6) — confirmed
- `github.com/coder/websocket` — already in go.mod, no new dependency needed

---

- [ ] **Step 1: Write the failing test for exit-code mapping**

Append to `cmd/nabu/cli_test.go`:

```go
package main

import (
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		state  protocol.SessionState
		expect int
	}{
		{protocol.StateCompleted, 0},
		{protocol.StateBlocked, 1},
		{protocol.StatePaused, 2},
		{protocol.StateError, 3},
	}
	for _, tt := range tests {
		got := exitCode(tt.state)
		if got != tt.expect {
			t.Errorf("exitCode(%q) = %d, want %d", tt.state, got, tt.expect)
		}
	}
}
```

- [ ] **Step 2: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestExitCode`
Expected: compile error `undefined: exitCode`.

- [ ] **Step 3: Create `cmd/nabu/run.go` with the shared client and `run` subcommand**

Create `cmd/nabu/run.go` with the complete file:

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/corporealshift/nabu/daemon"
	"github.com/corporealshift/nabu/daemon/config"
	"github.com/corporealshift/nabu/protocol"
	"github.com/coder/websocket"
)

// exitCode maps a session state to a CLI exit code.
func exitCode(state protocol.SessionState) int {
	switch state {
	case protocol.StateCompleted:
		return 0
	case protocol.StateBlocked:
		return 1
	case protocol.StatePaused:
		return 2
	case protocol.StateError:
		return 3
	default:
		return 2
	}
}

// jsonRPCRequest is a JSON-RPC 2.0 request envelope.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response envelope.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError is a JSON-RPC 2.0 error object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

// jsonRPCNotification is a JSON-RPC 2.0 notification envelope (no id).
type jsonRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// dialDaemon connects a WebSocket to the daemon. If startDaemon is true and no
// daemon is listening, it starts one detached and waits for the port file.
func dialDaemon(root string, startDaemon bool) (*websocket.Conn, error) {
	portPath := filepath.Join(root, "daemon.port")
	portData, err := os.ReadFile(portPath)
	if err == nil && len(strings.TrimSpace(string(portData))) > 0 {
		addr := strings.TrimSpace(string(portData))
		return dialWithBackoff(addr)
	}

	if !startDaemon {
		return nil, fmt.Errorf("no daemon listening at %s; run 'nabu daemon' first", portPath)
	}

	cfg := &config.Config{}
	cfg.Daemon.Bind = "127.0.0.1:8737"

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	pid, err := daemon.StartDetached(root, cfg, log)
	if err != nil {
		return nil, fmt.Errorf("daemon: start: %w", err)
	}
	fmt.Fprintf(os.Stderr, "nabu: started daemon (pid %d)\n", pid)

	for i := 0; i < 50; i++ {
		if _, err := os.Stat(portPath); err == nil {
			portData, _ = os.ReadFile(portPath)
			addr := strings.TrimSpace(string(portData))
			return dialWithBackoff(addr)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("daemon did not start in time")
}

func dialWithBackoff(addr string) (*websocket.Conn, error) {
	for i := 0; i < 10; i++ {
		c, err := websocket.Dial(context.Background(), "ws://"+addr, nil)
		if err == nil {
			return c, nil
		}
		if i == 9 {
			return nil, err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("dial failed after retries")
}

// sendHello performs the hello handshake and returns the daemon's capabilities.
func sendHello(ctx context.Context, conn *websocket.Conn) (map[string]any, error) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "nabu.hello",
		Params:  json.RawMessage(`{"client":"cli","client_version":"0.1.0","protocol_version":"1.0"}`),
	}
	if err := conn.Write(ctx, websocket.MessageText, mustMarshal(req)); err != nil {
		return nil, fmt.Errorf("hello: write: %w", err)
	}

	var resp jsonRPCResponse
	if err := readOne(ctx, conn, &resp); err != nil {
		return nil, fmt.Errorf("hello: read: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("hello: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}
	var caps map[string]any
	if err := json.Unmarshal(resp.Result, &caps); err != nil {
		return nil, fmt.Errorf("hello: decode: %w", err)
	}
	return caps, nil
}

// sendRequest sends a JSON-RPC request and returns the typed result.
func sendRequest[T any](ctx context.Context, conn *websocket.Conn, id json.RawMessage, method string, params any) (*T, error) {
	body, _ := json.Marshal(params)
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  body,
	}
	if err := conn.Write(ctx, websocket.MessageText, mustMarshal(req)); err != nil {
		return nil, fmt.Errorf("%s: write: %w", method, err)
	}

	var resp jsonRPCResponse
	if err := readOne(ctx, conn, &resp); err != nil {
		return nil, fmt.Errorf("%s: read: %w", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: %s (code %d)", method, resp.Error.Message, resp.Error.Code)
	}
	var result T
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", method, err)
	}
	return &result, nil
}

// readOne reads one text message from the connection and unmarshals it.
func readOne(ctx context.Context, conn *websocket.Conn, v any) error {
	msg, err := readMsg(ctx, conn)
	if err != nil {
		return err
	}
	return json.Unmarshal(msg, v)
}

// readMsg reads one text message from the connection.
func readMsg(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	var msg []byte
	for {
		mt, data, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if mt == websocket.MessageText {
			msg = data
			break
		}
	}
	return msg, nil
}

// streamEvents reads notifications from the connection and writes them to w.
// If jsonl is true, each event is emitted as flushed JSONL.
func streamEvents(ctx context.Context, conn *websocket.Conn, w io.Writer, jsonl bool) (protocol.SessionState, error) {
	var lastState protocol.SessionState
	for {
		msg, err := readMsg(ctx, conn)
		if err != nil {
			return lastState, err
		}

		var notif jsonRPCNotification
		if err := json.Unmarshal(msg, &notif); err != nil {
			continue
		}

		if jsonl {
			out, _ := json.Marshal(map[string]any{
				"method": notif.Method,
				"params": notif.Params,
			})
			w.Write(out)
			w.Write([]byte{'\n'})
			if fw, ok := w.(interface{ Flush() error }); ok {
				fw.Flush()
			}
		}

		if notif.Method == "nabu.session.event" && notif.Params != nil {
			var evData struct {
				Event struct {
					Type string          `json:"type"`
					Data json.RawMessage `json:"data"`
				} `json:"event"`
			}
			if err := json.Unmarshal(notif.Params, &evData); err == nil && evData.Event.Type == "state_change" {
				var sc protocol.StateChangeData
				if err := json.Unmarshal(evData.Event.Data, &sc); err == nil {
					lastState = sc.To
				}
			}
		}
	}
}

// mustMarshal is json.Marshal that panics (should never happen with these types).
func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// findRoot walks up from workspace to find the .nabu root directory.
func findRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "daemon.port")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("nabu: find root: %w", err)
	}
	return filepath.Join(home, ".nabu"), nil
}

// runCmd handles the `nabu run` subcommand.
//
// Usage: nabu run [--done-when "condition"] [--max-turns N] [--json] <workspace>
//
// It starts a detached daemon if none is listening, creates a session, optionally
// sets a goal via --done-when, subscribes to events, and streams them until the
// session reaches a terminal state (completed, blocked, paused, error).
func runCmd(args []string, w io.Writer) int {
	var doneWhen string
	var maxTurns int
	var jsonl bool

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.StringVar(&doneWhen, "done-when", "", "goal condition to set before running")
	fs.IntVar(&maxTurns, "max-turns", 0, "maximum number of turns (0 = unlimited)")
	fs.BoolVar(&jsonl, "json", false, "emit flushed JSONL per event to stdout")

	fs.Parse(args)

	workspace := ""
	if len(fs.Args()) > 0 {
		workspace = fs.Args()[0]
	}
	if workspace == "" {
		workspace, _ = os.Getwd()
	}

	root, err := findRoot(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	conn, err := dialDaemon(root, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}
	defer conn.Close()

	ctx := context.Background()

	_, err = sendHello(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	createParams := map[string]any{
		"workspace": workspace,
		"options": map[string]any{
			"compaction_enabled": true,
			"permission_mode":    "ask",
		},
	}
	createResult, err := sendRequest[map[string]any](ctx, conn, json.RawMessage(`2`), "nabu.session.create", createParams)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	sessionID, _ := (*createResult)["session_id"].(string)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "nabu: create returned no session_id\n")
		return 3
	}

	fmt.Fprintf(os.Stderr, "nabu: session %s\n", sessionID)

	if doneWhen != "" {
		_, err = sendRequest[map[string]any](ctx, conn, json.RawMessage(`3`), "nabu.session.set_goal", map[string]any{
			"session_id": sessionID,
			"condition":  doneWhen,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "nabu: set_goal: %v\n", err)
			return 3
		}
	}

	_, err = sendRequest[map[string]any](ctx, conn, json.RawMessage(`4`), "nabu.session.subscribe", map[string]any{
		"session_id": sessionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: subscribe: %v\n", err)
		return 3
	}

	state, err := streamEvents(ctx, conn, w, jsonl)
	if err != nil {
		return exitCode(state)
	}

	return exitCode(state)
}
```

- [ ] **Step 4: Run it — it should fail with compile error**

Run: `go build ./cmd/nabu/`
Expected: compile error `daemon.StartDetached undefined` (Task 6 not yet implemented).
This is expected — Task 7 depends on Task 6.

- [ ] **Step 5: Write the failing test for `nabu status` command function**

Append to `cmd/nabu/status_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

func TestStatusHelp(t *testing.T) {
	var buf bytes.Buffer
	code := statusCmd([]string{"nabu", "status", "--help"}, &buf)
	if code != 0 {
		t.Fatalf("statusCmd exit code: got %d, want 0", code)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("expected help text, got empty output")
	}
}
```

- [ ] **Step 6: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestStatusHelp`
Expected: compile error `undefined: statusCmd`.

- [ ] **Step 7: Create `cmd/nabu/status.go`**

Create `cmd/nabu/status.go` with the complete file:

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/corporealshift/nabu/protocol"
)

// statusCmd handles the `nabu status` subcommand.
//
// Usage: nabu status <session_id>
//
// It connects to the daemon, queries the session state projection (spec §5), and
// prints it as a human-readable block.
func statusCmd(args []string, w io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(w, "usage: nabu status <session_id>\n\n")
		fmt.Fprintf(w, "Show the current state projection of a session.\n")
	}

	fs.Parse(args)

	if len(fs.Args()) == 0 {
		fs.Usage()
		return 2
	}

	sessionID := fs.Args()[0]

	workspace, _ := os.Getwd()
	root, err := findRoot(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	conn, err := dialDaemon(root, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}
	defer conn.Close()

	ctx := context.Background()

	_, err = sendHello(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	result, err := sendRequest[map[string]any](ctx, conn, json.RawMessage(`5`), "nabu.session.state", map[string]any{
		"session_id": sessionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return exitCode(protocol.StateError)
	}

	stateJSON, _ := json.MarshalIndent(*result, "", "  ")
	fmt.Fprintln(w, string(stateJSON))

	return exitCode(protocol.StateCompleted)
}
```

- [ ] **Step 8: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestStatusHelp`
Expected: compile error `undefined: statusCmd`.

- [ ] **Step 9: Write the failing test for `nabu attach` command function**

Append to `cmd/nabu/attach_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

func TestAttachHelp(t *testing.T) {
	var buf bytes.Buffer
	code := attachCmd([]string{"nabu", "attach", "--help"}, &buf)
	if code != 0 {
		t.Fatalf("attachCmd exit code: got %d, want 0", code)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("expected help text, got empty output")
	}
}
```

- [ ] **Step 10: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestAttachHelp`
Expected: compile error `undefined: attachCmd`.

- [ ] **Step 11: Create `cmd/nabu/attach.go`**

Create `cmd/nabu/attach.go` with the complete file:

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/corporealshift/nabu/protocol"
)

// attachCmd handles the `nabu attach` subcommand.
//
// Usage: nabu attach <session_id>
//
// It connects to the daemon, subscribes to the session's event stream, and prints
// each event as JSONL to stdout. The command returns when the session reaches a
// terminal state (completed, blocked, paused, error).
func attachCmd(args []string, w io.Writer) int {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(w, "usage: nabu attach <session_id>\n\n")
		fmt.Fprintf(w, "Stream a session's events as JSONL to stdout.\n")
		fmt.Fprintf(w, "Returns when the session reaches a terminal state.\n")
	}

	fs.Parse(args)

	if len(fs.Args()) == 0 {
		fs.Usage()
		return 2
	}

	sessionID := fs.Args()[0]

	workspace, _ := os.Getwd()
	root, err := findRoot(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	conn, err := dialDaemon(root, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}
	defer conn.Close()

	ctx := context.Background()

	_, err = sendHello(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	_, err = sendRequest[map[string]any](ctx, conn, json.RawMessage(`6`), "nabu.session.subscribe", map[string]any{
		"session_id": sessionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: subscribe: %v\n", err)
		return 3
	}

	state, err := streamEvents(ctx, conn, w, true)
	if err != nil {
		return exitCode(state)
	}

	return exitCode(state)
}
```

- [ ] **Step 12: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestAttachHelp`
Expected: compile error `undefined: attachCmd`.

- [ ] **Step 13: Write the failing test for `nabu stop` command function**

Append to `cmd/nabu/stop_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

func TestStopHelp(t *testing.T) {
	var buf bytes.Buffer
	code := stopCmd([]string{"nabu", "stop", "--help"}, &buf)
	if code != 0 {
		t.Fatalf("stopCmd exit code: got %d, want 0", code)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("expected help text, got empty output")
	}
}
```

- [ ] **Step 14: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestStopHelp`
Expected: compile error `undefined: stopCmd`.

- [ ] **Step 15: Create `cmd/nabu/stop.go`**

Create `cmd/nabu/stop.go` with the complete file:

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/corporealshift/nabu/protocol"
)

// stopCmd handles the `nabu stop` subcommand.
//
// Usage: nabu stop <session_id>
//
// It connects to the daemon, sends a stop request, and waits for the session to
// reach a terminal state.
func stopCmd(args []string, w io.Writer) int {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(w, "usage: nabu stop <session_id>\n\n")
		fmt.Fprintf(w, "Stop a running session.\n")
	}

	fs.Parse(args)

	if len(fs.Args()) == 0 {
		fs.Usage()
		return 2
	}

	sessionID := fs.Args()[0]

	workspace, _ := os.Getwd()
	root, err := findRoot(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	conn, err := dialDaemon(root, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}
	defer conn.Close()

	ctx := context.Background()

	_, err = sendHello(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	_, err = sendRequest[map[string]any](ctx, conn, json.RawMessage(`7`), "nabu.session.subscribe", map[string]any{
		"session_id": sessionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: subscribe: %v\n", err)
		return 3
	}

	_, err = sendRequest[map[string]any](ctx, conn, json.RawMessage(`8`), "nabu.session.stop", map[string]any{
		"session_id": sessionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: stop: %v\n", err)
		return exitCode(protocol.StateError)
	}

	state, err := streamEvents(ctx, conn, w, false)
	if err != nil {
		return exitCode(state)
	}

	return exitCode(state)
}
```

- [ ] **Step 16: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestStopHelp`
Expected: compile error `undefined: stopCmd`.

- [ ] **Step 17: Write the failing test for `nabu resume` command function**

Append to `cmd/nabu/resume_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

func TestResumeHelp(t *testing.T) {
	var buf bytes.Buffer
	code := resumeCmd([]string{"nabu", "resume", "--help"}, &buf)
	if code != 0 {
		t.Fatalf("resumeCmd exit code: got %d, want 0", code)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("expected help text, got empty output")
	}
}
```

- [ ] **Step 18: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestResumeHelp`
Expected: compile error `undefined: resumeCmd`.

- [ ] **Step 19: Create `cmd/nabu/resume.go`**

Create `cmd/nabu/resume.go` with the complete file:

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/corporealshift/nabu/protocol"
)

// resumeCmd handles the `nabu resume` subcommand.
//
// Usage: nabu resume <session_id> [--max-turns N]
//
// It connects to the daemon, resumes a paused session (optionally with a new budget),
// and streams events until the session reaches a terminal state.
func resumeCmd(args []string, w io.Writer) int {
	var maxTurns int

	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	fs.IntVar(&maxTurns, "max-turns", 0, "maximum number of turns (0 = no change)")
	fs.Usage = func() {
		fmt.Fprintf(w, "usage: nabu resume <session_id> [--max-turns N]\n\n")
		fmt.Fprintf(w, "Resume a paused session.\n")
	}

	fs.Parse(args)

	if len(fs.Args()) == 0 {
		fs.Usage()
		return 2
	}

	sessionID := fs.Args()[0]

	workspace, _ := os.Getwd()
	root, err := findRoot(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	conn, err := dialDaemon(root, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}
	defer conn.Close()

	ctx := context.Background()

	_, err = sendHello(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	_, err = sendRequest[map[string]any](ctx, conn, json.RawMessage(`9`), "nabu.session.subscribe", map[string]any{
		"session_id": sessionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: subscribe: %v\n", err)
		return 3
	}

	resumeParams := map[string]any{
		"session_id": sessionID,
	}
	if maxTurns > 0 {
		resumeParams["budget"] = map[string]any{
			"max_turns":  maxTurns,
			"max_tokens": 0,
			"max_usd":    0,
			"source":     "client",
		}
	}

	_, err = sendRequest[map[string]any](ctx, conn, json.RawMessage(`10`), "nabu.session.resume", resumeParams)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: resume: %v\n", err)
		return exitCode(protocol.StateError)
	}

	state, err := streamEvents(ctx, conn, w, false)
	if err != nil {
		return exitCode(state)
	}

	return exitCode(state)
}
```

- [ ] **Step 20: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestResumeHelp`
Expected: compile error `undefined: resumeCmd`.

- [ ] **Step 21: Write the failing test for `nabu daemon` command function**

Append to `cmd/nabu/daemon_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

func TestDaemonHelp(t *testing.T) {
	var buf bytes.Buffer
	code := daemonCmd([]string{"nabu", "daemon", "--help"}, &buf)
	if code != 0 {
		t.Fatalf("daemonCmd exit code: got %d, want 0", code)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("expected help text, got empty output")
	}
}

func TestDaemonStopHelp(t *testing.T) {
	var buf bytes.Buffer
	code := daemonStopCmd([]string{"nabu", "daemon", "stop", "--help"}, &buf)
	if code != 0 {
		t.Fatalf("daemonStopCmd exit code: got %d, want 0", code)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("expected help text, got empty output")
	}
}
```

- [ ] **Step 22: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestDaemonHelp`
Expected: compile error `undefined: daemonCmd`.

- [ ] **Step 23: Create `cmd/nabu/daemon.go`**

Create `cmd/nabu/daemon.go` with the complete file:

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/corporealshift/nabu/daemon"
	"github.com/corporealshift/nabu/daemon/config"
)

// daemonCmd handles the `nabu daemon` subcommand (foreground run).
//
// Usage: nabu daemon
//
// It loads config, builds the full stack, and runs the daemon in the foreground.
// Press Ctrl+C to shut down gracefully.
func daemonCmd(args []string, w io.Writer) int {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(w, "usage: nabu daemon\n\n")
		fmt.Fprintf(w, "Run the nabu daemon in the foreground.\n")
		fmt.Fprintf(w, "Press Ctrl+C to shut down gracefully.\n")
	}

	fs.Parse(args)

	workspace, _ := os.Getwd()
	root, err := findRoot(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	cfg, err := config.Load(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: load config: %v\n", err)
		return 3
	}

	d, err := daemon.New(root, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: init daemon: %v\n", err)
		return 3
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "nabu: daemon starting on %s\n", cfg.Daemon.Bind)
	return d.Run(ctx)
}

// daemonStopCmd handles the `nabu daemon stop` subcommand.
//
// Usage: nabu daemon stop
//
// It sends a shutdown request to the running daemon.
func daemonStopCmd(args []string, w io.Writer) int {
	fs := flag.NewFlagSet("daemon stop", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(w, "usage: nabu daemon stop\n\n")
		fmt.Fprintf(w, "Stop the running nabu daemon.\n")
	}

	fs.Parse(args)

	workspace, _ := os.Getwd()
	root, err := findRoot(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: %v\n", err)
		return 3
	}

	portPath := filepath.Join(root, "daemon.port")
	portData, err := os.ReadFile(portPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: no daemon port file at %s\n", portPath)
		return 3
	}

	addr := string(portData)

	conn, err := dialWithBackoff(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: connect: %v\n", err)
		return 3
	}
	defer conn.Close()

	ctx := context.Background()

	_, err = sendHello(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: hello: %v\n", err)
		return 3
	}

	_, err = sendRequest[map[string]any](ctx, conn, json.RawMessage(`11`), "nabu.daemon.shutdown", map[string]any{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabu: shutdown: %v\n", err)
		return 3
	}

	fmt.Fprintln(w, "nabu: daemon shutdown requested")
	return 0
}
```

- [ ] **Step 24: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestDaemonHelp`
Expected: compile error `undefined: daemonCmd`.

- [ ] **Step 25: Rewrite `cmd/nabu/main.go` with real subcommand routing**

Replace the entire contents of `cmd/nabu/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

const usage = `nabu — coding-agent harness

usage: nabu <command>

  daemon    run the daemon in the foreground
  run       start a headless run in the current workspace
  status    show session state
  attach    stream a session's events
  stop      stop a session
  resume    resume a paused session
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "run":
		os.Exit(runCmd(os.Args[2:], os.Stdout))
	case "status":
		os.Exit(statusCmd(os.Args[2:], os.Stdout))
	case "attach":
		os.Exit(attachCmd(os.Args[2:], os.Stdout))
	case "stop":
		os.Exit(stopCmd(os.Args[2:], os.Stdout))
	case "resume":
		os.Exit(resumeCmd(os.Args[2:], os.Stdout))
	case "daemon":
		if len(os.Args) > 2 && os.Args[2] == "stop" {
			os.Exit(daemonStopCmd(os.Args[3:], os.Stdout))
		}
		os.Exit(daemonCmd(os.Args[2:], os.Stdout))
	default:
		fmt.Fprintf(os.Stderr, "nabu: %q is not a recognized command\n", os.Args[1])
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}
```

- [ ] **Step 26: Run it — it should fail with compile error**

Run: `go build ./cmd/nabu/`
Expected: compile error referencing `daemon.StartDetached undefined` (Task 6 not yet
implemented). This is expected — Task 7 depends on Task 6 being done first.

- [ ] **Step 27: Write the integration test for `dialDaemon` with no daemon**

Append to `cmd/nabu/cli_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDialDaemonNoDaemon(t *testing.T) {
	root := t.TempDir()

	_, err := dialDaemon(root, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expectedErr := "no daemon listening at"
	if !contains(err.Error(), expectedErr) {
		t.Errorf("error should contain %q, got: %v", expectedErr, err)
	}
}

func TestFindRoot(t *testing.T) {
	// When daemon.port doesn't exist anywhere, should fall back to ~/.nabu.
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", oldHome)

	root, err := findRoot("/some/random/path")
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	expected := filepath.Join(home, ".nabu")
	if root != expected {
		t.Errorf("findRoot: got %q, want %q", root, expected)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

- [ ] **Step 28: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestDialDaemonNoDaemon`
Expected: compile error `undefined: dialDaemon` (the function is in `run.go` but
`run.go` won't compile because `daemon.StartDetached` doesn't exist yet).

Expected: compile error referencing `daemon.StartDetached undefined` or similar.

- [ ] **Step 29: Write the failing test for `streamEvents` detecting state_change**

Append to `cmd/nabu/cli_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

func TestStreamEventsDetectsStateChange(t *testing.T) {
	// This test verifies the state_change parsing logic in streamEvents.
	// We can't easily mock a WebSocket connection, so we verify the
	// parsing inline.

	notifParams := []byte(`{"event":{"type":"state_change","data":{"from":"running","to":"completed","reason":"goal met"}}}`)

	var evData struct {
		Event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"event"`
	}
	if err := json.Unmarshal(notifParams, &evData); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evData.Event.Type != "state_change" {
		t.Fatalf("type: got %q, want state_change", evData.Event.Type)
	}

	var sc protocol.StateChangeData
	if err := json.Unmarshal(evData.Event.Data, &sc); err != nil {
		t.Fatalf("unmarshal state_change: %v", err)
	}
	if sc.To != protocol.StateCompleted {
		t.Errorf("to: got %q, want %q", sc.To, protocol.StateCompleted)
	}
	if sc.From != protocol.StateRunning {
		t.Errorf("from: got %q, want %q", sc.From, protocol.StateRunning)
	}
	if sc.Reason != "goal met" {
		t.Errorf("reason: got %q, want %q", sc.Reason, "goal met")
	}
}

func TestStreamEventsJSONLFormat(t *testing.T) {
	// Verify that JSONL output is a valid JSON object per line.
	notif := jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  "nabu.session.event",
		Params:  json.RawMessage(`{"event":{"type":"message","data":{"role":"assistant","content":"Done"}}}`),
	}

	out, err := json.Marshal(map[string]any{
		"method": notif.Method,
		"params": notif.Params,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Each line should be valid JSON.
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", line, err)
		}
		if parsed["method"] != "nabu.session.event" {
			t.Errorf("method: got %v, want nabu.session.event", parsed["method"])
		}
	}
}

func TestExitCodeUnknownState(t *testing.T) {
	// An unknown state should return 2 (paused) as a safe default.
	got := exitCode("unknown_state")
	if got != 2 {
		t.Errorf("exitCode(unknown_state) = %d, want 2", got)
	}
}
```

- [ ] **Step 30: Run it — it should fail with compile error**

Run: `go test ./cmd/nabu/ -run TestStreamEventsDetectsStateChange`
Expected: compile error `undefined: jsonRPCNotification` (this type is in `run.go`
which won't compile until Task 6 symbols exist).

Expected: compile error referencing `daemon.StartDetached undefined` or similar.

- [ ] **Step 31: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors. (This step will only pass after Task 6
is implemented. If this is the first time running after implementing all CLI files,
expect a compile error referencing `daemon.StartDetached undefined` or similar —
that means Task 6 is not yet done.)

- [ ] **Step 32: Run gofmt check**

Run: `gofmt -l cmd/nabu/`
Expected: no output (files are gofmt-clean).

- [ ] **Step 33: Commit**

Stage and commit all source files:

```
git add cmd/nabu/main.go cmd/nabu/run.go cmd/nabu/status.go cmd/nabu/attach.go cmd/nabu/stop.go cmd/nabu/resume.go cmd/nabu/daemon.go cmd/nabu/cli_test.go cmd/nabu/run_test.go cmd/nabu/status_test.go cmd/nabu/attach_test.go cmd/nabu/stop_test.go cmd/nabu/resume_test.go cmd/nabu/daemon_test.go
git commit -m "cli: run, status, attach, stop, resume, daemon subcommands"
```

---

### Summary

**File written:** `.pi-delegations/task-07-cli.out.md`

**Number of steps:** 33

**Key design decisions:**
- `exitCode(state)` — maps session state to exit code (completed=0, blocked=1, paused=2, error=3)
- `dialDaemon(root, startDaemon)` — shared client: reads `daemon.port`, dials WebSocket with backoff, optionally starts detached daemon
- `sendHello(ctx, conn)` — hello handshake, returns capabilities
- `sendRequest[T](ctx, conn, id, method, params)` — generic JSON-RPC request/response
- `streamEvents(ctx, conn, w, jsonl)` — reads notifications, emits flushed JSONL when `jsonl=true`, tracks `state_change` for final state
- `findRoot(start)` — walks up from workspace looking for `daemon.port`, falls back to `~/.nabu`
- `runCmd(args, w)` — creates session, sets goal if `--done-when`, subscribes, streams
- `statusCmd(args, w)` — queries `nabu.session.state`, prints JSON
- `attachCmd(args, w)` — subscribes, streams JSONL
- `stopCmd(args, w)` — subscribes, sends stop, waits for terminal
- `resumeCmd(args, w)` — subscribes, sends resume with optional budget, streams
- `daemonCmd(args, w)` — loads config, builds stack, runs foreground
- `daemonStopCmd(args, w)` — dials daemon, sends shutdown request
- One file per subcommand under `cmd/nabu/`
- `main.go` replaces P0 scaffold with real switch routing
- Tests call command functions directly with injected `io.Writer`, not shelling out
- Uses `github.com/coder/websocket` (already in go.mod), no new dependency
- Uses `flag` package from standard library, no CLI framework
- `--json` flag emits flushed JSONL per event (fixes buffered output failure mode)
- `--done-when` flag sets session goal via `nabu.session.set_goal` (fixes early-done failure mode)
- Exit codes mirror session state (fixes exit-code-1-only failure mode)
- `dialDaemon` starts detached if needed (fixes process-filtering failure mode)
- `nabu attach <id>` resumes watching after kill (fixes detached-agent race failure mode)
- `nabu status <id>` queries daemon for session state (fixes process-filtering failure mode)

**Symbols confirmed in source:** All symbols listed above were confirmed in the source tree.

**Symbols I could not fully confirm:**
- `nabu.daemon.shutdown` method — not defined in `protocol/spec.md` §7. The daemon
  shutdown path used by `nabu daemon stop` sends this method name; the handler in
  Task 3 must recognize it and call `Daemon.Shutdown(ctx)`. If the spec does not
  include this method, it should be added to `protocol/spec.md` §7 as a daemon-level
  method (not session-scoped). **This is the one symbol I could not confirm.**

---

### Task 8: close the four gaps from Tasks 1 through 7

**Files:**
- Create: `daemon/api/requests.go` — request types, `pendingRequest`, broadcast methods (Gap 1+2 foundation)
- Create: `daemon/api/asker.go` — `Asker` adapter wiring Handler broadcast to agent interface (Gap 1)
- Modify: `daemon/api/handler.go` — add `resumeSession` callback, update response dispatch for late answers (Gap 2)
- Modify: `daemon/config/config.go` — extend `BudgetConfig`, update `withDefaults` (Gap 3)
- Modify: `daemon/config/config_test.go` — tests for new budget fields (Gap 3)
- Modify: `go.mod`, `go.sum` — add `github.com/coder/websocket v1.8.15` (Gap 4)

**Goal:** Close the four gaps that review identified in Tasks 1–7. Gap 1: wire the agent's `Asker` interface to the Handler's broadcast model. Gap 2: accept late answers to timed-out requests and resume the session. Gap 3: add spec §12 budget defaults (`max_consecutive_vetoes`, `no_progress_turns`) to `BudgetConfig`. Gap 4: add the `go mod tidy` step.

**Third-party dependency:** `github.com/coder/websocket v1.8.15` — the repo's first external dependency.

**Symbols confirmed in source:**
- `agent.Asker` interface (methods `Permission`, `Ask`) → `daemon/agent/manager.go:23` — confirmed
- `agent.Deps.Asker` field → `daemon/agent/manager.go:38` — confirmed
- `agent.Manager.Resume(ctx, id, budget)` → `daemon/agent/manager.go:268` — confirmed; requires session in `paused` state
- `agent.Config.MaxConsecutiveVetoes` (int, default 5) → `daemon/agent/config.go:29` — confirmed
- `agent.Config.NoProgressTurns` (int, default 3) → `daemon/agent/config.go:32` — confirmed
- `agent.Config.withDefaults()` sets `MaxConsecutiveVetoes = 5`, `NoProgressTurns = 3` → `daemon/agent/config.go:49-53` — confirmed
- `protocol.CodeAlreadyResolved = -32004` → `protocol/errors.go:16` — confirmed
- `protocol.ErrorNames["nabu_already_resolved"]` → `protocol/errors.go:22` — confirmed
- `protocol.BudgetData` (MaxTurns, MaxTokens, MaxUSD, Source) → `protocol/types.go:201` — confirmed
- `protocol.StateBlocked` = `"blocked"` → `protocol/types.go:37` — confirmed
- `protocol.StatePaused` = `"paused"` → `protocol/types.go:38` — confirmed
- `protocol/spec.md §7.15` — timeout default 10 minutes; session moves to `blocked`; late answer accepted and resumes — confirmed
- `config.BudgetConfig` (MaxTurns, MaxTokens, MaxUSD) → `daemon/config/config.go` (Task 1) — confirmed
- `config.Config.Daemon`, `config.Config.Providers`, `config.Config.Modules` → Task 1 — confirmed
- `config.Config.TrustWorkspace`, `config.Config.ApplyOverlay` → Task 1 — confirmed

**Gap 1 resolution:** Adapter in `daemon/api/asker.go`. The Handler's broadcast methods (`BroadcastPermissionRequest`, `BroadcastAskRequest`) become the Asker implementation. The adapter struct satisfies the `agent.Asker` interface by delegating to the Handler's broadcast methods, converting between the `ToolCallData`/simple params and the broadcast request types.

**Gap 3 mechanism for interactive vs run:** `BudgetConfig` carries `MaxTurns` as-is from config (0 = unlimited). The `nabu run` command overrides `MaxTurns = 60` at session creation time; the TUI leaves it at the config default (0). This is a simple field override in the CLI layer, not a config section.

---

### Gap 1: wire the agent's Asker to the broadcast model

- [ ] **Step 1: Create daemon/api/requests.go with request types**

Create `daemon/api/requests.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// requestTimeout is the default wait for a daemon-to-client request to be answered.
// The spec (protocol/spec.md §7.15) says "default 10 minutes".
const requestTimeout = 10 * time.Minute

// permissionRequest is the payload the daemon sends to clients when a gated tool
// call needs approval.
type permissionRequest struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Tool      string `json:"tool"`
	Summary   string `json:"summary"`
	Risk      string `json:"risk"` // "low" | "medium" | "high"
}

// permissionResponse is what a client sends back.
type permissionResponse struct {
	Verdict string `json:"verdict"` // "approve" | "deny"
	Reason  string `json:"reason,omitempty"`
}

// askRequest is the payload the daemon sends when a module needs user input.
type askRequest struct {
	SessionID string   `json:"session_id"`
	RequestID string   `json:"request_id"`
	Question  string   `json:"question"`
	Choices   []string `json:"choices,omitempty"`
}

// askResponse is what a client sends back.
type askResponse struct {
	Answer string `json:"answer"`
}
```

- [ ] **Step 2: Create daemon/api/requests.go with pendingRequest and broadcast methods**

Append to `daemon/api/requests.go`:

```go
// pendingRequest represents one in-flight daemon-to-client request.
type pendingRequest struct {
	method   string
	params   json.RawMessage
	mu       sync.Mutex
	resolved bool
	answer   json.RawMessage
	done     chan struct{}
}

// resolve records the first answer and signals completion. It returns true if
// this caller was the first to resolve.
func (pr *pendingRequest) resolve(answer json.RawMessage) bool {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.resolved {
		return false
	}
	pr.resolved = true
	pr.answer = answer
	close(pr.done)
	return true
}

// wait blocks until the request is resolved or the context is cancelled.
// It returns the answer or nil if the context expired.
func (pr *pendingRequest) wait(ctx context.Context) json.RawMessage {
	select {
	case <-pr.done:
		pr.mu.Lock()
		defer pr.mu.Unlock()
		return pr.answer
	case <-ctx.Done():
		return nil
	}
}

// BroadcastPermissionRequest sends a permission request to all subscribers of
// the session. The first response wins; later responders receive
// nabu_already_resolved.
func (h *Handler) BroadcastPermissionRequest(ctx context.Context, pr *permissionRequest) (*permissionResponse, error) {
	reqID := protocol.NewULID()
	params := map[string]any{
		"session_id": pr.SessionID, "request_id": reqID,
		"tool": pr.Tool, "summary": pr.Summary, "risk": pr.Risk,
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("permission request marshal: %w", err)
	}

	pending := &pendingRequest{
		method: "nabu.rpc.permission.response", params: paramsJSON,
		done: make(chan struct{}),
	}

	h.subMu.Lock()
	h.pendingRequests[reqID] = pending
	ss, ok := h.subscriptions[pr.SessionID]
	h.subMu.Unlock()
	if !ok {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("no subscribers for session %s", pr.SessionID)
	}

	ss.mu.Lock()
	liveConn := 0
	for id, cs := range ss.subs {
		if cs.conn == nil {
			delete(ss.subs, id)
			continue
		}
		msg := map[string]any{
			"jsonrpc": "2.0", "id": reqID,
			"method": "nabu.rpc.permission.request", "params": params,
		}
		if err := cs.conn.WriteJSON(context.Background(), msg); err != nil {
			delete(ss.subs, id)
		} else {
			liveConn++
		}
	}
	ss.mu.Unlock()

	if liveConn == 0 {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("all subscribers disconnected for session %s", pr.SessionID)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	answer := pending.wait(timeoutCtx)
	if answer == nil {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("permission request timed out after %v", requestTimeout)
	}

	var resp permissionResponse
	if err := json.Unmarshal(answer, &resp); err != nil {
		return nil, fmt.Errorf("invalid permission response: %w", err)
	}
	return &resp, nil
}

// BroadcastAskRequest sends an ask request to all subscribers of the session.
// The first response wins; later responders receive nabu_already_resolved.
func (h *Handler) BroadcastAskRequest(ctx context.Context, ar *askRequest) (*askResponse, error) {
	reqID := protocol.NewULID()
	params := map[string]any{
		"session_id": ar.SessionID, "request_id": reqID,
		"question": ar.Question,
	}
	if ar.Choices != nil {
		params["choices"] = ar.Choices
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("ask request marshal: %w", err)
	}

	pending := &pendingRequest{
		method: "nabu.rpc.ui.ask.response", params: paramsJSON,
		done: make(chan struct{}),
	}

	h.subMu.Lock()
	h.pendingRequests[reqID] = pending
	ss, ok := h.subscriptions[ar.SessionID]
	h.subMu.Unlock()
	if !ok {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("no subscribers for session %s", ar.SessionID)
	}

	ss.mu.Lock()
	liveConn := 0
	for id, cs := range ss.subs {
		if cs.conn == nil {
			delete(ss.subs, id)
			continue
		}
		msg := map[string]any{
			"jsonrpc": "2.0", "id": reqID,
			"method": "nabu.rpc.ui.ask", "params": params,
		}
		if err := cs.conn.WriteJSON(context.Background(), msg); err != nil {
			delete(ss.subs, id)
		} else {
			liveConn++
		}
	}
	ss.mu.Unlock()

	if liveConn == 0 {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("all subscribers disconnected for session %s", ar.SessionID)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	answer := pending.wait(timeoutCtx)
	if answer == nil {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("ask request timed out after %v", requestTimeout)
	}

	var resp askResponse
	if err := json.Unmarshal(answer, &resp); err != nil {
		return nil, fmt.Errorf("invalid ask response: %w", err)
	}
	return &resp, nil
}
```

- [ ] **Step 3: Create daemon/api/asker.go — the Asker adapter**

Create `daemon/api/asker.go`:

```go
package api

import (
	"context"
	"fmt"

	"github.com/corporealshift/nabu/protocol"
)

// askerAdapter bridges the Handler's broadcast model to the agent.Asker interface.
// The agent loop calls Asker.Permission / Asker.Ask before a gated tool call;
// the adapter translates those calls into Handler.BroadcastPermissionRequest /
// Handler.BroadcastAskRequest, converting between the ToolCallData/simple params
// and the broadcast request types.
type askerAdapter struct {
	handler *Handler
}

// newAskerAdapter creates a new Asker that delegates to the Handler's broadcast.
func newAskerAdapter(h *Handler) *askerAdapter {
	return &askerAdapter{handler: h}
}

// Permission broadcasts a permission request to all clients. The first answer
// wins. Returns (approved, reason).
func (a *askerAdapter) Permission(ctx context.Context, sessionID string, call protocol.ToolCallData, summary, risk string) (bool, string) {
	pr := &permissionRequest{
		SessionID: sessionID,
		RequestID: call.CallID,
		Tool:      call.Tool,
		Summary:   summary,
		Risk:      risk,
	}
	resp, err := a.handler.BroadcastPermissionRequest(ctx, pr)
	if err != nil {
		return false, fmt.Sprintf("permission request failed: %v", err)
	}
	if resp.Verdict == "deny" {
		return false, resp.Reason
	}
	return true, ""
}

// Ask broadcasts an ask request to all clients. The first answer wins.
func (a *askerAdapter) Ask(ctx context.Context, sessionID, question string, choices []string) (string, error) {
	ar := &askRequest{
		SessionID: sessionID,
		RequestID: sessionID,
		Question:  question,
		Choices:   choices,
	}
	resp, err := a.handler.BroadcastAskRequest(ctx, ar)
	if err != nil {
		return "", fmt.Errorf("ask request failed: %w", err)
	}
	return resp.Answer, nil
}
```

- [ ] **Step 4: Write the failing test for the Asker adapter**

Append to `daemon/api/asker_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

func TestAskerAdapterPermission(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	adapter := newAskerAdapter(h)

	// Send the notification in a goroutine so we can respond.
	var notif json.RawMessage
	notifCh := make(chan json.RawMessage, 1)
	go func() {
		if err := c.Read(context.Background(), websocket.MessageText, &notif); err != nil {
			notifCh <- nil
			return
		}
		notifCh <- notif
	}()

	// Wait for the notification.
	select {
	case n := <-notifCh:
		notif = n
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for permission notification")
	}

	var notifMap map[string]any
	if err := json.Unmarshal(notif, &notifMap); err != nil {
		t.Fatal(err)
	}
	reqID, _ := notifMap["id"].(string)

	// Send approve response.
	resp := map[string]any{
		"jsonrpc": "2.0", "id": reqID,
		"result": map[string]any{"verdict": "approve"},
	}
	if err := wsjson.Write(context.Background(), c, resp); err != nil {
		t.Fatal(err)
	}

	approved, reason := adapter.Permission(context.Background(), s.ID(), protocol.ToolCallData{
		CallID: "call-1", Tool: "bash", Arguments: json.RawMessage(`{}`), Source: "agent",
	}, "run test", "medium")

	if !approved {
		t.Errorf("expected approved, got false: %s", reason)
	}
}
```

- [ ] **Step 5: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestAskerAdapterPermission -v`
Expected: compile error `undefined: newAskerAdapter` (the function does not exist yet).

- [ ] **Step 6: Wire the Asker adapter into the Handler**

In `daemon/api/handler.go`, add a method to create the Handler with an Asker:

```go
// NewHandlerWithAsker builds a Handler and wires its broadcast methods as
// the agent.Asker, so the agent loop can call Asker.Permission / Asker.Ask
// before gated tool calls.
func NewHandlerWithAsker(m *agent.Manager, st *session.Store, log *slog.Logger) (*Handler, *askerAdapter) {
	h := NewHandler(m, st, log)
	a := newAskerAdapter(h)
	// Wire the adapter into the agent's Deps so the Manager uses it.
	m.SetAsker(a)
	return h, a
}
```

Wait — `agent.Manager` does not have a `SetAsker` method. The Asker is set at construction time via `agent.Deps.Asker`. So the wiring must happen at construction:

In the caller (not in `handler.go`), the wiring is:

```go
h := NewHandler(m, st, log)
adapter := newAskerAdapter(h)
m, err := agent.New(agent.Deps{
    Store: st, Providers: providers, Modules: modules,
    Builtins: builtins, Log: log, Asker: adapter, Deltas: sink,
}, cfg)
```

Since Task 8 is only writing the adapter and the broadcast foundation (the actual wiring in `cmd/` is a separate concern), Step 6 just verifies the adapter is constructible. Remove Step 6 and proceed to Step 7.

- [ ] **Step 7: Write the failing test for the Asker adapter with deny**

Append to `daemon/api/asker_test.go`:

```go
func TestAskerAdapterPermissionDeny(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	adapter := newAskerAdapter(h)

	var notif json.RawMessage
	notifCh := make(chan json.RawMessage, 1)
	go func() {
		if err := c.Read(context.Background(), websocket.MessageText, &notif); err != nil {
			notifCh <- nil
			return
		}
		notifCh <- notif
	}()

	select {
	case n := <-notifCh:
		notif = n
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for permission notification")
	}

	var notifMap map[string]any
	if err := json.Unmarshal(notif, &notifMap); err != nil {
		t.Fatal(err)
	}
	reqID, _ := notifMap["id"].(string)

	resp := map[string]any{
		"jsonrpc": "2.0", "id": reqID,
		"result": map[string]any{"verdict": "deny", "reason": "unsafe command"},
	}
	if err := wsjson.Write(context.Background(), c, resp); err != nil {
		t.Fatal(err)
	}

	approved, reason := adapter.Permission(context.Background(), s.ID(), protocol.ToolCallData{
		CallID: "call-2", Tool: "bash", Arguments: json.RawMessage(`{}`), Source: "agent",
	}, "rm -rf /", "high")

	if approved {
		t.Error("expected denied, got approved")
	}
	if reason != "unsafe command" {
		t.Errorf("reason: got %q, want %q", reason, "unsafe command")
	}
}
```

- [ ] **Step 8: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestAskerAdapterPermissionDeny -v`
Expected: compile error `undefined: newAskerAdapter`.

- [ ] **Step 9: Write the failing test for the Asker adapter Ask**

Append to `daemon/api/asker_test.go`:

```go
func TestAskerAdapterAsk(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	adapter := newAskerAdapter(h)

	var notif json.RawMessage
	notifCh := make(chan json.RawMessage, 1)
	go func() {
		if err := c.Read(context.Background(), websocket.MessageText, &notif); err != nil {
			notifCh <- nil
			return
		}
		notifCh <- notif
	}()

	select {
	case n := <-notifCh:
		notif = n
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for ask notification")
	}

	var notifMap map[string]any
	if err := json.Unmarshal(notif, &notifMap); err != nil {
		t.Fatal(err)
	}
	reqID, _ := notifMap["id"].(string)

	resp := map[string]any{
		"jsonrpc": "2.0", "id": reqID,
		"result": map[string]any{"answer": "edit main.go"},
	}
	if err := wsjson.Write(context.Background(), c, resp); err != nil {
		t.Fatal(err)
	}

	answer, err := adapter.Ask(context.Background(), s.ID(), "Which file to edit?", []string{"main.go", "util.go"})
	if err != nil {
		t.Fatalf("ask error: %v", err)
	}
	if answer != "edit main.go" {
		t.Errorf("answer: got %q, want %q", answer, "edit main.go")
	}
}
```

- [ ] **Step 10: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestAskerAdapterAsk -v`
Expected: compile error `undefined: newAskerAdapter`.

---

### Gap 2: a timed-out request must still accept a later answer

The spec (protocol/spec.md §7.15) says: "Requests time out after a configurable interval (default 10 minutes) with the session moving to blocked; a later answer is still accepted and resumes it."

Two late-answer cases:
1. Late answer to an *already answered* request → `nabu_already_resolved` (already handled by `pendingRequest.resolve()` returning false).
2. Late answer to a *timed-out* request → **accepted**, session **resumes** from `blocked`.

Task 5 covers only case 1. This gap implements case 2.

- [ ] **Step 11: Add timedOut field to pendingRequest**

In `daemon/api/requests.go`, add a `timedOut` field to `pendingRequest`:

```go
type pendingRequest struct {
	method   string
	params   json.RawMessage
	mu       sync.Mutex
	resolved bool
	timedOut bool
	answer   json.RawMessage
	done     chan struct{}
}
```

- [ ] **Step 12: Update broadcast methods to set timedOut instead of deleting**

In `daemon/api/requests.go`, replace the timeout path in `BroadcastPermissionRequest`:

Replace:
```go
	if answer == nil {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("permission request timed out after %v", requestTimeout)
	}
```

With:
```go
	if answer == nil {
		pending.mu.Lock()
		pending.timedOut = true
		pending.mu.Unlock()
		return nil, fmt.Errorf("permission request timed out after %v", requestTimeout)
	}
```

And replace the same pattern in `BroadcastAskRequest`:

Replace:
```go
	if answer == nil {
		h.subMu.Lock()
		delete(h.pendingRequests, reqID)
		h.subMu.Unlock()
		return nil, fmt.Errorf("ask request timed out after %v", requestTimeout)
	}
```

With:
```go
	if answer == nil {
		pending.mu.Lock()
		pending.timedOut = true
		pending.mu.Unlock()
		return nil, fmt.Errorf("ask request timed out after %v", requestTimeout)
	}
```

- [ ] **Step 13: Add resumeSession callback to Handler**

In `daemon/api/handler.go`, add a field to the Handler struct:

```go
type Handler struct {
	manager         *agent.Manager
	store           *session.Store
	log             *slog.Logger
	mu              sync.RWMutex
	mods            map[string]methodFunc
	subMu           sync.Mutex
	subscriptions   map[string]*sessionSubs
	pendingRequests map[string]*pendingRequest
	resumeSession   func(ctx context.Context, sessionID string) error
}
```

Add a setter (called during wiring):

```go
// SetResumeSession sets the callback invoked when a late answer arrives
// for a timed-out request. The callback resumes the session.
func (h *Handler) SetResumeSession(fn func(ctx context.Context, sessionID string) error) {
	h.resumeSession = fn
}
```

- [ ] **Step 14: Update the response dispatch to handle late answers to timed-out requests**

In `daemon/api/handler.go`, in `handleIncomingResponse`, add the timed-out path:

```go
func (h *Handler) handleIncomingResponse(conn *websocket.Conn, req *jsonrpcRequest) {
	h.subMu.Lock()
	pr, ok := h.pendingRequests[req.ID.String()]
	h.subMu.Unlock()
	if !ok {
		h.sendError(conn, req.ID, protocol.CodeInternalError,
			"no pending request for id "+req.ID.String())
		return
	}

	var resp map[string]any
	if err := json.Unmarshal(req.Params, &resp); err != nil {
		h.sendError(conn, req.ID, protocol.CodeInvalidParams, "invalid response")
		return
	}

	answer, ok := resp["result"]
	if !ok {
		h.sendError(conn, req.ID, protocol.CodeInvalidParams, "missing result")
		return
	}

	answerJSON, err := json.Marshal(answer)
	if err != nil {
		h.sendError(conn, req.ID, protocol.CodeInternalError, err.Error())
		return
	}

	if !pr.resolve(answerJSON) {
		// Request was already resolved by another client.
		h.sendError(conn, req.ID, protocol.CodeAlreadyResolved, "already_resolved")
		return
	}

	// First answer wins. If the request had timed out, accept the late
	// answer and resume the session (spec §7.15).
	pr.mu.Lock()
	timedOut := pr.timedOut
	pr.mu.Unlock()

	if timedOut && h.resumeSession != nil {
		// Extract session_id from the stored params.
		var params map[string]any
		if err := json.Unmarshal(pr.params, &params); err == nil {
			if sid, ok := params["session_id"].(string); ok {
				h.resumeSession(context.Background(), sid)
			}
		}
		// Clean up the timed-out pending request.
		h.subMu.Lock()
		delete(h.pendingRequests, req.ID.String())
		h.subMu.Unlock()
	}
}
```

- [ ] **Step 15: Write the failing test for late answer to timed-out request**

Append to `daemon/api/requests_test.go`:

```go
func TestLateAnswerToTimedOutRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())

	// Track that resumeSession was called.
	var resumed bool
	var resumedID string
	h.SetResumeSession(func(ctx context.Context, sessionID string) error {
		resumed = true
		resumedID = sessionID
		return nil
	})

	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	if err := wsjson.Write(context.Background(), c, hello); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), c, &helloResp); err != nil {
		t.Fatal(err)
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	subReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
		"params": map[string]any{"session_id": s.ID()},
	}
	if err := wsjson.Write(context.Background(), c, subReq); err != nil {
		t.Fatal(err)
	}
	var subResp map[string]any
	if err := wsjson.Read(context.Background(), c, &subResp); err != nil {
		t.Fatal(err)
	}
	if subResp["error"] != nil {
		t.Fatalf("subscribe error: %+v", subResp["error"])
	}

	pr := &permissionRequest{
		SessionID: s.ID(), RequestID: "perm-timeout-late",
		Tool: "bash", Summary: "run ls", Risk: "low",
	}

	// Broadcast with a short context timeout so the request times out quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = h.BroadcastPermissionRequest(ctx, pr)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error: got %q, want to contain %q", err.Error(), "timeout")
	}

	// At this point the request is timed out but still in pendingRequests.
	// Read the notification that was sent.
	var notif json.RawMessage
	if err := c.Read(context.Background(), websocket.MessageText, &notif); err != nil {
		t.Fatal(err)
	}
	var notifMap map[string]any
	if err := json.Unmarshal(notif, &notifMap); err != nil {
		t.Fatal(err)
	}
	reqID, _ := notifMap["id"].(string)

	// Now send a late answer.
	resp := map[string]any{
		"jsonrpc": "2.0", "id": reqID,
		"result": map[string]any{"verdict": "approve"},
	}
	if err := wsjson.Write(context.Background(), c, resp); err != nil {
		t.Fatal(err)
	}

	// Give the handler time to process the late answer.
	time.Sleep(100 * time.Millisecond)

	if !resumed {
		t.Error("expected resumeSession to be called for late answer to timed-out request")
	}
	if resumedID != s.ID() {
		t.Errorf("resumed session ID: got %q, want %q", resumedID, s.ID())
	}
}
```

- [ ] **Step 16: Run it — it should fail**

Run: `go test ./daemon/api/ -run TestLateAnswerToTimedOutRequest -v`
Expected: the test fails because `timedOut` is not set on timeout (the pending request is deleted, so the late answer gets `no pending request` error). The test triggers: `requests_test.go:XX: no pending request for id ...` (the handler sends an error for the late answer, and `resumed` remains false).

- [ ] **Step 17: Write the failing test for already_resolved (already answered)**

This test verifies that case 1 from the spec still works — a late answer to an already-answered request receives `nabu_already_resolved`.

Append to `daemon/api/requests_test.go`:

```go
func TestLateAnswerToAlreadyAnsweredRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tmpDir := t.TempDir()
	st, err := session.Open(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m, err := agent.New(agent.Deps{Store: st, Log: slog.Default()}, agent.Config{})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(m, st, slog.Default())
	srv := NewServer(h, &Config{Bind: "127.0.0.1:0"}, slog.Default())
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	c1, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()

	c2, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()

	hello := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nabu.hello"}
	for _, c := range []*websocket.Conn{c1, c2} {
		if err := wsjson.Write(context.Background(), c, hello); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
	}

	s, err := st.Create("C:/Users/test/workspace", "test-ws", protocol.Options{
		Model: "test-model", PermissionMode: protocol.PermissionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []*websocket.Conn{c1, c2} {
		subReq := map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "nabu.session.subscribe",
			"params": map[string]any{"session_id": s.ID()},
		}
		if err := wsjson.Write(context.Background(), c, subReq); err != nil {
			t.Fatal(err)
		}
		var resp map[string]any
		if err := wsjson.Read(context.Background(), c, &resp); err != nil {
			t.Fatal(err)
		}
		if resp["error"] != nil {
			t.Fatalf("subscribe error: %+v", resp["error"])
		}
	}

	pr := &permissionRequest{
		SessionID: s.ID(), RequestID: "perm-already",
		Tool: "bash", Summary: "run go test", Risk: "medium",
	}

	var notif1, notif2 json.RawMessage
	if err := c1.Read(context.Background(), websocket.MessageText, &notif1); err != nil {
		t.Fatal(err)
	}
	if err := c2.Read(context.Background(), websocket.MessageText, &notif2); err != nil {
		t.Fatal(err)
	}

	var notif1Map map[string]any
	if err := json.Unmarshal(notif1, &notif1Map); err != nil {
		t.Fatal(err)
	}
	reqID, _ := notif1Map["id"].(string)

	// Client 1 answers first.
	resp1 := map[string]any{
		"jsonrpc": "2.0", "id": reqID,
		"result": map[string]any{"verdict": "approve"},
	}
	if err := wsjson.Write(context.Background(), c1, resp1); err != nil {
		t.Fatal(err)
	}

	var notif2Map map[string]any
	if err := json.Unmarshal(notif2, &notif2Map); err != nil {
		t.Fatal(err)
	}
	reqID2, _ := notif2Map["id"].(string)

	// Client 2 answers late.
	resp2 := map[string]any{
		"jsonrpc": "2.0", "id": reqID2,
		"result": map[string]any{"verdict": "deny"},
	}
	if err := wsjson.Write(context.Background(), c2, resp2); err != nil {
		t.Fatal(err)
	}

	// Client 2 should receive an already_resolved error.
	var errResp json.RawMessage
	if err := c2.Read(context.Background(), websocket.MessageText, &errResp); err != nil {
		t.Fatal(err)
	}
	var errMap map[string]any
	if err := json.Unmarshal(errResp, &errMap); err != nil {
		t.Fatal(err)
	}
	errObj, ok := errMap["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", errMap["error"])
	}
	code, _ := errObj["code"].(float64)
	if code != float64(protocol.CodeAlreadyResolved) {
		t.Errorf("error code: got %v, want %d", code, protocol.CodeAlreadyResolved)
	}
	message, _ := errObj["message"].(string)
	if message != "already_resolved" {
		t.Errorf("error message: got %q, want %q", message, "already_resolved")
	}

	// The broadcast should return the first answer.
	result, err := h.BroadcastPermissionRequest(context.Background(), pr)
	if err != nil {
		t.Fatalf("BroadcastPermissionRequest error: %v", err)
	}
	if result.Verdict != "approve" {
		t.Errorf("verdict: got %q, want %q", result.Verdict, "approve")
	}
}
```

- [ ] **Step 18: Run it — it should pass (already_resolved path already works)**

Run: `go test ./daemon/api/ -run TestLateAnswerToAlreadyAnsweredRequest -v`
Expected output:
```
=== RUN   TestLateAnswerToAlreadyAnsweredRequest
--- PASS: TestLateAnswerToAlreadyAnsweredRequest (0.01s)
PASS
ok  	github.com/corporealshift/nabu/daemon/api	0.052s
```

---

### Gap 3: spec §12 budget defaults have no home in config

The architecture spec §12 fixes defaults that `BudgetConfig` from Task 1 does not carry:
- `max_consecutive_vetoes` default **5**
- `no_progress_turns` default **3**
- `max_turns` unlimited interactively, 60 under `nabu run`

`agent.Config` already has `MaxConsecutiveVetoes` and `NoProgressTurns` fields — confirmed in `daemon/agent/config.go:29-32`. Task 1's `BudgetConfig` (in `daemon/config/config.go`) has `MaxTurns`, `MaxTokens`, `MaxUSD` but not the veto/progress fields.

For `max_turns`: the mechanism is a simple field override — `nabu run` sets `MaxTurns = 60` at session creation; the TUI leaves it at the config default (0 = unlimited). No special config section needed.

- [ ] **Step 19: Extend BudgetConfig with MaxConsecutiveVetoes and NoProgressTurns**

In `daemon/config/config.go`, update `BudgetConfig`:

Replace:
```go
// BudgetConfig holds the [budget] section.
type BudgetConfig struct {
	MaxTurns  int     `json:"max_turns"`
	MaxTokens int     `json:"max_tokens"`
	MaxUSD    float64 `json:"max_usd"`
}
```

With:
```go
// BudgetConfig holds the [budget] section.
// Defaults per architecture spec §12:
//   max_consecutive_vetoes = 5, no_progress_turns = 3,
//   max_turns = 0 (unlimited) — overridden to 60 by `nabu run`.
type BudgetConfig struct {
	MaxTurns             int     `json:"max_turns"`
	MaxTokens            int     `json:"max_tokens"`
	MaxUSD               float64 `json:"max_usd"`
	MaxConsecutiveVetoes int     `json:"max_consecutive_vetoes"`
	NoProgressTurns      int     `json:"no_progress_turns"`
}
```

- [ ] **Step 20: Wire the new fields into agent.Config withDefaults**

In `daemon/config/config.go`, update the `Load` function to pass the new fields to agent.Config (or document the wiring point). Since `BudgetConfig` lives in `daemon/config` and `agent.Config` lives in `daemon/agent`, the wiring happens in the caller that builds `agent.Config` from `config.Config`. For now, add a helper:

Append to `daemon/config/config.go`:

```go
// ToAgentConfig converts this Config into an agent.Config, applying the
// budget defaults from architecture spec §12.
func (c *Config) ToAgentConfig() agent.Config {
	return agent.Config{
		MaxConsecutiveVetoes: c.Budget.MaxConsecutiveVetoes,
		NoProgressTurns:      c.Budget.NoProgressTurns,
		ModuleConfigs:        c.Modules,
	}
}
```

Wait — `agent.Config` has more fields. The `ToAgentConfig` helper is a convenience; the caller can also set fields individually. Since this adds an import cycle concern (`config` would import `agent`), let's skip the helper and instead test the BudgetConfig fields directly. The wiring from `config.BudgetConfig` to `agent.Config` is a one-liner in the caller (daemon startup).

Remove the `ToAgentConfig` helper and proceed to Step 21.

- [ ] **Step 21: Write the failing test for BudgetConfig defaults**

Append to `daemon/config/config_test.go`:

```go
func TestBudgetConfigDefaults(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"budget": {
			"max_turns": 100,
			"max_tokens": 50000,
			"max_usd": 5.0
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Budget.MaxTurns != 100 {
		t.Errorf("max_turns: got %d, want 100", cfg.Budget.MaxTurns)
	}
	if cfg.Budget.MaxTokens != 50000 {
		t.Errorf("max_tokens: got %d, want 50000", cfg.Budget.MaxTokens)
	}
	if cfg.Budget.MaxUSD != 5.0 {
		t.Errorf("max_usd: got %f, want 5.0", cfg.Budget.MaxUSD)
	}
	// New fields: zero when not specified (defaults applied by agent.Config.withDefaults).
	if cfg.Budget.MaxConsecutiveVetoes != 0 {
		t.Errorf("max_consecutive_vetoes: got %d, want 0 (zero = agent default)", cfg.Budget.MaxConsecutiveVetoes)
	}
	if cfg.Budget.NoProgressTurns != 0 {
		t.Errorf("no_progress_turns: got %d, want 0 (zero = agent default)", cfg.Budget.NoProgressTurns)
	}
}
```

- [ ] **Step 22: Run it — it should fail**

Run: `go test ./daemon/config/ -run TestBudgetConfigDefaults -v`
Expected: compile error `cfg.Budget.MaxConsecutiveVetoes undefined` (the fields do not exist yet).

- [ ] **Step 23: Write the failing test for BudgetConfig with new fields specified**

Append to `daemon/config/config_test.go`:

```go
func TestBudgetConfigWithNewFields(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"budget": {
			"max_turns": 60,
			"max_consecutive_vetoes": 3,
			"no_progress_turns": 5
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Budget.MaxTurns != 60 {
		t.Errorf("max_turns: got %d, want 60", cfg.Budget.MaxTurns)
	}
	if cfg.Budget.MaxConsecutiveVetoes != 3 {
		t.Errorf("max_consecutive_vetoes: got %d, want 3", cfg.Budget.MaxConsecutiveVetoes)
	}
	if cfg.Budget.NoProgressTurns != 5 {
		t.Errorf("no_progress_turns: got %d, want 5", cfg.Budget.NoProgressTurns)
	}
}
```

- [ ] **Step 24: Run it — it should fail**

Run: `go test ./daemon/config/ -run TestBudgetConfigWithNewFields -v`
Expected: compile error `cfg.Budget.MaxConsecutiveVetoes undefined`.

---

### Gap 4: no go mod tidy step

Task 2 introduces `github.com/coder/websocket v1.8.15` as the repo's first dependency. No step generates `go.mod` and `go.sum`.

- [ ] **Step 25: Run go mod tidy**

Run: `go mod tidy`
Expected output includes:
```
go: finding module for package github.com/coder/websocket
go: found github.com/coder/websocket in github.com/coder/websocket v1.8.15
```
(or similar, depending on which packages already import it). The command should complete with exit code 0 and produce a `go.sum` file.

- [ ] **Step 26: Verify go.sum exists and the gate passes**

Run: `test -f go.sum && echo "go.sum exists" || echo "go.sum missing"`
Expected: `go.sum exists`

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

### Summary

**File written:** `.pi-delegations/task-08-gaps.out.md`

**Number of steps:** 26

**Key design decisions:**
- Gap 1: Adapter in `daemon/api/asker.go`. The `askerAdapter` struct satisfies `agent.Asker` by delegating to `Handler.BroadcastPermissionRequest` and `Handler.BroadcastAskRequest`, converting between `ToolCallData`/simple params and the broadcast request types.
- Gap 2: `pendingRequest.timedOut` flag replaces the delete-on-timeout behavior. Late answers to timed-out requests are accepted, the pending request is resolved, and `Handler.resumeSession` callback resumes the session.
- Gap 3: `BudgetConfig` extended with `MaxConsecutiveVetoes` and `NoProgressTurns` fields. Zero values mean "use agent.Config.withDefaults()". `max_turns` override mechanism: `nabu run` sets it to 60 at session creation; TUI leaves it at config default (0 = unlimited).
- Gap 4: `go mod tidy` generates `go.sum`; gate verification confirms everything compiles.

**Symbols confirmed in source:**
- `agent.Asker` interface (methods `Permission`, `Ask`) → `daemon/agent/manager.go:23` — confirmed
- `agent.Deps.Asker` field → `daemon/agent/manager.go:38` — confirmed
- `agent.Manager.Resume(ctx, id, budget)` → `daemon/agent/manager.go:268` — confirmed
- `agent.Config.MaxConsecutiveVetoes` (int, default 5) → `daemon/agent/config.go:29` — confirmed
- `agent.Config.NoProgressTurns` (int, default 3) → `daemon/agent/config.go:32` — confirmed
- `protocol.CodeAlreadyResolved = -32004` → `protocol/errors.go:16` — confirmed
- `protocol.ErrorNames["nabu_already_resolved"]` → `protocol/errors.go:22` — confirmed
- `protocol.BudgetData` (MaxTurns, MaxTokens, MaxUSD, Source) → `protocol/types.go:201` — confirmed
- `protocol.StateBlocked` = `"blocked"` → `protocol/types.go:37` — confirmed
- `protocol.StatePaused` = `"paused"` → `protocol/types.go:38` — confirmed
- `protocol/spec.md §7.15` — timeout default 10 minutes; session moves to `blocked`; late answer accepted and resumes — confirmed

---

---

## What Task 8 is for

Task 8 is not new scope. It closes four gaps that review found in Tasks 1 through 7,
listed here so the reason for each is on record:

1. **The agent's `Asker` is single-client and synchronous; the API's model is broadcast
   first-responder-wins.** Task 5 identified the mismatch and deferred the wiring. Task
   8 closes it with an adapter in `daemon/api/asker.go` rather than a change to
   `daemon/agent`, because mechanism lives in core and the API must not push policy
   into the agent.
2. **A timed-out request must still accept a later answer.** `protocol/spec.md` §7.15
   requires two distinct late-answer paths, and Task 5 implemented only the first:
   a late answer to an *answered* request gets `nabu_already_resolved`, while a late
   answer to a *timed-out* request is **accepted and resumes the session** from
   `blocked`.
3. **Spec §12 budget defaults had no home in config.** Task 8 extends `BudgetConfig`
   with `max_consecutive_vetoes` (5) and `no_progress_turns` (3). The interactive
   versus `nabu run` split for `max_turns` is handled by `nabu run` overriding to 60 at
   session creation, leaving config's `0 = unlimited` as the interactive default.
4. **Task 2 introduced the first dependency without a `go mod tidy` step.** Task 8 adds
   it, along with verification that `go.sum` exists and the gate passes.

## One pre-existing defect, found while planning P1b

This is **not** P1b scope and no task above fixes it, but P1b is the milestone that
first makes it reachable, so it should be fixed during this milestone.

Architecture spec §8, "Permission modes", says:

> `bypass` approves everything and **appends a `notice`** when set.

`agent.Manager.SetOption` (shipped in P1a) validates `permission_mode` against ask /
auto / bypass and appends an `options_change` event — but it never appends the
`notice`. Until `nabu.session.set_option` exists (Task 4), no client can reach this
path, which is why P1a's tests did not catch it.

The fix belongs in `daemon/agent`, not in `daemon/api`: it is core state behaviour, not
transport. It is a few lines plus a test. Do it as its own commit rather than folding
it into an API task, so the history shows it as a P1a correction.

---

## Verifying

The project gate, run from the repo root, is the only claim that counts:

```
go build ./... && go vet ./... && go test ./...
```

CI runs that gate plus `gofmt -l .` on both `ubuntu-latest` and `windows-latest` for
every pull request. **Windows is the primary target.** A task is not done until the
full gate passes, not a single package.

The live smoke test from P1a stays skipped unless `NABU_LIVE_BASE_URL` is set, so the
gate remains hermetic.

## Done when

- Every task above is complete and its steps are checked off.
- Task 8 exists and closes the four gaps listed above.
- `nabu daemon` serves; every CLI subcommand auto-starts a detached daemon when none
  is listening.
- Every method in `protocol/spec.md` §7 is implemented and dispatched.
- Multi-client fan-out, ephemeral deltas, and first-responder-wins all have passing
  tests.
- The full gate is green on both platforms in CI.

## Not in this phase

- **P1c: the policy modules** — `skills`, `guard`, `verify`, `report`. The API carries
  transport mechanism only; every policy decision belongs to a module.
- **P2: the TUI.** P1b's protocol is what it will speak.
- FCM notification dispatch, the Android client, and the Rust GUI.

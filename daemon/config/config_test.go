package config

import (
	"os"
	"path/filepath"
	"strings"
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
	if !cfg.Modules["judge"]["enabled"].(bool) {
		t.Errorf("modules.judge.enabled: got %v, want true", cfg.Modules["judge"])
	}
	if cfg.Modules["curator"]["enabled"].(bool) {
		t.Errorf("modules.curator.enabled: got %v, want false", cfg.Modules["curator"])
	}
}

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

// Providers live in a map of values, so defaults must be written back by key.
// Ranging and mutating the loop variable alone silently discards them.
func TestProviderDefaultsSurviveLoad(t *testing.T) {
	root := t.TempDir()
	body := `{"providers": {"local": {"base_url": "http://localhost:8033/v1"}}}`
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Providers["local"]
	if p.MaxInFlight != 1 {
		t.Errorf("max_in_flight: got %d, want 1", p.MaxInFlight)
	}
	if p.TasksEnabled == nil || !*p.TasksEnabled {
		t.Errorf("tasks_enabled: got %v, want true", p.TasksEnabled)
	}
	if p.BaseURL != "http://localhost:8033/v1" {
		t.Errorf("base_url should not be defaulted over: got %q", p.BaseURL)
	}
}

// The spec puts the overlay at <workspace>/.nabu/config.json, so trusting the
// workspace must be enough — the trust check has to look past the .nabu dir.
func TestOverlayAtSpecPathUsesWorkspaceTrust(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "workspace")
	nabuDir := filepath.Join(ws, ".nabu")
	if err := os.MkdirAll(nabuDir, 0o755); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(nabuDir, "config.json")
	body := `{"providers": {"local": {"base_url": "http://localhost:8033/v1"}}}`
	if err := os.WriteFile(overlay, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.ApplyOverlay(overlay); err != nil {
		t.Fatalf("ApplyOverlay untrusted: %v", err)
	}
	if _, ok := cfg.Providers["local"]; ok {
		t.Fatal("overlay must be ignored while the workspace is untrusted")
	}

	cfg.TrustWorkspace(ws)
	if err := cfg.ApplyOverlay(overlay); err != nil {
		t.Fatalf("ApplyOverlay trusted: %v", err)
	}
	p, ok := cfg.Providers["local"]
	if !ok {
		t.Fatal("overlay must apply once the workspace is trusted")
	}
	if p.MaxInFlight != 1 {
		t.Errorf("overlay provider defaults: max_in_flight got %d, want 1", p.MaxInFlight)
	}
}

// Spec 12 fixes these two loop bounds, and config must be able to override
// them. Without a home here, the agent's defaults were unreachable.
func TestBudgetLoopBoundDefaults(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Budget.MaxConsecutiveVetoes != 5 {
		t.Errorf("max_consecutive_vetoes: got %d, want 5", cfg.Budget.MaxConsecutiveVetoes)
	}
	if cfg.Budget.NoProgressTurns != 3 {
		t.Errorf("no_progress_turns: got %d, want 3", cfg.Budget.NoProgressTurns)
	}
	if cfg.Budget.MaxTurns != 0 {
		t.Errorf("max_turns should default to unlimited, got %d", cfg.Budget.MaxTurns)
	}
}

func TestBudgetLoopBoundsOverridable(t *testing.T) {
	root := t.TempDir()
	body := `{"budget": {"max_turns": 12, "max_consecutive_vetoes": 9, "no_progress_turns": 1}}`
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Budget.MaxTurns != 12 {
		t.Errorf("max_turns: got %d, want 12", cfg.Budget.MaxTurns)
	}
	if cfg.Budget.MaxConsecutiveVetoes != 9 {
		t.Errorf("max_consecutive_vetoes: got %d, want 9", cfg.Budget.MaxConsecutiveVetoes)
	}
	if cfg.Budget.NoProgressTurns != 1 {
		t.Errorf("no_progress_turns: got %d, want 1", cfg.Budget.NoProgressTurns)
	}
}

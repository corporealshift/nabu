package vcs

import (
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

func toolNames(m *Module) []string {
	var names []string
	for _, t := range m.Tools() {
		names = append(names, t.Name)
	}
	return names
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// A tool the model can see but never use is a tax on every request: its
// description rides in the prompt whether or not the program exists.
func TestAToolIsOfferedOnlyWhenItsProgramExists(t *testing.T) {
	t.Run("neither installed offers nothing", func(t *testing.T) {
		m := &Module{enabled: true}
		if names := toolNames(m); len(names) != 0 {
			t.Errorf("tools = %v, want none", names)
		}
	})

	t.Run("git alone offers git alone", func(t *testing.T) {
		m := &Module{enabled: true, gitPath: "git"}
		names := toolNames(m)
		if !has(names, "git") || has(names, "gh") {
			t.Errorf("tools = %v, want just git", names)
		}
	})

	t.Run("both installed offers both", func(t *testing.T) {
		m := &Module{enabled: true, gitPath: "git", ghPath: "gh"}
		names := toolNames(m)
		if !has(names, "git") || !has(names, "gh") {
			t.Errorf("tools = %v, want both", names)
		}
	})
}

// A workspace that is not a repository, or a benchmark run that should not see
// these tools, turns the module off.
func TestDisabledOffersNothing(t *testing.T) {
	m := &Module{enabled: false, gitPath: "git", ghPath: "gh"}
	if names := toolNames(m); len(names) != 0 {
		t.Errorf("tools = %v, want none when disabled", names)
	}
}

// Not having gh installed is a fact about the machine, not a misconfiguration,
// so Init must not fail over it.
func TestInitSucceedsWithNeitherProgram(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{"git_path": "", "gh_path": ""}); err != nil {
		t.Fatalf("Init should not fail when the programs are missing: %v", err)
	}
	if m.timeout <= 0 || m.maxOutput <= 0 {
		t.Errorf("defaults not applied: timeout=%v maxOutput=%d", m.timeout, m.maxOutput)
	}
}

func TestConfiguredPathsWin(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{"git_path": "/custom/git", "gh_path": "/custom/gh"}); err != nil {
		t.Fatal(err)
	}
	if m.gitPath != "/custom/git" || m.ghPath != "/custom/gh" {
		t.Errorf("gitPath=%q ghPath=%q, want the configured ones", m.gitPath, m.ghPath)
	}
}

func TestModuleName(t *testing.T) {
	if (&Module{}).Name() != "vcs" {
		t.Error("the name is what config sections and event sources key on")
	}
}

// A reply that got cut has to say so. Silently handing back half a JSON
// document would have the model parse it, fail, and blame its own request.
func TestTruncationIsAnnounced(t *testing.T) {
	m := &Module{maxOutput: 32}
	out := m.truncateJSON(`{"body":"` + string(make([]byte, 200)) + `"}`)
	if len(out) <= 32 {
		t.Fatal("the note should be appended, not counted inside the budget")
	}
	if !contains(out, "truncated") {
		t.Errorf("a cut reply must say so, got %q", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// twoWorkspaces builds a session workspace and a second repository to read
// from, and returns a Builtins wired to both.
func twoWorkspaces(t *testing.T) (*Builtins, fakeSession, string) {
	t.Helper()
	here := t.TempDir()
	there := t.TempDir()

	write := func(dir, name, body string) {
		t.Helper()
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(here, "mine.txt", "this workspace\n")
	write(there, "theirs.txt", "the other workspace\nwith a needle in it\n")
	write(there, "pkg/deep.go", "package pkg // needle\n")

	b := &Builtins{}
	if err := b.Init(nil, module.Config{
		"workspaces": map[string]any{"other": there},
	}); err != nil {
		t.Fatal(err)
	}
	return b, fakeSession{here}, there
}

func TestReadingAnotherConfiguredWorkspace(t *testing.T) {
	b, s, _ := twoWorkspaces(t)

	t.Run("without a workspace it reads this one", func(t *testing.T) {
		out, err := run(t, b, s, "read", `{"path":"mine.txt"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "this workspace") {
			t.Errorf("got %q", out)
		}
	})

	t.Run("naming one reads that one", func(t *testing.T) {
		out, err := run(t, b, s, "read", `{"workspace":"other","path":"theirs.txt"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "the other workspace") {
			t.Errorf("got %q", out)
		}
	})

	t.Run("glob searches the named one and names hits relative to it", func(t *testing.T) {
		out, err := run(t, b, s, "glob", `{"workspace":"other","pattern":"**/*.go"}`)
		if err != nil {
			t.Fatal(err)
		}
		// Relative to the other root, so the model can pass it straight back.
		// Relative to this workspace it would be a pile of "..".
		if strings.TrimSpace(out) != "pkg/deep.go" {
			t.Errorf("hits = %q, want pkg/deep.go", out)
		}
	})

	t.Run("grep searches the named one", func(t *testing.T) {
		out, err := run(t, b, s, "grep", `{"workspace":"other","pattern":"needle"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "theirs.txt") || !strings.Contains(out, "pkg/deep.go") {
			t.Errorf("got %q", out)
		}
		if strings.Contains(out, "..") {
			t.Errorf("hits should be named relative to the other root, got %q", out)
		}
	})
}

// The containment check is the whole security boundary of this feature.
func TestAConfiguredRootCannotBeEscaped(t *testing.T) {
	b, s, there := twoWorkspaces(t)

	// A file next to the configured root, which a "../" would reach.
	outside := filepath.Join(filepath.Dir(there), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		args string
	}{
		{"climbing out with dot dot", `{"workspace":"other","path":"../outside.txt"}`},
		{"climbing out repeatedly", `{"workspace":"other","path":"../../../../etc/passwd"}`},
		{"climbing out and back down", `{"workspace":"other","path":"pkg/../../outside.txt"}`},
		{"an absolute path elsewhere", `{"workspace":"other","path":"` + jsonPath(outside) + `"}`},
		{"a workspace that is not configured", `{"workspace":"nope","path":"theirs.txt"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, b, s, "read", tc.args)
			if err == nil {
				t.Fatalf("should have been refused, got %q", out)
			}
			if strings.Contains(out, "secret") {
				t.Fatal("the file outside the root was read")
			}
		})
	}
}

// A sibling directory whose name merely starts with the root's is not inside
// it. A string prefix test would say otherwise, which is the classic version of
// this bug.
func TestASiblingWithASharedPrefixIsNotInside(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{"/srv/nabu", "/srv/nabu", true},
		{"/srv/nabu", "/srv/nabu/daemon/agent.go", true},
		{"/srv/nabu", "/srv/nabu-secrets/key.pem", false},
		{"/srv/nabu", "/srv/nabufoo", false},
		{"/srv/nabu", "/srv", false},
		{"/srv/nabu", "/etc/passwd", false},
	}
	for _, tc := range cases {
		if got := within(filepath.FromSlash(tc.root), filepath.FromSlash(tc.path)); got != tc.want {
			t.Errorf("within(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
		}
	}
}

// Reading another repository is a bounded convenience. Writing to one is not,
// so the writing tools do not take the argument at all — and must not start
// honouring it by accident.
func TestWritingToolsNeverLeaveTheWorkspace(t *testing.T) {
	b, s, there := twoWorkspaces(t)

	t.Run("write ignores a workspace argument", func(t *testing.T) {
		if _, err := run(t, b, s, "write", `{"workspace":"other","path":"planted.txt","content":"x"}`); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(there, "planted.txt")); err == nil {
			t.Fatal("write reached the other workspace")
		}
		if _, err := os.Stat(filepath.Join(s.dir, "planted.txt")); err != nil {
			t.Fatalf("write should have stayed here: %v", err)
		}
	})

	t.Run("edit ignores a workspace argument", func(t *testing.T) {
		_, err := run(t, b, s, "edit",
			`{"workspace":"other","path":"theirs.txt","old":"the other","new":"CHANGED"}`)
		if err == nil {
			t.Fatal("editing the other workspace's file should not have resolved")
		}
		data, readErr := os.ReadFile(filepath.Join(there, "theirs.txt"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(data), "CHANGED") {
			t.Fatal("edit reached the other workspace")
		}
	})
}

// A tool that advertises an ability the daemon has not been given is an
// invitation to keep trying it.
func TestTheArgumentIsNotOfferedWhenNothingIsConfigured(t *testing.T) {
	b := &Builtins{}
	if err := b.Init(nil, module.Config{}); err != nil {
		t.Fatal(err)
	}
	for _, tool := range b.Tools() {
		switch tool.Name {
		case "read", "glob", "grep":
			if strings.Contains(string(tool.Schema), "workspace") {
				t.Errorf("%s offers a workspace argument with no roots configured", tool.Name)
			}
			if strings.Contains(tool.Description, "another configured repository") {
				t.Errorf("%s advertises other workspaces with none configured", tool.Name)
			}
		}
	}

	// And naming one says so rather than failing obscurely.
	_, err := run(t, b, fakeSession{t.TempDir()}, "read", `{"workspace":"other","path":"x"}`)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "no other workspaces are configured") {
		t.Errorf("error = %q, want it to say none are configured", err)
	}
}

func TestConfiguredRootsAppearInTheSchemaAndDescription(t *testing.T) {
	b, _, _ := twoWorkspaces(t)
	for _, tool := range b.Tools() {
		switch tool.Name {
		case "read", "glob", "grep":
			if !strings.Contains(string(tool.Schema), `"workspace"`) {
				t.Errorf("%s does not offer the argument", tool.Name)
			}
			if !strings.Contains(string(tool.Schema), `"other"`) {
				t.Errorf("%s does not name the configured root", tool.Name)
			}
			// The schema must still be JSON after the fragment is spliced in.
			var parsed map[string]any
			if err := json.Unmarshal(tool.Schema, &parsed); err != nil {
				t.Errorf("%s schema is not valid JSON: %v\n%s", tool.Name, err, tool.Schema)
			}
		case "write", "edit", "bash":
			if strings.Contains(string(tool.Schema), `"workspace"`) {
				t.Errorf("%s must not offer a workspace argument", tool.Name)
			}
		}
	}
}

// A relative root would resolve against whatever directory the daemon happened
// to start in, which is not a decision the owner made.
func TestRootsAreMadeAbsolute(t *testing.T) {
	roots := parseRoots(module.Config{
		"workspaces": map[string]any{"rel": "some/relative/path"},
	})
	if len(roots) != 1 {
		t.Fatalf("roots = %v", roots)
	}
	if !filepath.IsAbs(roots["rel"]) {
		t.Errorf("root %q is not absolute", roots["rel"])
	}
}

func TestMalformedRootConfigIsDropped(t *testing.T) {
	roots := parseRoots(module.Config{
		"workspaces": map[string]any{"good": "/tmp/x", "": "/tmp/y", "blank": "  ", "num": 42},
	})
	if _, ok := roots["good"]; !ok {
		t.Error("a usable entry should survive")
	}
	for _, bad := range []string{"", "blank", "num"} {
		if _, ok := roots[bad]; ok {
			t.Errorf("entry %q should have been dropped", bad)
		}
	}
}

// jsonPath escapes a path for embedding in a JSON string literal, so a Windows
// path's separators do not become escape sequences.
func jsonPath(p string) string {
	b, _ := json.Marshal(p)
	return string(b[1 : len(b)-1])
}

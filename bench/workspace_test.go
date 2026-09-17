package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture writes a small repository and returns its path.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWorkspaceCopiesTheFixture(t *testing.T) {
	src := fixture(t, map[string]string{
		"main.go":      "package main\n",
		"sub/util.go":  "package sub\n",
		".git/HEAD":    "ref: refs/heads/main\n",
		"sub/data.txt": "hello\n",
	})

	ws, err := NewWorkspace(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	defer ws.Remove()

	for _, want := range []string{"main.go", "sub/util.go", "sub/data.txt"} {
		if _, err := os.Stat(filepath.Join(ws.Dir, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s did not arrive: %v", want, err)
		}
	}
	// The fixture's own history is not the baseline; the workspace makes its own.
	if body, err := os.ReadFile(filepath.Join(ws.Dir, ".git", "HEAD")); err == nil {
		if strings.Contains(string(body), "refs/heads/main") {
			t.Error("the fixture's .git was copied instead of replaced")
		}
	}
}

// The fixture itself must never be what a harness edits.
func TestWorkspaceIsNotTheFixture(t *testing.T) {
	src := fixture(t, map[string]string{"main.go": "package main\n"})

	ws, err := NewWorkspace(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	defer ws.Remove()

	if err := os.WriteFile(filepath.Join(ws.Dir, "main.go"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(src, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "package main\n" {
		t.Error("editing the workspace changed the fixture")
	}
}

func TestChangedReportsEditsAddsAndDeletes(t *testing.T) {
	src := fixture(t, map[string]string{
		"keep.go":   "package a\n",
		"edit.go":   "package a\n",
		"delete.go": "package a\n",
	})
	ws, err := NewWorkspace(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	defer ws.Remove()

	write(t, ws.Dir, "edit.go", "package a // changed\n")
	write(t, ws.Dir, "added.go", "package a\n")
	if err := os.Remove(filepath.Join(ws.Dir, "delete.go")); err != nil {
		t.Fatal(err)
	}

	changed, err := ws.Changed(context.Background())
	if err != nil {
		t.Fatalf("changed: %v", err)
	}

	got := strings.Join(changed, ",")
	if want := "added.go,delete.go,edit.go"; got != want {
		t.Errorf("changed = %q, want %q", got, want)
	}
}

// A rename touches both paths. Git detects renames by content and would
// otherwise report only the destination, hiding that the source is gone.
func TestARenameCountsAsChangingBothPaths(t *testing.T) {
	src := fixture(t, map[string]string{"guard_test.go": "package a\n"})
	ws, err := NewWorkspace(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	defer ws.Remove()

	if err := os.Rename(
		filepath.Join(ws.Dir, "guard_test.go"),
		filepath.Join(ws.Dir, "moved_test.go"),
	); err != nil {
		t.Fatal(err)
	}

	changed, err := ws.Changed(context.Background())
	if err != nil {
		t.Fatalf("changed: %v", err)
	}
	if got, want := strings.Join(changed, ","), "guard_test.go,moved_test.go"; got != want {
		t.Errorf("changed = %q, want %q", got, want)
	}
}

func TestNothingChangedIsEmpty(t *testing.T) {
	src := fixture(t, map[string]string{"main.go": "package main\n"})
	ws, err := NewWorkspace(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	defer ws.Remove()

	changed, err := ws.Changed(context.Background())
	if err != nil {
		t.Fatalf("changed: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("a untouched workspace reported %v", changed)
	}
}

// A file rewritten to the same bytes was not altered, and git agrees.
func TestAnIdenticalRewriteIsNotAChange(t *testing.T) {
	src := fixture(t, map[string]string{"main.go": "package main\n"})
	ws, err := NewWorkspace(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	defer ws.Remove()

	write(t, ws.Dir, "main.go", "package main\n")

	changed, err := ws.Changed(context.Background())
	if err != nil {
		t.Fatalf("changed: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("an identical rewrite counted as a change: %v", changed)
	}
}

func TestDiffCarriesTheChange(t *testing.T) {
	src := fixture(t, map[string]string{"main.go": "package main\n"})
	ws, err := NewWorkspace(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	defer ws.Remove()

	write(t, ws.Dir, "main.go", "package main\n\nfunc main() {}\n")

	diff, err := ws.Diff(context.Background())
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, "+func main() {}") {
		t.Errorf("the diff does not show the change:\n%s", diff)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

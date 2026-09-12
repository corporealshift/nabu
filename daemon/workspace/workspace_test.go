package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveNonGitUsesAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	ws, err := Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Path != dir || ws.GitRoot != "" {
		t.Fatalf("ws: %+v", ws)
	}
	if ws.Key == "" || len(ws.Key) > 64 {
		t.Fatalf("key: %q", ws.Key)
	}
	ws2, _ := Resolve(dir)
	if ws2.Key != ws.Key {
		t.Fatal("key must be stable")
	}
	other, _ := Resolve(t.TempDir())
	if other.Key == ws.Key {
		t.Fatal("different paths must not collide")
	}
}

func TestResolveGitSharesKeyAcrossSubdirs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	top, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := Resolve(sub)
	if err != nil {
		t.Fatal(err)
	}
	if top.Key != nested.Key {
		t.Fatalf("keys differ: %q vs %q", top.Key, nested.Key)
	}
	if nested.Path != sub {
		t.Fatalf("path must be the working dir, got %q", nested.Path)
	}
	if top.GitRoot == "" {
		t.Fatal("GitRoot must be set inside a repo")
	}
}

func TestSlug(t *testing.T) {
	if got := slug("My Repo!!"); got != "my-repo" {
		t.Fatalf("slug: %q", got)
	}
}

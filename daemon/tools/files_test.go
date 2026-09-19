package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// fakeSession gives tools a workspace and nothing else.
type fakeSession struct{ dir string }

func (f fakeSession) ID() string                               { return "s" }
func (f fakeSession) Workspace() module.Workspace              { return module.Workspace{Path: f.dir, Key: "k"} }
func (f fakeSession) Events(*string) ([]protocol.Event, error) { return nil, nil }
func (f fakeSession) State() protocol.State                    { return protocol.State{} }
func (f fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

func run(t *testing.T, b *Builtins, s module.Session, name string, args string) (string, error) {
	t.Helper()
	for _, tool := range b.Tools() {
		if tool.Name == name {
			return tool.Run(context.Background(), s, json.RawMessage(args))
		}
	}
	t.Fatalf("no tool %q", name)
	return "", nil
}

func TestWriteReadEdit(t *testing.T) {
	dir := t.TempDir()
	s := fakeSession{dir}
	b := &Builtins{}
	out, err := run(t, b, s, "write", `{"path":"a/b.txt","content":"one\ntwo\nthree\n"}`)
	if err != nil || !strings.Contains(out, "a/b.txt") {
		t.Fatalf("write: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "b.txt")); err != nil {
		t.Fatal("write must create parent dirs")
	}
	out, err = run(t, b, s, "read", `{"path":"a/b.txt"}`)
	if err != nil || out != "1\tone\n2\ttwo\n3\tthree\n" {
		t.Fatalf("read: %q %v", out, err)
	}
	// A truncated window tells the model how to continue, so it cannot mistake
	// a partial read for the whole file.
	out, _ = run(t, b, s, "read", `{"path":"a/b.txt","offset":2,"limit":1}`)
	if out != "2\ttwo\n[... more lines; continue with offset 3 ...]\n" {
		t.Fatalf("read window: %q", out)
	}
	out, _ = run(t, b, s, "read", `{"path":"a/b.txt","offset":3,"limit":1}`)
	if out != "3\tthree\n" {
		t.Fatalf("last line must carry no continuation hint: %q", out)
	}
	if _, err := run(t, b, s, "read", `{"path":"missing.txt"}`); err == nil {
		t.Fatal("missing file must error")
	}
	out, err = run(t, b, s, "edit", `{"path":"a/b.txt","old":"two","new":"2"}`)
	if err != nil || !strings.Contains(out, "1 occurrence") {
		t.Fatalf("edit: %q %v", out, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a", "b.txt"))
	if string(got) != "one\n2\nthree\n" {
		t.Fatalf("after edit: %q", got)
	}
	if _, err := run(t, b, s, "edit", `{"path":"a/b.txt","old":"nope","new":"x"}`); err == nil {
		t.Fatal("edit of absent text must error")
	}
	os.WriteFile(filepath.Join(dir, "dup.txt"), []byte("x x"), 0o644)
	if _, err := run(t, b, s, "edit", `{"path":"dup.txt","old":"x","new":"y"}`); err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Fatalf("ambiguous edit must error: %v", err)
	}
	out, _ = run(t, b, s, "edit", `{"path":"dup.txt","old":"x","new":"y","replace_all":true}`)
	if !strings.Contains(out, "2 occurrence") {
		t.Fatalf("replace_all: %q", out)
	}
}

func TestGlobAndGrep(t *testing.T) {
	dir := t.TempDir()
	s := fakeSession{dir}
	b := &Builtins{}
	os.MkdirAll(filepath.Join(dir, "src", "deep"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.go"), []byte("package a\nfunc Foo() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "deep", "b.go"), []byte("package deep\n// foo bar\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "c.txt"), []byte("foo\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".git", "x.go"), []byte("ignored"), 0o644)
	os.WriteFile(filepath.Join(dir, "bin.dat"), append([]byte("foo"), 0, 1, 2), 0o644)

	out, err := run(t, b, s, "glob", `{"pattern":"**/*.go"}`)
	if err != nil || out != "src/a.go\nsrc/deep/b.go" {
		t.Fatalf("glob: %q %v", out, err)
	}
	out, _ = run(t, b, s, "glob", `{"pattern":"*.go","path":"src"}`)
	if out != "src/a.go" {
		t.Fatalf("glob in subdir: %q", out)
	}
	out, err = run(t, b, s, "grep", `{"pattern":"foo","ignore_case":true}`)
	if err != nil {
		t.Fatal(err)
	}
	want := "src/a.go:2: func Foo() {}\nsrc/c.txt:1: foo\nsrc/deep/b.go:2: // foo bar"
	if out != want {
		t.Fatalf("grep:\n%s\nwant:\n%s", out, want)
	}
	out, _ = run(t, b, s, "grep", `{"pattern":"foo","glob":"*.go"}`)
	if out != "src/deep/b.go:2: // foo bar" {
		t.Fatalf("grep with glob: %q", out)
	}
	out, _ = run(t, b, s, "grep", `{"pattern":"zzz"}`)
	if out != "no matches" {
		t.Fatalf("no matches: %q", out)
	}
}

func TestGlobToRegexp(t *testing.T) {
	cases := map[string][]string{
		"**/*.go":  {"a.go", "x/y/z.go"},
		"src/*.go": {"src/a.go"},
		"*.txt":    {"a.txt"},
		"src/**":   {"src/a", "src/x/y"},
		"file?.md": {"file1.md"},
	}
	negatives := map[string][]string{
		"src/*.go": {"src/x/a.go", "a.go"},
		"*.txt":    {"x/a.txt"},
	}
	for pat, oks := range cases {
		re := globToRegexp(pat)
		for _, p := range oks {
			if !re.MatchString(p) {
				t.Errorf("%q should match %q (%s)", pat, p, re)
			}
		}
	}
	for pat, bads := range negatives {
		re := globToRegexp(pat)
		for _, p := range bads {
			if re.MatchString(p) {
				t.Errorf("%q should not match %q", pat, p)
			}
		}
	}
}

// Dotted directories are searchable on purpose. The picker and the watcher both
// skip them, and both are right to; a search tool that silently cannot find
// .github/workflows/ci.yml is worse than one that returns a few extra hits.
func TestGrepStillSearchesDottedDirectories(t *testing.T) {
	dir := t.TempDir()
	b := &Builtins{}
	s := fakeSession{dir}

	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"),
		[]byte("name: CI\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, b, s, "grep", `{"pattern":"name: CI"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ci.yml") {
		t.Errorf("grep should search .github, got %q", out)
	}
}

// Generated content is not searched, which is what it shares with the picker
// and the watcher.
func TestGrepSkipsGeneratedDirectories(t *testing.T) {
	dir := t.TempDir()
	b := &Builtins{}
	s := fakeSession{dir}

	for _, d := range []string{"build", "vendor", "node_modules", "dist", "__pycache__"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, d, "x.txt"), []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, b, s, "grep", `{"pattern":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "real.txt") {
		t.Fatalf("the real file should be found, got %q", out)
	}
	for _, d := range []string{"build", "vendor", "node_modules", "dist", "__pycache__"} {
		if strings.Contains(out, d) {
			t.Errorf("%s should not be searched, got %q", d, out)
		}
	}
}

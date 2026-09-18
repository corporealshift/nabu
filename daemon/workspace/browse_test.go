package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree builds a directory layout and returns its root.
func tree(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func names(l Listing) []string {
	out := make([]string, 0, len(l.Entries))
	for _, e := range l.Entries {
		out = append(out, e.Name)
	}
	return out
}

func TestBrowseListsDirectoriesSorted(t *testing.T) {
	root := tree(t, "Zebra", "alpha", "Beta")
	if err := os.WriteFile(filepath.Join(root, "a-file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := Browse([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}

	got := names(l)
	want := []string{"alpha", "Beta", "Zebra"}
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entries = %v, want %v (case-insensitive order)", got, want)
		}
	}
}

// A picker's taps should not go into build output, and none of it is a place a
// session starts.
func TestBrowseHidesNoiseAndDottedDirectories(t *testing.T) {
	root := tree(t, "src", "node_modules", "build", "target", "vendor", "dist", ".hidden", ".git")

	l, err := Browse([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(l); len(got) != 1 || got[0] != "src" {
		t.Errorf("entries = %v, want just [src]", got)
	}
}

func TestBrowseMarksRepositories(t *testing.T) {
	root := tree(t, "plain", "repo/.git", "worktree")
	// A worktree's .git is a file, not a directory, and still counts.
	if err := os.WriteFile(filepath.Join(root, "worktree", ".git"), []byte("gitdir: ..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := Browse([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Entry{}
	for _, e := range l.Entries {
		byName[e.Name] = e
	}
	if !byName["repo"].IsRepo {
		t.Error("a directory with .git should be marked a repo")
	}
	if !byName["worktree"].IsRepo {
		t.Error("a worktree, whose .git is a file, should be marked a repo")
	}
	if byName["plain"].IsRepo {
		t.Error("a plain directory should not be marked a repo")
	}
}

// A client needs to know where climbing stops. Finding out by being refused one
// tap later is a worse way to learn it.
func TestParentIsNilAtARootAndSetBelowIt(t *testing.T) {
	root := tree(t, "child")

	top, err := Browse([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	if top.Parent != nil {
		t.Errorf("parent at a root = %q, want null", *top.Parent)
	}

	below, err := Browse([]string{root}, filepath.Join(root, "child"))
	if err != nil {
		t.Fatal(err)
	}
	if below.Parent == nil {
		t.Fatal("a directory below a root should offer a parent")
	}
	if *below.Parent != filepath.ToSlash(root) {
		t.Errorf("parent = %q, want %q", *below.Parent, filepath.ToSlash(root))
	}
}

// The roots bound listing. An authenticated client may already create a session
// anywhere, but it should not be able to enumerate the machine for the asking.
func TestBrowseRefusesOutsideTheRoots(t *testing.T) {
	root := tree(t, "inside")
	outside := t.TempDir()

	for _, p := range []string{
		outside,
		filepath.Join(root, ".."),
		filepath.Join(root, "inside", "..", "..", ".."),
	} {
		if _, err := Browse([]string{root}, p); err == nil {
			t.Errorf("browsing %q should have been refused", p)
		}
	}
}

// A sibling whose name merely starts with a root's is not inside it. A string
// prefix test would say otherwise.
func TestASiblingWithASharedPrefixIsOutside(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "projects")
	sibling := filepath.Join(base, "projects-private")
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Browse([]string{root}, sibling); err == nil {
		t.Error("projects-private is not inside projects")
	}
}

// With no path, the roots themselves are the listing: the client has to start
// somewhere and it cannot know the names.
func TestNoPathListsTheRoots(t *testing.T) {
	a := tree(t, "one")
	b := tree(t, "two")

	l, err := Browse([]string{a, b}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 2 {
		t.Fatalf("entries = %v, want both roots", names(l))
	}
	if l.Parent != nil {
		t.Error("the top level has no parent")
	}
	if l.Path != "" {
		t.Errorf("path = %q, want empty at the top", l.Path)
	}
}

// A configured root that is not there is a mistake, and a dead entry in a
// picker is worse than its absence.
func TestAMissingRootIsLeftOut(t *testing.T) {
	real := tree(t, "one")
	l, err := Browse([]string{real, filepath.Join(real, "nope")}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 {
		t.Errorf("entries = %v, want only the root that exists", names(l))
	}
}

func TestBrowseWithNoRootsConfiguredIsAnError(t *testing.T) {
	if _, err := Browse(nil, ""); err == nil {
		t.Error("browsing with nothing configured should fail rather than list everything")
	}
}

func TestBrowseRejectsAFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Browse([]string{root}, file)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("err = %v, want a not-a-directory error", err)
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		root, p string
		want    bool
	}{
		{"/srv/projects", "/srv/projects", true},
		{"/srv/projects", "/srv/projects/nabu", true},
		{"/srv/projects", "/srv/projects-private", false},
		{"/srv/projects", "/srv", false},
		{"/srv/projects", "/etc", false},
	}
	for _, tc := range cases {
		got := Within(filepath.FromSlash(tc.root), filepath.FromSlash(tc.p))
		if got != tc.want {
			t.Errorf("Within(%q, %q) = %v, want %v", tc.root, tc.p, got, tc.want)
		}
	}
}

func TestCreateDirectory(t *testing.T) {
	root := tree(t, "projects")
	parent := filepath.Join(root, "projects")

	l, err := CreateDirectory([]string{root}, parent, "new-thing")
	if err != nil {
		t.Fatal(err)
	}
	// The listing is the new directory, so a picker moves into what it made
	// rather than asking again.
	if l.Path != filepath.ToSlash(filepath.Join(parent, "new-thing")) {
		t.Errorf("path = %q, want the new directory", l.Path)
	}
	if l.Parent == nil || *l.Parent != filepath.ToSlash(parent) {
		t.Errorf("parent = %v, want %q", l.Parent, filepath.ToSlash(parent))
	}
	if info, err := os.Stat(filepath.Join(parent, "new-thing")); err != nil || !info.IsDir() {
		t.Errorf("the directory was not created: %v", err)
	}
}

// The name comes from a client and is never a path: a separator or a ".." would
// be a way to write outside the roots the parent check already approved.
func TestCreateDirectoryRejectsAnythingThatIsNotAName(t *testing.T) {
	root := tree(t, "projects")
	parent := filepath.Join(root, "projects")

	cases := []struct{ name, arg string }{
		{"empty", ""},
		{"only spaces", "   "},
		{"a path", "a/b"},
		{"a windows path", `a\b`},
		{"climbing out", "../escape"},
		{"dot dot", ".."},
		{"dot", "."},
		{"a hidden directory", ".secret"},
		{"a leading space", " leading"},
		{"a trailing space", "trailing "},
		{"too long", strings.Repeat("x", 65)},
		{"a character windows refuses", "a:b"},
		{"a wildcard", "a*b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CreateDirectory([]string{root}, parent, tc.arg); err == nil {
				t.Fatalf("creating %q should have been refused", tc.arg)
			}
		})
	}

	// Nothing was created anywhere, including above the root.
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused name still created something: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); err == nil {
		t.Error("a name climbed out of the root")
	}
}

func TestCreateDirectoryRefusesOutsideTheRoots(t *testing.T) {
	root := tree(t, "inside")
	outside := t.TempDir()

	if _, err := CreateDirectory([]string{root}, outside, "nope"); err == nil {
		t.Error("creating below a directory outside the roots should be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "nope")); err == nil {
		t.Error("it was created anyway")
	}
}

// The picker lists what is there, so asking to create an existing directory
// means the reader did not see it. Saying so beats silently succeeding.
func TestCreateDirectorySaysWhenItAlreadyExists(t *testing.T) {
	root := tree(t, "projects/taken")

	_, err := CreateDirectory([]string{root}, filepath.Join(root, "projects"), "taken")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want it to say the name is taken", err)
	}
}

func TestCreateDirectoryRefusesAParentThatIsNotThere(t *testing.T) {
	root := tree(t)
	if _, err := CreateDirectory([]string{root}, filepath.Join(root, "nope"), "child"); err == nil {
		t.Error("a missing parent should be refused")
	}
}

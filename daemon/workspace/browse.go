package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one directory a client may descend into or start a session in.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// IsRepo marks a git repository, so a picker can show which of these are
	// projects rather than making the reader recognise names.
	IsRepo bool `json:"is_repo"`
}

// Listing is one level of the tree.
type Listing struct {
	// Path is the directory listed, empty at the top where Entries are the
	// configured roots rather than the contents of anything.
	Path string `json:"path"`
	// Parent is where "up" goes, or nil at a root. A client needs to know
	// where climbing stops, and finding out by being refused is a worse way.
	Parent  *string `json:"parent"`
	Entries []Entry `json:"entries"`
}

// noise are directories never listed. They are where a picker's taps go to
// die, and none of them is somewhere a session starts.
var noise = map[string]bool{
	"node_modules": true, ".git": true, "build": true, "target": true,
	"vendor": true, ".gradle": true, ".idea": true, "__pycache__": true,
	"dist": true, ".venv": true,
}

// Browse lists the directories under path, or the roots themselves when path
// is empty.
//
// roots bound what may be listed. An authenticated client can already create a
// session at any path, so this is not a new tier of access — but listing is not
// acting, and without a bound a client would be handed every directory name on
// the machine for the asking.
func Browse(roots []string, path string) (Listing, error) {
	clean := make([]string, 0, len(roots))
	for _, r := range roots {
		abs, err := filepath.Abs(strings.TrimSpace(r))
		if err != nil || abs == "" {
			continue
		}
		clean = append(clean, filepath.Clean(abs))
	}
	if len(clean) == 0 {
		return Listing{}, fmt.Errorf("no browsable directories are configured")
	}

	if strings.TrimSpace(path) == "" {
		return rootListing(clean), nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return Listing{}, fmt.Errorf("%s: %w", path, err)
	}
	abs = filepath.Clean(abs)
	if !underAny(clean, abs) {
		return Listing{}, fmt.Errorf("%s is outside the browsable directories", path)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Listing{}, err
	}
	if !info.IsDir() {
		return Listing{}, fmt.Errorf("%s is not a directory", abs)
	}

	out := Listing{Path: filepath.ToSlash(abs), Entries: []Entry{}}
	// Up stops at a root: the parent of a root is outside what may be listed,
	// so offering it would only produce a refusal one tap later.
	if !isRoot(clean, abs) {
		parent := filepath.ToSlash(filepath.Dir(abs))
		out.Parent = &parent
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return Listing{}, err
	}
	for _, e := range entries {
		if !e.IsDir() || skip(e.Name()) {
			continue
		}
		full := filepath.Join(abs, e.Name())
		out.Entries = append(out.Entries, Entry{
			Name:   e.Name(),
			Path:   filepath.ToSlash(full),
			IsRepo: isRepo(full),
		})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}

// rootListing is the top level: the roots themselves, with no parent.
func rootListing(roots []string) Listing {
	out := Listing{Entries: []Entry{}}
	seen := map[string]bool{}
	for _, r := range roots {
		if seen[r] {
			continue
		}
		seen[r] = true
		if info, err := os.Stat(r); err != nil || !info.IsDir() {
			// A root that is not there is a configuration mistake, and showing
			// it as a dead entry is worse than leaving it out.
			continue
		}
		out.Entries = append(out.Entries, Entry{
			Name:   filepath.ToSlash(r),
			Path:   filepath.ToSlash(r),
			IsRepo: isRepo(r),
		})
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	return out
}

// skip hides dotted directories and the noisy ones.
func skip(name string) bool {
	return strings.HasPrefix(name, ".") || noise[strings.ToLower(name)]
}

// isRepo reports whether dir holds a .git entry. A worktree's .git is a file
// rather than a directory, so the kind is not checked.
func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// underAny reports whether p is one of the roots or lies inside one.
func underAny(roots []string, p string) bool {
	for _, r := range roots {
		if Within(r, p) {
			return true
		}
	}
	return false
}

func isRoot(roots []string, p string) bool {
	for _, r := range roots {
		if filepath.Clean(r) == filepath.Clean(p) {
			return true
		}
	}
	return false
}

// Within reports whether p is root or lies under it.
//
// filepath.Rel does the comparison rather than a string prefix, which would
// accept /srv/projects-private as being inside /srv/projects.
func Within(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

// DefaultRoots is what browsing offers when nothing is configured: the user's
// home directory. Everything, which is the alternative, would hand every
// directory name on the machine to anything holding the token.
func DefaultRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{filepath.Clean(home)}
}

// badName rejects anything that is not a single directory name.
//
// The name comes from a client, so it is never treated as a path: a separator
// or a ".." here would be a way to write outside the roots that the parent
// check has already approved.
func badName(name string) error {
	trimmed := strings.TrimSpace(name)
	switch {
	case trimmed == "":
		return fmt.Errorf("a name is required")
	case trimmed != name:
		return fmt.Errorf("a name cannot begin or end with a space")
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("a name is one directory, not a path")
	case name == "." || name == "..":
		return fmt.Errorf("%q is not a name", name)
	case strings.HasPrefix(name, "."):
		// Browsing hides dotted directories, so creating one would make
		// something the reader then could not see.
		return fmt.Errorf("a name cannot start with a dot")
	case len(name) > 64:
		return fmt.Errorf("a name is limited to 64 characters")
	case strings.ContainsAny(name, `:*?"<>|`):
		// Windows refuses these, and failing here says why rather than
		// surfacing an errno the reader cannot act on.
		return fmt.Errorf(`a name cannot contain any of : * ? " < > |`)
	}
	return nil
}

// CreateDirectory makes one directory inside parent and lists it.
//
// It returns the new directory's listing rather than its path, so a picker can
// navigate into what it just made instead of asking again.
//
// This is the one place a client writes to the daemon's filesystem. It is not a
// new tier of access — a client may already create a session anywhere and have
// the agent make directories — but it is the daemon acting because a client
// asked, so both halves are checked: parent must be inside the roots, and name
// must be a name.
func CreateDirectory(roots []string, parent, name string) (Listing, error) {
	if err := badName(name); err != nil {
		return Listing{}, err
	}
	// Browse does the root containment check and proves parent is a directory
	// that exists, so nothing is created below somewhere unlistable.
	if _, err := Browse(roots, parent); err != nil {
		return Listing{}, err
	}
	abs, err := filepath.Abs(parent)
	if err != nil {
		return Listing{}, err
	}
	target := filepath.Join(filepath.Clean(abs), name)

	// Join cleans, so a name that somehow slipped through still cannot land
	// outside. Belt and braces on the one call that writes.
	if !Within(filepath.Clean(abs), target) {
		return Listing{}, fmt.Errorf("%q would be outside %s", name, parent)
	}
	if _, err := os.Stat(target); err == nil {
		// The picker lists what is already there, so asking to create it means
		// the reader did not see it. Saying so beats silently succeeding.
		return Listing{}, fmt.Errorf("%s already exists", name)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		return Listing{}, err
	}
	return Browse(roots, target)
}

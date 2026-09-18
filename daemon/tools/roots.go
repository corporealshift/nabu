package tools

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
)

// Roots are the other repositories this daemon may read, by name.
//
// nabu's own complaint: locked to one workspace, it could not reference another
// project's code while working. Reading another repository is a convenience
// with a bounded blast radius; writing to one is a foot-gun with an unbounded
// one. So this list is read-only, and only read, glob and grep consult it.
//
// Names rather than paths, because a name cannot be escaped from. The model
// asks for "nabu" and the daemon decides what that means; there is no spelling
// of a name that reaches a directory the owner did not list.
type Roots map[string]string

// parseRoots reads the configured name → path map, dropping entries that could
// not be made absolute. A name that does not resolve is a configuration
// mistake, and silently keeping a relative one would resolve it against
// whatever directory the daemon happened to start in.
func parseRoots(cfg module.Config) Roots {
	raw := cfg.StringMap("workspaces")
	if len(raw) == 0 {
		return nil
	}
	out := make(Roots, len(raw))
	for name, path := range raw {
		name = strings.TrimSpace(name)
		path = strings.TrimSpace(path)
		if name == "" || path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		out[name] = filepath.Clean(abs)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Names lists the configured roots, sorted, for a tool description or an error.
func (r Roots) Names() []string {
	out := make([]string, 0, len(r))
	for name := range r {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// resolveIn turns a path into an absolute one inside the named root.
//
// The containment check is the whole security boundary of this feature, so it
// is done on the cleaned absolute result rather than by inspecting the input
// for "..": a check that reasons about the spelling of a path loses to the
// first spelling it did not anticipate.
func (r Roots) resolveIn(name, path string) (string, error) {
	root, ok := r[name]
	if !ok {
		if len(r) == 0 {
			return "", fmt.Errorf("no other workspaces are configured, so %q cannot be read", name)
		}
		return "", fmt.Errorf("unknown workspace %q; configured: %s", name, strings.Join(r.Names(), ", "))
	}
	// An empty path means the root itself, which is what glob and grep want
	// when asked to search a whole other repository.
	joined := root
	if path != "" {
		if filepath.IsAbs(path) {
			joined = filepath.Clean(path)
		} else {
			joined = filepath.Join(root, path)
		}
	}
	if !within(root, joined) {
		return "", fmt.Errorf("path %q is outside workspace %q", path, name)
	}
	return joined, nil
}

// within reports whether p is root or lies under it.
//
// filepath.Rel does the comparison, because it normalises both sides and a
// string prefix test would accept "/srv/nabu-secrets" as being inside
// "/srv/nabu".
func within(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

// rootsDescription tells the model what it may read, or says nothing when
// nothing is configured. A tool that advertises an ability the daemon has not
// been given is an invitation to keep trying it.
func rootsDescription(r Roots) string {
	if len(r) == 0 {
		return ""
	}
	return " Set workspace to read another configured repository instead of this one: " +
		strings.Join(r.Names(), ", ") + ". Reading only; writes always stay in this workspace."
}

// workspaceProperty is the schema fragment for the optional argument, empty
// when no roots are configured so the model is not offered a dead option.
//
// It carries its own leading comma, so a schema that gains nothing does not
// need a filler property to absorb a dangling one.
func workspaceProperty(r Roots) string {
	if len(r) == 0 {
		return ""
	}
	return `,"workspace":{"type":"string","enum":[` + quoteAll(r.Names()) +
		`],"description":"another configured repository to read from"}`
}

func quoteAll(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = `"` + n + `"`
	}
	return strings.Join(parts, ",")
}

// resolveMaybeElsewhere resolves a path for a reading tool: in the named root
// when one is given, in the session workspace otherwise.
//
// The empty name is the ordinary case and keeps exactly its old behaviour, so
// a session that never names a workspace cannot be affected by this feature.
func (b *Builtins) resolveMaybeElsewhere(s module.Session, name, path string) (string, error) {
	if name == "" {
		return resolve(s, path)
	}
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	return b.Roots.resolveIn(name, path)
}

// searchRoot is the same for glob and grep, which walk a directory rather than
// open a file. It returns the directory to walk and the root that hits should
// be named relative to.
//
// Those differ when a path narrows the search: walking
// <root>/daemon/agent should still report paths as daemon/agent/x.go, so the
// model can pass one straight back in another call.
func (b *Builtins) searchRoot(s module.Session, name, path string) (walkDir, base string, err error) {
	if name == "" {
		base = s.Workspace().Path
		walkDir = base
		if path != "" {
			if walkDir, err = resolve(s, path); err != nil {
				return "", "", err
			}
		}
		return walkDir, base, nil
	}
	root, ok := b.Roots[name]
	if !ok {
		// resolveIn owns the wording of both failures, so they cannot drift.
		_, err := b.Roots.resolveIn(name, ".")
		return "", "", err
	}
	if walkDir, err = b.Roots.resolveIn(name, path); err != nil {
		return "", "", err
	}
	return walkDir, root, nil
}

package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// Workspace identifies where a session runs. Key is stable across worktrees
// and nested directories of one repository (spec §11.2): slug of the repo
// directory name plus a short hash of the git common dir; outside git, slug
// plus hash of the absolute path.
type Workspace struct {
	Path    string // absolute working directory
	Key     string
	GitRoot string // absolute path of the git common dir's parent, or ""
}

// Resolve computes the Workspace for path.
func Resolve(path string) (Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Workspace{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Workspace{}, err
	}
	if !info.IsDir() {
		return Workspace{}, fmt.Errorf("workspace %s is not a directory", abs)
	}
	ws := Workspace{Path: abs}
	if common := gitCommonDir(abs); common != "" {
		ws.GitRoot = filepath.Dir(common)
		ws.Key = slug(filepath.Base(ws.GitRoot)) + "-" + shortHash(common)
		return ws, nil
	}
	ws.Key = slug(filepath.Base(abs)) + "-" + shortHash(abs)
	return ws, nil
}

// gitCommonDir returns the absolute git common dir for dir, or "" when dir is
// not inside a repository or git is unavailable. Worktrees share a common dir,
// which is exactly why it is the identity.
func gitCommonDir(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

// shortHash normalises separators and case so the same repo hashes the same
// whether it was reached from bash or from cmd.
func shortHash(s string) string {
	n := strings.ToLower(filepath.ToSlash(s))
	sum := sha256.Sum256([]byte(n))
	return hex.EncodeToString(sum[:])[:8]
}

// slug lowercases, keeps [a-z0-9], collapses everything else to single
// hyphens, and trims trailing hyphens.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (unicode.IsLetter(r) && r < 128) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

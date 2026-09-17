package bench

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Workspace is one harness's copy of a fixture. Every run gets its own, so a
// harness that leaves a mess cannot affect the next one.
type Workspace struct {
	Dir string
}

// NewWorkspace copies a fixture into a fresh directory under parent and makes
// it a git repository with one commit, which is what later tells changed files
// from untouched ones.
func NewWorkspace(ctx context.Context, fixture, parent string) (*Workspace, error) {
	dir, err := os.MkdirTemp(parent, "run-")
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	if err := copyTree(fixture, dir); err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}

	ws := &Workspace{Dir: dir}
	if err := ws.initGit(ctx); err != nil {
		return nil, err
	}
	return ws, nil
}

// initGit commits the fixture as it arrived. A fixture may ship its own .git,
// which is removed first: the baseline must be this copy, not its history.
func (w *Workspace) initGit(ctx context.Context) error {
	if err := os.RemoveAll(filepath.Join(w.Dir, ".git")); err != nil {
		return fmt.Errorf("workspace: %w", err)
	}

	// Identity is set locally because the machine's global git config may have
	// none, and a commit without one fails.
	steps := [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "bench@nabu.local"},
		{"config", "user.name", "nabu bench"},
		{"add", "-A"},
		{"commit", "--quiet", "-m", "fixture"},
	}
	for _, args := range steps {
		if out, err := w.git(ctx, args...); err != nil {
			return fmt.Errorf("workspace: git %s: %w: %s", args[0], err, out)
		}
	}
	return nil
}

func (w *Workspace) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = w.Dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Changed is every file the run added, edited or deleted, in sorted order.
func (w *Workspace) Changed(ctx context.Context) ([]string, error) {
	if _, err := w.git(ctx, "add", "-A"); err != nil {
		return nil, fmt.Errorf("workspace: staging: %w", err)
	}
	// --no-renames, because a rename touched two paths. Git would report only
	// the destination, and a harness that renamed a forbidden file would look
	// as though it had left it alone.
	out, err := w.git(ctx, "diff", "--cached", "--no-renames", "--name-only")
	if err != nil {
		return nil, fmt.Errorf("workspace: diff: %w", err)
	}
	if out == "" {
		return nil, nil
	}

	files := strings.Split(out, "\n")
	for i := range files {
		files[i] = strings.TrimSpace(files[i])
	}
	sort.Strings(files)
	return files, nil
}

// Diff is the whole change as a patch, which is what the judge reads.
func (w *Workspace) Diff(ctx context.Context) (string, error) {
	if _, err := w.git(ctx, "add", "-A"); err != nil {
		return "", fmt.Errorf("workspace: staging: %w", err)
	}
	out, err := w.git(ctx, "diff", "--cached")
	if err != nil {
		return "", fmt.Errorf("workspace: diff: %w", err)
	}
	return out, nil
}

// Remove deletes the workspace. Failure is not worth reporting: a leftover
// directory under the run's parent is harmless and the parent is temporary.
func (w *Workspace) Remove() { _ = os.RemoveAll(w.Dir) }

// copyTree copies a directory recursively, skipping .git: the fixture's own
// history is not the baseline.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

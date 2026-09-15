package memory

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
)

// gitTimeout bounds every git call. A wedged git must not hold a session open.
const gitTimeout = 30 * time.Second

// initGit makes the memory directory a repository. A missing git is not an
// error: memory is the product and versioning is a convenience on top of it,
// so anything that fails here turns versioning off and leaves memory working.
func (m *Module) initGit() error {
	if m.NoGit || m.Root == "" {
		return nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		m.NoGit = true
		return fmt.Errorf("git is not installed: %w", err)
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		m.NoGit = true
		return err
	}

	// Leave an existing work tree alone rather than reinitialise state nabu
	// does not own.
	if out, err := m.git("rev-parse", "--is-inside-work-tree"); err == nil &&
		strings.TrimSpace(out) == "true" {
		return nil
	}

	if _, err := m.git("init"); err != nil {
		m.NoGit = true
		return fmt.Errorf("cannot make %s a repository: %w", m.Root, err)
	}
	// Without this a machine with no global identity fails every commit. The
	// identity is local to nabu's own repository.
	if out, err := m.git("config", "user.email"); err != nil || strings.TrimSpace(out) == "" {
		if _, err := m.git("config", "user.email", "nabu@localhost"); err != nil {
			m.NoGit = true
			return err
		}
		if _, err := m.git("config", "user.name", "nabu"); err != nil {
			m.NoGit = true
			return err
		}
	}
	return nil
}

// commitSession commits what one session changed, once. One commit per fact
// would bury the history: a session is what taught memory the facts in it.
func (m *Module) commitSession(ctx context.Context, s module.Session, w writes) {
	if m.NoGit || m.Root == "" || (w.saved == 0 && w.forgotten == 0) {
		return
	}

	// Staged by directory, so nothing that is not a memory reaches the commit.
	for _, dir := range []string{"global", "ws"} {
		if _, err := os.Stat(filepath.Join(m.Root, dir)); err != nil {
			continue
		}
		if _, err := m.gitCtx(ctx, "add", "--all", "--", dir); err != nil {
			m.log.Warn("memory: cannot stage", "dir", dir, "error", err)
		}
	}

	// Saving then forgetting one fact leaves nothing staged.
	if _, err := m.gitCtx(ctx, "diff", "--cached", "--quiet"); err == nil {
		return
	}

	if _, err := m.gitCtx(ctx, "commit", "-m", commitMessage(s.ID(), w)); err != nil {
		m.log.Warn("memory: cannot commit", "session", s.ID(), "error", err)
		return
	}
	m.log.Info("memory: committed", "session", s.ID(), "saved", w.saved, "forgotten", w.forgotten)
}

// commitMessage names the session and what changed, so the log answers who
// taught memory this.
func commitMessage(sessionID string, w writes) string {
	var parts []string
	if w.saved > 0 {
		parts = append(parts, fmt.Sprintf("%d saved", w.saved))
	}
	if w.forgotten > 0 {
		parts = append(parts, fmt.Sprintf("%d forgotten", w.forgotten))
	}
	return fmt.Sprintf("memory: %s (session %s)", strings.Join(parts, ", "), sessionID)
}

// git runs one git command in the memory root.
func (m *Module) git(args ...string) (string, error) {
	return m.gitCtx(context.Background(), args...)
}

// gitCtx runs one git command, bounded by gitTimeout as well as by ctx.
func (m *Module) gitCtx(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = m.Root
	out, err := cmd.Output()
	return string(out), err
}

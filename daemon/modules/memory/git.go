package memory

import (
	"context"

	"github.com/corporealshift/nabu/daemon/module"
)

// initGit makes the memory directory a repository. Filled in by the git task.
func (m *Module) initGit() error { return nil }

// commitSession commits what one session changed. Filled in by the git task.
func (m *Module) commitSession(context.Context, module.Session, writes) {}

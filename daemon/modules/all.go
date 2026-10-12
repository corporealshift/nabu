package modules

import (
	"github.com/corporealshift/rho/daemon/module"
	"github.com/corporealshift/rho/daemon/modules/answer"
	"github.com/corporealshift/rho/daemon/modules/artifact"
	"github.com/corporealshift/rho/daemon/modules/ask"
	"github.com/corporealshift/rho/daemon/modules/claude"
	"github.com/corporealshift/rho/daemon/modules/clock"
	"github.com/corporealshift/rho/daemon/modules/guard"
	"github.com/corporealshift/rho/daemon/modules/loop"
	"github.com/corporealshift/rho/daemon/modules/memory"
	"github.com/corporealshift/rho/daemon/modules/notes"
	"github.com/corporealshift/rho/daemon/modules/prompt"
	"github.com/corporealshift/rho/daemon/modules/report"
	"github.com/corporealshift/rho/daemon/modules/skills"
	"github.com/corporealshift/rho/daemon/modules/vcs"
	"github.com/corporealshift/rho/daemon/modules/verify"
	"github.com/corporealshift/rho/daemon/modules/watch"
	"github.com/corporealshift/rho/daemon/modules/web"
)

// All is the registration list: every module compiled into the daemon, in
// dispatch order. Adding a module is one import and one line here.
// Order matters for the gates. guard runs before verify because broad safety
// policy should decide before narrow verification does, and the first Deny on
// a tool call wins.
var All = []module.Module{
	&skills.Module{},
	&clock.Module{},
	&prompt.Module{},
	&ask.Module{},
	&claude.Module{},
	&memory.Module{},
	&notes.Module{},
	&watch.Module{},
	&web.Module{},
	&artifact.Module{},
	&vcs.Module{},
	&answer.Module{},
	&guard.Module{},
	&verify.Module{},
	&loop.Module{},
	&report.Module{},
}

package modules

import (
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/modules/answer"
	"github.com/corporealshift/nabu/daemon/modules/artifact"
	"github.com/corporealshift/nabu/daemon/modules/ask"
	"github.com/corporealshift/nabu/daemon/modules/claude"
	"github.com/corporealshift/nabu/daemon/modules/guard"
	"github.com/corporealshift/nabu/daemon/modules/memory"
	"github.com/corporealshift/nabu/daemon/modules/notes"
	"github.com/corporealshift/nabu/daemon/modules/report"
	"github.com/corporealshift/nabu/daemon/modules/skills"
	"github.com/corporealshift/nabu/daemon/modules/vcs"
	"github.com/corporealshift/nabu/daemon/modules/verify"
	"github.com/corporealshift/nabu/daemon/modules/watch"
	"github.com/corporealshift/nabu/daemon/modules/web"
)

// All is the registration list: every module compiled into the daemon, in
// dispatch order. Adding a module is one import and one line here.
// Order matters for the gates. guard runs before verify because broad safety
// policy should decide before narrow verification does, and the first Deny on
// a tool call wins.
var All = []module.Module{
	&skills.Module{},
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
	&report.Module{},
}

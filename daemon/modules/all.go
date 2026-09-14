package modules

import (
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/modules/guard"
	"github.com/corporealshift/nabu/daemon/modules/report"
	"github.com/corporealshift/nabu/daemon/modules/skills"
	"github.com/corporealshift/nabu/daemon/modules/verify"
)

// All is the registration list: every module compiled into the daemon, in
// dispatch order. P1 fills it with skills, guard, verify and report; P3 adds
// memory. Adding a module is one import and one line here.
// Order matters for the gates. guard runs before verify because broad safety
// policy should decide before narrow verification does, and the first Deny on
// a tool call wins.
var All = []module.Module{
	&skills.Module{},
	&guard.Module{},
	&verify.Module{},
	&report.Module{},
}

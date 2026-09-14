package modules

import (
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/modules/guard"
)

// All is the registration list: every module compiled into the daemon, in
// dispatch order. P1 fills it with skills, guard, verify and report; P3 adds
// memory. Adding a module is one import and one line here.
var All = []module.Module{
	&guard.Module{},
}

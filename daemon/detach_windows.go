package daemon

import (
	"os/exec"
	"syscall"
)

// detachedProcess and newProcessGroup keep the daemon alive after the CLI
// exits: without them the child shares the console and dies with it.
const (
	detachedProcess   = 0x00000008
	newProcessGroup   = 0x00000200
	noWindowOnConsole = 0x08000000
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | newProcessGroup | noWindowOnConsole,
		HideWindow:    true,
	}
}

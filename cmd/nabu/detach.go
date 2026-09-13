//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts the child in its own session so it survives the parent exiting
// and does not receive the parent's terminal signals.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

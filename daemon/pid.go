//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// processAlive reports whether pid names a running process. Signal 0 performs
// the permission and existence checks without delivering anything: ESRCH means
// the process is gone, EPERM means it exists under another user.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}

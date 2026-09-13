package daemon

import (
	"syscall"
)

// stillActive is what GetExitCodeProcess reports for a process that has not
// exited. A handle can outlive the process, so the exit code is what actually
// distinguishes running from finished.
const stillActive = 259

// processAlive reports whether pid names a running process. Opening the
// process can succeed for one that has already exited but whose handle is
// still held, so the exit code is checked too.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)

	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		// The handle opened, so something is there. Treat it as alive rather
		// than stealing a pid file from a live daemon.
		return true
	}
	return code == stillActive
}

//go:build unix

package locks

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with the given PID exists on
// this machine. It sends signal 0 (no signal delivered, permission +
// existence check only): nil or EPERM means the process exists; ESRCH
// means it does not.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	// EPERM: the process exists but we can't signal it (different owner).
	return errors.Is(err, syscall.EPERM)
}

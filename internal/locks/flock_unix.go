//go:build !windows

package locks

import (
	"os"
	"syscall"
)

// isPIDAlive returns true if the process with the given PID is currently
// running. On Unix we send signal 0 which does not actually deliver a
// signal but succeeds only if the caller has permission to signal the
// process and the process exists.
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil
}

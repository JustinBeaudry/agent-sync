//go:build windows

package locks

import (
	"golang.org/x/sys/windows"
)

// isPIDAlive returns true if the process with the given PID is still
// running. On Windows we use OpenProcess with SYNCHRONIZE access; if the
// call succeeds, the process exists and we close the handle immediately.
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(h)
	return true
}

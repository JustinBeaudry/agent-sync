//go:build windows

package locks

import "golang.org/x/sys/windows"

// stillActive is Windows' STILL_ACTIVE (259) exit code, reported by
// GetExitCodeProcess for a process that has not yet exited.
// golang.org/x/sys/windows does not export the constant, so define it.
const stillActive = 259

// processAlive reports whether a process with the given PID exists on
// this machine. It opens the process for query and reads its exit
// code: STILL_ACTIVE means alive. A failure to open (the process is
// gone, or access is denied for a still-existing process) is handled
// conservatively — an access-denied open still implies existence.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// ERROR_ACCESS_DENIED means the process exists but is owned by
		// another principal; treat as alive (conservative — never break
		// a maybe-live lock). Anything else (ERROR_INVALID_PARAMETER for
		// a missing PID) means gone.
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true // opened but couldn't query — assume alive, fail safe
	}
	return code == stillActive
}

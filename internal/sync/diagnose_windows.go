//go:build windows

package sync

// Diagnosis describes which processes (if any) hold a path open, used to
// enrich an ErrLocked report.
type Diagnosis struct {
	Available bool
	Processes []string
	Note      string
}

// Diagnose enumerates processes holding handles in path. The live
// Restart Manager binding (RmStartSession / RmRegisterResources /
// RmGetList, read-only — never RmShutdown) is a follow-up; for now it
// reports unavailable so the ErrLocked path degrades cleanly rather than
// shipping a half-tested syscall binding with no Windows CI runner.
func Diagnose(path string) Diagnosis {
	return Diagnosis{
		Available: false,
		Note:      "Restart Manager enumeration not yet implemented; check which editor/agent has the path open",
	}
}

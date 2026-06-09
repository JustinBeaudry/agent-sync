//go:build !windows

package sync

// Diagnosis describes which processes (if any) hold a path open, used to
// enrich an ErrLocked report. Process enumeration is a Windows-only
// capability (Restart Manager); on other platforms it is unavailable.
type Diagnosis struct {
	Available bool
	Processes []string
	Note      string
}

// Diagnose is a no-op on non-Windows platforms — rename locking is not a
// normal failure mode there.
func Diagnose(path string) Diagnosis {
	return Diagnosis{
		Available: false,
		Note:      "process diagnosis is a Windows-only capability",
	}
}

//go:build windows

package fsroot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// errorNotSameDevice is Windows ERROR_NOT_SAME_DEVICE (17). Returned by
// MoveFileEx / SetFileInformationByHandle when the source and target
// live on different volumes.
const errorNotSameDevice = syscall.Errno(17)

// isCrossDevice reports whether err is Windows' cross-volume rename
// signal.
func isCrossDevice(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == errorNotSameDevice
	}
	return false
}

// SameFilesystem reports whether paths a and b live on the same
// filesystem. This is ADVISORY ONLY (see plan decision #20). The
// Windows implementation compares [filepath.VolumeName] of the absolute
// paths, which catches distinct drive letters and UNC hosts but NOT
// mount points inside a single volume. For authoritative detection,
// observe the kernel error from rename (ERROR_NOT_SAME_DEVICE).
func SameFilesystem(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false, fmt.Errorf("fsroot: resolve %q: %w", a, err)
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false, fmt.Errorf("fsroot: resolve %q: %w", b, err)
	}
	if _, err := os.Stat(absA); err != nil {
		return false, fmt.Errorf("fsroot: stat %q: %w", a, err)
	}
	if _, err := os.Stat(absB); err != nil {
		return false, fmt.Errorf("fsroot: stat %q: %w", b, err)
	}
	va := strings.ToUpper(filepath.VolumeName(absA))
	vb := strings.ToUpper(filepath.VolumeName(absB))
	return va == vb, nil
}

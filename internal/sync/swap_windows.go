//go:build windows

package sync

import "syscall"

// Windows system error codes relevant to rename. Numeric literals keep
// this free of a golang.org/x/sys/windows dependency; they are stable
// Win32 error codes.
const (
	errorAccessDenied     syscall.Errno = 5  // ERROR_ACCESS_DENIED
	errorNotSameDevice    syscall.Errno = 17 // ERROR_NOT_SAME_DEVICE
	errorSharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION
)

// mapRenameErrno maps a Windows rename errno to a swap-taxonomy
// sentinel, or nil when the errno is not one we classify.
func mapRenameErrno(errno syscall.Errno) error {
	switch errno {
	case errorNotSameDevice:
		return ErrCrossVolume
	case errorSharingViolation, errorAccessDenied:
		// Sharing violations are transient (a reader has the file open);
		// the swap retries before surfacing ErrLocked.
		return ErrLocked
	default:
		return nil
	}
}

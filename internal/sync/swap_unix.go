//go:build unix

package sync

import "syscall"

// mapRenameErrno maps a Unix rename errno to a swap-taxonomy sentinel,
// or nil when the errno is not one we classify.
func mapRenameErrno(errno syscall.Errno) error {
	switch errno {
	case syscall.EXDEV:
		return ErrCrossVolume
	case syscall.EACCES, syscall.EPERM, syscall.EROFS:
		return ErrPermission
	default:
		return nil
	}
}

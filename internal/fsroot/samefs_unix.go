//go:build unix

package fsroot

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// isCrossDevice reports whether err is the kernel's "cross-device link"
// signal (EXDEV on Unix).
func isCrossDevice(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EXDEV
	}
	return false
}

// SameFilesystem reports whether paths a and b live on the same
// filesystem. This is ADVISORY ONLY: plan decision #20 states that the
// authoritative cross-FS signal is the EXDEV error returned by rename,
// not a statfs pre-flight. Bind mounts, btrfs subvolumes, overlayfs, and
// macOS firmlinks all routinely return matching device identifiers while
// rename still returns EXDEV across them.
//
// Use this only to enrich error messages and diagnostics; never to gate
// an operation.
func SameFilesystem(a, b string) (bool, error) {
	as, err := os.Stat(a)
	if err != nil {
		return false, fmt.Errorf("fsroot: stat %q: %w", a, err)
	}
	bs, err := os.Stat(b)
	if err != nil {
		return false, fmt.Errorf("fsroot: stat %q: %w", b, err)
	}
	asys, aok := as.Sys().(*syscall.Stat_t)
	bsys, bok := bs.Sys().(*syscall.Stat_t)
	if !aok || !bok {
		return false, fmt.Errorf("fsroot: stat_t unavailable on %T", as.Sys())
	}
	return asys.Dev == bsys.Dev, nil
}

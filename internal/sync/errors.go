// Package sync implements the atomic staging + two-rename swap pipeline
// that lets a reserved-subdirectory target update atomically: a sync
// either fully lands its new generation or leaves the previous one
// byte-intact. A crash mid-swap is always recoverable to a clean
// pre-sync-or-post-sync state via the persisted sentinel + the startup
// recovery reconciler.
//
// All renames are scoped to the single workspace os.Root (both operands
// relative to it), which satisfies Go 1.25's no-cross-Root-rename
// constraint and keeps staging on the same filesystem as the live
// prefix by construction.
//
// Authoritative design: docs/plans/2026-06-08-004-feat-unit-13-atomic-swap-plan.md
// and docs/operations/atomic-swap.md.
package sync

import (
	"errors"
	"syscall"
)

// Swap error taxonomy. All are sentinel values matched with errors.Is.
var (
	// ErrLocked: a rename was blocked by another process holding the
	// path open (Windows ERROR_SHARING_VIOLATION / ERROR_ACCESS_DENIED).
	// Retried with bounded backoff; surfaced with the retry count.
	ErrLocked = errors.New("sync: rename blocked by another process (locked)")

	// ErrCrossVolume: a rename crossed filesystems (EXDEV /
	// ERROR_NOT_SAME_DEVICE). Unreachable in normal use because staging
	// is co-located under the workspace; if it surfaces the workspace
	// contains a bind-mount/submount.
	ErrCrossVolume = errors.New("sync: rename crossed filesystems (cross-volume)")

	// ErrStale: a prior half-completed swap was found at startup
	// (leftover prefix.old or a sentinel at intend|step1_done). Requires
	// the recovery reconciler, not a retry.
	ErrStale = errors.New("sync: stale half-completed swap; run recovery")

	// ErrPermission: a non-retryable permission/ACL denial.
	ErrPermission = errors.New("sync: permission denied")

	// ErrMidLifeDrift: a file inside a managed reserved prefix is not
	// recorded in the ledger (a hand-edit or out-of-band addition).
	// The sync refuses rather than clobber it; remediation is
	// --adopt-prefix.
	ErrMidLifeDrift = errors.New("sync: unmanaged file in reserved prefix (mid-life drift); adopt or remove it")

	// ErrDeletionCountMismatch: the actual orphan-deletion count differs
	// from the caller's --expect-deletions guard.
	ErrDeletionCountMismatch = errors.New("sync: orphan deletion count differs from --expect-deletions")
)

// classifyRenameError maps an os.Rename error to the swap taxonomy. An
// unmapped error is returned unchanged (wrapped at the call site).
// nil in → nil out. The errno→sentinel mapping is platform-specific
// (mapRenameErrno, build-tag split).
func classifyRenameError(err error) error {
	if err == nil {
		return nil
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if mapped := mapRenameErrno(errno); mapped != nil {
			return mapped
		}
	}
	return err
}

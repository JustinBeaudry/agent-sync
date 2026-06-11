// Package locks provides the two flock-backed concurrency primitives
// the sync engine depends on: a per-target lock that one whole sync
// holds end-to-end, and a per-external-file lock registry that
// serializes the read-merge-write of shared tool-owned files
// (workspace-root AGENTS.md, .mcp.json) across adapters and processes.
//
// Locking needs a real OS file descriptor, which os.Root cannot
// provide, so lock files resolve to absolute paths at the flock
// boundary (joining the validated workspace root with a constant
// .agent-sync/state/ segment). This deviation from the fsroot-only rule is
// shared with internal/trust and is guarded: the .agent-sync and
// .agent-sync/state path components are checked for symlinks through the
// fsroot Root before any lock FD is opened, so a symlinked prefix
// cannot redirect a lock file outside the workspace.
package locks

import "errors"

// Sentinel errors. Callers branch with errors.Is.
var (
	// ErrTargetLocked means another live sync holds the target lock.
	// flock releases on process death, so a busy lock is a live holder;
	// --break-lock (AcquireOpts.BreakLock) is the only forced override.
	ErrTargetLocked = errors.New("locks: target locked by another sync")

	// ErrFileLockTimeout means a per-external-file lock could not be
	// acquired within the bounded deadline. It names the path and holder.
	ErrFileLockTimeout = errors.New("locks: timed out acquiring file lock")

	// ErrUnsafeStatePrefix means .agent-sync or .agent-sync/state is a symlink /
	// reparse point — refused before opening any lock FD so a symlinked
	// prefix cannot redirect lock files outside the workspace.
	ErrUnsafeStatePrefix = errors.New("locks: .agent-sync state-dir path component is a symlink")
)

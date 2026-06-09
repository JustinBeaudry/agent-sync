package sync

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ledger"
)

// Orphans returns the paths present in the old ledger but absent from
// the new one — the set to delete on this sync. It is computed from the
// ledger diff ONLY, never from a filesystem scan, so mid-life drift
// (hand-added files) can never induce a phantom orphan deletion.
// Result is sorted for deterministic ordering.
func Orphans(old, next ledger.Ledger) []string {
	keep := make(map[string]bool, len(next.Entries))
	for _, e := range next.Entries {
		keep[e.Path] = true
	}
	var del []string
	for _, e := range old.Entries {
		if !keep[e.Path] {
			del = append(del, e.Path)
		}
	}
	sort.Strings(del)
	return del
}

// DeleteOrphans removes each path through the root. A path already gone
// (out-of-band delete) is a silent no-op — not an error. Must be called
// only AFTER the new ledger is durable, so a crash mid-delete is
// recoverable (the caller owns that ordering). Returns the paths
// actually removed.
func DeleteOrphans(root *fsroot.Root, paths []string) (deleted []string, err error) {
	for _, p := range paths {
		if rerr := root.Inner().Remove(p); rerr != nil {
			if errors.Is(rerr, fs.ErrNotExist) {
				continue
			}
			return deleted, fmt.Errorf("sync: delete orphan %s: %w", p, rerr)
		}
		deleted = append(deleted, p)
	}
	return deleted, nil
}

// CheckExpectedDeletions enforces the --expect-deletions=N safety guard.
// A negative expected means "not specified" and always passes.
func CheckExpectedDeletions(expected, actual int) error {
	if expected >= 0 && expected != actual {
		return fmt.Errorf("%w: expected %d, got %d", ErrDeletionCountMismatch, expected, actual)
	}
	return nil
}

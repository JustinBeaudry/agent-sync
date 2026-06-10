package sync

import (
	"fmt"
	"path"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

// oldSuffix is appended to the live prefix when it is moved aside in
// step 1 of the swap.
const oldSuffix = ".old"

// sentinelPrefix names the per-leaf recovery sentinel files inside a
// generation dir: ".state-<leaf base>". A generation dir may hold several
// (one per staged leaf for shared-subdir syncs).
const sentinelPrefix = ".state-"

// sentinelRelFor returns the per-leaf sentinel path for a staged leaf:
// <gen-dir>/.state-<leaf base>.
func sentinelRelFor(stagingLeafRel string) string {
	return path.Join(path.Dir(stagingLeafRel), sentinelPrefix+path.Base(stagingLeafRel))
}

// renameStep performs one rename, scoped to the workspace os.Root. It
// is a package var so tests can inject a crash at a chosen step.
var renameStep = func(root *fsroot.Root, oldRel, newRel string) error {
	return root.Inner().Rename(oldRel, newRel)
}

// Swap promotes a staged generation to the live prefix atomically via
// the two-rename algorithm, recording each transition in the sentinel.
// Both rename operands are relative to the single workspace os.Root.
//
// On a step-1 rename failure the sentinel is left at intend and nothing
// moved. On a step-2 failure the sentinel is left at step1_done with
// the prefix moved aside — a recoverable shape the reconciler completes.
// On any classified rename error the original prefix is byte-intact
// (step 1 failure) or recoverable (step 2 failure); the swap never
// leaves a torn tree.
func Swap(root *fsroot.Root, s Sentinel) error {
	if s.PrefixRel == "" || s.StagingLeafRel == "" {
		return fmt.Errorf("sync: swap requires PrefixRel and StagingLeafRel")
	}
	prefixOld := s.PrefixRel + oldSuffix
	// One sentinel PER staged leaf, not per generation dir. Shared-subdir
	// syncs stage several leaves (e.g. .agents/skills/aienvs-A, aienvs-B) into
	// the same generation dir; a single per-gen-dir .state would let one
	// leaf's swap overwrite another's recovery record, orphaning a .old dir
	// and wedging future syncs with ErrStale. Keying the sentinel by the leaf
	// base keeps each swap unit's recovery state independent. (Owned-subdir
	// gen dirs hold exactly one leaf, so this is just a rename for them.)
	sentinelRel := sentinelRelFor(s.StagingLeafRel)

	// Defensive pre-flight: a leftover prefix.old means a prior swap did
	// not finish; refuse rather than stomp it.
	if exists(root, prefixOld) {
		return fmt.Errorf("%w: %s already exists", ErrStale, prefixOld)
	}

	// Step 0: intend.
	s.Status = StatusIntend
	if err := writeSentinel(root, sentinelRel, s); err != nil {
		return err
	}

	// Step 1: move the live prefix aside (skip if this is a first sync
	// with no prior generation).
	if exists(root, s.PrefixRel) {
		if err := renameStep(root, s.PrefixRel, prefixOld); err != nil {
			return fmt.Errorf("sync: step1 move-aside %s: %w", s.PrefixRel, classifyRenameError(err))
		}
	}
	s.Status = StatusStep1Done
	if err := writeSentinel(root, sentinelRel, s); err != nil {
		return err
	}

	// Step 2: promote staging to the live prefix.
	if err := renameStep(root, s.StagingLeafRel, s.PrefixRel); err != nil {
		return fmt.Errorf("sync: step2 promote %s: %w", s.StagingLeafRel, classifyRenameError(err))
	}
	s.Status = StatusStep2Done
	if err := writeSentinel(root, sentinelRel, s); err != nil {
		return err
	}

	// Step 3: clean up the old generation, then drop the sentinel. The
	// swap has already succeeded (prefix is the new generation), so this
	// returns nil regardless. But the sentinel is removed ONLY if .old is
	// actually gone: if RemoveAll fails (e.g. a Windows reader holds it),
	// leaving the sentinel at step2_done lets the recovery reconciler
	// finish the cleanup. Deleting the sentinel while .old lingers would
	// orphan it — the next pre-flight would refuse with ErrStale and
	// --recover would have no sentinel to act on (a dead end).
	if err := root.Inner().RemoveAll(prefixOld); err == nil {
		_ = root.Inner().Remove(sentinelRel)
	}
	return nil
}

// exists reports whether relPath exists under the workspace root.
// Lstat (not Stat) so a dangling symlink still counts as present.
func exists(root *fsroot.Root, relPath string) bool {
	_, err := root.Lstat(relPath)
	return err == nil
}

package sync

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/aienvs/aienvs/internal/fsroot"
)

// generationsToKeep is how many completed/failed staging generations to
// retain per parent for forensics; older ones are pruned.
const generationsToKeep = 3

// RecoveryEvent records one reconciler action, returned for logging by
// the caller (this package emits no logs of its own).
type RecoveryEvent struct {
	Gen    string // generation dir name
	Action string // human-readable outcome
}

// Recover scans <parentRel>/.aienv-staging/* and drives each sentinel
// to a terminal clean state per the recovery table, then prunes to the
// last generationsToKeep generations. It is idempotent: on a clean tree
// it does nothing. Defensive "impossible" states are logged (returned
// as events) and skipped — never guessed.
func Recover(root *fsroot.Root, parentRel string) ([]RecoveryEvent, error) {
	stagingRoot := path.Join(parentRel, stagingDirName)
	gens, err := listGenerations(root, stagingRoot)
	if err != nil {
		return nil, err
	}

	var events []RecoveryEvent
	for _, gen := range gens {
		genRel := path.Join(stagingRoot, gen)
		ev, err := reconcileGen(root, genRel, gen)
		if err != nil {
			return events, err
		}
		events = append(events, ev)
	}

	if err := prune(root, stagingRoot, gens); err != nil {
		return events, err
	}
	return events, nil
}

// reconcileGen applies the recovery state table to one generation dir.
func reconcileGen(root *fsroot.Root, genRel, gen string) (RecoveryEvent, error) {
	ev := RecoveryEvent{Gen: gen}
	sentinelRel := path.Join(genRel, ".state")

	s, err := readSentinel(root, sentinelRel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			ev.Action = "no sentinel; left as-is (forensic generation)"
			return ev, nil
		}
		// Corrupt/unreadable sentinel: never guess. Leave for an operator.
		ev.Action = "unreadable sentinel; requires operator intervention: " + err.Error()
		return ev, nil
	}

	prefixOld := s.PrefixRel + oldSuffix
	prefixThere := exists(root, s.PrefixRel)
	oldThere := exists(root, prefixOld)
	stagingThere := exists(root, s.StagingLeafRel)

	switch s.Status {
	case StatusIntend:
		if oldThere {
			ev.Action = "impossible state (intend + .old present); requires operator intervention"
			return ev, nil
		}
		// Crash before step 1: discard the staging generation.
		if err := root.Inner().RemoveAll(genRel); err != nil {
			return ev, fmt.Errorf("sync: recover discard %s: %w", genRel, err)
		}
		ev.Action = "crash before step1; discarded staging"
		return ev, nil

	case StatusStep1Done:
		if prefixThere && oldThere {
			ev.Action = "impossible state (step1_done + prefix + .old both present); requires operator intervention"
			return ev, nil
		}
		if !prefixThere && oldThere && stagingThere {
			// Crash between step 1 and step 2: complete the promotion.
			if err := renameStep(root, s.StagingLeafRel, s.PrefixRel); err != nil {
				return ev, fmt.Errorf("sync: recover promote %s: %w", s.StagingLeafRel, classifyRenameError(err))
			}
			s.Status = StatusStep2Done
			if err := writeSentinel(root, sentinelRel, s); err != nil {
				return ev, err
			}
			_ = root.Inner().RemoveAll(prefixOld)
			_ = root.Inner().Remove(sentinelRel)
			ev.Action = "crash between step1 and step2; completed promotion"
			return ev, nil
		}
		// Prefix already present without .old: step 2 effectively done.
		_ = root.Inner().RemoveAll(prefixOld)
		_ = root.Inner().Remove(sentinelRel)
		ev.Action = "step1_done but prefix present; treated as completed, cleaned up"
		return ev, nil

	case StatusStep2Done:
		// Crash before/after cleanup: remove .old (if any) and sentinel.
		_ = root.Inner().RemoveAll(prefixOld)
		_ = root.Inner().Remove(sentinelRel)
		ev.Action = "step2_done; cleaned up old generation"
		return ev, nil

	default:
		ev.Action = "unknown status; requires operator intervention"
		return ev, nil
	}
}

// listGenerations returns the generation dir names under stagingRoot,
// sorted ascending (chronological, given sortable timestamps). A
// missing staging dir yields an empty list, not an error.
func listGenerations(root *fsroot.Root, stagingRoot string) ([]string, error) {
	d, err := root.Inner().Open(stagingRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("sync: open staging %s: %w", stagingRoot, err)
	}
	defer func() { _ = d.Close() }()

	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("sync: read staging %s: %w", stagingRoot, err)
	}
	var gens []string
	for _, e := range entries {
		if e.IsDir() {
			gens = append(gens, e.Name())
		}
	}
	sort.Strings(gens)
	return gens, nil
}

// prune keeps the newest generationsToKeep generations and removes the
// rest. gens must be sorted ascending.
func prune(root *fsroot.Root, stagingRoot string, gens []string) error {
	if len(gens) <= generationsToKeep {
		return nil
	}
	for _, gen := range gens[:len(gens)-generationsToKeep] {
		if err := root.Inner().RemoveAll(path.Join(stagingRoot, gen)); err != nil {
			return fmt.Errorf("sync: prune %s: %w", gen, err)
		}
	}
	return nil
}

// CleanScratch force-removes all staging generations under a parent
// (`aienvs sync --clean-scratch`).
func CleanScratch(root *fsroot.Root, parentRel string) error {
	stagingRoot := path.Join(parentRel, stagingDirName)
	if err := root.Inner().RemoveAll(stagingRoot); err != nil {
		return fmt.Errorf("sync: clean scratch %s: %w", stagingRoot, err)
	}
	return nil
}

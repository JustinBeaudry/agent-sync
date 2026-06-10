package sync

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/agent-sync/agent-sync/internal/fsroot"
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

// Recover scans <parentRel>/.agent-sync-staging/* and drives each sentinel
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
		evs, err := reconcileGen(root, genRel, gen)
		if err != nil {
			return events, err
		}
		events = append(events, evs...)
	}

	if err := prune(root, stagingRoot, gens); err != nil {
		return events, err
	}
	return events, nil
}

// reconcileGen reconciles every per-leaf sentinel in one generation dir. A
// shared-subdir sync stages several leaves into one generation dir, each with
// its own ".state-<leaf>" sentinel; an owned-subdir gen dir has exactly one.
func reconcileGen(root *fsroot.Root, genRel, gen string) ([]RecoveryEvent, error) {
	sentinels, err := listSentinels(root, genRel)
	if err != nil {
		return nil, err
	}
	if len(sentinels) == 0 {
		return []RecoveryEvent{{Gen: gen, Action: "no sentinel; left as-is (forensic generation)"}}, nil
	}
	var events []RecoveryEvent
	for _, name := range sentinels {
		ev, err := reconcileSentinel(root, path.Join(genRel, name), gen)
		if err != nil {
			return events, err
		}
		events = append(events, ev)
	}
	return events, nil
}

// listSentinels returns the ".state-<leaf>" sentinel file names in a generation
// dir, sorted. A missing dir yields an empty list, not an error.
func listSentinels(root *fsroot.Root, genRel string) ([]string, error) {
	d, err := root.Inner().Open(genRel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("sync: open generation %s: %w", genRel, err)
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("sync: read generation %s: %w", genRel, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), sentinelPrefix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// reconcileSentinel applies the recovery state table to one leaf's sentinel.
func reconcileSentinel(root *fsroot.Root, sentinelRel, gen string) (RecoveryEvent, error) {
	ev := RecoveryEvent{Gen: gen}

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
		// Crash before step 1: discard only this leaf's staging + sentinel
		// (other leaves share the generation dir; never RemoveAll genRel).
		if err := root.Inner().RemoveAll(s.StagingLeafRel); err != nil {
			return ev, fmt.Errorf("sync: recover discard %s: %w", s.StagingLeafRel, err)
		}
		_ = root.Inner().Remove(sentinelRel)
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
// (`agent-sync sync --clean-scratch`).
func CleanScratch(root *fsroot.Root, parentRel string) error {
	stagingRoot := path.Join(parentRel, stagingDirName)
	if err := root.Inner().RemoveAll(stagingRoot); err != nil {
		return fmt.Errorf("sync: clean scratch %s: %w", stagingRoot, err)
	}
	return nil
}

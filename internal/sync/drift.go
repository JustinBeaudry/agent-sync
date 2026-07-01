package sync

import (
	"fmt"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ledger"
)

// ScanDrift refuses if any file inside the reserved prefix is not
// recorded in the ledger — a hand-edit or out-of-band addition that a
// sync would otherwise clobber. Returns ErrMidLifeDrift naming the
// first (lexically) rogue file, pointing the user at --adopt-prefix.
//
// A file recorded in the ledger but missing on disk (out-of-band
// delete) is NOT drift — it is handled as a silent no-op during sync.
func ScanDrift(root *fsroot.Root, prefixRel string, led ledger.Ledger) error {
	return ScanDriftUnion(root, prefixRel, led, nil)
}

// ScanDriftUnion is ScanDrift for a shared-subdir leaf that may be co-owned by
// more than one target. A file under prefixRel is drift only if it is absent
// from BOTH the current target's ledger AND every sibling target's ledger
// entries in extra. This lets codex and pi legitimately co-own
// .agents/skills/agent-sync-<id> without each seeing the other's files as a
// foreign hand-edit (ADV-1). For an owned-subdir prefix, callers pass extra=nil
// and the behavior is identical to the single-ledger scan.
func ScanDriftUnion(root *fsroot.Root, prefixRel string, led ledger.Ledger, extra []ledger.Entry) error {
	known := make(map[string]bool, len(led.Entries)+len(extra))
	for _, e := range led.Entries {
		known[e.Path] = true
	}
	for _, e := range extra {
		known[e.Path] = true
	}
	files, err := walkFiles(root, prefixRel)
	if err != nil {
		return err
	}
	for _, f := range files {
		if !known[f] {
			return fmt.Errorf("%w: %s", ErrMidLifeDrift, f)
		}
	}
	return nil
}

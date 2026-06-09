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
	known := make(map[string]bool, len(led.Entries))
	for _, e := range led.Entries {
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

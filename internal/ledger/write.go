package ledger

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/aienvs/aienvs/internal/fsroot"
	"github.com/aienvs/aienvs/internal/ir"
)

// stateDir is the workspace-relative directory holding all per-target
// ledgers and lock files.
const stateDir = ".aienv/state"

// Write persists l for its target atomically.
//
// It stamps the current schema version, sorts entries by path (so
// output is deterministic and golden-comparable), ensures the
// .aienv/state/ directory exists (StagedWrite does not create parent
// directories), then writes through fsroot's atomic temp+fsync+rename.
func Write(root *fsroot.Root, l Ledger) error {
	if !ir.IsValidID(l.Target) {
		return fmt.Errorf("%w: %q", ErrInvalidTarget, l.Target)
	}

	if err := ensureStateDir(root); err != nil {
		return err
	}

	entries := slices.Clone(l.Entries)
	if entries == nil {
		entries = []Entry{}
	}
	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Compare(a.Path, b.Path) })

	out := Ledger{
		SchemaVersion: SchemaVersionCurrent,
		Target:        l.Target,
		Entries:       entries,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("ledger: marshal %s: %w", l.Target, err)
	}
	data = append(data, '\n')

	if err := root.StagedWrite(relPath(l.Target), data, 0o644); err != nil {
		return fmt.Errorf("ledger: write %s: %w", l.Target, err)
	}
	return nil
}

// ensureStateDir creates .aienv/state/ through the fsroot Root if it
// does not already exist. MkdirAll through os.Root keeps the creation
// inside the containment boundary. StagedWrite (and gofrs/flock) do
// not create parent directories, so first sync depends on this.
func ensureStateDir(root *fsroot.Root) error {
	if err := root.Inner().MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("ledger: mkdir %s: %w", stateDir, err)
	}
	return nil
}

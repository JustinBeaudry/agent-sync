package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aienvs/aienvs/internal/fsroot"
)

// WriteOptions controls Write behaviour.
type WriteOptions struct {
	// Commit is the canonical-repo commit SHA to record in the ledger.
	Commit string
}

// Write atomically persists l to <workspaceRoot>/.aienv/state/<target>.json.
//
// It normalises the entry list (sort by EntryKey) and sets GeneratedAt and
// SchemaVersion before encoding. The write uses fsroot.StagedWrite so
// the file is never partially visible.
//
// Write creates <workspaceRoot>/.aienv/state/ if it does not exist.
func Write(workspaceRoot string, l *Ledger, opts WriteOptions) error {
	if l.Target == "" {
		return fmt.Errorf("ledger: Write: target is empty")
	}

	l.SchemaVersion = SchemaVersion
	l.GeneratedAt = time.Now().UTC()
	if opts.Commit != "" {
		l.Commit = opts.Commit
	}

	// Normalise: sort entries so identical IR produces identical bytes.
	sort.Slice(l.Entries, func(i, j int) bool {
		ki := EntryKey(l.Entries[i])
		kj := EntryKey(l.Entries[j])
		return ki < kj
	})

	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("ledger: marshal %s: %w", l.Target, err)
	}
	data = append(data, '\n')

	stateDir := filepath.Join(workspaceRoot, ".aienv", "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("ledger: mkdir %s: %w", stateDir, err)
	}

	// Open a root scoped at the state directory so StagedWrite enforces
	// containment. The state directory is not the workspace reserved
	// prefix — no swap needed here; the ledger itself is written
	// atomically via the temp-rename in StagedWrite.
	root, err := fsroot.OpenWorkspaceRoot(stateDir)
	if err != nil {
		return fmt.Errorf("ledger: open state root %s: %w", stateDir, err)
	}
	defer func() { _ = root.Close() }()

	return root.StagedWrite(l.Target+".json", data, 0o600)
}

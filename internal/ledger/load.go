package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadOptions controls Load behaviour.
type LoadOptions struct {
	// MigrateState, when true, allows automatic in-memory schema migration
	// so the caller gets a current-version Ledger. No file is written; the
	// caller must call Write to persist the upgraded ledger.
	//
	// When false, Load returns ErrSchemaVersionMismatch if the on-disk
	// version differs from SchemaVersion.
	MigrateState bool
}

// StatePath returns the absolute path of the ledger file for target
// inside workspaceRoot. The file lives at
// <workspaceRoot>/.aienv/state/<target>.json.
//
// StatePath does not create any directories; callers that need the
// directory must call os.MkdirAll themselves or use Write which does it
// automatically.
func StatePath(workspaceRoot, target string) string {
	return filepath.Join(workspaceRoot, ".aienv", "state", target+".json")
}

// Load reads and decodes the ledger for target from workspaceRoot.
//
// If the file does not exist, Load returns (nil, ErrLedgerNotFound).
// If the file exists but cannot be decoded, Load returns (nil, ErrLedgerCorrupted).
// If the schema version differs and opts.MigrateState is false, Load returns
// (nil, ErrSchemaVersionMismatch).
// If opts.MigrateState is true and migration succeeds, Load returns a
// current-version Ledger without writing anything to disk.
func Load(workspaceRoot, target string, opts LoadOptions) (*Ledger, error) {
	path := StatePath(workspaceRoot, target)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrLedgerNotFound, path)
		}
		return nil, fmt.Errorf("ledger: read %s: %w", path, err)
	}

	var l Ledger
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrLedgerCorrupted, path, err)
	}

	if l.Target == "" || l.SchemaVersion == 0 {
		return nil, fmt.Errorf("%w: %s: missing required fields", ErrLedgerCorrupted, path)
	}

	if l.SchemaVersion != SchemaVersion {
		if !opts.MigrateState {
			return nil, fmt.Errorf("%w: %s: on-disk version %d, current version %d; rerun with --migrate-state to upgrade",
				ErrSchemaVersionMismatch, path, l.SchemaVersion, SchemaVersion)
		}
		if err := migrate(&l); err != nil {
			return nil, fmt.Errorf("ledger: migrate %s from v%d: %w", path, l.SchemaVersion, err)
		}
	}

	return &l, nil
}

// LoadOrEmpty is a convenience wrapper that returns an empty Ledger
// instead of an error on ErrLedgerNotFound. It is used by the
// first-sync-guard path where a missing ledger means "no prior state."
// Corruption and schema-version mismatches are still surfaced as errors.
func LoadOrEmpty(workspaceRoot, target string, opts LoadOptions) (*Ledger, error) {
	l, err := Load(workspaceRoot, target, opts)
	if errors.Is(err, ErrLedgerNotFound) {
		return &Ledger{
			SchemaVersion: SchemaVersion,
			Target:        target,
		}, nil
	}
	return l, err
}

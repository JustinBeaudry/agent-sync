package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
)

// relPath returns the workspace-relative ledger path for a target.
// Wire paths are forward-slash regardless of host OS (cross-platform
// learning); fsroot validates containment.
func relPath(target string) string {
	return ".agent-sync/state/" + target + ".json"
}

// Load reads and validates the ledger for target.
//
// Returns ErrLedgerNotFound when the file is absent (normal first
// sync), ErrLedgerCorrupted when it exists but doesn't parse / has
// trailing data / a zero (missing) schema version / a target field
// that disagrees with the requested target, ErrLedgerSchemaTooOld
// when the version is older than current (migration required), and
// ErrLedgerSchemaTooNew when newer (hard refusal — downgrade is never
// guessed). On the older/newer cases the parsed (untrusted) ledger is
// also returned so a --migrate-state caller can hand it to Migrate.
func Load(root *fsroot.Root, target string) (Ledger, error) {
	if !ir.IsValidID(target) {
		return Ledger{}, fmt.Errorf("%w: %q", ErrInvalidTarget, target)
	}
	rel := relPath(target)

	f, err := root.Inner().Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Ledger{}, ErrLedgerNotFound
		}
		return Ledger{}, fmt.Errorf("ledger: open %s: %w", rel, err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(f)
	if err != nil {
		return Ledger{}, fmt.Errorf("ledger: read %s: %w", rel, err)
	}

	l, err := decode(data)
	if err != nil {
		return Ledger{}, fmt.Errorf("%w: %s: %s", ErrLedgerCorrupted, rel, err.Error())
	}

	// Zero/missing schema version is corruption, never a migratable v0.
	if l.SchemaVersion == 0 {
		return Ledger{}, fmt.Errorf("%w: %s: missing or zero schema_version", ErrLedgerCorrupted, rel)
	}
	// A target field that disagrees with the file we opened by name
	// signals a copied/renamed ledger — treat as corruption (fail safe).
	if l.Target != "" && l.Target != target {
		return Ledger{}, fmt.Errorf("%w: %s: target field %q != %q", ErrLedgerCorrupted, rel, l.Target, target)
	}

	switch {
	case l.SchemaVersion < SchemaVersionCurrent:
		return l, ErrLedgerSchemaTooOld
	case l.SchemaVersion > SchemaVersionCurrent:
		return l, fmt.Errorf("%w: %s: file=%d binary=%d", ErrLedgerSchemaTooNew, rel, l.SchemaVersion, SchemaVersionCurrent)
	}

	if l.Entries == nil {
		l.Entries = []Entry{}
	}
	return l, nil
}

// decode strict-parses ledger bytes: unknown fields and trailing data
// after the first JSON value are rejected so a malformed or
// truncated-then-appended file fails safe rather than parsing
// partially.
func decode(data []byte) (Ledger, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var l Ledger
	if err := dec.Decode(&l); err != nil {
		return Ledger{}, err
	}
	// Reject trailing tokens after the first value (two concatenated
	// objects, garbage suffix, etc.).
	if dec.More() {
		return Ledger{}, errors.New("trailing data after ledger object")
	}
	return l, nil
}

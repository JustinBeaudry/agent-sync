// Package ledger implements the per-target emitted-path ledger that
// records what aienvs wrote for a target, so a later sync can detect
// drift, remove orphans, and merge tool-owned slices surgically
// without re-deriving ownership from the filesystem.
//
// The ledger lives at .aienv/state/<target>.json, written atomically
// through internal/fsroot. It is schema-versioned from day one
// (SchemaVersionCurrent) so future shapes have a migration seam
// (see migrate.go). This package ships the durable state primitive;
// the sync engine (Unit 13) populates it and the orphan-deletion flow
// (Unit 14) consumes it.
package ledger

import (
	"errors"
	"time"
)

// SchemaVersionCurrent is the ledger schema version this binary
// writes and reads natively. A ledger with a lower version requires
// migration (see migrate.go); a higher version is a hard error.
const SchemaVersionCurrent = 1

// Entry is one emitted path recorded in the ledger. SHA256 is the hash
// of the exact bytes written for Path at emission time — never
// re-hashed on read. A consumer comparing on-disk bytes against this
// hash detects hand-edits (drift).
type Entry struct {
	Path      string    `json:"path"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	EmittedAt time.Time `json:"emitted_at"`
}

// Ledger is the on-disk per-target state document.
type Ledger struct {
	SchemaVersion int     `json:"schema_version"`
	Target        string  `json:"target"`
	Entries       []Entry `json:"entries"`
}

// Sentinel errors. Callers branch on these with errors.Is.
//
// ErrLedgerNotFound (file absent — a normal first sync) and
// ErrLedgerCorrupted (file present but unparseable, or a zero/missing
// schema version) both route the caller to the first-sync-guard
// (master plan decision #13): the reserved prefix is treated as user
// content needing adoption, never silently overwritten. They are
// distinct so the caller can log a silent first sync differently from
// a loud "your ledger is corrupted" recovery path.
var (
	ErrLedgerNotFound     = errors.New("ledger: not found")
	ErrLedgerCorrupted    = errors.New("ledger: corrupted")
	ErrLedgerSchemaTooOld = errors.New("ledger: schema version older than current; rerun with --migrate-state")
	ErrLedgerSchemaTooNew = errors.New("ledger: schema version newer than this binary supports")
	ErrInvalidTarget      = errors.New("ledger: invalid target name")
)

// Package ledger persists the set of files aienvs has emitted into a
// reserved prefix, keyed by target name. It is the authoritative record
// for orphan detection: a file that appeared in the previous ledger but
// not in the new one is an orphan and must be deleted.
//
// The ledger lives at <workspace>/.aienv/state/<target>.json — outside
// the reserved prefix so a user clearing the prefix does not destroy
// orphan-detection signal (plan decision #13).
package ledger

import (
	"errors"
	"time"
)

// SchemaVersion is the current ledger schema version. Increment when
// the on-disk shape changes in a backward-incompatible way; the migrate
// path in migrate.go handles the upgrade.
const SchemaVersion = 1

// Sentinel errors callers may inspect with [errors.Is].
var (
	// ErrLedgerNotFound is returned when the ledger file does not exist.
	// Callers treat this identically to ErrLedgerCorrupted: both require
	// the first-sync-guard path (plan decision #13).
	ErrLedgerNotFound = errors.New("ledger not found")

	// ErrLedgerCorrupted is returned when the ledger file exists but
	// cannot be decoded as valid JSON or fails schema validation.
	ErrLedgerCorrupted = errors.New("ledger corrupted")

	// ErrSchemaVersionMismatch is returned when the on-disk schema version
	// differs from SchemaVersion and the caller did not pass MigrateState.
	// The error message names the on-disk version and the remediation flag.
	ErrSchemaVersionMismatch = errors.New("ledger schema version mismatch")
)

// Entry describes a single file or tool-owned-file slice that aienvs has
// emitted. The SHA256 is computed over the emitted bytes at emission time;
// it is not re-hashed from disk on read.
type Entry struct {
	// Path is the workspace-relative slash path of the file.
	// For tool-owned-file entries this is the containing file path;
	// LocatorKind and LocatorValue identify the slice within it.
	Path string `json:"path"`

	// LocatorKind is non-empty for tool-owned-file entries.
	// Values: "json-pointer", "toml-path", "markdown-section".
	// Empty for whole-file entries.
	LocatorKind string `json:"locator_kind,omitempty"`

	// LocatorValue is the JSON-pointer, TOML path, or markdown section id
	// identifying the aienvs-owned slice within Path.
	LocatorValue string `json:"locator_value,omitempty"`

	// SHA256 is the hex-encoded SHA-256 hash of the emitted content.
	// For tool-owned-file entries it covers the aienvs-owned slice only.
	SHA256 string `json:"sha256"`

	// Size is the byte length of the emitted content.
	Size int64 `json:"size"`

	// EmittedAt is the wall-clock time the entry was last written.
	EmittedAt time.Time `json:"emitted_at"`
}

// Ledger is the top-level on-disk document for one target.
type Ledger struct {
	// SchemaVersion is written at creation; migrate.go uses it to decide
	// whether an upgrade is needed.
	SchemaVersion int `json:"schema_version"`

	// Target is the adapter target name (e.g. "claude", "cursor").
	Target string `json:"target"`

	// GeneratedAt is when this ledger was last written.
	GeneratedAt time.Time `json:"generated_at"`

	// Commit is the canonical-repo commit SHA that produced the emission.
	Commit string `json:"commit,omitempty"`

	// Entries is the ordered list of emitted files/slices.
	// Order is deterministic (sorted by Path then LocatorValue) so
	// byte-identical ledgers are produced from identical IR.
	Entries []Entry `json:"entries"`
}

// EntryKey returns a canonical string key for an Entry, suitable for
// map lookups and set operations.
func EntryKey(e Entry) string {
	if e.LocatorKind == "" {
		return e.Path
	}
	return e.Path + "\x00" + e.LocatorKind + "\x00" + e.LocatorValue
}

package ledger

import "fmt"

// upgrader transforms a ledger from version v to v+1. Registered in
// the upgraders chain keyed by the from-version.
type upgrader func(prev Ledger) (Ledger, error)

// upgraders maps a from-version to the function that upgrades it to
// from+1. v1 is the current version, so the chain is empty today; a
// future v1->v2 upgrader slots in here without touching Migrate's
// loop. Table-driven by design (no growing switch).
var upgraders = map[int]upgrader{}

// MigrateOpts controls Migrate. Empty today; reserved so the signature
// stays stable when knobs (dry-run, target-version pin) are added.
type MigrateOpts struct{}

// Migrate upgrades raw ledger bytes to the current schema version by
// applying the registered upgrader chain. It is pure (no I/O): the
// caller (the --migrate-state path, Unit 16) reads the file, calls
// Migrate, then Writes the result atomically.
//
//   - current version  -> returned validated-but-unchanged (identity)
//   - older recognized -> upgraded through the chain to current
//   - newer than current -> ErrLedgerSchemaTooNew (downgrade never guessed)
//   - zero/missing version -> ErrLedgerCorrupted (never a migratable v0)
func Migrate(raw []byte, _ MigrateOpts) (Ledger, error) {
	return migrateTo(raw, SchemaVersionCurrent, upgraders)
}

// migrateTo is the testable core: it migrates raw up to target using
// chain. Migrate calls it with SchemaVersionCurrent + the production
// upgraders; tests call it with a synthetic target/chain to exercise
// the chain mechanism before a real v2 exists.
func migrateTo(raw []byte, target int, chain map[int]upgrader) (Ledger, error) {
	l, err := decode(raw)
	if err != nil {
		return Ledger{}, fmt.Errorf("%w: %s", ErrLedgerCorrupted, err.Error())
	}
	if l.SchemaVersion == 0 {
		return Ledger{}, fmt.Errorf("%w: missing or zero schema_version", ErrLedgerCorrupted)
	}
	if l.SchemaVersion > target {
		return Ledger{}, fmt.Errorf("%w: file=%d target=%d", ErrLedgerSchemaTooNew, l.SchemaVersion, target)
	}
	for v := l.SchemaVersion; v < target; v++ {
		up, ok := chain[v]
		if !ok {
			return Ledger{}, fmt.Errorf("ledger: no upgrader registered for schema version %d", v)
		}
		if l, err = up(l); err != nil {
			return Ledger{}, fmt.Errorf("ledger: upgrade v%d->v%d: %w", v, v+1, err)
		}
		l.SchemaVersion = v + 1
	}
	if l.Entries == nil {
		l.Entries = []Entry{}
	}
	return l, nil
}

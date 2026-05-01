package ledger

import "fmt"

// migrate upgrades l from its current SchemaVersion to SchemaVersion.
// It is only called when the on-disk version differs from SchemaVersion
// and the caller has set MigrateState: true.
//
// When SchemaVersion is 1 there are no prior versions to migrate from,
// so this function always returns an error (no known migration path
// exists) — it serves as the insertion point for future migrations.
func migrate(l *Ledger) error {
	// Migration table: from → to. Add entries here as new schema
	// versions are introduced.
	switch l.SchemaVersion {
	default:
		return fmt.Errorf("no migration path from schema v%d to v%d", l.SchemaVersion, SchemaVersion)
	}
}

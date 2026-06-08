package ledger

import (
	"errors"
	"strings"
	"testing"
)

func TestMigrate_CurrentVersionIsIdentity(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"schema_version":1,"target":"claude","entries":[{"path":"a","sha256":"x","size":1,"emitted_at":"2026-06-08T12:00:00Z"}]}`)
	got, err := Migrate(raw, MigrateOpts{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got.SchemaVersion != SchemaVersionCurrent {
		t.Errorf("version=%d want %d", got.SchemaVersion, SchemaVersionCurrent)
	}
	if len(got.Entries) != 1 || got.Entries[0].Path != "a" {
		t.Errorf("entries mangled by identity migrate: %+v", got.Entries)
	}
}

func TestMigrate_ZeroVersionIsCorrupted(t *testing.T) {
	t.Parallel()
	_, err := Migrate([]byte(`{"schema_version":0,"target":"claude","entries":[]}`), MigrateOpts{})
	if !errors.Is(err, ErrLedgerCorrupted) {
		t.Errorf("err=%v want ErrLedgerCorrupted", err)
	}
}

func TestMigrate_NewerThanTargetIsHardError(t *testing.T) {
	t.Parallel()
	_, err := Migrate([]byte(`{"schema_version":2,"target":"claude","entries":[]}`), MigrateOpts{})
	if !errors.Is(err, ErrLedgerSchemaTooNew) {
		t.Errorf("err=%v want ErrLedgerSchemaTooNew", err)
	}
}

// TestMigrateTo_ChainMechanism proves the upgrader chain works before
// a real v2 exists: register a synthetic v1->v2 upgrader and assert a
// v1 fixture upgrades to v2 shape with the transformation applied.
func TestMigrateTo_ChainMechanism(t *testing.T) {
	t.Parallel()
	chain := map[int]upgrader{
		1: func(prev Ledger) (Ledger, error) {
			// Synthetic transformation: stamp a sentinel sha on every entry.
			for i := range prev.Entries {
				prev.Entries[i].SHA256 = "upgraded"
			}
			return prev, nil
		},
	}
	raw := []byte(`{"schema_version":1,"target":"claude","entries":[{"path":"a","sha256":"old","size":1,"emitted_at":"2026-06-08T12:00:00Z"}]}`)
	got, err := migrateTo(raw, 2, chain)
	if err != nil {
		t.Fatalf("migrateTo: %v", err)
	}
	if got.SchemaVersion != 2 {
		t.Errorf("version=%d want 2", got.SchemaVersion)
	}
	if got.Entries[0].SHA256 != "upgraded" {
		t.Errorf("v1->v2 transformation not applied: %+v", got.Entries[0])
	}
}

func TestMigrateTo_MissingUpgraderInChain(t *testing.T) {
	t.Parallel()
	// target 3 but only a 1->2 upgrader registered: the 2->3 hop has no
	// upgrader and must error rather than silently produce a v2 ledger.
	chain := map[int]upgrader{
		1: func(prev Ledger) (Ledger, error) { return prev, nil },
	}
	raw := []byte(`{"schema_version":1,"target":"claude","entries":[]}`)
	_, err := migrateTo(raw, 3, chain)
	if err == nil {
		t.Fatal("expected error for missing upgrader in chain")
	}
}

func TestMigrate_MalformedJSONIsCorrupted(t *testing.T) {
	t.Parallel()
	_, err := Migrate([]byte("{not json"), MigrateOpts{})
	if !errors.Is(err, ErrLedgerCorrupted) {
		t.Errorf("err=%v want ErrLedgerCorrupted", err)
	}
}

func TestMigrateTo_UpgraderErrorPropagates(t *testing.T) {
	t.Parallel()
	chain := map[int]upgrader{
		1: func(Ledger) (Ledger, error) { return Ledger{}, errors.New("boom") },
	}
	_, err := migrateTo([]byte(`{"schema_version":1,"target":"claude","entries":[]}`), 2, chain)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("upgrader error must propagate with context; got %v", err)
	}
}

func TestLoad_NewerSchemaVersionIsTooNew(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	writeRawLedger(t, root, "claude", []byte(`{"schema_version":2,"target":"claude","entries":[]}`))
	_, err := Load(root, "claude")
	if !errors.Is(err, ErrLedgerSchemaTooNew) {
		t.Errorf("err=%v want ErrLedgerSchemaTooNew", err)
	}
}

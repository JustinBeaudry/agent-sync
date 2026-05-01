package ledger_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aienvs/aienvs/internal/ledger"
)

// writeRawLedger writes arbitrary JSON bytes directly to the state dir,
// bypassing ledger.Write so we can inject unsupported schema versions.
func writeRawLedger(t *testing.T, workspaceRoot, target string, v any) {
	t.Helper()
	stateDir := filepath.Join(workspaceRoot, ".aienv", "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, target+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_SchemaVersionMismatch_NoMigrate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRawLedger(t, dir, "claude", map[string]any{
		"schema_version": 99,
		"target":         "claude",
		"entries":        []any{},
	})

	_, err := ledger.Load(dir, "claude", ledger.LoadOptions{MigrateState: false})
	if err == nil {
		t.Fatal("expected ErrSchemaVersionMismatch")
	}
	if !isError(err, ledger.ErrSchemaVersionMismatch) {
		t.Errorf("want ErrSchemaVersionMismatch, got %v", err)
	}
}

func TestLoad_SchemaVersionMismatch_WithMigrate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// SchemaVersion 2 doesn't exist yet; migrate should return an error.
	writeRawLedger(t, dir, "claude", map[string]any{
		"schema_version": 2,
		"target":         "claude",
		"entries":        []any{},
	})

	_, err := ledger.Load(dir, "claude", ledger.LoadOptions{MigrateState: true})
	if err == nil {
		t.Fatal("expected migrate error for unknown version")
	}
	// Error must not be a schema-version-mismatch (that is the no-migrate path).
	if isError(err, ledger.ErrSchemaVersionMismatch) {
		t.Errorf("should not surface ErrSchemaVersionMismatch when MigrateState is true")
	}
}

func TestLoad_CurrentVersion_NoMigrate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRawLedger(t, dir, "claude", map[string]any{
		"schema_version": ledger.SchemaVersion,
		"target":         "claude",
		"entries":        []any{},
	})

	l, err := ledger.Load(dir, "claude", ledger.LoadOptions{MigrateState: false})
	if err != nil {
		t.Fatalf("Load on current version: %v", err)
	}
	if l.SchemaVersion != ledger.SchemaVersion {
		t.Errorf("SchemaVersion: want %d, got %d", ledger.SchemaVersion, l.SchemaVersion)
	}
}

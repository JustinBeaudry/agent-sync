package ledger_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aienvs/aienvs/internal/ledger"
)

func TestRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	l := &ledger.Ledger{
		Target: "claude",
		Entries: []ledger.Entry{
			{Path: "z-last.md", SHA256: "aabb", Size: 10, EmittedAt: time.Now().UTC()},
			{Path: "a-first.md", SHA256: "ccdd", Size: 20, EmittedAt: time.Now().UTC()},
		},
	}

	if err := ledger.Write(dir, l, ledger.WriteOptions{Commit: "abc123"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := ledger.Load(dir, "claude", ledger.LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Target != "claude" {
		t.Errorf("Target: want claude, got %q", got.Target)
	}
	if got.SchemaVersion != ledger.SchemaVersion {
		t.Errorf("SchemaVersion: want %d, got %d", ledger.SchemaVersion, got.SchemaVersion)
	}
	if got.Commit != "abc123" {
		t.Errorf("Commit: want abc123, got %q", got.Commit)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries: want 2, got %d", len(got.Entries))
	}
	// Entries are sorted by EntryKey
	if got.Entries[0].Path != "a-first.md" {
		t.Errorf("sort: want a-first.md first, got %q", got.Entries[0].Path)
	}
	if got.Entries[1].Path != "z-last.md" {
		t.Errorf("sort: want z-last.md second, got %q", got.Entries[1].Path)
	}
}

func TestRoundtrip_ToolOwned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	l := &ledger.Ledger{
		Target: "cursor",
		Entries: []ledger.Entry{
			{
				Path:         ".cursor/mcp.json",
				LocatorKind:  "json-pointer",
				LocatorValue: "#/mcpServers/aienvs_foo",
				SHA256:       "deadbeef",
				Size:         99,
				EmittedAt:    time.Now().UTC(),
			},
		},
	}

	if err := ledger.Write(dir, l, ledger.WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := ledger.Load(dir, "cursor", ledger.LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(got.Entries) != 1 {
		t.Fatalf("Entries: want 1, got %d", len(got.Entries))
	}
	e := got.Entries[0]
	if e.LocatorKind != "json-pointer" {
		t.Errorf("LocatorKind: want json-pointer, got %q", e.LocatorKind)
	}
	if e.LocatorValue != "#/mcpServers/aienvs_foo" {
		t.Errorf("LocatorValue: want #/mcpServers/aienvs_foo, got %q", e.LocatorValue)
	}
}

func TestLoad_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, err := ledger.Load(dir, "cursor", ledger.LoadOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !isError(err, ledger.ErrLedgerNotFound) {
		t.Errorf("want ErrLedgerNotFound, got %v", err)
	}
}

func TestLoad_Corrupted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".aienv", "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude.json"), []byte("not json{{"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ledger.Load(dir, "claude", ledger.LoadOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !isError(err, ledger.ErrLedgerCorrupted) {
		t.Errorf("want ErrLedgerCorrupted, got %v", err)
	}
}

func TestLoad_MissingRequiredFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".aienv", "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Valid JSON but missing target and schema_version.
	data, _ := json.Marshal(map[string]any{"entries": []any{}})
	if err := os.WriteFile(filepath.Join(stateDir, "claude.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ledger.Load(dir, "claude", ledger.LoadOptions{})
	if !isError(err, ledger.ErrLedgerCorrupted) {
		t.Errorf("want ErrLedgerCorrupted, got %v", err)
	}
}

func TestLoadOrEmpty_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	l, err := ledger.LoadOrEmpty(dir, "codex", ledger.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadOrEmpty on missing: %v", err)
	}
	if l == nil {
		t.Fatal("expected empty ledger, got nil")
	}
	if l.Target != "codex" {
		t.Errorf("Target: want codex, got %q", l.Target)
	}
	if len(l.Entries) != 0 {
		t.Errorf("Entries: want 0, got %d", len(l.Entries))
	}
}

func TestWrite_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	l := &ledger.Ledger{
		Target: "claude",
		Entries: []ledger.Entry{
			{Path: "rule.md", SHA256: "aabb", Size: 5, EmittedAt: time.Now().UTC()},
		},
	}

	if err := ledger.Write(dir, l, ledger.WriteOptions{}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if err := ledger.Write(dir, l, ledger.WriteOptions{}); err != nil {
		t.Fatalf("second Write: %v", err)
	}

	got, err := ledger.Load(dir, "claude", ledger.LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Entries) != 1 {
		t.Errorf("Entries: want 1, got %d", len(got.Entries))
	}
}

func TestEntryKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		e    ledger.Entry
		want string
	}{
		{"whole-file", ledger.Entry{Path: "foo.md"}, "foo.md"},
		{"tool-owned", ledger.Entry{Path: ".mcp.json", LocatorKind: "json-pointer", LocatorValue: "#/x"}, ".mcp.json\x00json-pointer\x00#/x"},
	}
	for _, tc := range cases {
		got := ledger.EntryKey(tc.e)
		if got != tc.want {
			t.Errorf("%s: want %q, got %q", tc.name, tc.want, got)
		}
	}
}

func isError(got, target error) bool {
	return errors.Is(got, target)
}

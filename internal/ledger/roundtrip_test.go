package ledger

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

// openRoot opens a fresh fsroot.Root on a temp workspace with NO
// pre-existing .agent-sync/ — so the first-sync directory-creation path is
// actually exercised (a test that pre-created .agent-sync/state would mask
// the StagedWrite-doesn't-create-parents gap).
func openRoot(t *testing.T) *fsroot.Root {
	t.Helper()
	root, err := fsroot.OpenWorkspaceRoot(t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

// writeRawLedger writes raw bytes to the ledger path for a target,
// creating .agent-sync/state/ first. Used to simulate corrupted / odd
// on-disk files.
func writeRawLedger(t *testing.T, root *fsroot.Root, target string, data []byte) {
	t.Helper()
	dir := filepath.Join(root.Path(), stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, target+".json"), data, 0o644); err != nil {
		t.Fatalf("write raw ledger: %v", err)
	}
}

func sampleLedger() Ledger {
	ts := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	return Ledger{
		SchemaVersion: SchemaVersionCurrent,
		Target:        "claude",
		Entries: []Entry{
			{Path: ".claude/rules/aienvs/b.md", SHA256: "bbb", Size: 2, EmittedAt: ts},
			{Path: ".claude/rules/aienvs/a.md", SHA256: "aaa", Size: 1, EmittedAt: ts},
			{Path: ".mcp.json", SHA256: "ccc", Size: 3, EmittedAt: ts},
		},
	}
}

func TestWriteLoad_RoundTripSortedByPath(t *testing.T) {
	t.Parallel()
	root := openRoot(t)

	if err := Write(root, sampleLedger()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Load(root, "claude")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SchemaVersion != SchemaVersionCurrent || got.Target != "claude" {
		t.Errorf("header mismatch: %+v", got)
	}
	wantOrder := []string{".claude/rules/aienvs/a.md", ".claude/rules/aienvs/b.md", ".mcp.json"}
	if len(got.Entries) != len(wantOrder) {
		t.Fatalf("entry count=%d want %d", len(got.Entries), len(wantOrder))
	}
	for i, p := range wantOrder {
		if got.Entries[i].Path != p {
			t.Errorf("entry[%d].Path=%q want %q (entries must be sorted by path)", i, got.Entries[i].Path, p)
		}
	}
}

func TestWrite_Deterministic(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	p := filepath.Join(root.Path(), stateDir, "claude.json")

	if err := Write(root, sampleLedger()); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	first, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	if err := Write(root, sampleLedger()); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	second, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("ledger writes not byte-identical:\n1: %s\n2: %s", first, second)
	}
}

func TestLoad_AbsentIsNotFound(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	_, err := Load(root, "claude")
	if !errors.Is(err, ErrLedgerNotFound) {
		t.Errorf("err=%v want ErrLedgerNotFound", err)
	}
	if errors.Is(err, ErrLedgerCorrupted) {
		t.Error("absent ledger must not be ErrLedgerCorrupted")
	}
}

func TestLoad_MalformedJSONIsCorrupted(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	writeRawLedger(t, root, "claude", []byte("{not json"))
	_, err := Load(root, "claude")
	if !errors.Is(err, ErrLedgerCorrupted) {
		t.Errorf("err=%v want ErrLedgerCorrupted", err)
	}
}

func TestLoad_UnknownFieldIsCorrupted(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	writeRawLedger(t, root, "claude", []byte(`{"schema_version":1,"target":"claude","entries":[],"bogus":true}`))
	_, err := Load(root, "claude")
	if !errors.Is(err, ErrLedgerCorrupted) {
		t.Errorf("err=%v want ErrLedgerCorrupted (strict decode)", err)
	}
}

func TestLoad_TrailingDataIsCorrupted(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	writeRawLedger(t, root, "claude", []byte(`{"schema_version":1,"target":"claude","entries":[]}{"x":1}`))
	_, err := Load(root, "claude")
	if !errors.Is(err, ErrLedgerCorrupted) {
		t.Errorf("err=%v want ErrLedgerCorrupted (trailing data)", err)
	}
}

func TestLoad_ZeroOrMissingSchemaVersionIsCorrupted(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"explicit zero": `{"schema_version":0,"target":"claude","entries":[]}`,
		"missing":       `{"target":"claude","entries":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := openRoot(t)
			writeRawLedger(t, root, "claude", []byte(body))
			_, err := Load(root, "claude")
			if !errors.Is(err, ErrLedgerCorrupted) {
				t.Errorf("err=%v want ErrLedgerCorrupted (zero/missing version is corruption, never v0)", err)
			}
		})
	}
}

func TestLoad_TargetFieldMismatchIsCorrupted(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	writeRawLedger(t, root, "claude", []byte(`{"schema_version":1,"target":"cursor","entries":[]}`))
	_, err := Load(root, "claude")
	if !errors.Is(err, ErrLedgerCorrupted) {
		t.Errorf("err=%v want ErrLedgerCorrupted (target field mismatch)", err)
	}
}

func TestWriteLoad_EmptyEntriesRoundTripsAsArray(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	if err := Write(root, Ledger{Target: "claude"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root.Path(), stateDir, "claude.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"entries": []`)) {
		t.Errorf("empty entries must serialize as [] not null; got %s", raw)
	}
	got, err := Load(root, "claude")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Entries == nil {
		t.Error("loaded empty Entries should be non-nil []Entry")
	}
	if len(got.Entries) != 0 {
		t.Errorf("entries len=%d want 0", len(got.Entries))
	}
}

func TestWriteLoad_InvalidTargetRejected(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	for _, bad := range []string{"", "../escape", "Has Space", "UPPER"} {
		if err := Write(root, Ledger{Target: bad}); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("Write(target=%q) err=%v want ErrInvalidTarget", bad, err)
		}
		if _, err := Load(root, bad); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("Load(target=%q) err=%v want ErrInvalidTarget", bad, err)
		}
	}
	// No ledger file should have been created for any invalid target.
	if entries, _ := os.ReadDir(filepath.Join(root.Path(), stateDir)); len(entries) != 0 {
		t.Errorf("invalid target must not touch disk; found %d state files", len(entries))
	}
}

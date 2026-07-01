package engine

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	codexadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/codex"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/ledger"
	"github.com/agent-sync/agent-sync/internal/locks"
)

// TestSync_ReleasesRunLock verifies Sync acquires AND releases the per-workspace
// run lock: after a completed Sync, a fresh RunLock on the same workspace
// acquires immediately (a leaked lock would block until the 2m default timeout).
func TestSync_ReleasesRunLock(t *testing.T) {
	ws := t.TempDir()
	req, done := claudeReqOn(t, ws, []ir.Node{
		{ID: "greeter", Kind: ir.KindSkill, Version: 1, Body: []byte("# Greeter")},
	})
	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("sync: %v", err)
	}
	done()

	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	defer func() { _ = root.Close() }()
	rl, err := locks.NewRunLock(root)
	if err != nil {
		t.Fatalf("NewRunLock: %v", err)
	}
	release, err := rl.Acquire(context.Background(), locks.AcquireOpts{Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("run lock should be free after Sync, got: %v", err)
	}
	_ = release()
}

// TestSync_WarnsOrphanLedgerAndPreservesFiles pins the dropped-target behavior:
// a sync whose manifest no longer includes a target with an on-disk ledger
// warns (pointing at unmanage) and must NOT delete that target's files.
func TestSync_WarnsOrphanLedgerAndPreservesFiles(t *testing.T) {
	ws := t.TempDir()
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	defer func() { _ = root.Close() }()

	// Seed a pi ledger + its file on disk; pi is NOT in the sync's manifest.
	piSkill := ".agents/skills/agent-sync-x/SKILL.md"
	if err := root.Inner().MkdirAll(filepath.Dir(piSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, piSkill), []byte("# pi skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Write(root, ledger.Ledger{
		SchemaVersion: ledger.SchemaVersionCurrent,
		Target:        "pi",
		Entries:       []ledger.Entry{{Path: piSkill, SHA256: "x", Size: 1, EmittedAt: fixedNow()()}},
	}); err != nil {
		t.Fatalf("seed pi ledger: %v", err)
	}

	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{codexadapter.Bundled()},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	req := Request{
		Root: root, WorkspacePath: ws, Registry: reg,
		Targets: []string{"codex"}, // pi dropped from the manifest
		Nodes:   nil,
		Commit:  testCommit, Options: Options{Now: fixedNow(), Logger: logger},
	}
	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if got := buf.String(); !strings.Contains(got, "orphan ledger") || !strings.Contains(got, "pi") {
		t.Errorf("expected an orphan-ledger warning naming pi; got:\n%s", got)
	}
	// The dropped target's file must survive — sync warns, it does not reclaim.
	if _, err := os.Stat(filepath.Join(ws, piSkill)); err != nil {
		t.Errorf("dropped target's file must be preserved (unmanage reclaims, not sync): %v", err)
	}
}

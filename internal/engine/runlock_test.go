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
	claudeadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/claude"
	codexadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/codex"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/ledger"
	"github.com/agent-sync/agent-sync/internal/locks"
	"github.com/agent-sync/agent-sync/internal/report"
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

// TestSync_RunLockContendedReturnsBlockedNotError is the regression for the
// post-merge-yield break: when another holder has the run lock, Sync must
// return a clean summary with StatusBlocked targets (nil error) — NOT a hard
// error — so the post-merge git hook yields (exit 0) and never breaks
// `git pull`. Uses a short RunLockTimeout so the test doesn't wait the default.
func TestSync_RunLockContendedReturnsBlockedNotError(t *testing.T) {
	ws := t.TempDir()

	// Hold the run lock from a separate root, simulating an in-progress sync.
	holderRoot, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	defer func() { _ = holderRoot.Close() }()
	holder, err := locks.NewRunLock(holderRoot)
	if err != nil {
		t.Fatalf("NewRunLock: %v", err)
	}
	release, err := holder.Acquire(context.Background(), locks.AcquireOpts{})
	if err != nil {
		t.Fatalf("holder Acquire: %v", err)
	}
	defer func() { _ = release() }()

	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	defer func() { _ = root.Close() }()
	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{claudeadapter.Bundled()},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	req := Request{
		Root: root, WorkspacePath: ws, Registry: reg,
		Targets: []string{"claude"},
		Nodes:   []ir.Node{{ID: "greeter", Kind: ir.KindSkill, Version: 1, Body: []byte("# Greeter")}},
		Commit:  testCommit,
		Options: Options{Now: fixedNow(), RunLockTimeout: 200 * time.Millisecond},
	}
	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("contended run lock must NOT be a hard error; got: %v", err)
	}
	if len(summary.Targets) == 0 {
		t.Fatal("expected blocked target reports")
	}
	for _, tr := range summary.Targets {
		if tr.Status != report.StatusBlocked {
			t.Errorf("target %s status = %s, want blocked", tr.Target, tr.Status)
		}
	}
}

// TestSync_SecondSyncNoOrphanWarningForStateFiles is the regression for the
// capability-report.json false positive: the state dir holds non-ledger files
// (capability-report.json is written at the end of every sync), and a second
// sync must NOT mistake them for orphaned target ledgers.
func TestSync_SecondSyncNoOrphanWarningForStateFiles(t *testing.T) {
	ws := t.TempDir()
	req1, done1 := claudeReqOn(t, ws, []ir.Node{
		{ID: "greeter", Kind: ir.KindSkill, Version: 1, Body: []byte("# Greeter")},
	})
	if _, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	done1()

	// Second sync with a capturing logger — capability-report.json now exists.
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	defer func() { _ = root.Close() }()
	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{claudeadapter.Bundled()},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	req2 := Request{
		Root: root, WorkspacePath: ws, Registry: reg,
		Targets: []string{"claude"},
		Nodes:   []ir.Node{{ID: "greeter", Kind: ir.KindSkill, Version: 1, Body: []byte("# Greeter")}},
		Commit:  testCommit, Options: Options{Now: fixedNow(), Logger: logger},
	}
	if _, err := Sync(context.Background(), req2); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if got := buf.String(); strings.Contains(got, "orphan ledger") || strings.Contains(got, "capability-report") {
		t.Errorf("second sync must not warn about non-ledger state files; got:\n%s", got)
	}
}

// TestSync_FilteredTargetNoOrphanWarning pins that a target present in the
// manifest but excluded by TargetsFilter is NOT treated as a dropped orphan
// (warnOrphanLedgers keys off req.Targets, not the resolved/filtered set).
func TestSync_FilteredTargetNoOrphanWarning(t *testing.T) {
	ws := t.TempDir()
	// Seed a codex ledger on disk; codex stays in the manifest but is filtered out.
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	if err := ledger.Write(root, ledger.Ledger{
		SchemaVersion: ledger.SchemaVersionCurrent, Target: "codex",
		Entries: []ledger.Entry{{Path: "AGENTS.md", SHA256: "x", Size: 1, EmittedAt: fixedNow()()}},
	}); err != nil {
		t.Fatalf("seed codex ledger: %v", err)
	}
	_ = root.Close()

	root2, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	defer func() { _ = root2.Close() }()
	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{claudeadapter.Bundled(), codexadapter.Bundled()},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	req := Request{
		Root: root2, WorkspacePath: ws, Registry: reg,
		Targets: []string{"claude", "codex"}, // codex configured...
		Nodes:   nil,
		Commit:  testCommit,
		Options: Options{Now: fixedNow(), Logger: logger, TargetsFilter: []string{"claude"}}, // ...but filtered out this run
	}
	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := buf.String(); strings.Contains(got, "orphan ledger") {
		t.Errorf("a filtered-but-configured target must not warn as orphaned; got:\n%s", got)
	}
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

	// Seed a pi ledger + its file on disk (through the workspace root); pi is
	// NOT in the sync's manifest.
	piSkill := ".agents/skills/agent-sync-x/SKILL.md"
	if err := root.Inner().MkdirAll(filepath.Dir(piSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeRootFile(root, piSkill, "# pi skill\n"); err != nil {
		t.Fatalf("seed pi skill: %v", err)
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
	if _, err := root.Inner().Stat(piSkill); err != nil {
		t.Errorf("dropped target's file must be preserved (unmanage reclaims, not sync): %v", err)
	}
}

// writeRootFile writes content to a workspace-relative path through the fsroot
// Root (os.Root, which refuses symlink traversal), matching how production
// writes into a workspace — used by tests that seed workspace fixtures.
func writeRootFile(root *fsroot.Root, rel, content string) error {
	f, err := root.Inner().OpenFile(rel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

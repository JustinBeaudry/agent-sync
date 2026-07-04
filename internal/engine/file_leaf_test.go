package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter"
	cursoradapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/cursor"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
)

// cursorFileLeafReq builds a Request syncing the given nodes through the real cursor
// adapter against an existing workspace path, so a sequence of syncs runs
// against the same tree. Cursor commands use the file-leaf OutputMode
// (.cursor/commands/<id>.md), which is the surface under test.
func cursorFileLeafReq(t *testing.T, ws string, nodes []ir.Node, adopt ...string) (Request, func()) {
	t.Helper()
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{cursoradapter.Bundled()},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	req := Request{
		Root:          root,
		WorkspacePath: ws,
		Registry:      reg,
		Targets:       []string{"cursor"},
		Nodes:         nodes,
		Commit:        testCommit,
		Options:       Options{Now: fixedNow(), AdoptPrefixes: adopt},
	}
	return req, func() { _ = root.Close() }
}

func deployCmd() []ir.Node {
	return []ir.Node{{ID: "deploy", Kind: ir.KindCommand, Version: 1, Body: []byte("Run deploy.")}}
}

// TestSync_FileLeaf_PreservesForeignCommand is the file-leaf data-loss guard:
// .cursor/commands is a flat shared dir, so a sync that adds — then removes — an
// agent-sync command must never touch a foreign command file living alongside it.
func TestSync_FileLeaf_PreservesForeignCommand(t *testing.T) {
	ws := t.TempDir()

	// A foreign command the user authored; NOT in any agent-sync ledger.
	foreign := filepath.Join(ws, ".cursor", "commands", "mine.md")
	const foreignBody = "# My own command\n\nDo not delete me.\n"
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte(foreignBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sync 1: add an agent-sync command.
	req1, done1 := cursorFileLeafReq(t, ws, deployCmd())
	if summary, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync 1: %v", err)
	} else if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync 1 exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}
	done1()

	owned := filepath.Join(ws, ".cursor", "commands", "deploy.md")
	if _, err := os.Stat(owned); err != nil {
		t.Fatalf("expected agent-sync command at %s: %v", owned, err)
	}
	assertFileBytes(t, foreign, foreignBody, "after sync 1 (add)")

	// Sync 2: remove the command (no nodes). The owned file is orphan-deleted;
	// the foreign command must survive untouched, and so must the parent dir.
	req2, done2 := cursorFileLeafReq(t, ws, nil)
	if summary, err := Sync(context.Background(), req2); err != nil {
		t.Fatalf("sync 2: %v", err)
	} else if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync 2 exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}
	done2()

	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("agent-sync command should be orphan-removed; stat err = %v", err)
	}
	assertFileBytes(t, foreign, foreignBody, "after sync 2 (remove)")
}

// TestSync_FileLeaf_ExactTargetCollisionFailsClosed pins R10: a pre-existing
// UNMANAGED file at the exact target path must not be silently clobbered on
// first sync — the sync fails closed (drift), the file is byte-preserved, and
// the adopt escape takes ownership.
func TestSync_FileLeaf_ExactTargetCollisionFailsClosed(t *testing.T) {
	ws := t.TempDir()

	collide := filepath.Join(ws, ".cursor", "commands", "deploy.md")
	const userBody = "# User's own deploy command\n\nKeep me.\n"
	if err := os.MkdirAll(filepath.Dir(collide), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(collide, []byte(userBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sync emitting a `deploy` command collides with the user's file: fail closed.
	// A drift failure surfaces as a failed target (non-zero exit), not a Go error.
	req, done := cursorFileLeafReq(t, ws, deployCmd())
	summary, err := Sync(context.Background(), req)
	done()
	if err == nil && summary.Outcome.ExitCode == 0 {
		t.Fatalf("sync must fail closed on an exact-target collision; got exit=0 (%+v)", summary.Outcome)
	}
	// The load-bearing guard: the user's file is byte-preserved — never clobbered.
	assertFileBytes(t, collide, userBody, "after fail-closed collision")

	// With the adopt escape, the sync takes ownership and overwrites.
	req2, done2 := cursorFileLeafReq(t, ws, deployCmd(), ".cursor/commands/deploy.md")
	if summary, err := Sync(context.Background(), req2); err != nil {
		t.Fatalf("adopt sync: %v", err)
	} else if summary.Outcome.ExitCode != 0 {
		t.Fatalf("adopt sync exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}
	done2()
	// After adopt, agent-sync owns the file: it holds the emitted content
	// (managed header + body), replacing the user's original.
	adopted, err := os.ReadFile(collide)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(adopted), "Run deploy.") || !strings.Contains(string(adopted), "Managed by agent-sync") {
		t.Fatalf("after adopt, file must hold agent-sync-emitted content; got %q", adopted)
	}
}

// TestPlan_FileLeaf_ForeignCommandNotDrift: validate after a clean sync reports
// no drift despite a foreign command sitting in the shared parent.
func TestPlan_FileLeaf_ForeignCommandNotDrift(t *testing.T) {
	ws := t.TempDir()
	foreign := filepath.Join(ws, ".cursor", "commands", "mine.md")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req1, done1 := cursorFileLeafReq(t, ws, deployCmd())
	if _, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync: %v", err)
	}
	done1()

	req2, done2 := cursorFileLeafReq(t, ws, deployCmd())
	res, err := Plan(context.Background(), req2)
	done2()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if res.DriftDetected {
		t.Fatalf("clean re-sync must report no drift; got %+v", res.Targets)
	}
}

// TestPlan_FileLeaf_OwnedCommandOutOfBandDrift: a hand-edit of an owned command
// file is reported as OutOfBand drift; a foreign sibling is never flagged.
func TestPlan_FileLeaf_OwnedCommandOutOfBandDrift(t *testing.T) {
	ws := t.TempDir()
	req1, done1 := cursorFileLeafReq(t, ws, deployCmd())
	if _, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync: %v", err)
	}
	done1()

	// Hand-edit the owned command file out of band.
	owned := filepath.Join(ws, ".cursor", "commands", "deploy.md")
	if err := os.WriteFile(owned, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req2, done2 := cursorFileLeafReq(t, ws, deployCmd())
	res, err := Plan(context.Background(), req2)
	done2()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var sawOOB bool
	for _, ch := range res.Targets {
		for _, p := range ch.OutOfBand {
			if p == ".cursor/commands/deploy.md" {
				sawOOB = true
			}
		}
	}
	if !sawOOB {
		t.Fatalf("hand-edited owned command must be OutOfBand drift; got %+v", res.Targets)
	}
}

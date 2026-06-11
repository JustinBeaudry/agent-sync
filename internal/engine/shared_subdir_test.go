package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter"
	claudeadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/claude"
	codexadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/codex"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
)

// claudeReqOn builds a Request syncing the given nodes through the real claude
// adapter against an existing workspace path (so a sequence of syncs can run
// against the same tree).
func claudeReqOn(t *testing.T, ws string, nodes []ir.Node) (Request, func()) {
	t.Helper()
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{claudeadapter.Bundled()},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	req := Request{
		Root:          root,
		WorkspacePath: ws,
		Registry:      reg,
		Targets:       []string{"claude"},
		Nodes:         nodes,
		Commit:        testCommit,
		Options:       Options{Now: fixedNow()},
	}
	return req, func() { _ = root.Close() }
}

// TestSync_SharedSubdir_PreservesForeignSkill is the Unit 11 / shared-subdir
// data-loss regression guard: .claude/skills is a shared tree, so a sync that
// adds, then removes, an agent-sync skill must never touch a foreign skill
// living alongside it.
func TestSync_SharedSubdir_PreservesForeignSkill(t *testing.T) {
	ws := t.TempDir()

	// Seed a foreign skill the user/another tool placed under the shared tree;
	// it is NOT in any agent-sync ledger.
	foreign := filepath.Join(ws, ".claude", "skills", "user-thing", "SKILL.md")
	const foreignBody = "# User's own skill\n\nDo not delete me.\n"
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte(foreignBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sync 1: add an agent-sync skill.
	req1, done1 := claudeReqOn(t, ws, []ir.Node{
		{ID: "greeter", Kind: ir.KindSkill, Version: 1, Body: []byte("# Greeter")},
	})
	if summary, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync 1: %v", err)
	} else if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync 1 exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}
	done1()

	// The agent-sync skill landed under its agent-sync- leaf...
	agentSyncSkill := filepath.Join(ws, ".claude", "skills", "agent-sync-greeter", "SKILL.md")
	if _, err := os.Stat(agentSyncSkill); err != nil {
		t.Fatalf("expected agent-sync skill at %s: %v", agentSyncSkill, err)
	}
	// ...and the foreign skill is byte-identical after the swap.
	assertFileBytes(t, foreign, foreignBody, "after sync 1 (add)")

	// Sync 2: remove the agent-sync skill (no nodes). Its leaf is orphan-deleted;
	// the foreign skill must survive untouched.
	req2, done2 := claudeReqOn(t, ws, nil)
	if summary, err := Sync(context.Background(), req2); err != nil {
		t.Fatalf("sync 2: %v", err)
	} else if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync 2 exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}
	done2()

	// The agent-sync skill file is gone (orphan-removed)...
	if _, err := os.Stat(agentSyncSkill); !os.IsNotExist(err) {
		t.Fatalf("agent-sync skill should be orphan-removed; stat err = %v", err)
	}
	// ...and the foreign skill STILL survives.
	assertFileBytes(t, foreign, foreignBody, "after sync 2 (remove)")
}

// TestSync_SharedSubdir_CodexPreservesForeignSkill exercises the codex adapter
// (.agents/skills — the motivating shared tree) end to end: a foreign skill
// under .agents/skills survives a codex sync that adds an agent-sync skill.
func TestSync_SharedSubdir_CodexPreservesForeignSkill(t *testing.T) {
	ws := t.TempDir()
	foreign := filepath.Join(ws, ".agents", "skills", "user-thing", "SKILL.md")
	const foreignBody = "# User's own skill\n\nKeep me.\n"
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte(foreignBody), 0o644); err != nil {
		t.Fatal(err)
	}

	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	defer func() { _ = root.Close() }()
	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{codexadapter.Bundled()},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	req := Request{
		Root: root, WorkspacePath: ws, Registry: reg,
		Targets: []string{"codex"},
		Nodes:   []ir.Node{{ID: "greeter", Kind: ir.KindSkill, Version: 1, Body: []byte("# Greeter")}},
		Commit:  testCommit, Options: Options{Now: fixedNow()},
	}
	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}
	if _, err := os.Stat(filepath.Join(ws, ".agents", "skills", "agent-sync-greeter", "SKILL.md")); err != nil {
		t.Fatalf("expected agent-sync skill under .agents/skills/agent-sync-greeter: %v", err)
	}
	assertFileBytes(t, foreign, foreignBody, "after codex sync")
}

// TestSync_SharedSubdir_UpdatePreservesForeignSkill covers the update path:
// re-syncing a changed agent-sync skill replaces only its leaf, leaving the
// foreign sibling byte-identical. (Update exercises effectiveOwnedPrefixes
// deriving the leaf from BOTH the prior ledger and this run's ops.)
func TestSync_SharedSubdir_UpdatePreservesForeignSkill(t *testing.T) {
	ws := t.TempDir()
	foreign := filepath.Join(ws, ".claude", "skills", "user-thing", "SKILL.md")
	const foreignBody = "# User skill\n\nKeep me.\n"
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte(foreignBody), 0o644); err != nil {
		t.Fatal(err)
	}

	req1, done1 := claudeReqOn(t, ws, []ir.Node{
		{ID: "greeter", Kind: ir.KindSkill, Version: 1, Body: []byte("# Greeter v1")},
	})
	if _, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	done1()

	req2, done2 := claudeReqOn(t, ws, []ir.Node{
		{ID: "greeter", Kind: ir.KindSkill, Version: 2, Body: []byte("# Greeter v2 CHANGED")},
	})
	if _, err := Sync(context.Background(), req2); err != nil {
		t.Fatalf("sync 2 (update): %v", err)
	}
	done2()

	skill := filepath.Join(ws, ".claude", "skills", "agent-sync-greeter", "SKILL.md")
	body, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("read updated skill: %v", err)
	}
	if !strings.Contains(string(body), "v2 CHANGED") {
		t.Errorf("skill not updated to v2; got %q", body)
	}
	assertFileBytes(t, foreign, foreignBody, "after update sync")
}

func assertFileBytes(t *testing.T, path, want, when string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("foreign skill must survive %s, but reading it failed: %v", when, err)
	}
	if string(got) != want {
		t.Fatalf("foreign skill content changed %s:\n got: %q\nwant: %q", when, got, want)
	}
}

package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aienvs/aienvs/internal/fsroot"
	"github.com/aienvs/aienvs/internal/ir"
	"github.com/aienvs/aienvs/internal/report"
)

func intPtr(n int) *int { return &n }

// TestSync_ExpectDeletionsGuardAbortsBeforeMutation verifies the
// --expect-deletions guard runs before any swap: when the count is wrong,
// the target fails AND the would-be-deleted file is left byte-intact.
func TestSync_ExpectDeletionsGuardAbortsBeforeMutation(t *testing.T) {
	twoNodes := []ir.Node{
		{ID: "no-fri", Kind: ir.KindRule, Version: 1, Body: []byte("No PRs on Friday.")},
		{ID: "use-go", Kind: ir.KindRule, Version: 1, Body: []byte("Prefer Go.")},
	}
	req, ws, done := newClaudeRequest(t, twoNodes)
	defer done()
	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	// Second sync removes use-go but asserts zero deletions — a mismatch.
	root2, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = root2.Close() }()
	req.Root = root2
	req.Nodes = twoNodes[:1]
	req.Options.ExpectDeletions = intPtr(0)

	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("Sync returned top-level error: %v", err)
	}
	if summary.Targets[0].Status != report.StatusFailed {
		t.Fatalf("expected target failed on deletion-count mismatch, got %q", summary.Targets[0].Status)
	}
	// The guard fired before mutation: use-go.md must still be present.
	if _, statErr := os.Stat(filepath.Join(ws, ".claude/rules/aienvs/use-go.md")); statErr != nil {
		t.Fatalf("use-go.md must be intact when guard aborts: %v", statErr)
	}
}

func TestSync_NilRootIsError(t *testing.T) {
	if _, err := Sync(context.Background(), Request{}); err == nil {
		t.Fatal("expected error for nil root")
	}
	if _, err := Plan(context.Background(), Request{}); err == nil {
		t.Fatal("expected error for nil root in Plan")
	}
}

// TestSync_ExpectDeletionsMatchProceeds confirms a correct count permits
// the deletion.
func TestSync_ExpectDeletionsMatchProceeds(t *testing.T) {
	twoNodes := []ir.Node{
		{ID: "no-fri", Kind: ir.KindRule, Version: 1, Body: []byte("No PRs on Friday.")},
		{ID: "use-go", Kind: ir.KindRule, Version: 1, Body: []byte("Prefer Go.")},
	}
	req, ws, done := newClaudeRequest(t, twoNodes)
	defer done()
	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	root2, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = root2.Close() }()
	req.Root = root2
	req.Nodes = twoNodes[:1]
	req.Options.ExpectDeletions = intPtr(1)

	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("expected success with matching deletion count, got %+v", summary.Outcome)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".claude/rules/aienvs/use-go.md")); !os.IsNotExist(statErr) {
		t.Fatalf("use-go.md should be deleted, stat err = %v", statErr)
	}
}

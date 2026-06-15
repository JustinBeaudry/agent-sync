package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/ir"
)

// TestPlan_ToolOwnedIdempotent_NoDriftAfterCleanSync is the regression guard
// for the false-drift bug: validate (Plan) used to mark any ledgered
// tool-owned path as WouldUpdate unconditionally, so a clean sync was always
// followed by a spurious "drift detected" — breaking the git-hook / CI gate.
func TestPlan_ToolOwnedIdempotent_NoDriftAfterCleanSync(t *testing.T) {
	ws := t.TempDir()
	nodes := []ir.Node{
		{ID: "claude", Kind: ir.KindAgentsMD, Version: 1, Targets: []string{"claude"}, Body: []byte("# Guide\n\nWrite tests first.\n")},
	}

	// Sync, then plan against the resulting tree — expect zero drift.
	req1, done1 := claudeReqOn(t, ws, nodes)
	t.Cleanup(done1)
	if summary, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync 1: %v", err)
	} else if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync 1 exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}

	req2, done2 := claudeReqOn(t, ws, nodes)
	t.Cleanup(done2)
	plan, err := Plan(context.Background(), req2)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if plan.DriftDetected {
		t.Fatalf("validate reported drift on an in-sync workspace: %+v", plan.Targets)
	}
	for _, tc := range plan.Targets {
		if len(tc.WouldUpdate) != 0 || len(tc.WouldCreate) != 0 || len(tc.OutOfBand) != 0 {
			t.Errorf("target %q: unexpected change set after clean sync: %+v", tc.Target, tc)
		}
	}
}

// TestSync_ToolOwnedIdempotent_SecondSyncUnchanged confirms a second sync over
// an unchanged source rewrites nothing and still exits clean.
func TestSync_ToolOwnedIdempotent_SecondSyncUnchanged(t *testing.T) {
	ws := t.TempDir()
	nodes := []ir.Node{
		{ID: "claude", Kind: ir.KindAgentsMD, Version: 1, Targets: []string{"claude"}, Body: []byte("# Guide\n\nWrite tests first.\n")},
	}

	req1, done1 := claudeReqOn(t, ws, nodes)
	t.Cleanup(done1)
	if _, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	first := readFileString(t, filepath.Join(ws, "CLAUDE.md"))

	req2, done2 := claudeReqOn(t, ws, nodes)
	t.Cleanup(done2)
	summary, err := Sync(context.Background(), req2)
	if err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync 2 exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}

	if second := readFileString(t, filepath.Join(ws, "CLAUDE.md")); second != first {
		t.Errorf("CLAUDE.md changed across identical syncs:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestPlan_ToolOwned_DetectsOutOfBandEdit confirms the fix does not over-
// suppress: a hand edit INSIDE the managed section must still surface as
// drift (a sync would re-render it).
func TestPlan_ToolOwned_DetectsOutOfBandEdit(t *testing.T) {
	ws := t.TempDir()
	nodes := []ir.Node{
		{ID: "claude", Kind: ir.KindAgentsMD, Version: 1, Targets: []string{"claude"}, Body: []byte("# Guide\n\nWrite tests first.\n")},
	}

	req1, done1 := claudeReqOn(t, ws, nodes)
	t.Cleanup(done1)
	if _, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync 1: %v", err)
	}

	// Tamper inside the managed section.
	mdPath := filepath.Join(ws, "CLAUDE.md")
	orig := readFileString(t, mdPath)
	tampered := strings.Replace(orig, "Write tests first.", "Write tests LATER.", 1)
	if tampered == orig {
		t.Fatal("test setup: managed body not found to tamper")
	}
	if err := os.WriteFile(mdPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	req2, done2 := claudeReqOn(t, ws, nodes)
	t.Cleanup(done2)
	plan, err := Plan(context.Background(), req2)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if !plan.DriftDetected {
		t.Fatalf("validate must report drift after an out-of-band edit inside the managed section; got none: %+v", plan.Targets)
	}
	// Drift must land in WouldUpdate (the file exists and would be rewritten),
	// not WouldCreate/OutOfBand — a future refactor routing it elsewhere would
	// otherwise pass the bare DriftDetected check while mis-reporting the kind.
	if len(plan.Targets) != 1 || len(plan.Targets[0].WouldUpdate) != 1 || plan.Targets[0].WouldUpdate[0] != "CLAUDE.md" {
		t.Errorf("expected WouldUpdate=[CLAUDE.md], got %+v", plan.Targets)
	}
}

// TestPlan_MCPEntryIdempotent_NoDriftAcrossKinds locks the validate-idempotency
// fix for the JSON and TOML tool-owned merge kinds (not just markdown): an
// mcp-server-entry synced through all three adapters writes .mcp.json /
// .cursor/mcp.json (JSON) and .codex/config.toml (TOML); validate immediately
// after must report no drift for any of them.
func TestPlan_MCPEntryIdempotent_NoDriftAcrossKinds(t *testing.T) {
	ws := t.TempDir()
	nodes := []ir.Node{
		{ID: "echo", Kind: ir.KindMCPServerEntry, Version: 1, Body: []byte(`{"command":"echo-server"}`)},
	}

	req1, done1 := dogfoodReq(t, ws)
	t.Cleanup(done1)
	req1.Nodes = nodes
	if summary, err := Sync(context.Background(), req1); err != nil {
		t.Fatalf("sync: %v", err)
	} else if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}

	req2, done2 := dogfoodReq(t, ws)
	t.Cleanup(done2)
	req2.Nodes = nodes
	plan, err := Plan(context.Background(), req2)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if plan.DriftDetected {
		t.Fatalf("validate reported drift for JSON/TOML mcp entries after a clean sync: %+v", plan.Targets)
	}
}

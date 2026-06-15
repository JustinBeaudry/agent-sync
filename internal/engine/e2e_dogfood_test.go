package engine

import (
	"context"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter"
	claudeadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/claude"
	codexadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/codex"
	cursoradapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/cursor"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
)

// dogfoodNodes is a realistic canonical-repo content set covering every IR
// concept a dogfooder is likely to author: project guidance (agents-md), a
// rule, a skill, and an MCP server entry. Together they exercise all output
// modes — reserved-subdir (rule/skill), markdown-section tool-owned merge
// (AGENTS.md/CLAUDE.md), and JSON/TOML tool-owned merge (.mcp.json,
// .cursor/mcp.json, .codex/config.toml).
func dogfoodNodes() []ir.Node {
	return []ir.Node{
		{ID: "agents", Kind: ir.KindAgentsMD, Version: 1, Body: []byte("# Team Guide\n\nAlways write tests first.\n")},
		{ID: "no-friday-deploys", Kind: ir.KindRule, Version: 1, Body: []byte("Never deploy on Fridays.\n")},
		{ID: "code-review", Kind: ir.KindSkill, Version: 1, Body: []byte("# Code Review\n\nReview the diff carefully.\n")},
		{ID: "echo", Kind: ir.KindMCPServerEntry, Version: 1, Body: []byte(`{"command":"echo-server"}`)},
	}
}

func dogfoodReq(t *testing.T, ws string) (Request, func()) {
	t.Helper()
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{
			claudeadapter.Bundled(),
			cursoradapter.Bundled(),
			codexadapter.Bundled(),
		},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	req := Request{
		Root:          root,
		WorkspacePath: ws,
		Registry:      reg,
		Targets:       []string{"claude", "cursor", "codex"},
		Nodes:         dogfoodNodes(),
		Commit:        testCommit,
		Options:       Options{Now: fixedNow()},
	}
	return req, func() { _ = root.Close() }
}

// TestE2E_Dogfood_HappyPath locks the full dogfood flow across all three
// bundled adapters: init→sync→validate(clean)→re-sync(unchanged). This single
// test exercises both fixes in this pass (claude AGENTS.md emit + tool-owned
// validate idempotency) end to end and would fail if either regressed.
func TestE2E_Dogfood_HappyPath(t *testing.T) {
	ws := t.TempDir()

	// Sync 1: every target must succeed (warnings for unsupported kinds are
	// fine; failures are not).
	req1, done1 := dogfoodReq(t, ws)
	summary, err := Sync(context.Background(), req1)
	if err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	done1()
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync 1 exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}

	// Validate immediately after a clean sync: zero drift on every target.
	req2, done2 := dogfoodReq(t, ws)
	plan, err := Plan(context.Background(), req2)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	done2()
	if plan.DriftDetected {
		for _, tc := range plan.Targets {
			t.Logf("target %q: create=%v update=%v delete=%v oob=%v", tc.Target, tc.WouldCreate, tc.WouldUpdate, tc.WouldDelete, tc.OutOfBand)
		}
		t.Fatalf("validate reported drift immediately after a clean sync")
	}

	// Sync 2: nothing changed, still clean.
	req3, done3 := dogfoodReq(t, ws)
	summary2, err := Sync(context.Background(), req3)
	if err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	done3()
	if summary2.Outcome.ExitCode != 0 {
		t.Fatalf("sync 2 exit = %d, want 0 (%+v)", summary2.Outcome.ExitCode, summary2.Outcome)
	}
}

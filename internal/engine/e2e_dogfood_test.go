package engine

import (
	"context"
	"path/filepath"
	"strings"
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
	t.Cleanup(done1)
	summary, err := Sync(context.Background(), req1)
	if err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync 1 exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}

	// Tool-owned files were actually written, across all three merge kinds:
	// markdown-section (CLAUDE.md/AGENTS.md), JSON (.mcp.json, .cursor/mcp.json),
	// and TOML (.codex/config.toml). Asserting drift==false alone would not
	// prove the files exist with the expected content.
	for _, f := range []struct{ path, want string }{
		{"CLAUDE.md", "Always write tests first."}, // claude agents-md
		{"AGENTS.md", "Always write tests first."}, // cursor/codex agents-md
		{".mcp.json", "echo-server"},               // claude JSON
		{".cursor/mcp.json", "echo-server"},        // cursor JSON
		{".codex/config.toml", "echo-server"},      // codex TOML
	} {
		got := readFileString(t, filepath.Join(ws, f.path))
		if !strings.Contains(got, f.want) {
			t.Errorf("%s missing %q after sync; got:\n%s", f.path, f.want, got)
		}
	}

	// Cross-adapter marker parity: the engine owns the begin/end markers, so
	// every agents-md emitter (claude->CLAUDE.md, cursor/codex->AGENTS.md) must
	// produce exactly one well-formed pair — never a double-wrap. This guards
	// the same bug fixed in the claude adapter against cursor/codex regressing.
	for _, mdFile := range []string{"CLAUDE.md", "AGENTS.md"} {
		body := readFileString(t, filepath.Join(ws, mdFile))
		if n := strings.Count(body, "<!-- agent-sync:begin id=agents -->"); n != 1 {
			t.Errorf("%s begin-marker count = %d, want 1 (double-wrap?):\n%s", mdFile, n, body)
		}
		if n := strings.Count(body, "<!-- agent-sync:end id=agents -->"); n != 1 {
			t.Errorf("%s end-marker count = %d, want 1:\n%s", mdFile, n, body)
		}
	}

	// Validate immediately after a clean sync: zero drift on every target.
	req2, done2 := dogfoodReq(t, ws)
	t.Cleanup(done2)
	plan, err := Plan(context.Background(), req2)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if plan.DriftDetected {
		for _, tc := range plan.Targets {
			t.Logf("target %q: create=%v update=%v delete=%v oob=%v", tc.Target, tc.WouldCreate, tc.WouldUpdate, tc.WouldDelete, tc.OutOfBand)
		}
		t.Fatalf("validate reported drift immediately after a clean sync")
	}

	// Sync 2: nothing changed, still clean.
	req3, done3 := dogfoodReq(t, ws)
	t.Cleanup(done3)
	summary2, err := Sync(context.Background(), req3)
	if err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if summary2.Outcome.ExitCode != 0 {
		t.Fatalf("sync 2 exit = %d, want 0 (%+v)", summary2.Outcome.ExitCode, summary2.Outcome)
	}
}

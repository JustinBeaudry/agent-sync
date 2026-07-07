package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	claudeadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/claude"
	codexadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/codex"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/harness"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/ledger"
	"github.com/agent-sync/agent-sync/internal/report"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func fixedNow() func() time.Time {
	t := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// readFileString reads a workspace file and fails the test on error. Shared
// across engine tests that assert on emitted file contents.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// newClaudeRequest builds a Request that syncs a single rule node through
// the real bundled claude adapter into a fresh temp workspace.
func newClaudeRequest(t *testing.T, nodes []ir.Node) (Request, string, func()) {
	t.Helper()
	ws := t.TempDir()
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
	return req, ws, func() { _ = root.Close() }
}

func codexReqOn(t *testing.T, ws string) (Request, func()) {
	t.Helper()
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{codexadapter.Bundled()},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	req := Request{
		Root:          root,
		WorkspacePath: ws,
		Registry:      reg,
		Targets:       []string{"codex"},
		Commit:        testCommit,
		Options:       Options{Now: fixedNow()},
	}
	return req, func() { _ = root.Close() }
}

func TestSync_FirstSyncWritesRuleFileAndLedger(t *testing.T) {
	req, ws, done := newClaudeRequest(t, []ir.Node{
		{ID: "no-fri", Kind: ir.KindRule, Version: 1, Body: []byte("No PRs on Friday.")},
	})
	defer done()

	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (summary: %+v)", summary.Outcome.ExitCode, summary.Outcome)
	}
	if len(summary.Targets) != 1 || summary.Targets[0].Status != report.StatusOK {
		t.Fatalf("target status = %+v, want ok", summary.Targets)
	}

	// The rule file landed on disk via the swap.
	ruleFile := filepath.Join(ws, ".claude", "rules", "agent-sync", "no-fri.md")
	data, err := os.ReadFile(ruleFile)
	if err != nil {
		t.Fatalf("expected rule file at %s: %v", ruleFile, err)
	}
	if len(data) == 0 {
		t.Fatal("rule file is empty")
	}

	// The ledger recorded the emitted path.
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("reopen root: %v", err)
	}
	defer func() { _ = root.Close() }()
	led, err := ledger.Load(root, "claude")
	if err != nil {
		t.Fatalf("ledger.Load: %v", err)
	}
	found := false
	for _, e := range led.Entries {
		if e.Path == ".claude/rules/agent-sync/no-fri.md" {
			found = true
			if e.SHA256 == "" {
				t.Fatal("ledger entry missing sha256")
			}
		}
	}
	if !found {
		t.Fatalf("ledger missing rule path; entries: %+v", led.Entries)
	}
}

func TestSync_AppliesCodexNativeFeatureFragment(t *testing.T) {
	ws := t.TempDir()
	req, done := codexReqOn(t, ws)
	t.Cleanup(done)
	req.Fragments = []harness.Fragment{{
		ID: "hooks", Target: "codex", Path: ".codex/config.toml",
		Merge: harness.MergeTOMLKey, Locator: "features.hooks",
		Payload: []byte("[features]\nhooks = true\n"),
	}}

	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("exit = %d summary=%+v", summary.Outcome.ExitCode, summary)
	}
	data := readFileString(t, filepath.Join(ws, ".codex", "config.toml"))
	if !strings.Contains(data, "[features]\nhooks = true\n") {
		t.Fatalf("config.toml missing hooks feature:\n%s", data)
	}
	led, err := ledger.Load(req.Root, "codex")
	if err != nil {
		t.Fatalf("ledger.Load: %v", err)
	}
	if _, ok := entryForSuffix(led, ".codex/config.toml"); !ok {
		t.Fatalf("ledger missing config.toml entry: %+v", led.Entries)
	}
}

func TestSync_SecondSyncNoChangesIsUnchanged(t *testing.T) {
	nodes := []ir.Node{{ID: "no-fri", Kind: ir.KindRule, Version: 1, Body: []byte("No PRs on Friday.")}}
	req, ws, done := newClaudeRequest(t, nodes)
	defer done()

	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	// Re-open a fresh root over the same workspace for the second sync.
	root2, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = root2.Close() }()
	req.Root = root2

	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if got := summary.Targets[0].Status; got != report.StatusUnchanged {
		t.Fatalf("second sync status = %q, want unchanged", got)
	}
}

func TestSync_RemovedNodeOrphansItsFile(t *testing.T) {
	twoNodes := []ir.Node{
		{ID: "no-fri", Kind: ir.KindRule, Version: 1, Body: []byte("No PRs on Friday.")},
		{ID: "use-go", Kind: ir.KindRule, Version: 1, Body: []byte("Prefer Go.")},
	}
	req, ws, done := newClaudeRequest(t, twoNodes)
	defer done()
	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude/rules/agent-sync/use-go.md")); err != nil {
		t.Fatalf("use-go.md should exist after first sync: %v", err)
	}

	// Second sync with use-go removed → its file becomes an orphan.
	root2, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = root2.Close() }()
	req.Root = root2
	req.Nodes = twoNodes[:1]

	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude/rules/agent-sync/use-go.md")); !os.IsNotExist(err) {
		t.Fatalf("use-go.md should be gone after removal, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude/rules/agent-sync/no-fri.md")); err != nil {
		t.Fatalf("no-fri.md should remain: %v", err)
	}
}

func TestPlan_DetectsPendingCreate(t *testing.T) {
	req, _, done := newClaudeRequest(t, []ir.Node{
		{ID: "no-fri", Kind: ir.KindRule, Version: 1, Body: []byte("No PRs on Friday.")},
	})
	defer done()

	plan, err := Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.DriftDetected {
		t.Fatal("expected drift detected on clean workspace with pending create")
	}
	if len(plan.Targets) != 1 || len(plan.Targets[0].WouldCreate) == 0 {
		t.Fatalf("expected would-create entries, got %+v", plan.Targets)
	}

	// Plan must not have mutated the workspace.
	if _, err := os.Stat(filepath.Join(req.WorkspacePath, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("Plan should not write files; .claude exists (err=%v)", err)
	}
}

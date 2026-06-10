package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
)

// TestSync_MCPServerEntryWritesSidecarAsFile is the regression test for the
// exact-prefix bug: the claude adapter declares ".aienvs-managed" as an
// owned-subdir output but emits it as a single file. A naive
// stage-and-swap-as-directory turns it into ".aienvs-managed/.aienvs-managed".
// This asserts the sidecar lands as a regular file and the tool-owned
// .mcp.json merge happened.
func TestSync_MCPServerEntryWritesSidecarAsFile(t *testing.T) {
	req, ws, done := newClaudeRequest(t, []ir.Node{
		{
			ID:      "echo",
			Kind:    ir.KindMCPServerEntry,
			Version: 1,
			Body:    []byte(`{"command":"echo","args":["hi"]}`),
		},
	})
	defer done()

	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (targets: %+v)", summary.Outcome.ExitCode, summary.Targets)
	}

	// The sidecar must be a regular file, never a directory.
	sidecar := filepath.Join(ws, ".aienvs-managed")
	info, err := os.Stat(sidecar)
	if err != nil {
		t.Fatalf("expected .aienvs-managed sidecar: %v", err)
	}
	if info.IsDir() {
		t.Fatal(".aienvs-managed is a directory; exact-prefix bug regressed")
	}

	// The tool-owned .mcp.json must exist and carry the merged entry.
	mcp := filepath.Join(ws, ".mcp.json")
	data, err := os.ReadFile(mcp)
	if err != nil {
		t.Fatalf("expected .mcp.json: %v", err)
	}
	if len(data) == 0 {
		t.Fatal(".mcp.json is empty")
	}

	// Second sync must be unchanged (idempotent).
	root2, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = root2.Close() }()
	req.Root = root2
	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	info2, err := os.Stat(sidecar)
	if err != nil || info2.IsDir() {
		t.Fatalf("sidecar broken after second sync: err=%v isDir=%v", err, info2 != nil && info2.IsDir())
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		base, target string
		want         bool
	}{
		{".claude/rules/.agent-sync-staging/g/aienvs", ".claude/rules/.agent-sync-staging/g/aienvs/r.md", true},
		{".claude/rules/.agent-sync-staging/g/aienvs", ".claude/rules/.agent-sync-staging/g/aienvs", true},
		{".claude/rules/.agent-sync-staging/g/aienvs", ".claude/rules/.agent-sync-staging/g/aienvs/../../../../../evil.md", false},
		{".claude/x", ".claude/xother/f", false},
	}
	for _, c := range cases {
		if got := within(c.base, c.target); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.base, c.target, got, c.want)
		}
	}
}

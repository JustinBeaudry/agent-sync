package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/ir"
)

// TestSync_ClaudeAgentsMD_MergesThroughEngine is the regression guard for the
// claude agents-md double-wrap bug: the adapter must emit the INNER body and
// let the engine own the begin/end markers. Before the fix, the claude adapter
// pre-wrapped the body, the merge rejected it ("the engine owns markers; pass
// inner body only"), and `sync --target claude` failed on the most basic
// AGENTS.md config. The claude unit tests never caught it because none ran the
// emit through the real engine merge.
func TestSync_ClaudeAgentsMD_MergesThroughEngine(t *testing.T) {
	ws := t.TempDir()

	const body = "# Team Guide\n\nAlways write tests first.\n"
	req, done := claudeReqOn(t, ws, []ir.Node{
		{ID: "claude", Kind: ir.KindAgentsMD, Version: 1, Targets: []string{"claude"}, Body: []byte(body)},
	})
	defer done()

	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}

	got := readFileString(t, filepath.Join(ws, "CLAUDE.md"))

	// Exactly one well-formed begin/end pair — the engine renders the markers
	// once. A double-wrap would yield a nested or duplicated pair.
	if n := strings.Count(got, "<!-- agent-sync:begin id=claude -->"); n != 1 {
		t.Errorf("CLAUDE.md begin-marker count = %d, want 1; got:\n%s", n, got)
	}
	if n := strings.Count(got, "<!-- agent-sync:end id=claude -->"); n != 1 {
		t.Errorf("CLAUDE.md end-marker count = %d, want 1; got:\n%s", n, got)
	}
	if !strings.Contains(got, "Always write tests first.") {
		t.Errorf("CLAUDE.md missing user body; got:\n%s", got)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter"
	cursoradapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/cursor"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/ledger"
)

// cursorReqOn builds a Request syncing the given nodes through the real cursor
// adapter against an existing workspace path.
func cursorReqOn(t *testing.T, ws string, nodes []ir.Node) (Request, func()) {
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
		Options:       Options{Now: fixedNow()},
	}
	return req, func() { _ = root.Close() }
}

// TestSync_ForeignProvenanceRule_IsOpaque is the U1 (hierarchy-composition)
// design-gate guard. Composition injects user-scope rule nodes into a project
// scope's Request.Nodes; those injected nodes carry the USER manifest's
// Provenance{Path, BlobSHA} — a path outside the project root and a blob SHA
// from a different commit than the project's canonical source.
//
// The whole composition design (plan docs/plans/2026-07-01-002, decision D4)
// bets that Node.Provenance is OPAQUE downstream: the engine serializes it into
// the adapter wire payload (MarshalIR) but never uses it to locate or verify
// content against the project root, and the ledger records the SHA256 of the
// bytes actually written — not the git blob SHA. This test encodes that bet as
// a regression guard: a rule node whose provenance points nowhere near the
// project must sync cleanly, land at the expected path, be recorded in the
// ledger under the CONTENT hash (not the foreign blob SHA), and be idempotent.
//
// If a future engine/adapter change reintroduces provenance coupling (e.g. a
// re-read via the project root, or a blob-SHA cross-check), this test breaks —
// which is exactly the signal composition depends on.
func TestSync_ForeignProvenanceRule_IsOpaque(t *testing.T) {
	ws := t.TempDir()

	const body = "# Global rule\n\nAlways prefer composition over inheritance.\n"
	// Provenance that resolves NOWHERE within ws: an absolute-looking user-home
	// path and a blob SHA that is not the project's canonical commit.
	foreignProv := ir.Provenance{
		Path:    "../../home/user/.agent-sync-src/rules/global.md",
		BlobSHA: "ffffffffffffffffffffffffffffffffffffffff",
	}
	node := ir.Node{
		ID:         "global",
		Kind:       ir.KindRule,
		Version:    1,
		Targets:    []string{"cursor"},
		Provenance: foreignProv,
		Body:       []byte(body),
	}

	// Sync 1: emit the foreign-provenance rule.
	req1, done1 := cursorReqOn(t, ws, []ir.Node{node})
	summary, err := Sync(context.Background(), req1)
	if err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("sync 1 exit = %d, want 0 (%+v)", summary.Outcome.ExitCode, summary.Outcome)
	}

	// The rule landed at the project-relative Cursor path, derived from the node
	// ID — never from the foreign provenance path.
	rulePath := filepath.Join(ws, ".cursor", "rules", "agent-sync", "global.mdc")
	got := readFileString(t, rulePath)
	if !strings.Contains(got, "Always prefer composition over inheritance.") {
		t.Errorf(".mdc missing body; got:\n%s", got)
	}

	// The ledger records the written path with the CONTENT hash, not the foreign
	// blob SHA. This is the crux of "provenance is opaque": the ledger never sees
	// the git blob SHA.
	led, err := ledger.Load(req1.Root, "cursor")
	if err != nil {
		t.Fatalf("ledger.Load: %v", err)
	}
	entry, ok := entryForSuffix(led, filepath.Join(".cursor", "rules", "agent-sync", "global.mdc"))
	if !ok {
		t.Fatalf("no ledger entry for global.mdc; entries=%+v", led.Entries)
	}
	if entry.SHA256 == foreignProv.BlobSHA {
		t.Errorf("ledger recorded the FOREIGN blob SHA %q — provenance leaked into ownership", entry.SHA256)
	}
	if len(entry.SHA256) != 64 {
		t.Errorf("ledger SHA256 = %q, want a 64-hex content hash", entry.SHA256)
	}
	done1()

	// Sync 2: identical node, second run. Idempotent — foreign provenance must
	// not induce false drift (the ledger compares content bytes, not provenance).
	req2, done2 := cursorReqOn(t, ws, []ir.Node{node})
	summary2, err := Sync(context.Background(), req2)
	if err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if summary2.Outcome.ExitCode != 0 {
		t.Fatalf("sync 2 exit = %d, want 0 (idempotent) (%+v)", summary2.Outcome.ExitCode, summary2.Outcome)
	}
	assertFileBytes(t, rulePath, got, "after idempotent re-sync")
	done2()
}

// entryForSuffix finds the ledger entry whose Path ends with the given
// workspace-relative suffix (ledger paths are workspace-relative posix paths).
func entryForSuffix(led ledger.Ledger, suffix string) (ledger.Entry, bool) {
	suffix = filepath.ToSlash(suffix)
	for _, e := range led.Entries {
		if filepath.ToSlash(e.Path) == suffix {
			return e, true
		}
	}
	return ledger.Entry{}, false
}

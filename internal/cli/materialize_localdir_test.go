package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/manifest"
)

func writeWS(t *testing.T, ws, rel, body string) {
	t.Helper()
	full := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func localDirManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	m := &manifest.Manifest{Version: 1}
	m.Canonical.LocalDir = ".agents"
	m.Targets = []string{"claude"}
	if err := manifest.Validate(m, manifest.LoadOptions{}); err != nil {
		t.Fatalf("validate manifest: %v", err)
	}
	return m
}

// A local_dir source must materialize with no network, no pin, and no trust
// gate — proven here by running with Offline:true and asserting decoded IR.
func TestMaterialize_LocalDir_OfflineNoTrustNoPin(t *testing.T) {
	ws := t.TempDir()
	writeWS(t, ws, ".agents/skills/foo/SKILL.md", "skill body\n")
	writeWS(t, ws, ".agents/rules/r.md", "rule body\n")

	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer func() { _ = root.Close() }()

	mat, err := materialize(context.Background(), localDirManifest(t), materializeOptions{
		Offline: true,
		Now:     time.Now(),
		Root:    root,
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if mat.Commit != "" {
		t.Errorf("commit = %q, want empty (unpinned working-tree source)", mat.Commit)
	}
	if _, ok := mat.Skills["foo"]; !ok {
		t.Errorf("skill foo missing from materialized skills")
	}
	foundRule := false
	for _, n := range mat.Nodes {
		if n.Kind == ir.KindRule && n.ID == "r" {
			foundRule = true
		}
	}
	if !foundRule {
		t.Errorf("rule r missing from materialized nodes")
	}
}

func TestMaterialize_LocalDir_MissingDirErrors(t *testing.T) {
	ws := t.TempDir() // no .agents created
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer func() { _ = root.Close() }()

	_, err = materialize(context.Background(), localDirManifest(t), materializeOptions{Root: root})
	if err == nil {
		t.Fatal("expected error for missing local_dir source directory")
	}
}

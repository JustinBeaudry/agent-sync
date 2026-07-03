package cli

import (
	"context"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/manifest"
)

// TestMaterialize_LocalDir_SourceURLIsPathWithEmptyCommit pins the U2
// session-source rule for the working-tree source kind: the session's
// source_url is the path string from the manifest and the commit stays
// empty (there is no pin to report).
func TestMaterialize_LocalDir_SourceURLIsPathWithEmptyCommit(t *testing.T) {
	ws := t.TempDir()
	writeWS(t, ws, ".agents/rules/r.md", "rule body\n")

	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })

	mat, err := materialize(context.Background(), localDirManifest(t), materializeOptions{
		Offline: true,
		Now:     time.Now(),
		Root:    root,
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if mat.SourceURL != ".agents" {
		t.Errorf("SourceURL = %q, want the local_dir path %q", mat.SourceURL, ".agents")
	}
	if mat.Commit != "" {
		t.Errorf("Commit = %q, want empty for a working-tree source", mat.Commit)
	}
}

// TestMaterialize_LocalPath_SourceURLIsPath pins the local_path branch:
// the session's source_url is the manifest's path string.
func TestMaterialize_LocalPath_SourceURLIsPath(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)

	m := &manifest.Manifest{Version: 1}
	m.Canonical.LocalPath = canonical
	m.Canonical.Commit = sha
	m.Targets = []string{"claude"}

	mat, err := materialize(context.Background(), m, materializeOptions{Now: time.Now()})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if mat.SourceURL != canonical {
		t.Errorf("SourceURL = %q, want the local_path %q", mat.SourceURL, canonical)
	}
	if mat.Commit != sha {
		t.Errorf("Commit = %q, want the pinned sha %q", mat.Commit, sha)
	}
}

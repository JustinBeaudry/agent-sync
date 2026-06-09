package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aienvs/aienvs/internal/manifest"
)

func runInit(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	root.SetArgs(append([]string{"init", "--non-interactive"}, args...))
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestInit_NonInteractiveLocalPathWritesLoadableManifest(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := t.TempDir()

	_, errOut, err := runInit(t,
		"--dir", ws,
		"--local-path", canonical,
		"--commit", sha,
		"--target", "claude",
	)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, errOut)
	}
	mpath := filepath.Join(ws, ".aienv.yaml")
	m, lerr := manifest.LoadFile(mpath, manifest.LoadOptions{})
	if lerr != nil {
		t.Fatalf("written manifest does not load: %v", lerr)
	}
	if m.Canonical.LocalPath != canonical {
		t.Fatalf("local_path = %q, want %q", m.Canonical.LocalPath, canonical)
	}
	if m.Canonical.Commit != sha || m.TrustedSHA != sha {
		t.Fatalf("manifest not pinned: commit=%q trusted=%q want %q", m.Canonical.Commit, m.TrustedSHA, sha)
	}
}

func TestInit_NonInteractiveMissingSourceFailsFast(t *testing.T) {
	ws := t.TempDir()
	_, _, err := runInit(t, "--dir", ws)
	if err == nil {
		t.Fatal("expected fail-fast when --source/--local-path missing in non-interactive mode")
	}
	var mfe *MissingFlagError
	if !errors.As(err, &mfe) {
		t.Fatalf("expected MissingFlagError, got %T: %v", err, err)
	}
}

// TestInit_LocalPathPinsHEADWithoutCommit verifies init resolves a local
// repo's HEAD to a SHA (no --commit, no network), so a local-path manifest
// is pinned and immediately syncable.
func TestInit_LocalPathPinsHEADWithoutCommit(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := t.TempDir()

	if _, errOut, err := runInit(t, "--dir", ws, "--local-path", canonical, "--target", "claude"); err != nil {
		t.Fatalf("init: %v\n%s", err, errOut)
	}
	m, lerr := manifest.LoadFile(filepath.Join(ws, ".aienv.yaml"), manifest.LoadOptions{})
	if lerr != nil {
		t.Fatalf("manifest load: %v", lerr)
	}
	if m.Canonical.Commit != sha {
		t.Fatalf("expected init to pin HEAD %s, got commit %q", sha, m.Canonical.Commit)
	}
	// And it syncs.
	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("sync after local-path init: %v\n%s", err, errOut)
	}
}

func TestInit_RefusesToOverwrite(t *testing.T) {
	requireGit(t)
	canonical, _ := makeCanonicalRepo(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".aienv.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runInit(t, "--dir", ws, "--local-path", canonical, "--floating")
	if err == nil {
		t.Fatal("init should refuse to overwrite an existing manifest")
	}
}

// TestInit_ThenSync proves the init -> sync loop closes: a manifest
// written by `aienvs init` is synced successfully by `aienvs sync`.
func TestInit_ThenSync(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := t.TempDir()

	if _, errOut, err := runInit(t, "--dir", ws, "--local-path", canonical, "--commit", sha, "--target", "claude"); err != nil {
		t.Fatalf("init: %v\n%s", err, errOut)
	}
	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("sync after init: %v\n%s", err, errOut)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".claude", "rules", "aienvs", "no-fri.md")); statErr != nil {
		t.Fatalf("expected synced rule file: %v", statErr)
	}
}

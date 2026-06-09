package workspace_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/workspace"
)

func writeManifest(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, workspace.ManifestName)
	const body = "version: 1\ncanonical:\n  url: https://example.com/x.git\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return p
}

func TestFind_NearestManifest(t *testing.T) {
	// Layout: root/a/b/c, manifest at root.
	root := t.TempDir()
	manifest := writeManifest(t, root)
	leaf := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("mkdir leaf: %v", err)
	}

	ws, err := workspace.Find(leaf, workspace.Options{})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if ws.ManifestPath != manifest {
		t.Errorf("manifest path = %q, want %q", ws.ManifestPath, manifest)
	}
	if ws.Root != root {
		t.Errorf("root = %q, want %q", ws.Root, root)
	}
	if ws.LogicalCwd != leaf {
		t.Errorf("LogicalCwd = %q, want %q", ws.LogicalCwd, leaf)
	}
}

func TestFind_NearestWinsOverAncestor(t *testing.T) {
	// Layout: outer/ and outer/inner/ both have manifests.
	// cwd inside outer/inner/ must pick inner's manifest.
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	_ = writeManifest(t, outer)
	innerManifest := writeManifest(t, inner)

	ws, err := workspace.Find(inner, workspace.Options{})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if ws.ManifestPath != innerManifest {
		t.Errorf("nearest not chosen: got %q, want %q", ws.ManifestPath, innerManifest)
	}
}

func TestFind_ExplicitWorkspaceDirectory(t *testing.T) {
	// --workspace <dir> short-circuits discovery.
	root := t.TempDir()
	manifest := writeManifest(t, root)
	unrelated := t.TempDir()

	ws, err := workspace.Find(unrelated, workspace.Options{Workspace: root})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if ws.ManifestPath != manifest {
		t.Errorf("manifest = %q, want %q", ws.ManifestPath, manifest)
	}
}

func TestFind_ExplicitWorkspaceManifestFile(t *testing.T) {
	// --workspace <path-to-manifest> also supported.
	root := t.TempDir()
	manifest := writeManifest(t, root)

	ws, err := workspace.Find(root, workspace.Options{Workspace: manifest})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if ws.Root != root {
		t.Errorf("root = %q, want %q", ws.Root, root)
	}
}

func TestFind_ExplicitWorkspaceMissing(t *testing.T) {
	// --workspace points at a dir without a manifest.
	empty := t.TempDir()
	_, err := workspace.Find(empty, workspace.Options{Workspace: empty})
	if err == nil {
		t.Fatal("expected ErrInvalidOptions for empty dir")
	}
	if !errors.Is(err, workspace.ErrInvalidOptions) {
		t.Errorf("expected ErrInvalidOptions, got %v", err)
	}
}

func TestFind_ExplicitWorkspaceNotManifestFile(t *testing.T) {
	root := t.TempDir()
	bogus := filepath.Join(root, "not-a-manifest.yaml")
	if err := os.WriteFile(bogus, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := workspace.Find(root, workspace.Options{Workspace: bogus})
	if err == nil {
		t.Fatal("expected rejection of non-manifest file")
	}
	if !errors.Is(err, workspace.ErrInvalidOptions) {
		t.Errorf("expected ErrInvalidOptions, got %v", err)
	}
}

func TestFind_NotFoundWithStopAt(t *testing.T) {
	// Structure: /tmpdir/a/b; stop at /tmpdir/a; no manifest anywhere.
	tmp := t.TempDir()
	stop := filepath.Join(tmp, "a")
	leaf := filepath.Join(stop, "b")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := workspace.Find(leaf, workspace.Options{StopAt: stop})
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "stop-at") {
		t.Errorf("error should name stop-at; got: %v", err)
	}
}

func TestFind_StopAtHonoredBeforeRoot(t *testing.T) {
	// Manifest exists at /tmpdir (would be found by default walk),
	// but StopAt at /tmpdir/a/b blocks the walk — never sees the
	// manifest above it.
	tmp := t.TempDir()
	_ = writeManifest(t, tmp)
	stop := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(stop, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := workspace.Find(stop, workspace.Options{StopAt: stop})
	if err == nil {
		t.Fatal("expected ErrNotFound when StopAt blocks the walk")
	}
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFind_StopAtAllowsDiscoveryAtStopDir(t *testing.T) {
	// StopAt == dir holding the manifest: should succeed (we search
	// the stop directory itself before terminating).
	tmp := t.TempDir()
	stopDir := filepath.Join(tmp, "home")
	_ = writeManifest(t, stopDir)

	ws, err := workspace.Find(stopDir, workspace.Options{StopAt: stopDir})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if ws.Root != stopDir {
		t.Errorf("root = %q, want %q", ws.Root, stopDir)
	}
}

func TestFind_MaxHopsExceeded(t *testing.T) {
	// Construct a chain deeper than MaxHops to force the guard.
	tmp := t.TempDir()
	dir := tmp
	for i := 0; i < 4; i++ {
		dir = filepath.Join(dir, "d")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir chain: %v", err)
	}

	// MaxHops=1 with no manifest + no stop means the walk should abort
	// after one hop.
	_, err := workspace.Find(dir, workspace.Options{MaxHops: 1})
	if err == nil {
		t.Fatal("expected ErrMaxWalkExceeded")
	}
	if !errors.Is(err, workspace.ErrMaxWalkExceeded) {
		t.Errorf("expected ErrMaxWalkExceeded, got %v", err)
	}
}

func TestFind_NotFoundAtFilesystemRoot(t *testing.T) {
	// Use a real subtree under t.TempDir() with no manifest; set
	// MaxHops high enough that the walk reaches filesystem root.
	tmp := t.TempDir()
	leaf := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := workspace.Find(leaf, workspace.Options{MaxHops: 100})
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFind_ManifestIsDirectoryReturnsError(t *testing.T) {
	// Create .aienv.yaml as a directory rather than a regular file.
	tmp := t.TempDir()
	manifestDir := filepath.Join(tmp, workspace.ManifestName)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("mkdir manifest-as-dir: %v", err)
	}

	_, err := workspace.Find(tmp, workspace.Options{})
	if err == nil {
		t.Fatal("expected ErrManifestNotRegular; got nil")
	}
	if !errors.Is(err, workspace.ErrManifestNotRegular) {
		t.Errorf("expected ErrManifestNotRegular, got %v", err)
	}
}

func TestFind_StopAtOutsideAncestorChain(t *testing.T) {
	// StopAt is an unrelated directory — not an ancestor of cwd.
	// This is always a user mistake and must be rejected.
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	writeManifest(t, filepath.Join(tmp, "a"))

	unrelated := filepath.Join(tmp, "unrelated")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatalf("mkdir unrelated: %v", err)
	}

	_, err := workspace.Find(cwd, workspace.Options{StopAt: unrelated})
	if err == nil {
		t.Fatal("expected ErrInvalidOptions for out-of-chain StopAt")
	}
	if !errors.Is(err, workspace.ErrInvalidOptions) {
		t.Errorf("expected ErrInvalidOptions, got %v", err)
	}
}

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv("AIENVS_WORKSPACE_STOP_AT", "/tmp/stop-here")
	opts := workspace.OptionsFromEnv()
	if opts.StopAt != "/tmp/stop-here" {
		t.Errorf("StopAt = %q, want /tmp/stop-here", opts.StopAt)
	}
}

func TestFind_EmptyCwdFallsBackToProcessCwd(t *testing.T) {
	// Empty cwd means "use os.Getwd". Verify by placing a manifest
	// next to the current process working directory for the test —
	// accomplished by chdir'ing into a tempdir with a manifest.
	tmp := t.TempDir()
	_ = writeManifest(t, tmp)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	ws, err := workspace.Find("", workspace.Options{})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	// On macOS, t.TempDir() paths go through /var -> /private/var
	// symlink. Find preserves the user's logical path — resolve both
	// sides before comparison so the test works on darwin.
	got, _ := filepath.EvalSymlinks(ws.Root)
	want, _ := filepath.EvalSymlinks(tmp)
	if got != want {
		t.Errorf("root = %q, want %q", ws.Root, tmp)
	}
}

func TestFind_LogicalPathPreservedThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows; covered by dedicated windows test in a later unit")
	}
	// Layout: real/.aienv.yaml + link -> real. Find cwd via the link.
	// LogicalCwd should show the link path, not the resolved target.
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real")
	linkDir := filepath.Join(tmp, "link")
	_ = writeManifest(t, realDir)
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Walk from linkDir/sub so we actually traverse a parent hop.
	leaf := filepath.Join(linkDir, "sub")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("mkdir leaf: %v", err)
	}

	ws, err := workspace.Find(leaf, workspace.Options{})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.HasPrefix(ws.LogicalCwd, linkDir) {
		t.Errorf("LogicalCwd = %q, want prefix %q (logical path must be preserved)", ws.LogicalCwd, linkDir)
	}
	// Root is the directory holding the manifest on the *logical*
	// path, so it should be linkDir (not realDir).
	if ws.Root != linkDir {
		t.Errorf("Root = %q, want %q (logical path discovery)", ws.Root, linkDir)
	}
}

func TestFind_DanglingSymlinkReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	// Create a .aienv.yaml symlink pointing at a nonexistent target.
	// Find must return ErrManifestNotRegular, not silently walk to an ancestor.
	tmp := t.TempDir()
	manifestPath := filepath.Join(tmp, workspace.ManifestName)
	if err := os.Symlink(filepath.Join(tmp, "nonexistent-target"), manifestPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := workspace.Find(tmp, workspace.Options{})
	if err == nil {
		t.Fatal("expected ErrManifestNotRegular for dangling symlink; got nil")
	}
	if !errors.Is(err, workspace.ErrManifestNotRegular) {
		t.Errorf("expected ErrManifestNotRegular, got %v", err)
	}
}

func TestFind_StopAtFilesystemRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filesystem root semantics differ on Windows")
	}
	// StopAt "/" should be a valid ancestor of any path. The walk should
	// proceed normally and find the manifest somewhere below root.
	tmp := t.TempDir()
	leaf := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("mkdir leaf: %v", err)
	}
	manifest := writeManifest(t, tmp)

	ws, err := workspace.Find(leaf, workspace.Options{StopAt: "/"})
	if err != nil {
		t.Fatalf("Find with StopAt=\"/\" should not reject a valid path: %v", err)
	}
	if ws.ManifestPath != manifest {
		t.Errorf("manifest path = %q, want %q", ws.ManifestPath, manifest)
	}
}

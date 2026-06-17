package hierarchy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/workspace"
)

// writeManifest creates a minimal manifest file at dir/.agent-sync.yaml.
func writeManifest(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	path := filepath.Join(dir, workspace.ManifestName)
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write manifest %q: %v", path, err)
	}
	return path
}

// mkGit creates a .git directory marking dir as a git project root.
func mkGit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git in %q: %v", dir, err)
	}
}

func TestManifestAt(t *testing.T) {
	dir := t.TempDir()
	if _, ok := manifestAt(dir); ok {
		t.Fatal("manifestAt reported a manifest in an empty dir")
	}
	writeManifest(t, dir)
	path, ok := manifestAt(dir)
	if !ok {
		t.Fatal("manifestAt did not find the manifest we wrote")
	}
	if path != filepath.Join(dir, workspace.ManifestName) {
		t.Errorf("manifestAt path = %q, want %q", path, filepath.Join(dir, workspace.ManifestName))
	}
}

func TestManifestAtIgnoresDirectory(t *testing.T) {
	dir := t.TempDir()
	// A directory named like the manifest must not count as a manifest.
	if err := os.MkdirAll(filepath.Join(dir, workspace.ManifestName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok := manifestAt(dir); ok {
		t.Fatal("manifestAt accepted a directory as a manifest")
	}
}

func TestFindProjectRoot(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "work", "repo")
	nested := filepath.Join(repo, "packages", "api")
	mkGit(t, repo)
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	root, ok := findProjectRoot(nested, home, workspace.DefaultMaxHops)
	if !ok {
		t.Fatal("findProjectRoot did not find the .git ancestor")
	}
	if root != repo {
		t.Errorf("project root = %q, want %q", root, repo)
	}
}

func TestFindProjectRootNoGit(t *testing.T) {
	home := t.TempDir()
	plain := filepath.Join(home, "notes", "deep")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok := findProjectRoot(plain, home, workspace.DefaultMaxHops); ok {
		t.Fatal("findProjectRoot found a root where there is no .git")
	}
}

func TestFindProjectRootStopsAtHome(t *testing.T) {
	home := t.TempDir()
	// .git sits at home; we must NOT treat home as a project root, since
	// home is the user level.
	mkGit(t, home)
	sub := filepath.Join(home, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok := findProjectRoot(sub, home, workspace.DefaultMaxHops); ok {
		t.Fatal("findProjectRoot treated home as a project root")
	}
}

func TestCollectEmitScopesProjectAndDirectory(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	pkg := filepath.Join(repo, "packages", "api")
	mkGit(t, repo)
	writeManifest(t, repo) // project level
	writeManifest(t, pkg)  // directory level
	if err := os.MkdirAll(filepath.Join(pkg, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	cwd := filepath.Join(pkg, "src") // cwd below the deepest manifest

	scopes, err := collectEmitScopes(cwd, repo)
	if err != nil {
		t.Fatalf("collectEmitScopes: %v", err)
	}
	if len(scopes) != 2 {
		t.Fatalf("got %d scopes, want 2: %+v", len(scopes), scopes)
	}
	// Shallow→deep: project first, then the directory-level manifest.
	if scopes[0].Root != repo || scopes[0].Level != LevelProject {
		t.Errorf("scope[0] = %+v, want project at %q", scopes[0], repo)
	}
	if scopes[1].Root != pkg || scopes[1].Level != LevelDirectory {
		t.Errorf("scope[1] = %+v, want directory at %q", scopes[1], pkg)
	}
	for i, s := range scopes {
		if !s.Emit {
			t.Errorf("scope[%d] Emit = false, want true", i)
		}
	}
}

func TestCollectEmitScopesSkipsDirsWithoutManifest(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	deep := filepath.Join(repo, "a", "b")
	mkGit(t, repo)
	writeManifest(t, repo) // only the project root has a manifest
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	scopes, err := collectEmitScopes(deep, repo)
	if err != nil {
		t.Fatalf("collectEmitScopes: %v", err)
	}
	if len(scopes) != 1 || scopes[0].Root != repo || scopes[0].Level != LevelProject {
		t.Fatalf("got %+v, want a single project scope at %q", scopes, repo)
	}
}

func TestCollectEmitScopesCwdIsProjectRoot(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mkGit(t, repo)
	writeManifest(t, repo)

	scopes, err := collectEmitScopes(repo, repo)
	if err != nil {
		t.Fatalf("collectEmitScopes: %v", err)
	}
	if len(scopes) != 1 || scopes[0].Level != LevelProject {
		t.Fatalf("got %+v, want single project scope", scopes)
	}
}

func TestUserScopeReadOnlyByDefault(t *testing.T) {
	home := t.TempDir()
	writeManifest(t, home)

	scope, ok := userScope(home, false)
	if !ok {
		t.Fatal("userScope did not find the home manifest")
	}
	if scope.Level != LevelUser {
		t.Errorf("level = %v, want user", scope.Level)
	}
	if scope.Emit {
		t.Error("user scope Emit = true without IncludeUser; want false")
	}
}

func TestUserScopeEmitWhenIncluded(t *testing.T) {
	home := t.TempDir()
	writeManifest(t, home)

	scope, ok := userScope(home, true)
	if !ok {
		t.Fatal("userScope did not find the home manifest")
	}
	if !scope.Emit {
		t.Error("user scope Emit = false with IncludeUser; want true")
	}
}

func TestUserScopeAbsent(t *testing.T) {
	home := t.TempDir() // no manifest written
	if _, ok := userScope(home, true); ok {
		t.Fatal("userScope reported a scope where home has no manifest")
	}
}

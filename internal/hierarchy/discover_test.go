package hierarchy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/workspace"
)

func writeManifestContent(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	path := filepath.Join(dir, workspace.ManifestName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest %q: %v", path, err)
	}
	return path
}

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

func gotLevels(scopes []Scope) []Level {
	levels := make([]Level, 0, len(scopes))
	for _, scope := range scopes {
		levels = append(levels, scope.Level)
	}
	return levels
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

func TestFindProjectRootMaxHopsExhausted(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "a", "b", "c")
	mkGit(t, repo)
	deep := filepath.Join(repo, "d", "e")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// .git is 2 hops above `deep`; a budget of 2 cannot reach it.
	if _, ok := findProjectRoot(deep, home, 2); ok {
		t.Fatal("findProjectRoot found a root outside the hop budget")
	}
	// A generous budget finds it, proving the prior failure was the bound.
	if _, ok := findProjectRoot(deep, home, 64); !ok {
		t.Fatal("findProjectRoot missed the root with an ample budget")
	}
}

func TestDiscoverRespectsMaxHopsOption(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "a", "b")
	mkGit(t, repo)
	writeManifest(t, repo)
	deep := filepath.Join(repo, "c", "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// MaxHops=1 cannot reach the .git 2 hops up, so no project root is
	// found and the no-git fallback applies (cwd has no manifest → empty).
	scopes, err := Discover(deep, Options{Home: home, MaxHops: 1})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(scopes) != 0 {
		t.Fatalf("got %+v, want no scopes when MaxHops is too small", scopes)
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

func TestDiscoverFullHierarchy(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "work", "repo")
	pkg := filepath.Join(repo, "packages", "api")
	mkGit(t, repo)
	writeManifest(t, home) // user
	writeManifest(t, repo) // project
	writeManifest(t, pkg)  // directory

	scopes, err := Discover(pkg, Options{Home: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Order: user (lowest precedence) → project → directory (highest).
	wantRoots := []string{home, repo, pkg}
	wantLevels := []Level{LevelUser, LevelProject, LevelDirectory}
	if len(scopes) != 3 {
		t.Fatalf("got %d scopes, want 3: %+v", len(scopes), scopes)
	}
	for i := range scopes {
		if scopes[i].Root != wantRoots[i] || scopes[i].Level != wantLevels[i] {
			t.Errorf("scope[%d] = {%q, %v}, want {%q, %v}", i, scopes[i].Root, scopes[i].Level, wantRoots[i], wantLevels[i])
		}
	}
	// User read-only by default; emit scopes set.
	if scopes[0].Emit {
		t.Error("user scope Emit = true without IncludeUser")
	}
	if !scopes[1].Emit || !scopes[2].Emit {
		t.Error("project/directory scopes must have Emit = true")
	}
}

func TestDiscover_ActivationRootStopsUserScope(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(home, "workspace")
	repo := filepath.Join(workspaceRoot, "repo")
	mkGit(t, repo)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	writeManifest(t, home) // user scope
	writeManifestContent(t, workspaceRoot, "version: 1\nscope: workspace\nactivation_root: true\n")
	writeManifest(t, repo) // project scope

	scopes, err := Discover(repo, Options{Home: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	wantRoots := []string{workspaceRoot, repo}
	wantLevels := []Level{LevelWorkspace, LevelProject}
	if len(scopes) != len(wantRoots) {
		t.Fatalf("got %d scopes, want %d: %+v", len(scopes), len(wantRoots), scopes)
	}
	for i := range scopes {
		if scopes[i].Root != wantRoots[i] {
			t.Errorf("scope[%d].Root = %q, want %q", i, scopes[i].Root, wantRoots[i])
		}
		if scopes[i].Level != wantLevels[i] {
			t.Errorf("scope[%d].Level = %v, want %v", i, scopes[i].Level, wantLevels[i])
		}
	}
	if got := gotLevels(scopes); len(got) != len(wantLevels) {
		t.Fatalf("unexpected levels count: %v", got)
	}
}

func TestDiscover_OutsideActivationRootIncludesUserScope(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mkGit(t, repo)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	writeManifest(t, home) // user scope
	writeManifest(t, repo) // project scope

	scopes, err := Discover(repo, Options{Home: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	wantRoots := []string{home, repo}
	wantLevels := []Level{LevelUser, LevelProject}
	if len(scopes) != len(wantRoots) {
		t.Fatalf("got %d scopes, want %d: %+v", len(scopes), len(wantRoots), scopes)
	}
	for i := range scopes {
		if scopes[i].Root != wantRoots[i] {
			t.Errorf("scope[%d].Root = %q, want %q", i, scopes[i].Root, wantRoots[i])
		}
		if scopes[i].Level != wantLevels[i] {
			t.Errorf("scope[%d].Level = %v, want %v", i, scopes[i].Level, wantLevels[i])
		}
	}
}

func TestDiscover_MalformedManifestSkipsActivationButDoesNotFail(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mid := filepath.Join(repo, "packages", "api")
	cwd := filepath.Join(mid, "src")
	mkGit(t, repo)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	writeManifest(t, home)                           // user scope
	writeManifest(t, repo)                           // project scope
	writeManifestContent(t, mid, ":\n  not: [valid") // malformed directory manifest

	scopes, err := Discover(cwd, Options{Home: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Discovery must stay presence-based: user and both project / directory manifests
	// are returned even though the directory manifest is malformed.
	wantRoots := []string{home, repo, mid}
	wantLevels := []Level{LevelUser, LevelProject, LevelDirectory}
	if len(scopes) != len(wantRoots) {
		t.Fatalf("got %d scopes, want %d: %+v", len(scopes), len(wantRoots), scopes)
	}
	for i := range scopes {
		if scopes[i].Root != wantRoots[i] || scopes[i].Level != wantLevels[i] {
			t.Errorf("scope[%d] = {%q, %v}, want {%q, %v}", i, scopes[i].Root, scopes[i].Level, wantRoots[i], wantLevels[i])
		}
	}
}

func TestDiscover_MalformedWorkspaceActivationManifestFailsClosed(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(home, "workspace")
	repo := filepath.Join(workspaceRoot, "repo")
	cwd := filepath.Join(repo, "packages", "api")
	mkGit(t, repo)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	writeManifest(t, home) // user scope
	manifest := writeManifestContent(t, workspaceRoot, "version: 1\nscope: workspace\nactivation_root: true\n:\n  not: [valid")
	writeManifest(t, repo) // project scope

	scopes, err := Discover(cwd, Options{Home: home})
	if err == nil {
		t.Fatalf("expected malformed activation-root manifest to fail discovery, got scopes=%+v", scopes)
	}
	if len(scopes) != 0 {
		t.Fatalf("expected no scopes on fail-closed activation-root parse error, got %+v", scopes)
	}
	if !strings.Contains(err.Error(), manifest) {
		t.Fatalf("error = %q, want include manifest path %q", err, manifest)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "activation-root") || !strings.Contains(strings.ToLower(err.Error()), "malformed") {
		t.Fatalf("error = %q, want activation-root malformed wording", err)
	}
}

func TestDiscover_CwdAtWorkspaceActivationRoot(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(home, "workspace")
	writeManifest(t, home) // user scope
	writeManifestContent(t, workspaceRoot, "version: 1\nscope: workspace\nactivation_root: true\n")

	scopes, err := Discover(workspaceRoot, Options{Home: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(scopes) != 1 {
		t.Fatalf("got %d scopes, want 1: %+v", len(scopes), scopes)
	}
	if scopes[0].Root != workspaceRoot {
		t.Errorf("scope.Root = %q, want %q", scopes[0].Root, workspaceRoot)
	}
	if scopes[0].Level != LevelWorkspace {
		t.Errorf("scope.Level = %v, want %v", scopes[0].Level, LevelWorkspace)
	}
	if scopes[0].ManifestPath == "" {
		t.Fatal("workspace scope missing manifest path")
	}
}

func TestDiscover_NestedActivationRootsFailClosed(t *testing.T) {
	home := t.TempDir()
	outer := filepath.Join(home, "workspace")
	inner := filepath.Join(outer, "team")
	repo := filepath.Join(inner, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	outerManifest := writeManifestContent(t, outer, "version: 1\nscope: workspace\nactivation_root: true\n")
	innerManifest := writeManifestContent(t, inner, "version: 1\nscope: workspace\nactivation_root: true\n")

	_, err := Discover(repo, Options{Home: home})
	if err == nil {
		t.Fatal("expected discover to fail with nested activation roots")
	}
	if !strings.Contains(err.Error(), "nested activation roots") {
		t.Fatalf("error = %v, want nested activation roots", err)
	}
	if !strings.Contains(err.Error(), outerManifest) || !strings.Contains(err.Error(), innerManifest) {
		t.Fatalf("error = %q, want include %q and %q", err, outerManifest, innerManifest)
	}
}

// TestDiscoverUserScopeCarriesManifestPathForComposition guards the exact
// contract hierarchy composition relies on (plan docs/plans/2026-07-01-002, U3):
// when a project sync runs WITHOUT --user, Discover must still surface the
// user scope carrying a non-empty ManifestPath and Emit=false, so the compose
// step can materialize the user IR read-only without emitting the user scope.
// A regression that dropped the user scope, or emptied its ManifestPath, would
// silently disable composition rather than fail loudly.
func TestDiscoverUserScopeCarriesManifestPathForComposition(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "work", "repo")
	mkGit(t, repo)
	userManifest := writeManifest(t, home) // user
	writeManifest(t, repo)                 // project

	scopes, err := Discover(repo, Options{Home: home, IncludeUser: false})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	var user *Scope
	for i := range scopes {
		if scopes[i].Level == LevelUser {
			user = &scopes[i]
			break
		}
	}
	if user == nil {
		t.Fatalf("no LevelUser scope in Discover output: %+v", scopes)
	}
	if user.Emit {
		t.Error("user scope Emit = true without IncludeUser; want false")
	}
	if user.ManifestPath != userManifest {
		t.Errorf("user scope ManifestPath = %q, want %q", user.ManifestPath, userManifest)
	}
	if user.Root != home {
		t.Errorf("user scope Root = %q, want %q", user.Root, home)
	}
}

func TestDiscoverIncludeUser(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mkGit(t, repo)
	writeManifest(t, home)
	writeManifest(t, repo)

	scopes, err := Discover(repo, Options{Home: home, IncludeUser: true})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(scopes) != 2 || scopes[0].Level != LevelUser || !scopes[0].Emit {
		t.Fatalf("got %+v, want user(emit)+project", scopes)
	}
}

func TestDiscoverNoGitFallbackIsCwdOnly(t *testing.T) {
	home := t.TempDir()
	notes := filepath.Join(home, "notes")
	deep := filepath.Join(notes, "deep")
	writeManifest(t, notes) // would-be directory level, but no git → ignored
	writeManifest(t, deep)  // cwd's own manifest

	scopes, err := Discover(deep, Options{Home: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// No .git anywhere → only cwd's own manifest, classified project.
	if len(scopes) != 1 || scopes[0].Root != deep || scopes[0].Level != LevelProject {
		t.Fatalf("got %+v, want single project scope at cwd %q", scopes, deep)
	}
}

func TestDiscoverCwdIsHomeNoDuplicate(t *testing.T) {
	home := t.TempDir()
	writeManifest(t, home) // the only manifest; cwd == home, no .git

	scopes, err := Discover(home, Options{Home: home, IncludeUser: true})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Must be exactly one scope (the user scope), not duplicated as a
	// project fallback.
	if len(scopes) != 1 || scopes[0].Level != LevelUser || scopes[0].Root != home {
		t.Fatalf("got %+v, want single user scope at home", scopes)
	}
}

func TestDiscoverRelativeHomeResolvesToAbsolute(t *testing.T) {
	// A caller may supply a relative Options.Home. Discover must resolve it
	// to absolute before comparing against the absolute cwd chain in
	// findProjectRoot; otherwise the home boundary never matches and the
	// user scope / root detection misbehaves.
	base := t.TempDir()
	home := filepath.Join(base, "home")
	repo := filepath.Join(home, "repo")
	mkGit(t, repo)
	writeManifest(t, home) // user
	writeManifest(t, repo) // project

	// Run from base so that the relative "home" resolves to the absolute
	// home dir above.
	t.Chdir(base)

	absScopes, err := Discover(repo, Options{Home: home, IncludeUser: true})
	if err != nil {
		t.Fatalf("Discover (absolute home): %v", err)
	}
	relScopes, err := Discover(repo, Options{Home: "home", IncludeUser: true})
	if err != nil {
		t.Fatalf("Discover (relative home): %v", err)
	}

	if len(relScopes) != len(absScopes) {
		t.Fatalf("relative home gave %d scopes, absolute gave %d: rel=%+v abs=%+v",
			len(relScopes), len(absScopes), relScopes, absScopes)
	}
	for i := range absScopes {
		if relScopes[i].Root != absScopes[i].Root ||
			relScopes[i].Level != absScopes[i].Level ||
			relScopes[i].Emit != absScopes[i].Emit {
			t.Errorf("scope[%d]: relative home = %+v, want %+v", i, relScopes[i], absScopes[i])
		}
	}
	// Sanity: the user scope must be detected and rooted at the absolute home.
	if len(relScopes) == 0 || relScopes[0].Level != LevelUser || relScopes[0].Root != home {
		t.Fatalf("got %+v, want a user scope at absolute home %q", relScopes, home)
	}
}

func TestDiscoverNoManifestsIsEmpty(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mkGit(t, repo) // git but no manifests anywhere

	scopes, err := Discover(repo, Options{Home: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(scopes) != 0 {
		t.Fatalf("got %+v, want no scopes", scopes)
	}
}

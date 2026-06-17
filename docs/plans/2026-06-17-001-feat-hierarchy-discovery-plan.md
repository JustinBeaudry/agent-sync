# Hierarchy Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a pure `internal/hierarchy` package that, from a starting directory, discovers every agent-sync manifest in the scope hierarchy (user / directory / project), classifies each by level, and returns them ordered shallow→deep (lowest→highest precedence).

**Architecture:** A new leaf package with no dependencies on the engine, fsroot, or adapters. It walks the logical parent chain from cwd, finds the project root (nearest `.git` ancestor), collects manifests on the path from cwd up to that root, and adds the user-home manifest as a read-only scope unless emit is requested. This is the foundation Plan 2 (multi-scope sync orchestration) consumes; it produces no user-visible behavior on its own but is independently testable.

**Tech Stack:** Go 1.24+, standard library only (`os`, `path/filepath`), `testify` for tests, reuses `internal/workspace` constants (`ManifestName`, `DefaultMaxHops`).

This is Plan 1 of 3 for the hierarchy-aware-manifests design (`docs/brainstorms/2026-06-17-hierarchy-aware-manifests-design.md`). It implements the **Discovery** component only.

---

## File Structure

- Create: `internal/hierarchy/hierarchy.go` — `Level`, `Scope`, `Options` types and `Level.String()`.
- Create: `internal/hierarchy/discover.go` — `Discover` plus unexported helpers (`findProjectRoot`, `collectEmitScopes`, `userScope`, `manifestAt`, `hasGit`).
- Create: `internal/hierarchy/hierarchy_test.go` — `Level.String()` unit test.
- Create: `internal/hierarchy/discover_test.go` — all discovery behavior tests (table-driven over temp dir trees).

Discovery is split from the type declarations so each file has one responsibility: `hierarchy.go` is the public vocabulary, `discover.go` is the algorithm. They change together but read better apart.

---

## Task 1: Package types

**Files:**
- Create: `internal/hierarchy/hierarchy.go`
- Create: `internal/hierarchy/hierarchy_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/hierarchy/hierarchy_test.go`:

```go
package hierarchy

import "testing"

func TestLevelString(t *testing.T) {
	cases := []struct {
		level Level
		want  string
	}{
		{LevelUser, "user"},
		{LevelProject, "project"},
		{LevelDirectory, "directory"},
		{Level(99), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.level.String(); got != tc.want {
				t.Errorf("Level(%d).String() = %q, want %q", tc.level, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hierarchy/ -run TestLevelString -v`
Expected: FAIL — `undefined: Level` (package does not compile).

- [ ] **Step 3: Write minimal implementation**

Create `internal/hierarchy/hierarchy.go`:

```go
// Package hierarchy discovers the agent-sync manifests that apply at a
// given location, ordered from broadest (lowest precedence) to most
// specific (highest precedence).
//
// Unlike internal/workspace (which resolves the single nearest manifest),
// this package collects every manifest in the scope chain: the user-home
// manifest, the project manifest (at the nearest .git ancestor), and any
// intermediate directory manifests between the project root and cwd.
//
// It is a pure leaf package: it walks the filesystem and returns data. It
// performs no writes, opens no fsroot, and knows nothing about adapters or
// the engine. The multi-scope sync orchestrator consumes its output.
package hierarchy

// Level classifies a scope by where its manifest sits in the hierarchy.
type Level int

const (
	// LevelUser is the manifest at the user's home directory (~). It is
	// the broadest scope and has the lowest precedence.
	LevelUser Level = iota
	// LevelProject is the manifest at the project root (the nearest
	// ancestor of cwd containing a .git entry).
	LevelProject
	// LevelDirectory is a manifest in a directory strictly between the
	// project root and cwd. Deeper directories have higher precedence.
	LevelDirectory
)

// String returns the lowercase label used in status output and warnings.
func (l Level) String() string {
	switch l {
	case LevelUser:
		return "user"
	case LevelProject:
		return "project"
	case LevelDirectory:
		return "directory"
	default:
		return "unknown"
	}
}

// Scope is one discovered manifest and the directory it anchors.
type Scope struct {
	// Root is the absolute directory containing the manifest. It is the
	// boundary an fsroot is later opened against for this scope.
	Root string
	// ManifestPath is the absolute path to the scope's .agent-sync.yaml.
	ManifestPath string
	// Level classifies the scope (user / project / directory).
	Level Level
	// Emit is true when this scope should be synced in the current run.
	// Project and directory scopes are always Emit=true; the user scope is
	// Emit=true only when Options.IncludeUser is set (the --user flag).
	Emit bool
}

// Options controls discovery. All fields are optional.
type Options struct {
	// Home overrides the user home directory. Empty means os.UserHomeDir.
	// Injectable so tests do not depend on the real home directory.
	Home string
	// IncludeUser marks the user-home scope Emit=true. It corresponds to
	// the `sync --user` flag. When false the user scope is still returned
	// (for status/precedence display) but with Emit=false.
	IncludeUser bool
	// MaxHops caps the upward walk. Zero means workspace.DefaultMaxHops.
	MaxHops int
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hierarchy/ -run TestLevelString -v`
Expected: PASS (4 subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/hierarchy/hierarchy.go internal/hierarchy/hierarchy_test.go
git commit -m "feat(hierarchy): add Level/Scope/Options types"
```

---

## Task 2: Filesystem probes and project-root detection

**Files:**
- Create: `internal/hierarchy/discover.go`
- Create: `internal/hierarchy/discover_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/hierarchy/discover_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hierarchy/ -run 'TestManifestAt|TestFindProjectRoot' -v`
Expected: FAIL — `undefined: manifestAt`, `undefined: findProjectRoot`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/hierarchy/discover.go`:

```go
package hierarchy

import (
	"os"
	"path/filepath"

	"github.com/agent-sync/agent-sync/internal/workspace"
)

// manifestAt reports the manifest path in dir, if a regular .agent-sync.yaml
// file exists there. A directory or other non-regular entry with the
// manifest name does not count.
func manifestAt(dir string) (string, bool) {
	path := filepath.Join(dir, workspace.ManifestName)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

// hasGit reports whether dir contains a .git entry (directory for a normal
// clone, file for a worktree/submodule). Existence is all we need.
func hasGit(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// findProjectRoot returns the nearest ancestor of cwd (inclusive) that
// contains a .git entry, walking up the logical parent chain. The search
// stops at home: home is the user level and is never a project root, so a
// repo whose .git sits at home yields ok=false. ok is also false when the
// filesystem root or the hop budget is reached without a match.
func findProjectRoot(cwd, home string, maxHops int) (string, bool) {
	dir := cwd
	for hops := 0; hops < maxHops; hops++ {
		if dir == home {
			return "", false
		}
		if hasGit(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
	return "", false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hierarchy/ -run 'TestManifestAt|TestFindProjectRoot' -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/hierarchy/discover.go internal/hierarchy/discover_test.go
git commit -m "feat(hierarchy): add manifest/git probes and project-root detection"
```

---

## Task 3: Collect emit scopes (project + directory), ordered shallow→deep

**Files:**
- Modify: `internal/hierarchy/discover.go`
- Modify: `internal/hierarchy/discover_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/hierarchy/discover_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hierarchy/ -run TestCollectEmitScopes -v`
Expected: FAIL — `undefined: collectEmitScopes`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/hierarchy/discover.go`:

```go
// collectEmitScopes walks from cwd up to projectRoot (inclusive) and
// returns a Scope for each directory that holds a manifest. The manifest at
// projectRoot is LevelProject; every other one is LevelDirectory. Results
// are ordered shallow→deep (projectRoot first, cwd last), matching
// ascending precedence. All emit scopes have Emit=true.
//
// projectRoot must be cwd or an ancestor of it; the walk is bounded by
// reaching projectRoot or the filesystem root.
func collectEmitScopes(cwd, projectRoot string) ([]Scope, error) {
	var found []Scope
	dir := cwd
	for {
		if path, ok := manifestAt(dir); ok {
			level := LevelDirectory
			if dir == projectRoot {
				level = LevelProject
			}
			found = append(found, Scope{
				Root:         dir,
				ManifestPath: path,
				Level:        level,
				Emit:         true,
			})
		}
		if dir == projectRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// found is deep→shallow; reverse to shallow→deep.
	for i, j := 0, len(found)-1; i < j; i, j = i+1, j-1 {
		found[i], found[j] = found[j], found[i]
	}
	return found, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hierarchy/ -run TestCollectEmitScopes -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/hierarchy/discover.go internal/hierarchy/discover_test.go
git commit -m "feat(hierarchy): collect ordered project+directory emit scopes"
```

---

## Task 4: User-home scope

**Files:**
- Modify: `internal/hierarchy/discover.go`
- Modify: `internal/hierarchy/discover_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/hierarchy/discover_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hierarchy/ -run TestUserScope -v`
Expected: FAIL — `undefined: userScope`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/hierarchy/discover.go`:

```go
// userScope returns the user-home scope when home holds a manifest. Emit is
// set only when includeUser is true (the --user flag); otherwise the scope
// is returned read-only so callers can still display it in the precedence
// view without writing to the home directory.
func userScope(home string, includeUser bool) (Scope, bool) {
	path, ok := manifestAt(home)
	if !ok {
		return Scope{}, false
	}
	return Scope{
		Root:         home,
		ManifestPath: path,
		Level:        LevelUser,
		Emit:         includeUser,
	}, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hierarchy/ -run TestUserScope -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/hierarchy/discover.go internal/hierarchy/discover_test.go
git commit -m "feat(hierarchy): add user-home scope detection"
```

---

## Task 5: Discover — compose scopes, ordering, and edge cases

**Files:**
- Modify: `internal/hierarchy/discover.go`
- Modify: `internal/hierarchy/discover_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/hierarchy/discover_test.go`:

```go
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
	parent := filepath.Join(home, "notes") // parent of cwd; has a manifest but no .git anywhere
	deep := filepath.Join(notes, "deep")
	writeManifest(t, parent)   // would-be directory level, but no git → ignored
	writeManifest(t, deep)     // cwd's own manifest
	_ = parent

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hierarchy/ -run TestDiscover -v`
Expected: FAIL — `undefined: Discover`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/hierarchy/discover.go`. Add `"fmt"` to the import block (final block becomes `"fmt"`, `"os"`, `"path/filepath"`, and the workspace import):

```go
// Discover returns every manifest that applies at cwd, ordered from
// broadest (lowest precedence) to most specific (highest precedence):
// the user-home scope first (if present), then the project scope, then any
// directory scopes down to cwd.
//
// Emit scopes are the manifests from cwd up to the project root (the nearest
// .git ancestor). When there is no .git ancestor, only cwd's own manifest is
// an emit scope, classified as project. The user-home scope is included for
// visibility but is Emit=true only when Options.IncludeUser is set.
func Discover(cwd string, opts Options) ([]Scope, error) {
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("hierarchy: resolve cwd %q: %w", cwd, err)
	}
	absCwd = filepath.Clean(absCwd)

	home := opts.Home
	if home == "" {
		h, herr := os.UserHomeDir()
		if herr != nil {
			return nil, fmt.Errorf("hierarchy: resolve home: %w", herr)
		}
		home = h
	}
	home = filepath.Clean(home)

	maxHops := opts.MaxHops
	if maxHops <= 0 {
		maxHops = workspace.DefaultMaxHops
	}

	var emit []Scope
	if root, ok := findProjectRoot(absCwd, home, maxHops); ok {
		emit, err = collectEmitScopes(absCwd, root)
		if err != nil {
			return nil, err
		}
	} else if absCwd != home {
		// No .git ancestor: only cwd's own manifest applies, classified as
		// project. Skipped when cwd is home, where the manifest is the user
		// scope (added below) and must not be double-counted.
		if path, has := manifestAt(absCwd); has {
			emit = []Scope{{Root: absCwd, ManifestPath: path, Level: LevelProject, Emit: true}}
		}
	}

	var out []Scope
	if us, has := userScope(home, opts.IncludeUser); has {
		out = append(out, us)
	}
	out = append(out, emit...)
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hierarchy/ -run TestDiscover -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/hierarchy/discover.go internal/hierarchy/discover_test.go
git commit -m "feat(hierarchy): add Discover composing user/project/directory scopes"
```

---

## Task 6: Full-package verification gate

**Files:** none (verification only).

- [ ] **Step 1: Run the package test suite with the race detector**

Run: `go test -race -cover ./internal/hierarchy/...`
Expected: PASS, all tests, coverage reported (target ≥ 80%; this pure package should be well above it).

- [ ] **Step 2: Run static analysis**

Run: `go vet ./internal/hierarchy/... && golangci-lint run ./internal/hierarchy/...`
Expected: no findings.

- [ ] **Step 3: Confirm the broader build still compiles**

Run: `go build ./...`
Expected: success (the new package is a leaf; nothing else imports it yet).

- [ ] **Step 4: Commit any lint fixes**

If steps 1–3 surfaced fixes, commit them:

```bash
git add internal/hierarchy/
git commit -m "test(hierarchy): satisfy race detector and linters"
```

If there were no fixes, skip this commit.

---

## Self-Review Notes

- **Spec coverage:** This plan implements the **Discovery** component (design §Components 1) — walk-up from cwd, project root = nearest `.git` ancestor, levels (user/project/directory), read-up-to-`~`, ordered shallow→deep, `--user` via `IncludeUser`, and the no-git fallback (design §Components, §Key Decisions). It does **not** implement compile/emit (engine, already exists), the coverage analyzer, adapter `NativeAt`, the multi-root emit loop, or `status`/`--user` wiring — those are Plans 2 and 3.
- **Out of scope here on purpose:** `Discover` returns scopes but never opens an fsroot, loads a manifest, or emits. Consuming the scopes (per-scope `engine.Sync`, continue-and-report, per-scope ledgers) is Plan 2.
- **Type consistency:** `Level`/`Scope`/`Options` names and fields are stable across all tasks; `findProjectRoot`, `collectEmitScopes`, `userScope`, `manifestAt`, `hasGit` signatures match every call site in the tests.
- **Known limitation carried from design:** orphaned scopes (a manifest deleted in a directory you never revisit) are not this package's concern — discovery only reports what exists now.
```

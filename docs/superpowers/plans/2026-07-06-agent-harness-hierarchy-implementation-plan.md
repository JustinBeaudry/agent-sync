# Agent Harness Hierarchy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first canonical agent harness hierarchy: user, workspace, and project scopes; activation roots that stop user fallback; managed native fragments; and a narrow Codex-native implementation for current feature flags and lifecycle hooks.

**Architecture:** Keep portable assets in the existing IR/adapters path, and add a core-owned harness layer for target-native fragments. The CLI discovers active scopes, materializes ancestor scopes read-only, resolves portable assets and native fragments broadest-to-closest, and lets the engine write only the selected invocation root through `internal/fsroot`. Codex native config is applied by allowlisted core surfaces, not by widening adapter writes to arbitrary user-owned keys.

**Tech Stack:** Go 1.25+, `github.com/goccy/go-yaml`, `github.com/pelletier/go-toml/v2`, `encoding/json`, existing `internal/fsroot`, `internal/ledger`, `internal/merge`, bundled adapters, and Cobra CLI.

---

## Scope

This plan implements the first vertical slice of the design in `docs/superpowers/specs/2026-07-06-agent-harness-hierarchy-design.md`.

It includes:

- `init --user` and `init --workspace <dir> [--activation-root]`.
- Manifest `scope: workspace` and `activation_root: true`.
- Discovery semantics where the nearest activation root stops user fallback.
- Rejection of nested activation roots.
- A native fragment authoring format under `.agents/configs/<target>/<group>/<id>/`.
- Broadest-to-closest resolution for workspace-authored portable assets and native fragments.
- Codex native fragments for `.codex/config.toml` feature keys and generated `.codex/hooks.json`.
- Validate/sync parity for native changes.

It intentionally does not implement target-specific native config surfaces for Claude, Cursor, Antigravity, or Pi beyond decoding, resolving, explaining, and reporting unsupported fragments. Those tools need separate allowlists once their native file semantics are documented.

## Current Codex Docs Constraint

The current Codex docs say:

- Feature flags live under `[features]` in `config.toml`; `hooks` defaults to `true` and is Stable.
- Lifecycle hooks are loaded from `hooks.json` or inline `[hooks]` next to active config layers.
- Project-local `.codex/` config and hooks load only when the project layer is trusted.
- If one layer contains both `hooks.json` and inline `[hooks]`, Codex loads both and warns, so agent-sync should prefer one representation per layer.

Implementation consequence:

- Use a narrow TOML key merge for `features.<key>`.
- Generate a whole `.codex/hooks.json` file from agent-sync-owned hook fragments, but refuse to overwrite an unmanaged existing `.codex/hooks.json`.
- Do not bypass Codex hook trust. Agent-sync writes configuration; Codex remains responsible for trusting project-local command hooks.

## File Structure

Create:

- `internal/harness/types.go` - native fragment types, enums, identity, defaults.
- `internal/harness/decode.go` - decode `.agents/configs/**/fragment.yaml` and payload files from any `ir.SourceTree`.
- `internal/harness/decode_test.go` - fragment decoding tests.
- `internal/harness/resolve.go` - broadest-to-closest resolution for portable nodes, skill assets, and native fragments.
- `internal/harness/resolve_test.go` - precedence and inheritance tests.
- `internal/harness/codex.go` - Codex native surface allowlist and operation builder.
- `internal/harness/codex_test.go` - Codex feature/hook operation tests.
- `internal/merge/native.go` - core-owned native merge entry types and apply/dry-run entry points.
- `internal/merge/native_toml.go` - TOML key merge for allowlisted native keys.
- `internal/merge/native_json.go` - JSON whole-file generation guard for generated native files.
- `internal/merge/native_test.go` - native merge tests.

Modify:

- `internal/manifest/schema.go` - add `ActivationRoot` and scope constants.
- `internal/manifest/load.go` - validate workspace scope and activation roots.
- `internal/manifest/load_test.go` - manifest validation coverage.
- `internal/tui/wizard/initconfig.go` - render scope and activation root from init config.
- `internal/tui/wizard/wizard_test.go` - init manifest rendering coverage.
- `internal/cli/cmd_init.go` - add `init --user`, infer workspace scope from inherited `--workspace`, add `--activation-root`.
- `internal/cli/cmd_init_test.go` - init command coverage.
- `internal/hierarchy/hierarchy.go` - add `LevelWorkspace`.
- `internal/hierarchy/discover.go` - activation-root discovery and stop behavior.
- `internal/hierarchy/discover_test.go` - hierarchy behavior coverage.
- `internal/coverage/coverage.go` and tests - treat workspace as a root-level scope.
- `internal/cli/materialize.go` - decode native fragments alongside IR.
- `internal/cli/setup.go` - pass manifest scope to `engine.Request`, include fragments.
- `internal/cli/hierarchy_sync.go` - materialize read-only ancestors for inherited fragments and stop user reads inside activation roots.
- `internal/engine/request.go` - add native fragment operations to the request.
- `internal/engine/target.go` - apply native operations under target locks and ledger entries.
- `internal/engine/plan.go` - dry-run native operations with the same merge code.
- `internal/engine/*test.go` - sync/validate parity and ledger tests.
- `docs/adapters/codex.md` - document managed Codex feature/hook fragments.
- `docs/superpowers/specs/2026-07-06-agent-harness-hierarchy-design.md` - update if implementation decisions diverge.

---

## Task 1: Manifest Scopes And Init Surface

**Files:**

- Modify: `internal/manifest/schema.go`
- Modify: `internal/manifest/load.go`
- Modify: `internal/manifest/load_test.go`
- Modify: `internal/tui/wizard/initconfig.go`
- Modify: `internal/tui/wizard/wizard_test.go`
- Modify: `internal/cli/cmd_init.go`
- Modify: `internal/cli/cmd_init_test.go`

- [ ] **Step 1: Write failing manifest validation tests**

Add these tests to `internal/manifest/load_test.go`:

```go
func TestLoadBytes_AcceptsWorkspaceActivationRoot(t *testing.T) {
	src := []byte("version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\n")
	m, err := LoadBytes(src, LoadOptions{})
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if m.Scope != ScopeWorkspace {
		t.Fatalf("Scope = %q, want %q", m.Scope, ScopeWorkspace)
	}
	if !m.ActivationRoot {
		t.Fatal("ActivationRoot = false, want true")
	}
}

func TestLoadBytes_RejectsActivationRootOutsideWorkspace(t *testing.T) {
	for _, scope := range []string{"", "user", "project", "global"} {
		src := []byte("version: 1\nscope: " + scope + "\nactivation_root: true\ncanonical:\n  local_dir: .agents\n")
		if scope == "" {
			src = []byte("version: 1\nactivation_root: true\ncanonical:\n  local_dir: .agents\n")
		}
		_, err := LoadBytes(src, LoadOptions{})
		if err == nil || !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("scope %q: err = %v, want ErrInvalidManifest", scope, err)
		}
	}
}

func TestLoadBytes_AcceptsWorkspaceScopeWithoutActivationRoot(t *testing.T) {
	src := []byte("version: 1\nscope: workspace\ncanonical:\n  local_dir: .agents\n")
	m, err := LoadBytes(src, LoadOptions{})
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if m.ActivationRoot {
		t.Fatal("ActivationRoot = true, want false")
	}
}
```

Run:

```bash
go test ./internal/manifest -run 'TestLoadBytes_(AcceptsWorkspaceActivationRoot|RejectsActivationRootOutsideWorkspace|AcceptsWorkspaceScopeWithoutActivationRoot)' -count=1
```

Expected: FAIL because `activation_root` is an unknown field and `workspace` is not accepted.

- [ ] **Step 2: Implement manifest fields and validation**

Update `internal/manifest/schema.go`:

```go
const (
	ScopeUser      = "user"
	ScopeProject   = "project"
	ScopeWorkspace = "workspace"
	ScopeGlobal    = "global"
)

type Manifest struct {
	Version int `yaml:"version"`

	Canonical CanonicalSource `yaml:"canonical"`

	Targets []string `yaml:"targets,omitempty"`

	// Scope declares where rendered config is intended to apply for target tools.
	// v1 accepts: user, workspace, project, global.
	Scope string `yaml:"scope,omitempty"`

	// ActivationRoot makes a workspace scope a hard hierarchy boundary. When cwd
	// is inside this tree, discovery stops here and does not consider the user
	// home scope.
	ActivationRoot bool `yaml:"activation_root,omitempty"`

	Cache CacheConfig `yaml:"cache,omitempty"`
	Adapters []AdapterDecl `yaml:"adapters,omitempty"`
	TrustedSHA string `yaml:"trusted_sha,omitempty"`
	Trust TrustConfig `yaml:"trust,omitempty"`
	Compose ComposeConfig `yaml:"compose,omitempty"`
}
```

Update the scope validation block in `internal/manifest/load.go`:

```go
switch m.Scope {
case "", ScopeUser, ScopeProject, ScopeWorkspace, ScopeGlobal:
default:
	return fmt.Errorf("%w: scope must be one of user|workspace|project|global (got %q)", ErrInvalidManifest, m.Scope)
}
if m.ActivationRoot && m.Scope != ScopeWorkspace {
	return fmt.Errorf("%w: activation_root requires scope: workspace", ErrInvalidManifest)
}
```

- [ ] **Step 3: Verify manifest tests**

Run:

```bash
go test ./internal/manifest -count=1
```

Expected: PASS.

- [ ] **Step 4: Write failing init config rendering tests**

Add to `internal/tui/wizard/wizard_test.go`:

```go
func TestInitConfig_ManifestYAML_RendersScopeAndActivationRoot(t *testing.T) {
	out, err := InitConfig{
		LocalDir:       ".agents",
		Scope:          "workspace",
		ActivationRoot: true,
		Targets:        []string{"codex"},
	}.ManifestYAML()
	if err != nil {
		t.Fatalf("ManifestYAML: %v", err)
	}
	text := string(out)
	for _, want := range []string{"scope: workspace\n", "activation_root: true\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest missing %q:\n%s", want, text)
		}
	}
}
```

Run:

```bash
go test ./internal/tui/wizard -run TestInitConfig_ManifestYAML_RendersScopeAndActivationRoot -count=1
```

Expected: FAIL because `InitConfig` has no scope fields.

- [ ] **Step 5: Add scope fields to init config**

Update `internal/tui/wizard/initconfig.go`:

```go
type InitConfig struct {
	Dir            string
	SourceURL      string
	LocalPath      string
	LocalDir       string
	Ref            string
	Commit         string
	Floating       bool
	Targets        []string
	Scope          string
	ActivationRoot bool
}
```

In `Validate`, add:

```go
switch c.Scope {
case "", manifest.ScopeUser, manifest.ScopeProject, manifest.ScopeWorkspace, manifest.ScopeGlobal:
default:
	return fmt.Errorf("scope must be one of user|workspace|project|global (got %q)", c.Scope)
}
if c.ActivationRoot && c.Scope != manifest.ScopeWorkspace {
	return fmt.Errorf("activation_root requires scope workspace")
}
```

In `ManifestYAML`, render these lines after `version: 1`:

```go
if c.Scope != "" {
	fmt.Fprintf(&b, "scope: %s\n", c.Scope)
}
if c.ActivationRoot {
	b.WriteString("activation_root: true\n")
}
```

- [ ] **Step 6: Write failing init command tests**

Add to `internal/cli/cmd_init_test.go`:

```go
func TestInitUserWritesManifestAtHome(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	out, errOut, err := runInitInDir(t, repo, home, "init", "--user", "--non-interactive", "--target", "codex")
	if err != nil {
		t.Fatalf("init --user: %v\nstderr: %s\nstdout: %s", err, errOut, out)
	}
	data, err := os.ReadFile(filepath.Join(home, ".agent-sync.yaml"))
	if err != nil {
		t.Fatalf("read user manifest: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "scope: user\n") {
		t.Fatalf("manifest missing user scope:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); err != nil {
		t.Fatalf("user .agents source dir missing: %v", err)
	}
}

func TestInitWorkspaceActivationRootWritesWorkspaceManifest(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	out, errOut, err := runInitInDir(t, home, home, "init", "--workspace", ws, "--activation-root", "--non-interactive", "--target", "codex")
	if err != nil {
		t.Fatalf("init --workspace --activation-root: %v\nstderr: %s\nstdout: %s", err, errOut, out)
	}
	data, err := os.ReadFile(filepath.Join(ws, ".agent-sync.yaml"))
	if err != nil {
		t.Fatalf("read workspace manifest: %v", err)
	}
	text := string(data)
	for _, want := range []string{"scope: workspace\n", "activation_root: true\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest missing %q:\n%s", want, text)
		}
	}
}
```

Run:

```bash
go test ./internal/cli -run 'TestInit(UserWritesManifestAtHome|WorkspaceActivationRootWritesWorkspaceManifest)' -count=1
```

Expected: FAIL because `init --user` and `--activation-root` do not exist.

- [ ] **Step 7: Implement init flags**

In `internal/cli/cmd_init.go`, add local variables:

```go
userScope := false
activationRoot := false
```

Register flags:

```go
cmd.Flags().BoolVar(&userScope, "user", false, "create the user-level manifest under $HOME")
cmd.Flags().BoolVar(&activationRoot, "activation-root", false, "make a workspace manifest stop hierarchy discovery above this directory")
```

Before destination validation, resolve the destination and scope:

```go
destDir := dir
scope := manifest.ScopeProject
if destDir == "" {
	destDir = rc.Flags.Workspace
}
if userScope {
	if dir != "" || rc.Flags.Workspace != "" {
		return fmt.Errorf("init: --user cannot be combined with --dir or --workspace")
	}
	home, herr := resolveHome()
	if herr != nil {
		return fmt.Errorf("init: resolve home: %w", herr)
	}
	destDir = home
	scope = manifest.ScopeUser
} else if rc.Flags.Workspace != "" {
	scope = manifest.ScopeWorkspace
}
if activationRoot && scope != manifest.ScopeWorkspace {
	return fmt.Errorf("init: --activation-root requires --workspace")
}
```

When building `wizard.InitConfig`, set:

```go
Scope:          scope,
ActivationRoot: activationRoot,
```

When the interactive wizard returns `wcfg`, preserve `scope` and `activationRoot`:

```go
wcfg.Scope = scope
wcfg.ActivationRoot = activationRoot
```

- [ ] **Step 8: Verify init and manifest suite**

Run:

```bash
go test ./internal/manifest ./internal/tui/wizard ./internal/cli -run 'Test(LoadBytes_|InitConfig_|Init(User|Workspace))' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/manifest internal/tui/wizard internal/cli
git commit -m "feat: add user and workspace manifest scopes"
```

---

## Task 2: Activation Root Discovery

**Files:**

- Modify: `internal/hierarchy/hierarchy.go`
- Modify: `internal/hierarchy/discover.go`
- Modify: `internal/hierarchy/discover_test.go`
- Modify: `internal/hierarchy/hierarchy_test.go`
- Modify: `internal/coverage/coverage.go`
- Modify: `internal/coverage/coverage_test.go`

- [ ] **Step 1: Write failing hierarchy tests**

Add to `internal/hierarchy/discover_test.go`:

```go
func TestDiscover_ActivationRootStopsUserScope(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ActualReality")
	repo := filepath.Join(ws, "apps", "api")
	mustMkdirAll(t, repo)
	writeManifest(t, home, "version: 1\nscope: user\ncanonical:\n  local_dir: .agents\n")
	writeManifest(t, ws, "version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\n")
	mustWriteFile(t, filepath.Join(repo, ".git"), []byte("gitdir: .git/worktrees/api\n"))
	writeManifest(t, repo, "version: 1\nscope: project\ncanonical:\n  local_dir: .agents\n")

	scopes, err := Discover(repo, Options{Home: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got := gotLevels(scopes); !reflect.DeepEqual(got, []Level{LevelWorkspace, LevelProject}) {
		t.Fatalf("levels = %v, want [workspace project]; scopes=%+v", got, scopes)
	}
	if scopes[0].Root != ws {
		t.Fatalf("workspace scope = %+v, want root %s", scopes[0], ws)
	}
}

func TestDiscover_OutsideActivationRootIncludesUserScope(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "src", "small-repo")
	mustMkdirAll(t, repo)
	writeManifest(t, home, "version: 1\nscope: user\ncanonical:\n  local_dir: .agents\n")
	mustWriteFile(t, filepath.Join(repo, ".git"), []byte("gitdir: .git/worktrees/small-repo\n"))
	writeManifest(t, repo, "version: 1\nscope: project\ncanonical:\n  local_dir: .agents\n")

	scopes, err := Discover(repo, Options{Home: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got := gotLevels(scopes); !reflect.DeepEqual(got, []Level{LevelUser, LevelProject}) {
		t.Fatalf("levels = %v, want [user project]", got)
	}
}

func TestDiscover_NestedActivationRootsFailClosed(t *testing.T) {
	home := t.TempDir()
	outer := filepath.Join(home, "ActualReality")
	inner := filepath.Join(outer, "apps")
	repo := filepath.Join(inner, "api")
	mustMkdirAll(t, repo)
	writeManifest(t, outer, "version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\n")
	writeManifest(t, inner, "version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\n")
	mustWriteFile(t, filepath.Join(repo, ".git"), []byte("gitdir: .git/worktrees/api\n"))
	writeManifest(t, repo, "version: 1\nscope: project\ncanonical:\n  local_dir: .agents\n")

	_, err := Discover(repo, Options{Home: home})
	if err == nil {
		t.Fatal("Discover returned nil error for nested activation roots")
	}
	if !strings.Contains(err.Error(), "nested activation roots") {
		t.Fatalf("error = %v, want nested activation roots", err)
	}
	if !strings.Contains(err.Error(), outer) || !strings.Contains(err.Error(), inner) {
		t.Fatalf("error = %v, want both conflicting paths", err)
	}
}
```

Add helper:

```go
func gotLevels(scopes []Scope) []Level {
	out := make([]Level, 0, len(scopes))
	for _, sc := range scopes {
		out = append(out, sc.Level)
	}
	return out
}
```

Run:

```bash
go test ./internal/hierarchy -run 'TestDiscover_(ActivationRootStopsUserScope|OutsideActivationRootIncludesUserScope|NestedActivationRootsFailClosed)' -count=1
```

Expected: FAIL because `LevelWorkspace` and activation root discovery do not exist.

- [ ] **Step 2: Add workspace level**

Update `internal/hierarchy/hierarchy.go`:

```go
const (
	LevelUser Level = iota
	LevelWorkspace
	LevelProject
	LevelDirectory
)
```

Update `String`:

```go
case LevelWorkspace:
	return "workspace"
```

Update `internal/hierarchy/hierarchy_test.go` expected level strings to include `LevelWorkspace -> "workspace"`.

- [ ] **Step 3: Add lightweight manifest markers**

In `internal/hierarchy/discover.go`, add:

```go
type manifestMarker struct {
	Scope          string `yaml:"scope"`
	ActivationRoot bool   `yaml:"activation_root"`
}

func markerAt(dir string) (manifestMarker, string, bool, error) {
	manifestPath, ok := manifestAt(dir)
	if !ok {
		return manifestMarker{}, "", false, nil
	}
	info, err := os.Stat(manifestPath)
	if err != nil {
		return manifestMarker{}, "", false, fmt.Errorf("hierarchy: stat manifest %s: %w", manifestPath, err)
	}
	if info.Size() > manifest.MaxManifestSize {
		return manifestMarker{}, "", false, fmt.Errorf("hierarchy: manifest %s exceeds %d bytes", manifestPath, manifest.MaxManifestSize)
	}
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifestMarker{}, "", false, fmt.Errorf("hierarchy: read manifest %s: %w", manifestPath, err)
	}
	var marker manifestMarker
	if err := yaml.Unmarshal(b, &marker); err != nil {
		return manifestMarker{}, "", false, fmt.Errorf("hierarchy: parse manifest marker %s: %w", manifestPath, err)
	}
	return marker, manifestPath, true, nil
}
```

Imports needed:

```go
	"github.com/goccy/go-yaml"

	"github.com/agent-sync/agent-sync/internal/manifest"
```

This parser intentionally reads only scope markers, so discovery still works for tests and for old manifests whose canonical source is not valid enough for a full load.

- [ ] **Step 4: Implement activation-root scan**

Add to `internal/hierarchy/discover.go`:

```go
func activationRootsBetween(cwd, home string, maxHops int) ([]Scope, error) {
	var roots []Scope
	dir := cwd
	for hops := 0; hops < maxHops; hops++ {
		marker, path, ok, err := markerAt(dir)
		if err != nil {
			return nil, err
		}
		if ok && marker.Scope == manifest.ScopeWorkspace && marker.ActivationRoot {
			roots = append(roots, Scope{
				Root:         dir,
				ManifestPath: path,
				Level:        LevelWorkspace,
				Emit:         true,
			})
		}
		if dir == home {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	for i, j := 0, len(roots)-1; i < j; i, j = i+1, j-1 {
		roots[i], roots[j] = roots[j], roots[i]
	}
	if len(roots) > 1 {
		return nil, fmt.Errorf("hierarchy: nested activation roots are invalid: %s and %s", roots[0].ManifestPath, roots[len(roots)-1].ManifestPath)
	}
	return roots, nil
}
```

- [ ] **Step 5: Stop discovery at activation root**

Change `Discover` so it computes `activationRootsBetween` after resolving `home` and `maxHops`.

Use this rule:

```go
activation, err := activationRootsBetween(absCwd, home, maxHops)
if err != nil {
	return nil, err
}
if len(activation) == 1 {
	stopRoot := activation[0].Root
	projectRoot, hasProject := findProjectRoot(absCwd, stopRoot, maxHops)
	var emit []Scope
	if hasProject {
		emit, err = collectEmitScopes(absCwd, projectRoot)
		if err != nil {
			return nil, err
		}
	} else if absCwd != stopRoot {
		if path, has := manifestAt(absCwd); has {
			emit = []Scope{{Root: absCwd, ManifestPath: path, Level: LevelProject, Emit: true}}
		}
	}
	out := append([]Scope(nil), activation[0])
	out = append(out, emit...)
	return dedupeScopes(out), nil
}
```

Add `dedupeScopes` so a cwd equal to the activation root is not returned twice:

```go
func dedupeScopes(in []Scope) []Scope {
	seen := map[string]bool{}
	out := make([]Scope, 0, len(in))
	for _, sc := range in {
		if seen[sc.ManifestPath] {
			continue
		}
		seen[sc.ManifestPath] = true
		out = append(out, sc)
	}
	return out
}
```

Keep the existing user -> project -> directory logic when no activation root applies.

- [ ] **Step 6: Update coverage behavior for workspace**

Add to `internal/coverage/coverage_test.go`:

```go
func TestAnalyze_WorkspaceLevelActsLikeProjectRoot(t *testing.T) {
	got := Analyze(hierarchy.LevelWorkspace, []ir.Kind{ir.KindSkill, ir.KindCommand, ir.KindAgentsMD}, []string{"claude", "codex", "cursor"})
	if len(got) != 0 {
		t.Fatalf("workspace root warnings = %+v, want none", got)
	}
}
```

No code change is needed if `Analyze` keeps warning only for `LevelDirectory` and `LevelUser`.

- [ ] **Step 7: Verify hierarchy suite**

Run:

```bash
go test ./internal/hierarchy ./internal/coverage -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/hierarchy internal/coverage
git commit -m "feat: stop hierarchy discovery at activation roots"
```

---

## Task 3: Use Manifest Scope In Prepared Requests

**Files:**

- Modify: `internal/cli/setup.go`
- Modify: `internal/cli/hierarchy_sync.go`
- Modify: `internal/cli/hierarchy_sync_test.go`
- Modify: `internal/cli/cmd_validate.go`

- [ ] **Step 1: Write failing request-scope test**

Add to `internal/cli/hierarchy_sync_test.go`:

```go
func TestPrepareScope_UsesManifestScopeWhenPresent(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	writeWS(t, ws, ".agent-sync.yaml", "version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, ws, ".agents/AGENTS.md", "workspace instructions\n")

	rc := newTestRuntime(t, home)
	now := fixedNow()()
	prep, err := prepareScope(context.Background(), rc, ws, filepath.Join(ws, ".agent-sync.yaml"), "project", now)
	if err != nil {
		t.Fatalf("prepareScope: %v", err)
	}
	defer prep.Close()
	if prep.Request.Scope != "workspace" {
		t.Fatalf("Request.Scope = %q, want workspace", prep.Request.Scope)
	}
}
```

Run:

```bash
go test ./internal/cli -run TestPrepareScope_UsesManifestScopeWhenPresent -count=1
```

Expected: FAIL because `prepareScope` uses the caller-provided scope.

- [ ] **Step 2: Add scope selection helper**

In `internal/cli/setup.go`:

```go
func requestScope(m *manifest.Manifest, discovered string) string {
	if m != nil && m.Scope != "" {
		return m.Scope
	}
	if discovered != "" {
		return discovered
	}
	return manifest.ScopeProject
}
```

Change `engine.Request{Scope: scope}` to:

```go
Scope: requestScope(m, scope),
```

Change `prepareEngine` comment from "always project scope" to "project fallback unless the loaded manifest declares a scope."

- [ ] **Step 3: Ensure Cursor composition remains project-only**

In `prepareEngine`, call:

```go
actualScope := requestScope(prep.Manifest, "project")
user, ok := hierarchy.UserScope(home)
applyCursorComposition(ctx, rc, &prep.Request, prep.Manifest, actualScope, user, ok, now)
```

In `runHierarchySync`, call `applyCursorComposition` with `req.Scope` instead of `sc.Level.String()`.

- [ ] **Step 4: Verify CLI scope tests**

Run:

```bash
go test ./internal/cli -run 'TestPrepareScope_UsesManifestScopeWhenPresent|TestCompose' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "fix: honor manifest scope in engine requests"
```

---

## Task 4: Decode Native Fragments

**Files:**

- Create: `internal/harness/types.go`
- Create: `internal/harness/decode.go`
- Create: `internal/harness/decode_test.go`
- Modify: `internal/cli/materialize.go`

- [ ] **Step 1: Write failing decode tests**

Create `internal/harness/decode_test.go`:

```go
package harness

import (
	"testing"

	"github.com/agent-sync/agent-sync/internal/git"
)

type fakeSource struct {
	files map[string][]byte
}

func (f fakeSource) ReadTree(string) ([]git.TreeEntry, error) {
	out := make([]git.TreeEntry, 0, len(f.files))
	for p := range f.files {
		out = append(out, git.TreeEntry{Path: p, Mode: 0o100644})
	}
	return out, nil
}

func (f fakeSource) BlobContent(_ string, path string) ([]byte, error) {
	return f.files[path], nil
}

func TestDecodeFragments_CodexFeatureFlag(t *testing.T) {
	src := fakeSource{files: map[string][]byte{
		"configs/codex/features/hooks/fragment.yaml": []byte("id: hooks\ntarget: codex\npath: .codex/config.toml\nmerge: toml-key\nlocator: features.hooks\npayload: payload.toml\n"),
		"configs/codex/features/hooks/payload.toml":  []byte("[features]\nhooks = true\n"),
	}}
	frags, warnings, err := Decode(src, "", DecodeOptions{Scope: "workspace"})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	if len(frags) != 1 {
		t.Fatalf("fragments = %d, want 1", len(frags))
	}
	f := frags[0]
	if f.Identity() != "codex\x00.codex/config.toml\x00toml-key\x00features.hooks" {
		t.Fatalf("identity = %q", f.Identity())
	}
	if f.Visibility != VisibilityTeam || f.Inheritance != InheritanceDescendants || f.Safety != SafetyPassive {
		t.Fatalf("defaults = %+v", f)
	}
}

func TestDecodeFragments_RejectsPayloadTraversal(t *testing.T) {
	src := fakeSource{files: map[string][]byte{
		"configs/codex/features/hooks/fragment.yaml": []byte("id: hooks\ntarget: codex\npath: .codex/config.toml\nmerge: toml-key\nlocator: features.hooks\npayload: ../payload.toml\n"),
	}}
	_, _, err := Decode(src, "", DecodeOptions{Scope: "workspace"})
	if err == nil {
		t.Fatal("Decode returned nil error for payload traversal")
	}
}

func TestDecodeFragments_UserDefaultsArePersonalRootOnly(t *testing.T) {
	src := fakeSource{files: map[string][]byte{
		"configs/codex/features/hooks/fragment.yaml": []byte("id: hooks\ntarget: codex\npath: .codex/config.toml\nmerge: toml-key\nlocator: features.hooks\npayload: payload.toml\n"),
		"configs/codex/features/hooks/payload.toml":  []byte("[features]\nhooks = false\n"),
	}}
	frags, _, err := Decode(src, "", DecodeOptions{Scope: "user"})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if frags[0].Visibility != VisibilityPersonal || frags[0].Inheritance != InheritanceRootOnly {
		t.Fatalf("user defaults = %+v", frags[0])
	}
}
```

Run:

```bash
go test ./internal/harness -run TestDecodeFragments -count=1
```

Expected: FAIL because the package does not exist.

- [ ] **Step 2: Add fragment types**

Create `internal/harness/types.go`:

```go
package harness

import "github.com/agent-sync/agent-sync/internal/ir"

type Visibility string
type Inheritance string
type Safety string
type MergeKind string

const (
	VisibilityPersonal     Visibility = "personal"
	VisibilityTeam         Visibility = "team"
	VisibilityMachineLocal Visibility = "machine-local"

	InheritanceRootOnly    Inheritance = "root-only"
	InheritanceDescendants Inheritance = "descendants"

	SafetyPassive    Safety = "passive"
	SafetyToolAccess Safety = "tool-access"
	SafetyExecutable Safety = "executable"

	MergeTOMLKey    MergeKind = "toml-key"
	MergeCodexHooks MergeKind = "codex-hooks"
)

type Fragment struct {
	ID          string
	Target      string
	Path        string
	Merge       MergeKind
	Locator     string
	Visibility  Visibility
	Inheritance Inheritance
	Safety      Safety
	PayloadPath string
	Payload     []byte
	Scope       string
	Provenance  ir.Provenance
}

func (f Fragment) Identity() string {
	return f.Target + "\x00" + f.Path + "\x00" + string(f.Merge) + "\x00" + f.Locator
}

type Warning struct {
	Code    string
	Message string
	Path    string
}

type DecodeOptions struct {
	Scope string
}
```

- [ ] **Step 3: Implement fragment decoder**

Create `internal/harness/decode.go`:

```go
package harness

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/agent-sync/agent-sync/internal/ir"
)

type fragmentYAML struct {
	ID          string      `yaml:"id"`
	Target      string      `yaml:"target"`
	Path        string      `yaml:"path"`
	Merge       MergeKind   `yaml:"merge"`
	Locator     string      `yaml:"locator"`
	Visibility  Visibility  `yaml:"visibility,omitempty"`
	Inheritance Inheritance `yaml:"inheritance,omitempty"`
	Safety      Safety      `yaml:"safety,omitempty"`
	Payload     string      `yaml:"payload"`
}

func Decode(src ir.SourceTree, ref string, opts DecodeOptions) ([]Fragment, []Warning, error) {
	entries, err := src.ReadTree(ref)
	if err != nil {
		return nil, nil, fmt.Errorf("harness: read tree: %w", err)
	}
	var manifestPaths []string
	for _, e := range entries {
		if path.Base(e.Path) == "fragment.yaml" && strings.HasPrefix(e.Path, "configs/") {
			manifestPaths = append(manifestPaths, e.Path)
		}
	}
	slices.Sort(manifestPaths)

	var out []Fragment
	for _, manifestPath := range manifestPaths {
		data, err := src.BlobContent(ref, manifestPath)
		if err != nil {
			return nil, nil, fmt.Errorf("harness: read %s: %w", manifestPath, err)
		}
		var fy fragmentYAML
		if err := yaml.UnmarshalWithOptions(data, &fy, yaml.DisallowUnknownField()); err != nil {
			return nil, nil, fmt.Errorf("harness: parse %s: %s", manifestPath, yaml.FormatError(err, false, true))
		}
		frag, err := buildFragment(src, ref, opts.Scope, manifestPath, fy)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, frag)
	}
	return out, nil, nil
}

func buildFragment(src ir.SourceTree, ref, scope, manifestPath string, fy fragmentYAML) (Fragment, error) {
	if !ir.IsValidID(fy.ID) {
		return Fragment{}, fmt.Errorf("harness: %s has invalid id %q", manifestPath, fy.ID)
	}
	if fy.Target == "" || strings.ContainsAny(fy.Target, `/\`) {
		return Fragment{}, fmt.Errorf("harness: %s has invalid target %q", manifestPath, fy.Target)
	}
	cleanPath, err := cleanWorkspacePath(fy.Path)
	if err != nil {
		return Fragment{}, fmt.Errorf("harness: %s path: %w", manifestPath, err)
	}
	if fy.Merge == "" || fy.Locator == "" || fy.Payload == "" {
		return Fragment{}, fmt.Errorf("harness: %s requires merge, locator, and payload", manifestPath)
	}
	payloadPath, err := cleanPayloadPath(path.Dir(manifestPath), fy.Payload)
	if err != nil {
		return Fragment{}, fmt.Errorf("harness: %s payload: %w", manifestPath, err)
	}
	payload, err := src.BlobContent(ref, payloadPath)
	if err != nil {
		return Fragment{}, fmt.Errorf("harness: read payload %s: %w", payloadPath, err)
	}
	vis, inh := defaultsFor(scope)
	if fy.Visibility != "" {
		vis = fy.Visibility
	}
	if fy.Inheritance != "" {
		inh = fy.Inheritance
	}
	safety := fy.Safety
	if safety == "" {
		safety = SafetyPassive
	}
	if vis == VisibilityMachineLocal {
		inh = InheritanceRootOnly
	}
	return Fragment{
		ID: fy.ID, Target: fy.Target, Path: cleanPath, Merge: fy.Merge, Locator: fy.Locator,
		Visibility: vis, Inheritance: inh, Safety: safety, PayloadPath: payloadPath,
		Payload: payload, Scope: scope, Provenance: ir.Provenance{Path: manifestPath},
	}, nil
}

func defaultsFor(scope string) (Visibility, Inheritance) {
	switch scope {
	case "user":
		return VisibilityPersonal, InheritanceRootOnly
	case "workspace":
		return VisibilityTeam, InheritanceDescendants
	default:
		return VisibilityTeam, InheritanceRootOnly
	}
}

func cleanWorkspacePath(p string) (string, error) {
	if p == "" || path.IsAbs(p) || strings.ContainsRune(p, '\\') {
		return "", fmt.Errorf("must be a non-empty relative forward-slash path")
	}
	clean := path.Clean(p)
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("must stay inside the scope root")
	}
	return clean, nil
}

func cleanPayloadPath(base, rel string) (string, error) {
	if rel == "" || path.IsAbs(rel) || strings.ContainsRune(rel, '\\') {
		return "", fmt.Errorf("must be a relative forward-slash path")
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", fmt.Errorf("must not contain ..")
		}
	}
	return path.Clean(path.Join(base, rel)), nil
}
```

- [ ] **Step 4: Decode fragments during materialization**

Modify `internal/cli/materialize.go`:

```go
type materialized struct {
	Nodes     []ir.Node
	Skills    map[string]ir.Skill
	Fragments []harness.Fragment
	Commit    string
	SourceURL string
	Warnings  []ir.Warning
}
```

Import `internal/harness`.

Change `decodeAt` signature:

```go
func decodeAt(src ir.SourceTree, ref string, scope string) (materialized, error)
```

Call:

```go
fragments, fragmentWarnings, err := harness.Decode(src, ref, harness.DecodeOptions{Scope: scope})
if err != nil {
	return materialized{}, fmt.Errorf("cli: decode harness fragments at %s: %w", at, err)
}
for _, w := range fragmentWarnings {
	warnings = append(warnings, ir.Warning{Code: w.Code, Message: w.Message, Provenance: ir.Provenance{Path: w.Path}})
}
return materialized{Nodes: nodes, Skills: skills, Fragments: fragments, Commit: ref, Warnings: warnings}, nil
```

Update callers to pass `m.Scope`; when empty, pass `manifest.ScopeProject`.

- [ ] **Step 5: Verify decode and existing materialize tests**

Run:

```bash
go test ./internal/harness ./internal/cli -run 'TestDecodeFragments|TestSyncLocalDir|TestCompose' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/harness internal/cli/materialize.go
git commit -m "feat: decode managed native fragments"
```

---

## Task 5: Resolve Inherited Portable Assets And Native Fragments

**Files:**

- Create: `internal/harness/resolve.go`
- Create: `internal/harness/resolve_test.go`
- Modify: `internal/cli/hierarchy_sync.go`
- Modify: `internal/cli/cmd_sync.go`
- Modify: `internal/cli/setup.go`
- Modify: `internal/engine/request.go`

- [ ] **Step 1: Write failing resolver tests**

Create `internal/harness/resolve_test.go`:

```go
package harness

import (
	"testing"

	"github.com/agent-sync/agent-sync/internal/ir"
)

func TestResolveNodes_WorkspaceSkillInheritedIntoProject(t *testing.T) {
	wsNode := ir.Node{ID: "code-review", Kind: ir.KindSkill, Body: []byte("workspace skill\n")}
	wsSkill := ir.Skill{NodeID: "code-review", Assets: []ir.Asset{{RelPath: "SKILL.md", Content: []byte("workspace skill\n")}}}

	gotNodes, gotSkills := ResolveNodes([]Layer{{
		Scope: "workspace",
		Nodes: []ir.Node{wsNode},
		Skills: map[string]ir.Skill{"code-review": wsSkill},
		SourceURL: ".agents",
	}}, "project")

	if len(gotNodes) != 1 {
		t.Fatalf("nodes = %+v, want workspace skill", gotNodes)
	}
	if gotNodes[0].SourceURL != ".agents" {
		t.Fatalf("inherited SourceURL = %q, want .agents", gotNodes[0].SourceURL)
	}
	if _, ok := gotSkills["code-review"]; !ok {
		t.Fatalf("skill asset missing: %+v", gotSkills)
	}
}

func TestResolveNodes_ProjectShadowsWorkspaceByKindAndID(t *testing.T) {
	wsNode := ir.Node{ID: "code-review", Kind: ir.KindSkill, Body: []byte("workspace skill\n")}
	projectNode := ir.Node{ID: "code-review", Kind: ir.KindSkill, Body: []byte("project skill\n")}

	gotNodes, _ := ResolveNodes([]Layer{
		{Scope: "workspace", Nodes: []ir.Node{wsNode}, SourceURL: "workspace-source"},
		{Scope: "project", Nodes: []ir.Node{projectNode}, SourceURL: "project-source"},
	}, "project")

	if len(gotNodes) != 1 {
		t.Fatalf("nodes = %+v, want one shadowed node", gotNodes)
	}
	if string(gotNodes[0].Body) != "project skill\n" {
		t.Fatalf("body = %q, want project skill", gotNodes[0].Body)
	}
}

func TestResolveNodes_UserLayerDoesNotInheritIntoProjectByDefault(t *testing.T) {
	userNode := ir.Node{ID: "personal", Kind: ir.KindSkill, Body: []byte("personal skill\n")}
	gotNodes, _ := ResolveNodes([]Layer{{Scope: "user", Nodes: []ir.Node{userNode}}}, "project")
	if len(gotNodes) != 0 {
		t.Fatalf("nodes = %+v, want no inherited user nodes", gotNodes)
	}
}

func TestResolveFragments_ClosestScopeWinsByIdentity(t *testing.T) {
	ws := Fragment{ID: "hooks", Target: "codex", Path: ".codex/config.toml", Merge: MergeTOMLKey, Locator: "features.hooks", Inheritance: InheritanceDescendants, Visibility: VisibilityTeam, Payload: []byte("[features]\nhooks = true\n"), Scope: "workspace"}
	project := Fragment{ID: "hooks", Target: "codex", Path: ".codex/config.toml", Merge: MergeTOMLKey, Locator: "features.hooks", Inheritance: InheritanceRootOnly, Visibility: VisibilityTeam, Payload: []byte("[features]\nhooks = false\n"), Scope: "project"}

	got := ResolveFragments([]Layer{{Scope: "workspace", Fragments: []Fragment{ws}}, {Scope: "project", Fragments: []Fragment{project}}}, "project")
	if len(got) != 1 {
		t.Fatalf("fragments = %d, want 1", len(got))
	}
	if string(got[0].Payload) != string(project.Payload) {
		t.Fatalf("payload = %q, want project payload", got[0].Payload)
	}
}

func TestResolveFragments_SkipsPersonalAncestorForProject(t *testing.T) {
	user := Fragment{ID: "hooks", Target: "codex", Path: ".codex/config.toml", Merge: MergeTOMLKey, Locator: "features.hooks", Inheritance: InheritanceDescendants, Visibility: VisibilityPersonal, Payload: []byte("[features]\nhooks = false\n"), Scope: "user"}
	got := ResolveFragments([]Layer{{Scope: "user", Fragments: []Fragment{user}}}, "project")
	if len(got) != 0 {
		t.Fatalf("fragments = %+v, want none", got)
	}
}

func TestResolveFragments_IncludesTeamDescendantAncestor(t *testing.T) {
	ws := Fragment{ID: "hooks", Target: "codex", Path: ".codex/config.toml", Merge: MergeTOMLKey, Locator: "features.hooks", Inheritance: InheritanceDescendants, Visibility: VisibilityTeam, Payload: []byte("[features]\nhooks = true\n"), Scope: "workspace"}
	got := ResolveFragments([]Layer{{Scope: "workspace", Fragments: []Fragment{ws}}}, "project")
	if len(got) != 1 {
		t.Fatalf("fragments = %+v, want workspace fragment", got)
	}
}
```

Run:

```bash
go test ./internal/harness -run 'TestResolve(Nodes|Fragments)' -count=1
```

Expected: FAIL because the resolver does not exist.

- [ ] **Step 2: Implement resolver**

Create `internal/harness/resolve.go`:

```go
package harness

import "github.com/agent-sync/agent-sync/internal/ir"

type Layer struct {
	Scope     string
	Nodes     []ir.Node
	Skills    map[string]ir.Skill
	Fragments []Fragment
	SourceURL string
	Commit    string
}

func ResolveNodes(layers []Layer, targetScope string) ([]ir.Node, map[string]ir.Skill) {
	byID := map[string]ir.Node{}
	order := make([]string, 0)
	skills := map[string]ir.Skill{}
	for i, layer := range layers {
		isCurrent := i == len(layers)-1
		if !isCurrent && !portableLayerInherits(layer.Scope, targetScope) {
			continue
		}
		for _, n := range layer.Nodes {
			id := string(n.Kind) + "\x00" + n.ID
			if _, seen := byID[id]; !seen {
				order = append(order, id)
			}
			if !isCurrent {
				n.SourceURL = layer.SourceURL
				n.SourceCommit = layer.Commit
			}
			byID[id] = n
			if n.Kind == ir.KindSkill {
				delete(skills, n.ID)
				if sk, ok := layer.Skills[n.ID]; ok {
					skills[n.ID] = sk
				}
			}
		}
	}
	out := make([]ir.Node, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, skills
}

func portableLayerInherits(scope, targetScope string) bool {
	if scope == targetScope {
		return true
	}
	return scope == "workspace"
}

func ResolveFragments(layers []Layer, targetScope string) []Fragment {
	byID := map[string]Fragment{}
	order := make([]string, 0)
	for i, layer := range layers {
		isCurrent := i == len(layers)-1
		for _, f := range layer.Fragments {
			if !isCurrent && !canInherit(f, targetScope) {
				continue
			}
			id := f.Identity()
			if _, seen := byID[id]; !seen {
				order = append(order, id)
			}
			byID[id] = f
		}
	}
	out := make([]Fragment, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func canInherit(f Fragment, targetScope string) bool {
	if f.Scope == targetScope {
		return true
	}
	if f.Inheritance != InheritanceDescendants {
		return false
	}
	if f.Visibility != VisibilityTeam {
		return false
	}
	return true
}
```

- [ ] **Step 3: Add fragments to engine request**

Modify `internal/engine/request.go`:

```go
import "github.com/agent-sync/agent-sync/internal/harness"
```

Add to `Request`:

```go
// Fragments are resolved, target-native harness config fragments. The engine
// applies them through core allowlisted native surfaces; adapters never see or
// write them.
Fragments []harness.Fragment
```

- [ ] **Step 4: Pass current-scope fragments in single-scope setup**

In `internal/cli/setup.go`, set:

```go
Fragments: mat.Fragments,
```

This makes `sync --workspace` and `validate --workspace` apply fragments authored in that root before inherited resolution is added.

- [ ] **Step 5: Resolve ancestor layers in hierarchy sync**

In `internal/cli/hierarchy_sync.go`, first make write-target selection explicit. Discovery returns every active scope, but sync writes exactly one scope:

```go
func selectWriteScopes(scopes []hierarchy.Scope, includeUser bool) []hierarchy.Scope {
	out := append([]hierarchy.Scope(nil), scopes...)
	for i := range out {
		out[i].Emit = false
	}
	if includeUser {
		for i := range out {
			if out[i].Level == hierarchy.LevelUser {
				out[i].Emit = true
				return out
			}
		}
		return out
	}
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Level == hierarchy.LevelUser {
			continue
		}
		out[i].Emit = true
		return out
	}
	return out
}
```

Call it immediately after discovery:

```go
scopes = selectWriteScopes(scopes, opts.IncludeUser)
```

Remove the interactive "also sync the user-level manifest" offer from `newSyncCommand` and `runHierarchySync`. A plain project sync may read inherited layers, but it must not turn into a second write to `$HOME`. `sync --user` selects the user scope as the single write target.

Then, before the emit loop, materialize each discovered scope read-only into a lightweight layer:

```go
type preparedLayer struct {
	Scope hierarchy.Scope
	Materialized materialized
	Manifest *manifest.Manifest
}
```

Add helper:

```go
func materializeLayerReadOnly(ctx context.Context, rc *runtimeContext, sc hierarchy.Scope, now time.Time) (preparedLayer, bool) {
	m, err := manifest.LoadFile(sc.ManifestPath, manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		rc.Logger.Warn("harness: cannot read ancestor manifest", "path", sc.ManifestPath, "err", err)
		return preparedLayer{}, false
	}
	root, err := fsroot.OpenWorkspaceRoot(sc.Root)
	if err != nil {
		rc.Logger.Warn("harness: cannot open ancestor root", "root", sc.Root, "err", err)
		return preparedLayer{}, false
	}
	defer func() { _ = root.Close() }()
	offline := rc.Flags.Offline || m.Canonical.URL != ""
	mat, err := materialize(ctx, m, materializeOptions{Offline: offline, Now: now, Root: root})
	if err != nil {
		rc.Logger.Warn("harness: cannot materialize ancestor", "path", sc.ManifestPath, "err", err)
		return preparedLayer{}, false
	}
	return preparedLayer{Scope: sc, Materialized: mat, Manifest: m}, true
}
```

Build layers in discovery order. For each emitted scope, resolve portable nodes, skill assets, and fragments from all layers up to that scope:

```go
layersForScope := func(sc hierarchy.Scope, prepared []preparedLayer) []harness.Layer {
	var layers []harness.Layer
	for _, p := range prepared {
		layers = append(layers, harness.Layer{
			Scope: p.Scope.Level.String(),
			Nodes: p.Materialized.Nodes,
			Skills: p.Materialized.Skills,
			Fragments: p.Materialized.Fragments,
			SourceURL: p.Materialized.SourceURL,
			Commit: p.Materialized.Commit,
		})
		if p.Scope.ManifestPath == sc.ManifestPath {
			break
		}
	}
	return layers
}
```

After `req := prep.Request`, set:

```go
layers := layersForScope(sc, preparedLayers)
req.Nodes, req.Skills = harness.ResolveNodes(layers, req.Scope)
req.Fragments = harness.ResolveFragments(layers, req.Scope)
```

Activation roots already removed the user scope from discovery, so no user layer can leak into an activation-root subtree. Outside activation roots, user portable nodes still do not inherit by default because `portableLayerInherits` only cascades workspace layers. Existing `compose.cursor-rules-from-user` remains the explicit user-to-project exception until it is replaced by first-class portable metadata.

Extract the layer resolution into a helper used by both sync and validate:

```go
func applyResolvedLayers(req *engine.Request, sc hierarchy.Scope, preparedLayers []preparedLayer) {
	layers := layersForScope(sc, preparedLayers)
	req.Nodes, req.Skills = harness.ResolveNodes(layers, req.Scope)
	req.Fragments = harness.ResolveFragments(layers, req.Scope)
}
```

In `prepareEngine`, when `rc.Flags.Workspace == ""`, use hierarchy discovery plus `selectWriteScopes` to find the selected write scope, call `prepareScope` for that scope, materialize the read-only layers, and call `applyResolvedLayers` before returning. When `rc.Flags.Workspace != ""`, keep the explicit single-scope behavior and use only that scope's own materialized nodes/fragments. This makes `validate` match a plain `sync` while keeping explicit workspace overrides predictable.

Add selector tests in `internal/cli/hierarchy_sync_test.go`:

```go
func TestSelectWriteScopes_DefaultSelectsClosestNonUser(t *testing.T) {
	scopes := []hierarchy.Scope{
		{Level: hierarchy.LevelUser, Root: "/home/u"},
		{Level: hierarchy.LevelWorkspace, Root: "/home/u/ActualReality"},
		{Level: hierarchy.LevelProject, Root: "/home/u/ActualReality/apps/api"},
	}
	got := selectWriteScopes(scopes, false)
	if got[0].Emit || got[1].Emit || !got[2].Emit {
		t.Fatalf("emit flags = %+v, want only project", got)
	}
}

func TestSelectWriteScopes_UserFlagSelectsOnlyUser(t *testing.T) {
	scopes := []hierarchy.Scope{
		{Level: hierarchy.LevelUser, Root: "/home/u"},
		{Level: hierarchy.LevelProject, Root: "/home/u/src/repo"},
	}
	got := selectWriteScopes(scopes, true)
	if !got[0].Emit || got[1].Emit {
		t.Fatalf("emit flags = %+v, want only user", got)
	}
}
```

- [ ] **Step 6: Verify resolver and existing hierarchy sync**

Run:

```bash
go test ./internal/harness ./internal/cli -run 'TestResolve(Nodes|Fragments)|TestRunHierarchySync|TestCompose' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/harness internal/engine/request.go internal/cli
git commit -m "feat: resolve inherited harness layers"
```

---

## Task 6: Native Merge Primitives

**Files:**

- Create: `internal/merge/native.go`
- Create: `internal/merge/native_toml.go`
- Create: `internal/merge/native_json.go`
- Create: `internal/merge/native_test.go`

- [ ] **Step 1: Write failing native merge tests**

Create `internal/merge/native_test.go`:

```go
package merge

import (
	"strings"
	"testing"
)

func TestMergeNativeTOMLKey_UpsertsFeatureWithoutRewritingOtherContent(t *testing.T) {
	base := []byte("# user comment\nmodel = \"gpt-5\"\n\n[features]\nfast_mode = false\n")
	entry := NativeEntry{Kind: NativeKindTOMLKey, Locator: "features.hooks", Content: []byte("[features]\nhooks = true\n")}
	out, hash, err := mergeNative(base, []NativeEntry{entry})
	if err != nil {
		t.Fatalf("mergeNative: %v", err)
	}
	text := string(out)
	for _, want := range []string{"# user comment\n", "model = \"gpt-5\"\n", "fast_mode = false\n", "hooks = true\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	if hash == "" {
		t.Fatal("hash is empty")
	}
}

func TestMergeNativeTOMLKey_ReplacesExistingKey(t *testing.T) {
	base := []byte("[features]\nhooks = false\n")
	entry := NativeEntry{Kind: NativeKindTOMLKey, Locator: "features.hooks", Content: []byte("[features]\nhooks = true\n")}
	out, _, err := mergeNative(base, []NativeEntry{entry})
	if err != nil {
		t.Fatalf("mergeNative: %v", err)
	}
	if string(out) != "[features]\nhooks = true\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestMergeNativeGeneratedJSON_RefusesUnmanagedExistingFile(t *testing.T) {
	entry := NativeEntry{Kind: NativeKindGeneratedJSON, Locator: "codex-hooks", Content: []byte(`{"hooks":{"PreToolUse":[]}}`)}
	_, _, err := mergeNative([]byte(`{"hooks":{"Stop":[]}}`), []NativeEntry{entry})
	if err == nil || !strings.Contains(err.Error(), "unmanaged existing JSON") {
		t.Fatalf("err = %v, want unmanaged existing JSON refusal", err)
	}
}

func TestMergeNativeGeneratedJSON_AllowsIdempotentManagedRewrite(t *testing.T) {
	entry := NativeEntry{Kind: NativeKindGeneratedJSON, Locator: "codex-hooks", Content: []byte(`{"hooks":{"PreToolUse":[]}}`)}
	out, _, err := mergeNative(nil, []NativeEntry{entry})
	if err != nil {
		t.Fatalf("mergeNative create: %v", err)
	}
	out2, _, err := mergeNative(out, []NativeEntry{entry})
	if err != nil {
		t.Fatalf("mergeNative rewrite: %v", err)
	}
	if string(out) != string(out2) {
		t.Fatalf("rewrite changed bytes:\n%s\n---\n%s", out, out2)
	}
}
```

Run:

```bash
go test ./internal/merge -run TestMergeNative -count=1
```

Expected: FAIL because native merge does not exist.

- [ ] **Step 2: Add native merge types**

Create `internal/merge/native.go`:

```go
package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type NativeKind string

const (
	NativeKindTOMLKey       NativeKind = "toml-key"
	NativeKindGeneratedJSON NativeKind = "generated-json"
)

type NativeEntry struct {
	Kind    NativeKind
	Locator string
	Content []byte
}

func mergeNative(existing []byte, entries []NativeEntry) ([]byte, string, error) {
	out := append([]byte(nil), existing...)
	for _, e := range entries {
		var err error
		switch e.Kind {
		case NativeKindTOMLKey:
			out, err = mergeNativeTOMLKey(out, e)
		case NativeKindGeneratedJSON:
			out, err = mergeNativeGeneratedJSON(out, e)
		default:
			return nil, "", fmt.Errorf("merge: unknown native kind %q", e.Kind)
		}
		if err != nil {
			return nil, "", err
		}
	}
	h := sha256.Sum256(out)
	return out, hex.EncodeToString(h[:]), nil
}
```

- [ ] **Step 3: Implement TOML key merge**

Create `internal/merge/native_toml.go` with line-preserving support for `features.<key>`:

```go
package merge

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var featuresKeyLocator = regexp.MustCompile(`\Afeatures\.[A-Za-z0-9_-]+\z`)

func mergeNativeTOMLKey(existing []byte, e NativeEntry) ([]byte, error) {
	if !featuresKeyLocator.MatchString(e.Locator) {
		return nil, fmt.Errorf("merge: unsupported native TOML locator %q", e.Locator)
	}
	if !isBlank(existing) {
		var parsed map[string]any
		if err := toml.Unmarshal(existing, &parsed); err != nil {
			return nil, fmt.Errorf("%w: malformed TOML", ErrMalformedToolOwnedFile)
		}
	}
	var payload map[string]map[string]any
	if err := toml.Unmarshal(e.Content, &payload); err != nil {
		return nil, fmt.Errorf("%w: malformed native TOML payload", ErrMalformedToolOwnedFile)
	}
	key := strings.TrimPrefix(e.Locator, "features.")
	value, ok := payload["features"][key]
	if !ok {
		return nil, fmt.Errorf("merge: native TOML payload must set [features].%s", key)
	}
	rendered, err := renderNativeTOMLValue(value)
	if err != nil {
		return nil, err
	}
	return spliceFeatureKey(existing, key, rendered), nil
}

func renderNativeTOMLValue(v any) (string, error) {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(map[string]any{"v": v}); err != nil {
		return "", fmt.Errorf("merge: render native TOML value: %w", err)
	}
	line := strings.TrimSpace(b.String())
	_, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", fmt.Errorf("merge: render native TOML value produced %q", line)
	}
	return strings.TrimSpace(value), nil
}
```

Implement `spliceFeatureKey` by preserving all lines and only inserting/replacing under `[features]`:

```go
func spliceFeatureKey(existing []byte, key, rendered string) []byte {
	lines := splitLinesPreserve(existing)
	keyPrefix := key + " ="
	inFeatures := false
	featuresStart := -1
	featuresEnd := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == "[features]" {
				inFeatures = true
				featuresStart = i
				continue
			}
			if inFeatures {
				featuresEnd = i
				break
			}
		}
		if inFeatures && strings.HasPrefix(trimmed, keyPrefix) {
			lines[i] = key + " = " + rendered + "\n"
			return []byte(strings.Join(lines, ""))
		}
	}
	insert := key + " = " + rendered + "\n"
	if featuresStart >= 0 {
		out := append([]string{}, lines[:featuresStart+1]...)
		out = append(out, insert)
		out = append(out, lines[featuresStart+1:featuresEnd]...)
		out = append(out, lines[featuresEnd:]...)
		return []byte(strings.Join(out, ""))
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		lines = append(lines, "\n")
	}
	lines = append(lines, "[features]\n", insert)
	return []byte(strings.Join(lines, ""))
}
```

Add `splitLinesPreserve`:

```go
func splitLinesPreserve(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := strings.SplitAfter(string(b), "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
```

- [ ] **Step 4: Implement generated JSON guard**

Create `internal/merge/native_json.go`:

```go
package merge

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func mergeNativeGeneratedJSON(existing []byte, e NativeEntry, allowExisting bool) ([]byte, error) {
	if e.Locator != "codex-hooks" {
		return nil, fmt.Errorf("merge: unsupported generated JSON locator %q", e.Locator)
	}
	if !json.Valid(e.Content) {
		return nil, fmt.Errorf("%w: generated JSON payload is invalid", ErrMalformedToolOwnedFile)
	}
	if !isBlank(existing) && !allowExisting {
		return nil, ErrUnmanagedGeneratedFile
	}
	return append([]byte(nil), e.Content...), nil
}
```

- [ ] **Step 5: Add apply/dry-run entry points**

Extend `internal/merge/native.go`:

```go
func ApplyNativeToFile(ctx context.Context, root *fsroot.Root, reg *locks.FileLockRegistry, relPath string, entries []NativeEntry, holder string, opts NativeMergeOptions) (string, int64, error) {
	abs := filepath.Join(root.Path(), filepath.FromSlash(relPath))
	release, err := reg.Acquire(ctx, abs, holder, locks.FileLockOpts{})
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = release() }()
	existing, err := readExisting(root, relPath)
	if err != nil {
		return "", 0, err
	}
	merged, hash, err := mergeNativeWithOptions(existing, entries, opts)
	if err != nil {
		return "", 0, err
	}
	if dir := slashDir(relPath); dir != "" {
		if mkErr := root.Inner().MkdirAll(dir, 0o755); mkErr != nil {
			return "", 0, fmt.Errorf("merge: mkdir %s: %w", dir, mkErr)
		}
	}
	if err := root.StagedWrite(relPath, merged, 0o644); err != nil {
		return "", 0, fmt.Errorf("merge: write native %s: %w", relPath, err)
	}
	return hash, int64(len(merged)), nil
}

func DryNativeMerge(root *fsroot.Root, relPath string, entries []NativeEntry, opts NativeMergeOptions) (exists, changed bool, err error) {
	existing, exists, err := readExistingForDry(root, relPath)
	if err != nil {
		return false, false, err
	}
	merged, _, err := mergeNativeWithOptions(existing, entries, opts)
	if err != nil {
		return exists, false, err
	}
	return exists, !bytes.Equal(merged, existing), nil
}
```

Add imports required by this block.

- [ ] **Step 6: Verify merge package**

Run:

```bash
go test ./internal/merge -run TestMergeNative -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/merge
git commit -m "feat: add native config merge primitives"
```

---

## Task 7: Codex Native Fragment Operations

**Files:**

- Create: `internal/harness/codex.go`
- Create: `internal/harness/codex_test.go`
- Modify: `internal/harness/types.go`

- [ ] **Step 1: Write failing Codex operation tests**

Create `internal/harness/codex_test.go`:

```go
package harness

import (
	"encoding/json"
	"testing"

	"github.com/agent-sync/agent-sync/internal/merge"
)

func TestCodexNativeOperations_FeatureFlag(t *testing.T) {
	frag := Fragment{ID: "hooks", Target: "codex", Path: ".codex/config.toml", Merge: MergeTOMLKey, Locator: "features.hooks", Payload: []byte("[features]\nhooks = true\n")}
	ops, warnings := NativeOperationsForTarget([]Fragment{frag}, "codex")
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want 1", ops)
	}
	if ops[0].Path != ".codex/config.toml" || ops[0].Entries[0].Kind != merge.NativeKindTOMLKey {
		t.Fatalf("op = %+v", ops[0])
	}
}

func TestCodexNativeOperations_HooksJSON(t *testing.T) {
	payload := []byte(`{"matcher":"Bash","hooks":[{"type":"command","command":"python3 .codex/hooks/check.py","statusMessage":"Checking Bash command"}]}`)
	frag := Fragment{ID: "pre-tool-policy", Target: "codex", Path: ".codex/hooks.json", Merge: MergeCodexHooks, Locator: "PreToolUse/pre-tool-policy", Payload: payload}
	ops, warnings := NativeOperationsForTarget([]Fragment{frag}, "codex")
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want 1", ops)
	}
	var doc map[string]any
	if err := json.Unmarshal(ops[0].Entries[0].Content, &doc); err != nil {
		t.Fatalf("unmarshal generated hooks: %v", err)
	}
	if _, ok := doc["_agent_sync_generated"]; ok {
		t.Fatalf("generated hooks should not include agent-sync marker: %#v", doc)
	}
	if _, ok := doc["hooks"].(map[string]any)["PreToolUse"]; !ok {
		t.Fatalf("generated hooks missing PreToolUse: %#v", doc)
	}
}

func TestCodexNativeOperations_UnsupportedTargetWarns(t *testing.T) {
	frag := Fragment{ID: "hooks", Target: "claude", Path: ".claude/settings.json", Merge: MergeTOMLKey, Locator: "x.y", Payload: []byte("x = 1\n")}
	ops, warnings := NativeOperationsForTarget([]Fragment{frag}, "claude")
	if len(ops) != 0 {
		t.Fatalf("ops = %+v, want none", ops)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want 1", warnings)
	}
}
```

Run:

```bash
go test ./internal/harness -run TestCodexNativeOperations -count=1
```

Expected: FAIL because operations do not exist.

- [ ] **Step 2: Add operation type**

Update `internal/harness/types.go`:

```go
type NativeOperation struct {
	Target  string
	Path    string
	Entries []merge.NativeEntry
}
```

Import `internal/merge`.

- [ ] **Step 3: Implement Codex allowlist**

Create `internal/harness/codex.go`:

```go
package harness

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agent-sync/agent-sync/internal/merge"
)

func NativeOperationsForTarget(fragments []Fragment, target string) ([]NativeOperation, []Warning) {
	if target != "codex" {
		var warnings []Warning
		for _, f := range fragments {
			if f.Target == target {
				warnings = append(warnings, Warning{Code: "unsupported-native-fragment", Message: "target has no native fragment allowlist: " + target, Path: f.Provenance.Path})
			}
		}
		return nil, warnings
	}
	return codexOperations(fragments)
}

func codexOperations(fragments []Fragment) ([]NativeOperation, []Warning) {
	var warnings []Warning
	var configEntries []merge.NativeEntry
	hooksByEvent := map[string][]json.RawMessage{}
	for _, f := range fragments {
		if f.Target != "codex" {
			continue
		}
		switch {
		case f.Path == ".codex/config.toml" && f.Merge == MergeTOMLKey && strings.HasPrefix(f.Locator, "features."):
			configEntries = append(configEntries, merge.NativeEntry{Kind: merge.NativeKindTOMLKey, Locator: f.Locator, Content: f.Payload})
		case f.Path == ".codex/hooks.json" && f.Merge == MergeCodexHooks:
			event, err := codexHookEvent(f.Locator)
			if err != nil {
				warnings = append(warnings, Warning{Code: "invalid-codex-hook-locator", Message: err.Error(), Path: f.Provenance.Path})
				continue
			}
			if !json.Valid(f.Payload) {
				warnings = append(warnings, Warning{Code: "invalid-codex-hook-payload", Message: "payload is not valid JSON", Path: f.Provenance.Path})
				continue
			}
			hooksByEvent[event] = append(hooksByEvent[event], append([]byte(nil), f.Payload...))
		default:
			warnings = append(warnings, Warning{Code: "unsupported-codex-native-fragment", Message: fmt.Sprintf("unsupported codex native fragment path=%s merge=%s locator=%s", f.Path, f.Merge, f.Locator), Path: f.Provenance.Path})
		}
	}
	var ops []NativeOperation
	if len(configEntries) > 0 {
		ops = append(ops, NativeOperation{Target: "codex", Path: ".codex/config.toml", Entries: configEntries})
	}
	if len(hooksByEvent) > 0 {
		content, err := renderCodexHooks(hooksByEvent)
		if err != nil {
			warnings = append(warnings, Warning{Code: "invalid-codex-hooks", Message: err.Error()})
		} else {
			ops = append(ops, NativeOperation{Target: "codex", Path: ".codex/hooks.json", Entries: []merge.NativeEntry{{Kind: merge.NativeKindGeneratedJSON, Locator: "codex-hooks", Content: content}}})
		}
	}
	return ops, warnings
}

func codexHookEvent(locator string) (string, error) {
	event, id, ok := strings.Cut(locator, "/")
	if !ok || event == "" || id == "" {
		return "", fmt.Errorf("codex hook locator must be <Event>/<id>, got %q", locator)
	}
	return event, nil
}

func renderCodexHooks(byEvent map[string][]json.RawMessage) ([]byte, error) {
	events := make([]string, 0, len(byEvent))
	for event := range byEvent {
		events = append(events, event)
	}
	sort.Strings(events)
	hooks := map[string]any{}
	for _, event := range events {
		var arr []any
		for _, raw := range byEvent[event] {
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		hooks[event] = arr
	}
	return json.MarshalIndent(map[string]any{
		"hooks": hooks,
	}, "", "  ")
}
```

- [ ] **Step 4: Verify harness operations**

Run:

```bash
go test ./internal/harness -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/harness
git commit -m "feat: build codex native fragment operations"
```

---

## Task 8: Apply Native Operations In Sync And Validate

**Files:**

- Modify: `internal/engine/target.go`
- Modify: `internal/engine/plan.go`
- Modify: `internal/engine/engine_test.go`
- Modify: `internal/engine/validate_idempotency_test.go`

- [ ] **Step 1: Write failing engine sync test**

Add to `internal/engine/engine_test.go`:

```go
func TestSync_AppliesCodexNativeFeatureFragment(t *testing.T) {
	root := openTestRoot(t)
	req := testRequest(t, root, "codex")
	req.Fragments = []harness.Fragment{{
		ID: "hooks", Target: "codex", Path: ".codex/config.toml",
		Merge: harness.MergeTOMLKey, Locator: "features.hooks",
		Payload: []byte("[features]\nhooks = true\n"),
	}}
	summary, err := Sync(context.Background(), req)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if summary.Outcome.ExitCode != 0 {
		t.Fatalf("exit = %d summary=%+v", summary.Outcome.ExitCode, summary)
	}
	data := readRootFile(t, root, ".codex/config.toml")
	if !strings.Contains(string(data), "[features]\nhooks = true\n") {
		t.Fatalf("config.toml missing hooks feature:\n%s", data)
	}
	led, err := ledger.Load(root, "codex")
	if err != nil {
		t.Fatalf("ledger.Load: %v", err)
	}
	if _, ok := entryForSuffix(led, ".codex/config.toml"); !ok {
		t.Fatalf("ledger missing config.toml entry: %+v", led.Entries)
	}
}
```

Add imports for `internal/harness` and `internal/ledger`.

Run:

```bash
go test ./internal/engine -run TestSync_AppliesCodexNativeFeatureFragment -count=1
```

Expected: FAIL because `Sync` ignores fragments.

- [ ] **Step 2: Write failing validate parity test**

Add to `internal/engine/validate_idempotency_test.go`:

```go
func TestPlan_NativeFragmentsIdempotentAfterSync(t *testing.T) {
	root := openTestRoot(t)
	req := testRequest(t, root, "codex")
	req.Fragments = []harness.Fragment{{
		ID: "hooks", Target: "codex", Path: ".codex/config.toml",
		Merge: harness.MergeTOMLKey, Locator: "features.hooks",
		Payload: []byte("[features]\nhooks = true\n"),
	}}
	if _, err := Sync(context.Background(), req); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	plan, err := Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.DriftDetected {
		t.Fatalf("plan drift after clean sync: %+v", plan)
	}
}
```

Run:

```bash
go test ./internal/engine -run TestPlan_NativeFragmentsIdempotentAfterSync -count=1
```

Expected: FAIL because validate ignores fragments.

- [ ] **Step 3: Apply native operations in target sync**

In `internal/engine/target.go`, after adapter ops are grouped and before the ledger write, build native operations:

```go
nativeOps, nativeWarnings := harness.NativeOperationsForTarget(req.Fragments, target)
for _, w := range nativeWarnings {
	out.warnings = append(out.warnings, w.Message)
}
```

Apply them next to tool-owned merges:

```go
if len(nativeOps) > 0 {
	reg, regErr := locks.NewFileLockRegistry(root)
	if regErr != nil {
		return statusResult{}, fmt.Errorf("engine: file lock registry: %w", regErr)
	}
	holder := "engine:" + target
	for _, op := range nativeOps {
		hash, size, merr := merge.ApplyNativeToFile(ctx, root, reg, op.Path, op.Entries, holder, nativeMergeOptions(op, oldByPath))
		if merr != nil {
			return statusResult{}, fmt.Errorf("engine: native merge %s: %w", op.Path, merr)
		}
		bumpChanged(op.Path, hash)
		newEntries[op.Path] = ledger.Entry{Path: op.Path, SHA256: hash, Size: size, EmittedAt: now}
	}
}
```

Use a single file lock registry if both tool-owned and native operations exist:

```go
var fileLocks *locks.FileLockRegistry
getFileLocks := func() (*locks.FileLockRegistry, error) {
	if fileLocks != nil {
		return fileLocks, nil
	}
	reg, err := locks.NewFileLockRegistry(root)
	if err != nil {
		return nil, fmt.Errorf("engine: file lock registry: %w", err)
	}
	fileLocks = reg
	return reg, nil
}
```

- [ ] **Step 4: Plan native operations in validate**

In `internal/engine/plan.go`, after adapter ops are planned:

```go
nativeOps, nativeWarnings := harness.NativeOperationsForTarget(req.Fragments, target)
for _, w := range nativeWarnings {
	change.Warnings = append(change.Warnings, w.Message)
}
nativeSeen := map[string]bool{}
for _, op := range nativeOps {
	exists, changed, derr := merge.DryNativeMerge(req.Root, op.Path, op.Entries, nativeMergeOptions(op, oldByPath))
	if derr != nil {
		change.Error = derr.Error()
		return change
	}
	if nativeSeen[op.Path] {
		continue
	}
	switch {
	case !exists:
		change.WouldCreate = append(change.WouldCreate, op.Path)
	case changed:
		change.WouldUpdate = append(change.WouldUpdate, op.Path)
	}
	nativeSeen[op.Path] = true
}
```

- [ ] **Step 5: Verify engine native tests**

Run:

```bash
go test ./internal/engine -run 'Test(Sync_AppliesCodexNativeFeatureFragment|Plan_NativeFragmentsIdempotentAfterSync)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/engine
git commit -m "feat: apply native fragments in sync and validate"
```

---

## Task 9: End-To-End Workspace Activation, Portable Assets, And Codex Fragments

**Files:**

- Modify: `internal/cli/hierarchy_sync_test.go`
- Modify: `internal/cli/cmd_status.go`
- Modify: `internal/cli/cmd_statusvalidate_test.go`

- [ ] **Step 1: Write failing E2E test for activation-root inheritance**

Add to `internal/cli/hierarchy_sync_test.go`:

```go
func TestSyncInsideActivationRootInheritsWorkspaceCodexFragmentsAndSkipsUser(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ActualReality")
	repo := filepath.Join(ws, "apps", "api")
	mustMkdirAll(t, repo)
	mustWriteFile(t, filepath.Join(repo, ".git"), []byte("gitdir: .git/worktrees/api\n"))

	writeWS(t, home, ".agent-sync.yaml", "version: 1\nscope: user\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, home, ".agents/configs/codex/features/hooks/fragment.yaml", "id: hooks\ntarget: codex\npath: .codex/config.toml\nmerge: toml-key\nlocator: features.hooks\nvisibility: team\ninheritance: descendants\npayload: payload.toml\n")
	writeWS(t, home, ".agents/configs/codex/features/hooks/payload.toml", "[features]\nhooks = false\n")

	writeWS(t, ws, ".agent-sync.yaml", "version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, ws, ".agents/configs/codex/features/hooks/fragment.yaml", "id: hooks\ntarget: codex\npath: .codex/config.toml\nmerge: toml-key\nlocator: features.hooks\npayload: payload.toml\n")
	writeWS(t, ws, ".agents/configs/codex/features/hooks/payload.toml", "[features]\nhooks = true\n")

	writeWS(t, repo, ".agent-sync.yaml", "version: 1\nscope: project\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, repo, ".agents/AGENTS.md", "project instructions\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, errOut)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read project codex config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "hooks = true") {
		t.Fatalf("project config did not inherit workspace fragment:\n%s", text)
	}
	if strings.Contains(text, "hooks = false") {
		t.Fatalf("project config inherited user fragment despite activation root:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plain sync wrote user config, err=%v", err)
	}
}
```

Run:

```bash
go test ./internal/cli -run TestSyncInsideActivationRootInheritsWorkspaceCodexFragmentsAndSkipsUser -count=1
```

Expected: FAIL until Tasks 2, 5, and 8 are wired correctly.

- [ ] **Step 2: Write failing E2E test for workspace skill inheritance**

Add to `internal/cli/hierarchy_sync_test.go`:

```go
func TestSyncInsideActivationRootInheritsWorkspaceSkill(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ActualReality")
	repo := filepath.Join(ws, "apps", "api")
	mustMkdirAll(t, repo)
	mustWriteFile(t, filepath.Join(repo, ".git"), []byte("gitdir: .git/worktrees/api\n"))

	writeWS(t, ws, ".agent-sync.yaml", "version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, ws, ".agents/skills/code-review/SKILL.md", "workspace code review skill\n")

	writeWS(t, repo, ".agent-sync.yaml", "version: 1\nscope: project\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, repo, ".agents/AGENTS.md", "project instructions\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, errOut)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".agents", "skills", "agent-sync-code-review", "SKILL.md"))
	if err != nil {
		t.Fatalf("read inherited skill: %v", err)
	}
	if !strings.Contains(string(data), "workspace code review skill") {
		t.Fatalf("inherited skill content mismatch:\n%s", data)
	}
}
```

Run:

```bash
go test ./internal/cli -run TestSyncInsideActivationRootInheritsWorkspaceSkill -count=1
```

Expected: FAIL until Task 5 resolves portable nodes.

- [ ] **Step 3: Write failing E2E test for generated hooks.json**

Add to `internal/cli/hierarchy_sync_test.go`:

```go
func TestSyncCodexHooksFragmentGeneratesHooksJSON(t *testing.T) {
	home, repo, _ := hierarchyTree(t)
	writeWS(t, repo, ".agents/configs/codex/hooks/pre-tool-policy/fragment.yaml", "id: pre-tool-policy\ntarget: codex\npath: .codex/hooks.json\nmerge: codex-hooks\nlocator: PreToolUse/pre-tool-policy\nsafety: executable\npayload: payload.json\n")
	writeWS(t, repo, ".agents/configs/codex/hooks/pre-tool-policy/payload.json", `{"matcher":"Bash","hooks":[{"type":"command","command":"python3 .codex/hooks/check.py","statusMessage":"Checking Bash command"}]}`)

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, errOut)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"hooks": {`, `"PreToolUse"`, `"statusMessage": "Checking Bash command"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("hooks.json missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "_agent_sync_generated") {
		t.Fatalf("hooks.json should not include agent-sync marker:\n%s", text)
	}
}
```

Run:

```bash
go test ./internal/cli -run TestSyncCodexHooksFragmentGeneratesHooksJSON -count=1
```

Expected: FAIL until native operations are wired.

- [ ] **Step 4: Update status output for workspace level**

In `internal/cli/cmd_status.go`, apply the same selector before rendering read-only labels:

```go
scopes = selectWriteScopes(scopes, false)
```

Status remains a read-only command; this only makes the `[read-only]` label match what a plain `sync` would choose as its single write target.

Add a test in `internal/cli/cmd_statusvalidate_test.go`:

```go
func TestStatusHierarchyShowsWorkspaceActivationRoot(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ActualReality")
	mustMkdirAll(t, ws)
	writeWS(t, ws, ".agent-sync.yaml", "version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\n")
	out, _, err := runStatusInDir(t, ws, home, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "workspace") || strings.Contains(out, "user") {
		t.Fatalf("status output = %q, want workspace and no user scope", out)
	}
}
```

Run:

```bash
go test ./internal/cli -run 'Test(StatusHierarchyShowsWorkspaceActivationRoot|SyncInsideActivationRootInheritsWorkspaceCodexFragmentsAndSkipsUser|SyncInsideActivationRootInheritsWorkspaceSkill|SyncCodexHooksFragmentGeneratesHooksJSON)' -count=1
```

Expected: PASS after wiring.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "test: cover activation-root codex fragment sync"
```

---

## Task 10: Documentation And Verification

**Files:**

- Modify: `docs/adapters/codex.md`
- Modify: `docs/superpowers/specs/2026-07-06-agent-harness-hierarchy-design.md`
- Modify: `AGENTS.md` only if an invariant changed

- [ ] **Step 1: Document Codex native fragment authoring**

Add to `docs/adapters/codex.md`:

```markdown
## Managed Native Fragments

Codex supports the first native-fragment surfaces:

### Feature Flags

Author under `.agents/configs/codex/features/<id>/`:

```yaml
id: hooks
target: codex
path: .codex/config.toml
merge: toml-key
locator: features.hooks
visibility: team
inheritance: descendants
safety: passive
payload: payload.toml
```

```toml
[features]
hooks = true
```

agent-sync preserves existing `config.toml` content and only inserts or replaces the allowlisted `[features].hooks` key.

### Lifecycle Hooks

Author under `.agents/configs/codex/hooks/<id>/`:

```yaml
id: pre-tool-policy
target: codex
path: .codex/hooks.json
merge: codex-hooks
locator: PreToolUse/pre-tool-policy
visibility: team
inheritance: descendants
safety: executable
payload: payload.json
```

```json
{
  "matcher": "Bash",
  "hooks": [
    {
      "type": "command",
      "command": "python3 .codex/hooks/pre_tool_use_policy.py",
      "statusMessage": "Checking Bash command"
    }
  ]
}
```

agent-sync generates `.codex/hooks.json` as an agent-sync-owned file whose ownership is tracked by the ledger, not by private JSON keys. If an unmanaged `.codex/hooks.json` already exists, sync fails closed until the user moves or deletes that file. Codex remains responsible for trusting project-local hooks.
```
```

- [ ] **Step 2: Update design spec with implementation decisions**

In `docs/superpowers/specs/2026-07-06-agent-harness-hierarchy-design.md`, add a short "Implementation Notes" section:

```markdown
## Implementation Notes

The first implementation keeps adapter protocol unchanged. Native fragments are core-owned operations applied by allowlisted harness surfaces after adapter ops are computed.

Codex hooks use ledger-owned generated whole-file ownership for `.codex/hooks.json` rather than array splicing, because Codex's hook array entries do not carry an ignored stable id field that agent-sync can use for safe surgical replacement. This still honors the "no whole-file replacement of user-owned config" rule: unmanaged existing hook files fail closed and are not overwritten, and generated JSON keeps Codex's documented top-level schema.

Existing `compose.cursor-rules-from-user` remains as a transitional opt-in until portable asset resolution replaces it.
```

- [ ] **Step 3: Run focused tests**

Run:

```bash
go test ./internal/manifest ./internal/hierarchy ./internal/harness ./internal/merge ./internal/engine ./internal/cli -count=1
```

Expected: PASS.

- [ ] **Step 4: Run full Go gate**

Run:

```bash
go vet ./... && go test -race ./... && golangci-lint run
```

Expected: PASS.

- [ ] **Step 5: Review diff for invariants**

Run:

```bash
git diff --check
git diff --stat
rg 'os\\.(WriteFile|Create|Rename)' internal docs -n
```

Expected:

- `git diff --check` prints no errors.
- `rg` shows no new workspace writes outside existing accepted wrappers, except test setup writes.
- All production writes still flow through `fsroot`, `merge.ApplyToFile`, `merge.ApplyNativeToFile`, or existing manifest init write paths.

- [ ] **Step 6: Commit**

```bash
git add docs internal AGENTS.md
git commit -m "docs: describe harness hierarchy implementation"
```

---

## Self-Review

Spec coverage:

- User/workspace/project scopes: Tasks 1, 2, and 3.
- Activation root hard stop: Task 2 and Task 9.
- Nested activation roots invalid: Task 2.
- One write target per invocation: preserved by `prepareScope` and hierarchy sync; ancestor layers are materialized read-only in Task 5.
- Native fragments instead of a parallel config language: Tasks 4, 6, and 7.
- Codex current docs reconciliation: Tasks 6, 7, 8, and 10.
- Closest scope wins: Task 5.
- Personal and machine-local inheritance restrictions: Task 5.
- Trust handling for executable hooks: Task 7 emits config only; Task 10 documents that Codex trust remains authoritative.

Risk points to review carefully during execution:

- `merge.NativeKindGeneratedJSON` must refuse unmanaged existing `.codex/hooks.json` before writing.
- Activation roots must remove user scope from discovery entirely, including offer prompts and notices.
- Plain `validate` must see the same resolved hierarchy as plain `sync`; explicit `--workspace` remains intentionally single-scope.
- The first implementation must not extend adapter protocol for native fragments; adapters still emit only portable IR ops.

Final verification command:

```bash
go vet ./... && go test -race ./... && golangci-lint run
```

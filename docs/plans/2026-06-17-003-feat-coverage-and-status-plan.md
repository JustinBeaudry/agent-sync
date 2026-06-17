# Coverage Warnings & Hierarchy Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Warn when a scope emits a node kind a target tool will not read natively at that scope's level, and make `status` hierarchy-aware (list every discovered scope — user/project/directory — with its level, source, and per-target managed-file counts).

**Architecture:** A new pure `internal/coverage` package holds the native-support table (which `(target, kind, level)` combinations a tool reads natively) and an `Analyze` function returning `Warning`s. The `sync` orchestrator (Plan 2) computes warnings per scope from the already-decoded IR and renders them. `status` switches from single-workspace lookup to `hierarchy.Discover` and reports every scope read-only.

**Tech Stack:** Go 1.24+, reuses `internal/hierarchy`, `internal/ir`, `internal/cli`, `internal/ledger`, `internal/report`.

This is Plan 3 of 3 for the hierarchy-aware-manifests design (`docs/brainstorms/2026-06-17-hierarchy-aware-manifests-design.md`), implementing the **Coverage analyzer** (design §Components 3) and the **hierarchy status view**.

---

## Design decisions locked for this plan

1. **Coverage lives in its own package `internal/coverage`, not in `internal/hierarchy`.** The design suggested `internal/hierarchy`, but that package is a pure leaf (only stdlib + `internal/workspace`). Coverage needs `ir.Kind` and hierarchy `Level`; putting it in `hierarchy` would couple the leaf to `ir`. A dedicated package keeps `hierarchy` pure. **This is a deliberate, documented deviation from the design's suggested placement.**

2. **The native-support table is keyed by target NAME** (`"claude"`, `"cursor"`, `"codex"`), not obtained from a live adapter session. Declared outputs/capabilities are only available after an `Initialize` handshake (`InitializeResult`); a synchronous, read-only analyzer cannot pay that cost, and external adapters can't be queried at all. So the table is static data in `internal/coverage`. **This softens the design's "knowledge lives in the adapter" to "knowledge keyed by adapter name in the coverage package" — documented deviation, justified by the no-session constraint and to avoid an `adapter`↔`coverage` import cycle.** Unknown targets default to fully-native (no warnings).

3. **The native-support rule** (the design flagged this for verification — these are the project's documented assumptions, each commented in code, easily correctable):
   - **`project` and `user` levels:** every kind the tool supports is read natively (the tool reads its root `.claude/`, `~/.codex/AGENTS.md`, etc.). No warnings.
   - **`directory` level (nested dirs):** a kind is native only if the tool reads that kind from a nested directory. Confirmed nested-read mechanisms:
     - `claude`: `agents-md` (nested `CLAUDE.md`) → native; `rule`/`command`/`skill`/`mcp-server-entry` → NOT native (Claude reads those from the project/user `.claude/`, not nested ones).
     - `codex`: `agents-md` (nested `AGENTS.md` walk) → native; others → NOT native.
     - `cursor`: `rule` (nested `.cursor/rules`) → native; `agents-md`/`command`/`skill`/`mcp-server-entry` → NOT native.
   - Anything not in the table (unknown target or unknown kind) → native (no false warnings).

4. **Coverage warnings surface in `sync` output** (where the IR is already decoded). `status` shows hierarchy STRUCTURE only and does NOT recompute coverage, to keep it read-only and network-free (computing coverage needs decoded IR, which for a `url` source could require materialization/network). **Documented deviation: design mentioned coverage in `status`; we emit it in `sync` and keep `status` cheap.**

---

## File Structure

- Create: `internal/coverage/coverage.go` — `Warning`, native-support table, `Analyze`.
- Create: `internal/coverage/coverage_test.go` — table-driven analyzer tests.
- Modify: `internal/cli/hierarchy_sync.go` — compute + carry + render coverage warnings per scope.
- Modify: `internal/cli/hierarchy_sync_test.go` — assert warnings surface for a directory-level skill/command.
- Modify: `internal/cli/cmd_status.go` — hierarchy-aware status across all discovered scopes.
- Modify: `internal/cli/cmd_status_test.go` (or the existing status test file) — multi-scope status assertions.

---

## Task 1: Coverage package — types and native-support table

**Files:**
- Create: `internal/coverage/coverage.go`
- Create: `internal/coverage/coverage_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/coverage/coverage_test.go`:

```go
package coverage

import (
	"testing"

	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ir"
)

func TestAnalyzeProjectLevelNoWarnings(t *testing.T) {
	kinds := []ir.Kind{ir.KindSkill, ir.KindCommand, ir.KindRule, ir.KindAgentsMD}
	got := Analyze(hierarchy.LevelProject, kinds, []string{"claude"})
	if len(got) != 0 {
		t.Fatalf("project level should be fully native, got warnings: %+v", got)
	}
}

func TestAnalyzeUserLevelNoWarnings(t *testing.T) {
	kinds := []ir.Kind{ir.KindSkill, ir.KindCommand}
	if got := Analyze(hierarchy.LevelUser, kinds, []string{"claude", "codex"}); len(got) != 0 {
		t.Fatalf("user level should be native, got: %+v", got)
	}
}

func TestAnalyzeDirectoryLevelClaudeSkillWarns(t *testing.T) {
	got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindSkill}, []string{"claude"})
	if len(got) != 1 {
		t.Fatalf("expected 1 warning for claude nested skill, got %d: %+v", len(got), got)
	}
	w := got[0]
	if w.Target != "claude" || w.Kind != ir.KindSkill || w.Level != hierarchy.LevelDirectory {
		t.Errorf("warning fields = %+v, want claude/skill/directory", w)
	}
}

func TestAnalyzeDirectoryLevelClaudeAgentsMDNative(t *testing.T) {
	// Claude reads nested CLAUDE.md, so agents-md at directory level is native.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindAgentsMD}, []string{"claude"}); len(got) != 0 {
		t.Fatalf("claude nested agents-md should be native, got: %+v", got)
	}
}

func TestAnalyzeDirectoryLevelCursorRuleNative(t *testing.T) {
	// Cursor reads nested .cursor/rules, so rule at directory level is native.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindRule}, []string{"cursor"}); len(got) != 0 {
		t.Fatalf("cursor nested rule should be native, got: %+v", got)
	}
	// But cursor does not read nested skills natively.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindSkill}, []string{"cursor"}); len(got) != 1 {
		t.Fatalf("cursor nested skill should warn, got: %+v", got)
	}
}

func TestAnalyzeUnknownTargetNoWarnings(t *testing.T) {
	// An adapter we have no table for must never produce false warnings.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindSkill}, []string{"some-third-party"}); len(got) != 0 {
		t.Fatalf("unknown target must default to native, got: %+v", got)
	}
}

func TestAnalyzeMultipleTargetsAndKindsDeterministic(t *testing.T) {
	got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindSkill, ir.KindCommand}, []string{"claude", "codex"})
	// claude: skill+command warn (2); codex: skill+command warn (2) → 4 total.
	if len(got) != 4 {
		t.Fatalf("expected 4 warnings, got %d: %+v", len(got), got)
	}
	// Deterministic ordering: stable by target, then kind.
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.Target > cur.Target || (prev.Target == cur.Target && prev.Kind > cur.Kind) {
			t.Fatalf("warnings not deterministically ordered: %+v", got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coverage/ -v`
Expected: FAIL — package does not compile (`undefined: Analyze`).

- [ ] **Step 3: Implement**

Create `internal/coverage/coverage.go`:

```go
// Package coverage reports when a scope emits a node kind that a target tool
// will not read natively at that scope's hierarchy level.
//
// agent-sync resolves precedence by emitting each scope to its own filesystem
// location and letting the target tool's native config hierarchy apply it
// (see the hierarchy-aware-manifests design). That only works as far as each
// tool actually reads a given kind at a given level. This package encodes the
// known native-read behavior per target and flags the gaps so users are not
// silently surprised by emitted content that never takes effect.
//
// The native-support table is keyed by target NAME and is static: a read-only
// analyzer cannot run an adapter Initialize handshake to learn declared
// outputs, and external adapters cannot be queried at all. Unknown targets and
// unknown kinds default to native (no false warnings). The directory-level
// entries are the project's documented assumptions about each tool's nested-
// read behavior; correct them here if a tool's behavior is verified to differ.
package coverage

import (
	"sort"

	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ir"
)

// Warning is one (target, kind, level) combination that will be emitted but
// not read natively by the target tool at that level.
type Warning struct {
	Target string
	Kind   ir.Kind
	Level  hierarchy.Level
	Detail string
}

// nativeAtDirectory[target] is the set of kinds the target reads natively from
// a NESTED directory. A kind absent from a target's set is non-native at the
// directory level and warns. project/user levels are always native (the tool
// reads its root config), so they are not represented here.
//
// Documented assumptions (verify against current tool behavior, correct here):
//   - claude reads nested CLAUDE.md (agents-md); it does NOT read rules,
//     commands, skills, or mcp entries from nested .claude/ directories.
//   - codex walks nested AGENTS.md (agents-md); nothing else nested.
//   - cursor reads nested .cursor/rules (rule); nothing else nested.
var nativeAtDirectory = map[string]map[ir.Kind]bool{
	"claude": {ir.KindAgentsMD: true},
	"codex":  {ir.KindAgentsMD: true},
	"cursor": {ir.KindRule: true},
}

// known reports whether we have a native-support table for target. Unknown
// targets default to fully native (no warnings).
func known(target string) bool {
	_, ok := nativeAtDirectory[target]
	return ok
}

// Analyze returns the coverage warnings for emitting the given kinds to the
// given targets at the given level. Results are deterministically ordered by
// target then kind. project and user levels never warn; only the directory
// level (nested scopes) can produce gaps. Targets with no table never warn.
func Analyze(level hierarchy.Level, kinds []ir.Kind, targets []string) []Warning {
	if level != hierarchy.LevelDirectory {
		return nil
	}
	var out []Warning
	for _, target := range targets {
		if !known(target) {
			continue
		}
		nativeKinds := nativeAtDirectory[target]
		for _, k := range kinds {
			if nativeKinds[k] {
				continue
			}
			out = append(out, Warning{
				Target: target,
				Kind:   k,
				Level:  level,
				Detail: target + " does not read " + string(k) + " from a nested directory; this will not take effect until per-tool runtime mapping is added",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/coverage/ -v`
Expected: PASS (7 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/coverage/
git commit -m "feat(coverage): native-support analyzer for hierarchy levels"
```

---

## Task 2: Surface coverage warnings in `sync`

**Files:**
- Modify: `internal/cli/hierarchy_sync.go`
- Modify: `internal/cli/hierarchy_sync_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/cli/hierarchy_sync_test.go`, add `TestHierarchySyncEmitsCoverageWarning`. Build a tree where a DIRECTORY-level scope (nested under the git root) emits a `skill` for target `claude`. Run the orchestrator. Assert the directory scope's outcome carries a coverage warning naming `claude`/`skill`, and the project scope (same skill, project level) carries none. Mirror the existing harness in the file (`hierarchyTree`, `newTestRuntime`, `resolveHome` seam).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestHierarchySyncEmitsCoverageWarning -v`
Expected: FAIL — `scopeOutcome` has no warnings field / not computed.

- [ ] **Step 3: Implement**

In `internal/cli/hierarchy_sync.go`:
- Add a field to `scopeOutcome`: `Warnings []coverage.Warning`.
- In `runHierarchySync`, after a successful `prepareScope` and before/after `engine.Sync`, compute `coverage.Analyze(sc.Level, kindsOf(prep.Request.Nodes), prep.Request.Targets)` and store on the outcome. Add a small helper `kindsOf(nodes []ir.Node) []ir.Kind` returning the distinct kinds present (dedup). Compute warnings even when the scope's sync fails? No — only compute when the scope actually emitted (after a non-error prepare); if prepare failed there are no nodes. Compute after `engine.Sync` regardless of sync error is fine since nodes came from prepare; simplest: compute right after a successful prepare.
- In `renderHierarchyText`, after each scope's summary/error, print any warnings as `  warning: <Detail>` lines (indented under the scope header).
- In the JSON aggregate (`hierarchyScopeJSON`), add `Warnings []coverage.Warning` with a `json:"coverage_warnings,omitempty"` tag.
- Add the `coverage` import.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cli/ -run 'TestHierarchySync|TestRunHierarchySync|TestRenderHierarchy' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/hierarchy_sync.go internal/cli/hierarchy_sync_test.go
git commit -m "feat(cli): surface coverage warnings in hierarchy sync output"
```

---

## Task 3: Hierarchy-aware `status`

**Files:**
- Modify: `internal/cli/cmd_status.go`
- Modify: the existing status test file (`internal/cli/cmd_statusvalidate_test.go` or wherever status is tested — find it first)

- [ ] **Step 1: Write the failing test**

Read the current status test(s) first. Add `TestStatusShowsHierarchy`: temp tree with user (home) + project (git root) + directory manifests; run `status` with cwd in the nested dir and `resolveHome` pointed at temp home. Assert the output lists THREE scopes with their levels (`user`, `project`, `directory`) and roots, and that the user scope is marked read-only / not emitted. For a scope that has been synced (seed a ledger under its root), assert its managed-file count shows; for an unsynced scope assert `untracked`. Keep assertions tolerant of exact formatting (match on level labels + root paths + counts), not byte-exact layout.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestStatusShowsHierarchy -v`
Expected: FAIL (status is still single-scope).

- [ ] **Step 3: Implement**

Rework `internal/cli/cmd_status.go` RunE:
- When `rc.Flags.Workspace == ""`, use `hierarchy.Discover(cwd, hierarchy.Options{Home: home})` (home via the `resolveHome` seam from Plan 2; note the user scope comes back with `Emit=false` and IS listed). When `--workspace` is set, keep today's single-scope status.
- Build a hierarchy status document: for each discovered scope, load its manifest (`manifest.LoadFile`) for the source string (`sourceOf`), open its root read-only via `fsroot.OpenWorkspaceRoot(scope.Root)`, and for each manifest target load that scope's ledger (`ledger.Load(root, target)`) to get managed counts; close the root. Record level (`scope.Level.String()`), root, source, emit flag, and per-target counts. A manifest that fails to load for a scope is reported as an error line for that scope (do not abort the whole status — mirror continue-and-report).
- Extend `statusReport`/`targetStatusEntry` (or add a `scopes []scopeStatus` document) and `renderStatusText` to print per-scope blocks: `level (root) [read-only]` then the existing per-target lines. Keep the JSON output a superset (add a `scopes` array; keep existing top-level fields for the single-scope path or when only one scope exists).
- Opening a root to READ ledgers is allowed (status performs no staging/swap). Do not call the engine.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cli/ -run 'TestStatus' -v`
Expected: PASS (new hierarchy test + existing status tests; update existing status tests only if the output shape legitimately changed, and preserve their intent).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cmd_status.go internal/cli/*status*_test.go
git commit -m "feat(cli): hierarchy-aware status across all discovered scopes"
```

---

## Task 4: Full verification gate

**Files:** none.

- [ ] **Step 1:** `go test -race -cover ./internal/coverage/... ./internal/cli/... ./internal/hierarchy/...` → PASS.
- [ ] **Step 2:** `go vet ./... && golangci-lint run` → clean.
- [ ] **Step 3:** `go build ./... && go test ./...` → all packages PASS.
- [ ] **Step 4:** Manual smoke: a project+nested tree where the nested scope emits a `claude` skill → `sync` prints a coverage warning for the nested skill, and the project scope does not; `status` lists user(read-only)/project/directory scopes.
- [ ] **Step 5:** Commit any lint/race fixes (skip if none):

```bash
git add -A
git commit -m "test: satisfy race detector and linters for coverage + status"
```

---

## Self-Review Notes

- **Spec coverage:** Implements design §Components 3 (coverage analyzer — as `internal/coverage` rather than `internal/hierarchy`, documented deviation) and the hierarchy `status` view. Coverage warnings surface in `sync` rather than `status` (documented deviation to keep status read-only/network-free).
- **Honesty about the matrix:** the directory-level native-support entries are the project's documented assumptions (claude/codex nested prose, cursor nested rules); unknown targets/kinds default to native so there are never false warnings. Correcting an entry is a one-line table edit. This matches the design's Dependencies/Assumptions note that per-tool behavior must be verified.
- **Type consistency:** `coverage.Warning{Target, Kind, Level, Detail}` and `Analyze(level, kinds, targets)` are used identically in Task 1 tests, Task 2 wiring, and the JSON shape. `kindsOf` dedups node kinds.
- **No engine changes; no network in status.** Coverage is pure; status only reads manifests + ledgers.
- **Layering:** `internal/coverage` imports `ir` + `hierarchy` only; no `adapter` import (targets are plain names), avoiding a cycle.
```

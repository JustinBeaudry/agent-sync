# Multi-Scope Sync Orchestration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make `agent-sync sync` emit every manifest in the discovered hierarchy (cwd → project root), each to its own filesystem scope, with a `--user` flag to also emit the user-home scope, and continue-and-report semantics across scopes.

**Architecture:** Reuse the existing per-scope `engine.Sync` unchanged. Add a CLI-layer orchestrator that uses `internal/hierarchy.Discover` (Plan 1) to find the emit scopes, builds one `engine.Request` per scope (extracted from today's `prepareEngine`), runs `engine.Sync` against each scope's own `fsroot` root, and aggregates the per-scope `report.Summary` results. Per-scope ledgers already work because each `engine.Sync` runs against its own root.

**Tech Stack:** Go 1.24+, cobra, reuses `internal/hierarchy`, `internal/engine`, `internal/report`, `internal/workspace`, `internal/fsroot`.

This is Plan 2 of 3 for the hierarchy-aware-manifests design (`docs/brainstorms/2026-06-17-hierarchy-aware-manifests-design.md`). It implements the **multi-root emit orchestration** and `--user`. The **coverage analyzer / status hierarchy view** is Plan 3.

---

## Context the implementer needs

Read these before starting; they are the integration points:

- `internal/cli/setup.go` — `prepareEngine(ctx, rc, now) (prepared, error)` and the `prepared` struct. It does: `workspace.Find` → `manifest.LoadFile` → `fsroot.OpenWorkspaceRoot` → `materialize` → `adapter.DiscoverAdapters` → build `engine.Request`. The new per-scope builder is this same body, parameterized by an explicit `(root, manifestPath)` instead of `workspace.Find`.
- `internal/cli/cmd_sync.go` — the current single-scope `sync` RunE that calls `prepareEngine`, then `engine.Sync`, then `renderSummary`. This is what changes.
- `internal/cli/materialize.go` — `materialize(ctx, m, materializeOptions{Root: root})`; the `local_dir` path reads the working tree under `root`. Already per-scope-correct.
- `internal/hierarchy` (Plan 1) — `Discover(cwd string, Options{Home, IncludeUser, MaxHops}) ([]Scope, error)`; `Scope{Root, ManifestPath, Level, Emit}`; `Level.String()`. Scopes return ordered user→project→directory; `Emit` is true for project/directory and for user only when `IncludeUser`.
- `internal/report` — `Summary{Workspace, Commit, GeneratedAt, Mode, Targets, Outcome}`, `Outcome.ExitCode`, `RenderText(Summary) string`, `MarshalJSON(Summary) ([]byte,error)`.
- Existing CLI test patterns: `internal/cli/cmd_sync_localdir_test.go` and `internal/cli/cmd_sync_test.go` show how to drive the `sync` command end-to-end against a temp workspace with a `local_dir` manifest. Mirror those patterns (temp dir, write `.agent-sync.yaml`, run the root command with args, assert emitted files). The implementer MUST read those two files and follow their helpers rather than inventing a new harness.

**Scope of this plan:** only `sync` becomes hierarchy-aware. `validate` and `status` stay single-scope (nearest workspace) in this plan; `status` becomes hierarchy-aware in Plan 3. Note this in code comments where relevant.

**`--workspace` interaction:** when the global `--workspace` flag is explicitly set, `sync` keeps today's single-scope behavior (that explicit override wins and disables hierarchy discovery). Hierarchy discovery only runs when no explicit workspace override is given.

---

## File Structure

- Modify: `internal/cli/setup.go` — extract `prepareScope(ctx, rc, root, manifestPath, now) (prepared, error)`; reimplement `prepareEngine` to call it after `workspace.Find`.
- Create: `internal/cli/hierarchy_sync.go` — the orchestrator (`scopeOutcome`, `runHierarchySync`, aggregate rendering helpers, `homeDir` resolution).
- Modify: `internal/cli/cmd_sync.go` — RunE chooses hierarchy vs single-scope; add `--user` flag.
- Create: `internal/cli/hierarchy_sync_test.go` — orchestration + rendering integration tests.

`hierarchy_sync.go` is a new file (not bolted onto `cmd_sync.go`) so the orchestration logic has one focused home and `cmd_sync.go` stays a thin command definition.

---

## Task 1: Extract `prepareScope` from `prepareEngine`

**Files:**
- Modify: `internal/cli/setup.go`

- [ ] **Step 1: Read the current `prepareEngine` and `prepared`** in `internal/cli/setup.go`.

- [ ] **Step 2: Extract the per-scope builder**

Refactor so the body that builds a `prepared` from a concrete `(root, manifestPath)` lives in a new function, and `prepareEngine` resolves the workspace then delegates:

```go
// prepareScope builds the per-invocation engine inputs for one already-located
// scope: it loads the manifest at manifestPath, opens an fsroot at scopeRoot,
// materializes the canonical IR, discovers adapters, and assembles an
// engine.Request. The caller must Close the returned root (via prepared.Close).
//
// This is the multi-scope-safe core: the hierarchy orchestrator calls it once
// per discovered scope. prepareEngine is the single-scope wrapper used by
// validate (and by sync when an explicit --workspace override is in effect).
func prepareScope(ctx context.Context, rc *runtimeContext, scopeRoot, manifestPath string, now time.Time) (prepared, error) {
	// (moved verbatim from prepareEngine, but using manifestPath/scopeRoot
	// instead of workspace.Find: manifest.LoadFile(manifestPath, ...),
	// fsroot.OpenWorkspaceRoot(scopeRoot), materialize with Root=root,
	// adapter.DiscoverAdapters, build engine.Request with WorkspacePath=scopeRoot.)
}

func prepareEngine(ctx context.Context, rc *runtimeContext, now time.Time) (prepared, error) {
	if rc == nil {
		return prepared{}, errors.New("cli: prepareEngine called with nil runtime context")
	}
	flags := rc.Flags
	ws, err := workspace.Find(flags.Workspace, workspace.Options{Workspace: flags.Workspace})
	if err != nil {
		return prepared{}, fmt.Errorf("locate workspace: %w", err)
	}
	return prepareScope(ctx, rc, ws.Root, ws.ManifestPath, now)
}
```

Keep `prepared.Workspace` populated for the single-scope path if other code reads it; for `prepareScope` set `Workspace` to a minimal `&workspace.Workspace{Root: scopeRoot, ManifestPath: manifestPath}` (check whether any caller depends on `prepared.Workspace`; if none, the field may be left as today — do not break callers).

- [ ] **Step 3: Verify nothing else broke**

Run: `go build ./... && go test ./internal/cli/ -run 'TestSync|TestValidate|Prepare' -v`
Expected: PASS — existing sync/validate tests still green (the refactor is behavior-preserving).

- [ ] **Step 4: Commit**

```bash
git add internal/cli/setup.go
git commit -m "refactor(cli): extract prepareScope from prepareEngine"
```

---

## Task 2: Hierarchy sync orchestrator

**Files:**
- Create: `internal/cli/hierarchy_sync.go`
- Create: `internal/cli/hierarchy_sync_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/cli/hierarchy_sync_test.go`. Read `internal/cli/cmd_sync_localdir_test.go` first and reuse its workspace/manifest helpers and command-execution harness. The test builds a temp tree:

```
home/
  repo/            (.git here → project root)
    .agent-sync.yaml         (local_dir: .agents, targets: [claude])
    .agents/skills/proj-skill/SKILL.md
    packages/api/
      .agent-sync.yaml       (local_dir: .agents, targets: [claude])
      .agents/skills/api-skill/SKILL.md
```

Test `TestRunHierarchySyncEmitsEachScope`: run the orchestrator (or the `sync` command — match whichever the harness in the existing test file uses) with cwd = `home/repo/packages/api`, `IncludeUser=false`. Assert:
- Two scope outcomes returned, ordered project then directory.
- The project scope emitted `repo/.claude/skills/agent-sync-proj-skill/...` (i.e. files exist under `repo/.claude`).
- The directory scope emitted files under `repo/packages/api/.claude`.
- No `.claude` was written under `home/` (the user scope was not emitted).
- Aggregate exit code is 0.

Use the existing helper to set `AGENT_SYNC_WORKSPACE_STOP_AT` or pass `Options.Home` so discovery does not walk into the real home directory during tests. Inject `Home: <tempHome>` into discovery.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestRunHierarchySync -v`
Expected: FAIL — `undefined: runHierarchySync` (or the orchestrator entry the test calls).

- [ ] **Step 3: Implement the orchestrator**

Create `internal/cli/hierarchy_sync.go`:

```go
package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/report"
)

// scopeOutcome is one discovered scope's sync result. Exactly one of Summary
// (Err == nil) or Err (a prepare/sync failure for this scope) is meaningful.
type scopeOutcome struct {
	Scope   hierarchy.Scope
	Summary report.Summary
	Err     error
}

// hierarchySyncOptions carries the per-run knobs the orchestrator applies to
// every scope's engine.Request, plus the user-scope toggle.
type hierarchySyncOptions struct {
	IncludeUser bool
	EngineOpts  engine.Options // mode, adopt, target filter, expect-deletions, logger, now
}

// runHierarchySync discovers the emit scopes from cwd and runs engine.Sync
// against each, in order. A scope whose prepare or sync fails is recorded in
// its scopeOutcome.Err and the run continues (continue-and-report). Discovery
// failure aborts the whole run (the scope set is indeterminate).
func runHierarchySync(ctx context.Context, rc *runtimeContext, cwd, home string, opts hierarchySyncOptions, now time.Time) ([]scopeOutcome, error) {
	scopes, err := hierarchy.Discover(cwd, hierarchy.Options{Home: home, IncludeUser: opts.IncludeUser})
	if err != nil {
		return nil, fmt.Errorf("discover hierarchy: %w", err)
	}

	var outcomes []scopeOutcome
	for _, sc := range scopes {
		if !sc.Emit {
			continue // read-only (user) scope shown elsewhere, not emitted
		}
		out := scopeOutcome{Scope: sc}
		prep, perr := prepareScope(ctx, rc, sc.Root, sc.ManifestPath, now)
		if perr != nil {
			out.Err = perr
			outcomes = append(outcomes, out)
			continue // continue-and-report
		}
		req := prep.Request
		req.Options = opts.EngineOpts
		summary, serr := engine.Sync(ctx, req)
		prep.Close()
		if serr != nil {
			out.Err = serr
		} else {
			out.Summary = summary
		}
		outcomes = append(outcomes, out)
	}
	return outcomes, nil
}

// homeDir resolves the user's home directory for discovery. Injectable via the
// returned value so tests can override; production uses os.UserHomeDir.
func homeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cli: resolve home: %w", err)
	}
	return h, nil
}
```

Note: `engine.Options` is applied per scope. The orchestrator sets `req.Options = opts.EngineOpts` (the same per-run policy for every scope). If the test harness drives the cobra command rather than `runHierarchySync` directly, also complete Task 3 wiring before the test passes — in that case split this test to call `runHierarchySync` directly with a hand-built `runtimeContext`, mirroring how other `internal/cli` tests construct `rc`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestRunHierarchySync -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/hierarchy_sync.go internal/cli/hierarchy_sync_test.go
git commit -m "feat(cli): add multi-scope hierarchy sync orchestrator"
```

---

## Task 3: Aggregate rendering + exit code

**Files:**
- Modify: `internal/cli/hierarchy_sync.go`
- Modify: `internal/cli/hierarchy_sync_test.go`

- [ ] **Step 1: Write the failing test**

Add `TestRenderHierarchyOutcomes`:
- Given two `scopeOutcome`s, one OK summary and one with `Err`, the text renderer prints a per-scope header (`level` + root path) followed by either the scope's `report.RenderText` or the error line.
- `hierarchyExitCode` returns non-zero when any outcome has `Err != nil` or any `Summary.Outcome.ExitCode != 0`, and 0 when all scopes are clean.

```go
func TestHierarchyExitCode(t *testing.T) {
	clean := scopeOutcome{Summary: report.Summary{Outcome: report.Outcome{ExitCode: 0}}}
	failedSync := scopeOutcome{Summary: report.Summary{Outcome: report.Outcome{ExitCode: 1}}}
	prepareErr := scopeOutcome{Err: errors.New("boom")}

	if got := hierarchyExitCode([]scopeOutcome{clean, clean}); got != 0 {
		t.Errorf("all-clean exit = %d, want 0", got)
	}
	if got := hierarchyExitCode([]scopeOutcome{clean, failedSync}); got == 0 {
		t.Error("a failed-sync scope must yield non-zero exit")
	}
	if got := hierarchyExitCode([]scopeOutcome{clean, prepareErr}); got == 0 {
		t.Error("a prepare-error scope must yield non-zero exit")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestHierarchyExitCode -v`
Expected: FAIL — `undefined: hierarchyExitCode`.

- [ ] **Step 3: Implement rendering + exit code**

Add to `internal/cli/hierarchy_sync.go`:

```go
// hierarchyExitCode is non-zero when any scope failed to prepare/sync or any
// scope's own summary reported a non-zero exit (continue-and-report: one bad
// scope fails the run without blocking the others' emit).
func hierarchyExitCode(outcomes []scopeOutcome) int {
	for _, o := range outcomes {
		if o.Err != nil || o.Summary.Outcome.ExitCode != 0 {
			return 1
		}
	}
	return 0
}

// renderHierarchyText writes a per-scope block: a header naming the level and
// root, then either the scope's report text or its error. Implement using
// report.RenderText for the success case; print "ERROR: <err>" for the failure
// case. Mirror the spacing/format of renderSummary in cmd_sync.go.
func renderHierarchyText(w io.Writer, outcomes []scopeOutcome) error { /* ... */ }
```

For JSON output define a small aggregate type and marshal it:

```go
type hierarchyScopeJSON struct {
	Root    string          `json:"root"`
	Level   string          `json:"level"`
	Summary *report.Summary `json:"summary,omitempty"`
	Error   string          `json:"error,omitempty"`
}
```

Use `report.MarshalJSON` semantics for the embedded summary (or marshal the `report.Summary` via the standard library — it has JSON tags). Add the needed imports (`encoding/json`, `io`, `errors`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestHierarchyExitCode -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/hierarchy_sync.go internal/cli/hierarchy_sync_test.go
git commit -m "feat(cli): aggregate hierarchy sync rendering and exit code"
```

---

## Task 4: Wire `sync` to the orchestrator and add `--user`

**Files:**
- Modify: `internal/cli/cmd_sync.go`
- Modify: `internal/cli/hierarchy_sync_test.go`

- [ ] **Step 1: Write the failing test**

Add `TestSyncCommandHierarchy` and `TestSyncCommandUserFlag` driving the real `sync` cobra command (mirror `cmd_sync_localdir_test.go`'s command harness):
- `TestSyncCommandHierarchy`: temp tree with project + directory manifests (as in Task 2), run `sync` with cwd in the nested dir and discovery `Home` pointed at the temp home (inject via the existing `AGENT_SYNC_WORKSPACE_STOP_AT`-style mechanism or a test seam). Assert both scopes emitted and exit 0.
- `TestSyncCommandUserFlag`: add a user manifest at temp home; run `sync --user`; assert the user scope ALSO emitted (`home/.claude` exists). Run `sync` without `--user`; assert `home/.claude` is NOT created.

If driving the real command requires overriding the home directory, add a minimal test seam: a package-level `var resolveHome = homeDir` that tests can swap, OR thread an explicit `--home` hidden flag. Choose the seam that fits existing patterns; document it in a comment.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestSyncCommandHierarchy|TestSyncCommandUserFlag' -v`
Expected: FAIL (flag/behavior not present yet).

- [ ] **Step 3: Implement the wiring**

In `internal/cli/cmd_sync.go`:
- Add `var userScope bool` and `cmd.Flags().BoolVar(&userScope, "user", false, "also sync the user-level (~) manifest")`.
- In RunE: when `rc.Flags.Workspace == ""` (no explicit override), use the hierarchy path:
  - resolve `cwd` via `os.Getwd()` and `home` via the home seam;
  - build `engine.Options` exactly as today (mode, adopt, target filter, expect-deletions, logger, now);
  - call `runHierarchySync(ctx, rc, cwd, home, hierarchySyncOptions{IncludeUser: userScope, EngineOpts: opts}, now)`;
  - render via `renderHierarchyText` / JSON aggregate per `rc.Access.Output`;
  - return an `exitError` with `hierarchyExitCode(outcomes)` when non-zero.
- When `rc.Flags.Workspace != ""`, keep today's single-scope path (`prepareEngine` → `engine.Sync` → `renderSummary`) unchanged, and if `--user` was passed alongside `--workspace`, return a clear error that the two are mutually exclusive.
- Preserve the existing `--post-merge` / `anyBlocked` behavior. For the hierarchy path, apply the post-merge skip check across the union of scope summaries (if any scope blocked, write the skip marker against that scope's root and exit 0). Keep this behavior-equivalent to today for the single-scope case; for the hierarchy case it is acceptable to evaluate post-merge per scope.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cli/ -run 'TestSyncCommand' -v`
Expected: PASS (both new tests and existing sync command tests).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cmd_sync.go internal/cli/hierarchy_sync_test.go
git commit -m "feat(cli): sync emits the manifest hierarchy; add --user"
```

---

## Task 5: Full verification gate

**Files:** none (verification only).

- [ ] **Step 1: Race + coverage**

Run: `go test -race -cover ./internal/cli/... ./internal/hierarchy/...`
Expected: PASS; coverage reported. Investigate any drop below the existing `internal/cli` baseline.

- [ ] **Step 2: Static analysis**

Run: `go vet ./... && golangci-lint run`
Expected: no findings.

- [ ] **Step 3: Full build + whole suite**

Run: `go build ./... && go test ./...`
Expected: PASS across the repo (the change is additive to `sync`; validate/status unchanged).

- [ ] **Step 4: Manual smoke (optional but recommended)**

In a scratch temp tree with a project + nested manifest, run the built binary's `sync` and confirm both scopes' `.claude` trees appear and re-running is idempotent (no drift).

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "test(cli): satisfy race detector and linters for hierarchy sync"
```

(Skip if there were no fixes.)

---

## Self-Review Notes

- **Spec coverage:** Implements design §Components 5 (multi-root emitter — as a CLI orchestration loop over `engine.Sync`), §Data Flow steps 1/2/5/6 for `sync`, §Error Handling "continue-and-report" + "discovery fails → abort" + "`--user` write to `~` only when targeted", and the `--workspace` single-scope back-compat path.
- **Deliberately deferred to Plan 3:** coverage warnings, adapter `NativeAt`, the whole-hierarchy `status` view, and making `validate` hierarchy-aware. `status`/`validate` remain single-scope here.
- **Reused, not rebuilt:** `engine.Sync`, `report.RenderText`/`MarshalJSON`, `materialize`, per-scope ledgers (each scope's `engine.Sync` writes its own `.agent-sync/state/<target>.json`). The engine is not modified.
- **Type consistency:** `scopeOutcome`, `hierarchySyncOptions`, `runHierarchySync`, `hierarchyExitCode`, `renderHierarchyText` are referenced consistently across Tasks 2–4. `prepareScope`'s signature in Task 1 matches its call in Task 2.
- **Test seam:** home-directory injection for tests is via a swappable `resolveHome`/`Options.Home` rather than touching the real `~`; this keeps the suite hermetic.
```

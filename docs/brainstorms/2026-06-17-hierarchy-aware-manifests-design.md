---
date: 2026-06-17
topic: hierarchy-aware-manifests
---

# Hierarchy-Aware Manifests — Design

## Summary

agent-sync becomes hierarchy-aware. Manifests may live at multiple filesystem
scopes — user (`~`), intermediate directories, and project — discovered by
walking up from the current directory. Each manifest compiles its single
canonical source and emits to **its own** scope; precedence is then resolved by
each target tool's native config hierarchy (Claude Code, Codex's `AGENTS.md`
walk, Cursor's nested rules), never by agent-sync merging anything.

## Problem Frame

Today a workspace has exactly one canonical source and the three source kinds
(`url`, `local_path`, `local_dir`) are mutually exclusive (see
`internal/manifest/load.go` — "canonical source must set exactly one of url,
local_path, or local_dir"). There is no way to have an org/shared baseline and a
repo-specific layer apply together: `ir.Decode`'s `markSeen` hard-errors on any
duplicate `(kind, id)`, and the prior in-repo `local_dir` work explicitly
deferred overlay-on-remote.

The real need is broader than "remote base + in-repo overlay." It is
**hierarchy**: a personal baseline that applies everywhere, optional
intermediate-directory layers, and a repo-specific layer — with the deeper
(more specific) layer taking precedence, the same way Claude Code, Codex, and
Cursor already resolve their own layered config at read time. agent-sync's job
is to place compiled output at the right scope and let each tool do the
layering it already knows how to do.

## Key Decisions

- **Hierarchy, resolved by the tool — not by agent-sync.** Each manifest emits
  to its own filesystem scope; the target tool resolves precedence natively at
  read time. agent-sync never merges across levels and never rewrites file
  contents to bake in precedence.

- **One canonical source per manifest (unchanged).** The existing "exactly one
  of `url`/`local_path`/`local_dir`" rule stays. Org-vs-repo is expressed by
  *level* (e.g. a user-level manifest whose canonical is the org's remote git,
  plus a project-level manifest), not by combining sources inside one manifest.
  The earlier "base + overlay in one manifest" idea is dropped in favor of
  levels.

- **Walk-up auto-discovery; deeper wins.** From cwd, walk up collecting
  manifests. Precedence is implied by directory depth; no registration and no
  `extends` chain.

- **Project root = nearest `.git` ancestor.** Default `sync` emits scopes from
  cwd up to that root. Discovery still reads above it (up to `~`) so `status`
  can show the user-level manifest, but a plain repo sync never emits there.

- **User level is explicit.** The `~` scope is emitted only when `--user` is
  passed; a repo sync can never silently write into the home directory.

- **Precedence order is descriptive, not enforced.** The hierarchy plan records
  scope order for `status` and coverage reporting only. agent-sync places files;
  the tool enforces precedence. Nothing flows between scopes at emit time.

- **Coverage gaps are surfaced, not silently emitted.** When a level emits a
  kind a tool won't read natively at that level, agent-sync still emits the
  files but warns, naming the `(tool × kind × level)` combinations that won't
  take effect until the deferred fallback.

- **`internal/fsroot` evolves to multiple roots.** One `os.Root` per scope root,
  each enforcing its own boundary. Invariant #1 ("all writes go through fsroot")
  holds — there are simply multiple instances. AGENTS.md's "do not reach outside
  the single workspace" framing must be updated to "outside a scope root."

## Architecture

A new **hierarchy layer** sits above today's engine. The engine is unchanged in
what it does per scope.

```
cwd
 │
 ▼
HIERARCHY LAYER (new)
  Discovery ──▶ per-scope Compile (reuses ir.Decode) ──▶ Coverage analysis
       └──────────────────────────┬───────────────────────────┘
                                   ▼
                       Hierarchy Plan (ordered ScopePlans + warnings)
                                   │
                                   ▼
MULTI-ROOT EMITTER (engine, extended)
  for each ScopePlan:
    open fsroot at scope.Root ──▶ EXISTING engine (emit → ledger)
```

**Contract:** the hierarchy layer owns **discovery, scope ordering, and coverage
analysis only**. The existing engine still owns **compile, emit, and ledger**,
run once per scope against its own `fsroot` root and its own ledger. This adds a
layer; it does not rewrite the engine.

## Components

### 1. Discovery — `internal/hierarchy` (new)

- **Does:** from cwd, walk up to the nearest `.git` ancestor collecting
  manifests (the *emit* scopes); continue up to `~` to find the user manifest
  (read-only for `status` unless `--user`). Assigns each scope a level:
  `project` (at git root), `directory` (below it), `user` (at `~`).
- **Interface:** `Discover(cwd) → ([]Scope, error)`, where
  `Scope = { Root, Level, ManifestPath }`, ordered shallow→deep.
- **Depends on:** filesystem walk only. No engine, no fsroot.
- **Edge:** outside a git repo, the default scope set is just cwd's manifest
  (plus `--user` on request).

### 2. Per-scope compile — reuses `ir.Decode` (unchanged)

For each scope, load its one canonical source and decode to `[]ir.Node`. This is
exactly today's path; within-scope duplicate `(kind, id)` still hard-errors here.

### 3. Coverage analyzer — `internal/hierarchy` (new)

- **Does:** for each scope's node kinds × target adapters × the scope's level,
  determine whether the tool reads that kind natively at that level → produce
  warnings for the gaps.
- **Design choice:** that knowledge lives **in the adapter**, not the analyzer.
  The adapter capability declaration is extended with
  `NativeAt(kind, level) → bool`, keeping tool-specific config-reading rules in
  the tool's adapter, consistent with the existing architecture.
- **Interface:** `Analyze(plan, registry) → []CoverageWarning`.

### 4. Hierarchy plan — data structure

- `HierarchyPlan = { Scopes: []ScopePlan }`
- `ScopePlan = { Root, Level, Nodes, Skills, CoverageWarnings }`
- Pure data, no behavior. This is where the deferred runtime-mapping fallback
  later plugs in as an alternate emit strategy.

### 5. Multi-root emitter — `internal/engine` (extended)

For each `ScopePlan`, open an `fsroot` rooted at `scope.Root`, then invoke the
**existing** `applyTarget` emit→ledger pipeline. Each scope gets its own
`.agent-sync/state/<target>.json` ledger; orphan detection stays per-scope.

### 6. fsroot multi-root — `internal/fsroot` (extended)

Change from one root per process to one `os.Root` per scope root, each enforcing
its own boundary. The `~` root is only ever opened on explicit `--user`.

### 7. CLI surface — `internal/cli` (extended)

- `agent-sync sync` → emit cwd→git-root scopes.
- `agent-sync sync --user` → include/target the user scope.
- `agent-sync status` → whole-hierarchy view (incl. read-only user level) with
  precedence order and coverage warnings.

## Data Flow

A single `agent-sync sync` from some directory:

1. **Discovery** — walk up to nearest `.git` ancestor → emit scopes; walk up to
   `~` → user scope (tagged read-only unless `--user`).
2. **Per-scope compile** — load manifest → resolve canonical source →
   `ir.Decode` → `[]Node`. Base-source gates (pinning, trust/TOFU,
   offline-strict) and the `local_dir` exemptions apply **per scope**, exactly
   as for a single source today.
3. **Coverage analysis** — per scope: nodes × adapters × level →
   `adapter.NativeAt(kind, level)` → `CoverageWarnings`.
4. **Assemble** `HierarchyPlan` (ordered `ScopePlan`s + warnings).
5. **Multi-root emit** — per `ScopePlan`: open fsroot at `scope.Root` → existing
   `applyTarget` (drift scan → lock → stage → two-rename swap → write ledger →
   delete orphans). Each scope independent: own fsroot, own ledger.
6. **Report** coverage warnings (and precedence order on `status`).

Steps 2 and 5 are per-scope and isolated — the same code paths agent-sync runs
today, invoked once per scope against different roots. Nothing flows *between*
scopes except into the descriptive plan.

`status` runs steps 1–4 only and renders the whole hierarchy including the
read-only user level — no emit, no fsroot opened.

## Error Handling

| Failure | Behavior |
|---|---|
| Within-scope duplicate `(kind, id)` | Hard error in that scope's compile (unchanged). |
| Base-source gate fails (offline+uncached, trust/TOFU mismatch, floating-local) | Fails *that scope* with today's existing errors. `local_dir` scopes stay exempt. |
| Discovery fails (e.g. unreadable dir) | Abort the whole run — the scope set is indeterminate, so a partial hierarchy would mislead. |
| A scope's compile or emit fails | **Continue-and-report:** skip that scope, still emit the others, end non-zero with an aggregated report naming each failure. |
| Coverage gaps | Warnings, never errors. |
| Orphaned scope (manifest deleted in a dir not revisited) | Not an error; the dead scope's ledger is reconciled next time you sync at/above it. `status` flags it if discovery sees it. |
| `--user` write to `~` | The user root is opened only when `--user` is passed; a plain repo sync can never open it. |

Per-scope isolation is *safe* rather than half-merged: each scope has its own
`fsroot`, its own atomic two-rename swap (invariant #6 holds per scope), and its
own ledger. A failure in one scope cannot corrupt another, and because the tool
resolves precedence at read time, a failed shallow scope does not leave a deeper
one inconsistent — the tool reads whatever successfully landed.

## Testing

Go standards: table-driven with `t.Run`, `testify`, `-race`, `t.Cleanup`,
temp-dir trees via `t.TempDir`. No DB, so no testcontainers.

**Unit**

- **Discovery** — table-driven over synthetic dir trees: cwd at git root / in a
  nested dir / multiple intermediate manifests / no `.git` fallback / user
  manifest at `~`. Assert the ordered scope set and each scope's level.
- **Coverage analyzer** — table-driven over `(kind × level × adapter)` →
  expected warnings, plus a direct unit test of each bundled adapter's
  `NativeAt(kind, level)` so tool-specific rules are pinned.
- **fsroot multi-root** — assert each scope's writes stay within its own root and
  cannot escape; assert the `~` root is never opened without `--user`.

**Integration**

- **Multi-scope emit** — temp tree with project + directory (+ optionally user)
  manifests; run sync; assert each scope's expected files land under *its* root
  and each gets its own ledger; re-run and assert idempotence / no drift.
- **Continue-and-report** — inject one scope with a broken manifest; assert
  healthy scopes still emit, the bad scope is skipped, and the run exits non-zero
  with an aggregated report naming the failure.
- **`status`** — assert it renders the whole hierarchy (incl. read-only user
  level) with precedence order + coverage warnings and opens no write root.
- **Determinism** — same hierarchy → byte-identical outputs across runs.

Existing engine/ledger/emit tests carry over unchanged, since per-scope behavior
is the same code.

## Scope Boundaries

**Deferred (explicit flag, later):**

- Runtime filesystem-mapping fallback — an on-the-fly mapping for tools/kinds
  that cannot layer natively, gated behind an explicit flag. The
  `HierarchyPlan` is the plug-in point.

**Outside this design:**

- Combining multiple canonical sources inside one manifest — superseded by
  levels.
- agent-sync acting on precedence at emit time (merging, content-splicing,
  conflict resolution across levels) — the tool owns precedence.
- An abstract N-source precedence list or `extends` chain — depth-based walk-up
  is the model.

## Dependencies / Assumptions

- **`internal/fsroot` multi-root** is a prerequisite and changes a load-bearing
  invariant (single-workspace → per-scope root). AGENTS.md must be updated in the
  same change.
- **Adapter capability extension** (`NativeAt`) is additive to the existing
  capability declaration; each bundled adapter (`claude`, `cursor`, `codex`)
  implements it.
- **Per-scope ledgers** assume a scope cleans its own orphans only when synced
  within its tree — a manifest deleted in a dir never revisited leaves an
  orphaned scope until the next sync at/above it (known limitation).
- Assumes each target tool's native config hierarchy behaves as understood:
  Claude Code (user vs project, `CLAUDE.md` imports, nested reads), Codex
  (`AGENTS.md` directory walk, deeper wins), Cursor (nested `.cursor/rules`).
  Verify against current tool behavior during planning.

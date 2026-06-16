---
title: "feat: In-repo local skill source (.agents/ working-tree source)"
type: feat
status: active
date: 2026-06-16
origin: null
---

# feat: In-repo local skill source (`.agents/` working-tree source)

## Summary

Add a third canonical-source kind to agent-sync — an **in-repo working-tree
directory** (default `.agents/`) that the CLI reads directly from the
filesystem, compiles through the existing IR pipeline, and syncs into each
target tool's reserved output. Today a workspace must point at a remote git
URL or a local git clone, both pinned by a 40-hex commit SHA; a repo cannot
author its own skills locally. This plan ships the **standalone** in-repo
source (the directory *is* the source) end-to-end: manifest field, a
git-decoupled source reader, CLI dispatch, `init`/wizard support, and docs.

The **overlay-on-remote** model (local skills layered on top of a remote
canonical) is explicitly **deferred** — it breaks single-source assumptions in
the engine and ledger that the standalone path does not. See Scope Boundaries.

---

## Problem Frame

The source model is single-canonical, git-only, SHA-pinned, and that
assumption is baked into every stage of the pipeline:

```
manifest.LoadFile (.agent-sync.yaml)
  → cli.materialize          (dispatch: URL vs local_path)
      → git.Materialize(URL) | git.Open(local_path)   [both → *git.Repository keyed by SHA]
      → trust.Decide          (URL path only)
      → ir.Decode(repo, sha)  + ir.SkillsByID(repo, sha)   [reads git tree objects]
  → engine.Request{Nodes, Skills, Commit}
      → engine.Sync → applyTarget → emit ops → stage+swap → ledger.Write → DeleteOrphans
```

Two hard couplings block per-repo skills:

1. **The manifest requires exactly one of `url` or `local_path`, both pinned**
   (`internal/manifest/schema.go:31-42`, validation at
   `internal/manifest/load.go:143-174`). `local_path` itself is a *git clone*
   that still requires a 40-hex `commit` (`internal/cli/materialize.go:58-68`),
   so it does not satisfy "author skills in the working tree."
2. **The IR decoder is hard-wired to `*git.Repository`** —
   `ir.Decode(repo *git.Repository, commitSHA string, …)`
   (`internal/ir/decode.go:35`) reads tree objects via `repo.ReadTree(sha)` and
   blobs via `repo.BlobContent(sha, path)`. There is no filesystem path to
   producing the source content tree.

The goal: let a user drop skills/rules/commands into `.agents/` in their repo
and run `agent-sync sync`, with no remote, no clone, no commit, no trust
prompt.

---

## Requirements

- **R1** — A workspace can declare an in-repo directory as its canonical
  source in `.agent-sync.yaml` (new `canonical.local_dir`, default `.agents`).
- **R2** — The in-repo source is read from the **working tree** (no git object
  store, no clone), through the `internal/fsroot` safe-read path.
- **R3** — The in-repo source is **exempt from pinning, trust (TOFU), and
  offline-strict** — there is nothing remote to fetch, pin, or trust.
- **R4** — The existing canonical layout (`AGENTS.md`, `rules/`,
  `skills/<id>/SKILL.md` + assets, `commands/`, `mcp/`, `plugins/`) is honored
  rooted at the in-repo directory, reusing the current decoder.
- **R5** — Authored source skills coexist with agent-sync's own Codex output in
  the shared `.agents/skills` tree: the reader skips `agent-sync-`-prefixed
  output leaves, and authored ids starting with `agent-sync-` are rejected.
- **R6** — `agent-sync init --local-dir <path>` and the interactive wizard can
  select the in-repo source kind; it is mutually exclusive with `--source` /
  `--local-path` and rejects `--commit` / `--ref` / `--floating`.
- **R7** — `sync`, `validate`, `status`, and `watch` all work against an
  in-repo source. Sync is idempotent; orphan deletion works unchanged.
- **R8** — The manifest/IR spec docs are updated in the same change (AGENTS.md
  "silent drift is a bug"), and `local_dir` vs `local_path` is documented.

---

## Key Technical Decisions

### KTD1 — Standalone source now, overlay deferred

The in-repo source is its **own canonical-source kind**, mutually exclusive
with `url`/`local_path`. It reuses the entire existing single-source pipeline
(`engine.Request` carries one `{Nodes, Skills, Commit}`,
`internal/engine/request.go:64-70`; one `materialize` call,
`internal/cli/setup.go:64`). Overlay-on-remote would require merging two node
sets with per-`(Kind,id)` precedence (no such logic exists —
`ir.Decode`'s `markSeen` *errors* on collision, `internal/ir/decode.go:51-61`)
and a ledger provenance column (`internal/ledger/types.go:28-40` has none).
That is a separate, larger feature. Shipping standalone first delivers
per-repo skills without touching the engine or ledger schema.

### KTD2 — Decouple the decoder via a consumer-defined `SourceTree` interface

Introduce a narrow interface in `internal/ir` over the three methods the
decoder uses (`ReadTree`, `BlobContent`, and the existing `HasCommit`/commit
guard), defined at the consumer per the repo's "interfaces at the consumer"
convention. `git.Repository` satisfies it unchanged; a new working-tree reader
provides a second implementation. The `commitSHA` argument becomes an opaque
`ref` — git uses the SHA, the working-tree reader ignores it. This is the only
structural git coupling and the largest piece of new code.

### KTD3 — Working-tree reader is rooted at `local_dir` and strips the prefix

The reader presents entries with the `local_dir` prefix removed, so the
decoder sees the canonical layout at tree root (`skills/foo/SKILL.md`, not
`.agents/skills/foo/SKILL.md`) and needs **zero changes** to its path-matching
switch (`internal/ir/decode.go:63-176`). It reads through
`fsroot.ValidateRelPath` and rejects absolute/`..`/bare-rooted paths
(per `docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`).

### KTD4 — Coexistence with the shared `.agents/skills` output tree

Codex emits owned skills to `.agents/skills/agent-sync-<id>/`, and the
`agent-sync-` prefix already isolates owned outputs from user/other-tool skills
in that tree (`internal/adapter/bundled/codex/emit_reserved.go:18-33`). The
in-repo reader therefore **skips any `skills/agent-sync-*` directory** (those
are outputs, not source) and validation **rejects authored skill ids beginning
with `agent-sync-`**. This prevents the source from reading its own output and
keeps a `.agents/`-rooted source and a `.agents/skills` Codex target stable
across repeated syncs.

### KTD5 — Unpinned "commit" uses the existing zero-SHA placeholder

The in-repo source has no SHA. The engine already tolerates an empty commit via
`commitOrPlaceholder` (all-zeros, `internal/engine/target.go:555-560`). The
in-repo path passes an empty/sentinel ref; `status`/reports render it as an
unpinned local-dir source. Content-hashing for change detection is deferred —
the existing idempotent stage+swap only swaps changed files, and `watch` is
already filesystem-triggered.

### KTD6 — Reconciliation with `--floating` and `local_path`

`--floating` today means "git source without a pin" (absence of
`commit`/`trusted_sha`, `internal/cli/cmd_init.go:129`,
`internal/tui/wizard/initconfig.go:87-92`). The in-repo source is *inherently*
unpinned but is also **trust- and offline-exempt**, which floating git sources
are not. We keep them distinct: `local_path` = a local **git repo** (pinned);
`local_dir` = an in-repo **working-tree directory** (no git). Docs spell out
the three-way distinction.

---

## High-Level Technical Design

New dispatch branch and decoder seam (new/changed elements in **bold**):

```mermaid
flowchart TD
    M[manifest.LoadFile<br/>url xor local_path xor **local_dir**] --> D{cli.materialize<br/>dispatch on kind}
    D -->|url| U[git.Materialize → trust.Decide → git.Open]
    D -->|local_path| L[git.Open pinned clone]
    D -->|**local_dir**| W[**worktree.NewReader rooted at local_dir**<br/>**skip trust + offline gate**]
    U --> R[("*git.Repository<br/>(satisfies ir.SourceTree)")]
    L --> R
    W --> WR[("**worktree.Reader<br/>(satisfies ir.SourceTree)**")]
    R --> DEC[**ir.Decode(SourceTree, ref)**<br/>+ ir.SkillsByID]
    WR --> DEC
    DEC --> REQ[engine.Request<br/>Nodes, Skills, Commit]
    REQ --> SYNC[engine.Sync → emit → stage+swap → ledger → orphans<br/>*unchanged*]
```

The decoder's layout switch is untouched: the working-tree reader strips the
`local_dir` prefix so canonical paths arrive at tree root, and the
`*git.Repository` and `worktree.Reader` are interchangeable behind
`ir.SourceTree`.

---

## Output Structure

```
internal/
  ir/
    source.go            # NEW — SourceTree interface (consumer-defined) + ref type
    decode.go            # CHANGED — Decode/SkillsByID accept ir.SourceTree
  worktree/
    reader.go            # NEW — fs-backed SourceTree rooted at local_dir
    reader_test.go       # NEW
  git/
    read.go              # CHANGED — compile-time assert *Repository satisfies ir.SourceTree
  manifest/
    schema.go            # CHANGED — CanonicalSource.LocalDir
    load.go              # CHANGED — one-of-three validation + local_dir safety/exemptions
  cli/
    materialize.go       # CHANGED — third dispatch branch (local_dir)
    cmd_init.go          # CHANGED — --local-dir flag
  tui/wizard/
    initconfig.go        # CHANGED — InitConfig.LocalDir + ManifestYAML + Validate
docs/spec/
  manifest-v1.md         # CHANGED — local_dir field + three-way source distinction
  ir-v1.md               # CHANGED — SourceTree note
```

---

## Implementation Units

### U1. Manifest schema + validation for the in-repo source kind

**Goal:** Add `canonical.local_dir` and make exactly one of
`url`/`local_path`/`local_dir` required, with `local_dir` exempt from the
40-hex pin requirement and validated as a safe workspace-relative subdirectory.

**Requirements:** R1, R3, R8.

**Dependencies:** none.

**Files:**
- `internal/manifest/schema.go` — add `LocalDir string` to `CanonicalSource`
  (`schema.go:31-42`) with `yaml:"local_dir,omitempty"`.
- `internal/manifest/load.go` — relax the mutual-exclusion switch
  (`load.go:143-151`) to one-of-three; exempt `local_dir` from the
  `commit`/`trusted_sha` 40-hex block (`load.go:156-174`); validate `local_dir`
  via the existing `ValidateReservedPrefix`-style shape check / `fsroot`
  rules (clean, forward-slash, not absolute, no `..`, not workspace root `.`).
- `internal/manifest/load_test.go`, `internal/manifest/schema_test.go`.
- `docs/spec/manifest-v1.md` — document `local_dir` and the three-way source
  distinction (same change set, per AGENTS.md).

**Approach:** Additive, `omitempty`, no new *required* field — preserves
backward compatibility per the "freeze the wire frame, grow capabilities"
policy. The one-of-three rule replaces the current both/neither errors.
`local_dir` must reject a value that equals or contains a target's reserved
output prefix **except** the documented shared `.agents/skills` ownership case
(that coexistence is handled in U3/U6, not by rejection).

**Patterns to follow:** `ValidateReservedPrefix` (`load.go:51-84`); strict
decode with `x-` allowance (`load.go:108-123`); the existing both/neither
switch (`load.go:143-151`).

**Test scenarios:**
- Happy: manifest with only `local_dir: .agents` and no `url`/`local_path`/
  `commit` loads and validates.
- Edge: `local_dir` defaulting — empty string vs explicit `.agents` (decide one;
  document). Covers R1.
- Error: `local_dir` set together with `url` or `local_path` → "exactly one
  source" error.
- Error: `local_dir` with `commit`/`trusted_sha`/`ref` → rejected (pin is
  meaningless for a working-tree source).
- Error: `local_dir` absolute (`/etc`), bare-rooted (`/x`), `..`-escaping, or
  `.` (workspace root) → rejected.
- Edge: unknown top-level key still rejected; `x-` prefix still allowed.

---

### U2. `SourceTree` interface + `*git.Repository` adoption (refactor)

**Goal:** Decouple the decoder from `*git.Repository` behind a narrow
consumer-defined interface, with zero behavior change against git sources.

**Requirements:** R2, R4 (enabling).

**Dependencies:** none (parallelizable with U1).

**Files:**
- `internal/ir/source.go` — NEW: `SourceTree` interface with the methods the
  decoder uses (`ReadTree(ref) ([]TreeEntry, error)`,
  `BlobContent(ref, path) ([]byte, error)`, and the commit/ref existence guard
  currently provided by `git.Repository`, `internal/git/read.go:50-198`).
- `internal/ir/decode.go` — change `Decode` and `SkillsByID` signatures
  (`decode.go:35`, `decode.go:226`) to accept `ir.SourceTree` and an opaque
  `ref string` instead of `*git.Repository` + `commitSHA`. Internal builders
  (`buildAgentsMD`, `buildSimpleNode`, `buildSkillNode`) call through the
  interface.
- `internal/git/read.go` — add a compile-time assertion that `*Repository`
  satisfies `ir.SourceTree`.
- `internal/ir/decode_test.go` — characterization assertions.

**Approach:** Pure extraction. `TreeEntry`/`Provenance` types stay in
`internal/ir`. The `Provenance.BlobSHA` field (`types.go:88-95`) is git-only;
leave it populated by git and empty/`""` for non-git sources (U3 fills a note,
not a SHA).

**Execution note:** Characterization-first — capture byte-identical IR output
for a representative git fixture before the extraction, and assert equality
after. This is a refactor of a load-bearing path; the test is the safety net.

**Patterns to follow:** "interfaces defined at the consumer"
(`AGENTS.md`); existing `git.Repository` read methods.

**Test scenarios:**
- Happy: existing decoder fixtures produce byte-identical IR through the
  interface (characterization).
- Edge: `markSeen` duplicate-id error and `sortNodes` ordering unchanged
  (`docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`
  flags these invariants).
- Integration: `*git.Repository` satisfies `ir.SourceTree` (compile-time +
  a decode through the interface against a real bare clone).

---

### U3. Working-tree `SourceTree` reader

**Goal:** Implement `ir.SourceTree` over the filesystem, rooted at the
workspace `local_dir`, reading through `internal/fsroot`.

**Requirements:** R2, R4, R5.

**Dependencies:** U2.

**Files:**
- `internal/worktree/reader.go` — NEW: `Reader` built from a workspace root +
  `local_dir`. `ReadTree(_ ref)` walks `local_dir` (via `os.DirFS` /
  `fsroot`), returns `[]ir.TreeEntry` with the `local_dir` prefix **stripped**
  so paths arrive as `skills/foo/SKILL.md`. Skips any `skills/agent-sync-*`
  directory (owned Codex output, `emit_reserved.go:18-19`). `BlobContent`
  reads the file through `fsroot`.
- `internal/worktree/reader_test.go` — NEW.

**Approach:** Read-only surface, but still routes every path through
`fsroot.ValidateRelPath` (reject absolute and bare-rooted, normalize with
`filepath.ToSlash` —
`docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`).
The `ref` argument is ignored (working tree, no commit). Authored skill ids
beginning with `agent-sync-` are rejected (KTD4) — implement as a reader-level
skip of `agent-sync-*` plus a decoder/validation error if such an id is
explicitly authored; pick the single clearest enforcement point and test it.

**Execution note:** Use `testing.T.TempDir` with a real filesystem (AGENTS.md
mandates real FS at/below `fsroot`; `afero` mem only above it). Always `-race`.

**Test scenarios:**
- Happy: `.agents/` with `AGENTS.md`, `rules/x.md`, `skills/foo/SKILL.md` +
  an asset, `commands/y.md`, `mcp/z.json` → decodes to the expected nodes with
  root-relative paths. Covers R4.
- Edge: `skills/agent-sync-foo/` present (a prior Codex output) → skipped, not
  read as source. Covers R5.
- Error: authored `skills/agent-sync-bar/SKILL.md` (user-named with reserved
  prefix) → rejected with a clear message.
- Error: a symlink or `..` path inside `local_dir` → rejected by `fsroot`.
- Edge: empty/absent `local_dir` → empty IR (and the existing
  `WarnAgentsMDMissing` behavior).
- Edge: cross-platform path separators normalized (`filepath.ToSlash`).

---

### U4. CLI materialize dispatch + trust/offline exemption

**Goal:** Route the `local_dir` source kind to the working-tree reader,
skipping git materialization, trust, and the offline-strict SHA gate.

**Requirements:** R3, R7.

**Dependencies:** U1, U2, U3.

**Files:**
- `internal/cli/materialize.go` — add a third branch to the dispatch
  (`materialize.go:47-56`) for `LocalDir != ""`: build `worktree.NewReader`,
  call a `decodeAt`-equivalent (`materialize.go:114-122`) with an empty `ref`,
  and **bypass** `trust.Decide` (precedent: `materializeLocal` already skips
  trust, `materialize.go:58-68`) and the offline gate.
- `internal/cli/setup.go` — ensure `prepareEngine` (`setup.go:46-102`) carries
  an empty commit cleanly into `engine.Request` (zero-SHA placeholder, KTD5).
- `internal/cli/materialize_test.go`, `internal/cli/setup_test.go`.

**Approach:** Mirror `materializeLocal`'s trust-skip; additionally short-circuit
the offline-strict requirement since a working-tree source never touches the
network. Stamp the report/status as an unpinned local-dir source.

**Patterns to follow:** `materializeLocal`/`materializeURL`
(`materialize.go:58-112`); `commitOrPlaceholder` (`target.go:555-560`).

**Test scenarios:**
- Happy: a `local_dir` manifest drives `sync` to emit the expected per-target
  outputs (handed to U6 for the full e2e). Covers R7.
- Edge: `--offline` with a `local_dir` source succeeds (no network, no pin
  required). Covers R3.
- Integration: no trust prompt / no TOFU state write for a `local_dir` source.
- Error: `local_dir` pointing at a non-existent directory → clear error (not a
  git error).

---

### U5. `init` command + wizard support

**Goal:** Let users create a `local_dir` workspace via flag and interactively.

**Requirements:** R6.

**Dependencies:** U1.

**Files:**
- `internal/cli/cmd_init.go` — add `--local-dir <path>` (`cmd_init.go:125-131`),
  mutually exclusive with `--source`/`--local-path`; reject `--commit`/`--ref`/
  `--floating` when `--local-dir` is set.
- `internal/tui/wizard/initconfig.go` — add `LocalDir` to `InitConfig`
  (`initconfig.go:16-38`); extend `Validate` (`initconfig.go:42-65`) to the
  one-of-three rule; emit `canonical.local_dir` in `ManifestYAML`
  (`initconfig.go:69-100`).
- Wizard `SourceEntry` screen — add an "in-repo directory" option (default
  `.agents`).
- `internal/cli/cmd_init_test.go`, `internal/tui/wizard/initconfig_test.go`.

**Approach:** `InitConfig` is the single convergence point for flags + wizard;
keep all source-kind logic there. Document `.agents` as the default value.

**Test scenarios:**
- Happy: `init --local-dir .agents --target claude` writes a manifest with
  `canonical.local_dir: .agents` and no `commit`. Covers R6.
- Error: `--local-dir` with `--source` or `--local-path` → mutual-exclusion
  error.
- Error: `--local-dir` with `--commit`/`--ref`/`--floating` → rejected.
- Integration: wizard selecting the in-repo option produces the same manifest
  as the flag path.

---

### U6. End-to-end sync, idempotency, and orphan/coexistence test

**Goal:** Prove the full path from authored `.agents/` source to emitted tool
outputs, including idempotency, orphan deletion, and `.agents/skills`
coexistence.

**Requirements:** R5, R7.

**Dependencies:** U4, U5.

**Files:**
- `internal/cli/sync_localdir_test.go` (or `internal/engine` integration test)
  — NEW, real-filesystem e2e.

**Approach:** Build a temp workspace, author `.agents/skills/foo/SKILL.md` +
`.agents/rules/r.md`, target `claude` and `codex`, run `sync`.

**Test scenarios:**
- Happy: emits `.claude/skills/agent-sync-foo/SKILL.md` and
  `.agents/skills/agent-sync-foo/SKILL.md` from the authored source. Covers R7.
- Coexistence: the authored source `.agents/skills/foo/` is **not** clobbered by
  the Codex output `.agents/skills/agent-sync-foo/`; both exist after sync.
  Covers R5.
- Idempotency: a second `sync` with no source change performs no spurious file
  swaps (ledger stable).
- Orphan: deleting `.agents/rules/r.md` and re-syncing removes the emitted rule
  via `DeleteOrphans` (`target.go:514`), and leaves unowned `.agents/skills`
  entries untouched.
- Offline: the whole flow succeeds under `--offline`.

---

### U7. Documentation and spec updates

**Goal:** Keep spec and impl in lockstep and make the feature discoverable.

**Requirements:** R8.

**Dependencies:** U1–U6 (content reflects final shape).

**Files:**
- `docs/spec/manifest-v1.md` — `local_dir` field; three-way `url` /
  `local_path` / `local_dir` distinction; pin/trust/offline exemption.
- `docs/spec/ir-v1.md` — note the `SourceTree` abstraction and that the
  working-tree reader strips the `local_dir` prefix.
- `README.md` — "What agent-sync does" / source model: mention in-repo source.
- `docs/quickstart.md` — an `init --local-dir .agents` → `sync` walkthrough.
- `AGENTS.md` — invariant note: the in-repo source is inherently floating and
  **trust/offline-exempt**, and reserves the `agent-sync-` skill-id prefix.
- `CHANGELOG.md` — `[Unreleased]` entry.

**Approach:** Per the rename playbook
(`docs/solutions/best-practices/large-mechanical-rename-of-load-bearing-identifiers.md`),
treat `local_dir` as a wire/on-disk identifier: grep that the spelling is
byte-identical across schema, validation, wizard emission, and docs, and that
no stray earlier spelling leaked in.

**Test scenarios:** `Test expectation: none — docs only.` (Spec-locked fixture
coverage for the schema lives in U1's `load_test.go`.)

---

## Scope Boundaries

### In scope
- Standalone in-repo working-tree source (`canonical.local_dir`), default
  `.agents`, read through `fsroot`, exempt from pin/trust/offline.
- Decoder decoupling behind `ir.SourceTree`; working-tree reader.
- `init`/wizard/flag support; full sync/validate/status/watch.
- Coexistence with the Codex `.agents/skills` shared output tree.

### Deferred to Follow-Up Work
- **Interactive wizard source-picker for `local_dir`** (R6, wizard portion). The
  non-interactive `--local-dir` flag path and the `InitConfig` convergence point
  are complete and tested; the Bubble Tea `SourceEntry` screen does not yet offer
  the in-repo option. Agent-native parity holds (the flag path covers all
  functionality non-interactively); the TTY screen is a human-convenience follow-up.
- **Overlay-on-remote** (local skills layered on a pinned remote canonical).
  Requires: a multi-source `engine.Request`, per-`(Kind,id)` precedence/merge
  semantics (none exist — `markSeen` errors on collision,
  `internal/ir/decode.go:51-61`), and a ledger provenance column
  (`internal/ledger/types.go:28-40`, a schema-version bump via
  `internal/ledger/migrate.go`). Sketch only; not built here.
- **Content-hash pinning** of the in-repo source for change detection /
  reproducible status (v1 uses the zero-SHA placeholder + idempotent swap).
- A `docs/solutions/` capture of the local-source/floating-trust model
  (post-ship via `ce-compound`; the learnings corpus has no prior art here).

### Out of scope
- Changing `local_path` semantics (stays a pinned local git clone).
- New adapters or changes to the adapter wire protocol.

---

## Risks & Mitigations

- **Refactoring the decoder (U2) regresses git sources.** Mitigation:
  characterization tests asserting byte-identical IR before/after; the
  extraction is mechanical and the interface is narrow.
- **Source/output collision in `.agents/skills`.** Mitigation: reader skips
  `agent-sync-*` leaves (KTD4); validation rejects authored `agent-sync-` ids;
  U6 explicitly tests coexistence and idempotency. The existing shared-subdir
  ownership model (`docs/plans/2026-06-09-003-fix-shared-subdir-ownership-plan.md`)
  already governs that tree.
- **Path-safety on a new user-supplied read surface.** Mitigation: route every
  path through `fsroot.ValidateRelPath`; reject absolute/bare-rooted/`..`;
  normalize separators; `GOOS=windows go vet` locally
  (`docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`).
- **Spec/impl drift on a contract change.** Mitigation: spec docs updated in the
  same change (U7) with a spec-locked fixture test (U1)
  (`docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`).
- **Offline-strict exemption hides a real misconfiguration.** Mitigation: the
  exemption applies only to the `local_dir` kind; URL/local_path keep their
  gates. A missing `local_dir` directory errors clearly (U4).

---

## Alternatives Considered

- **Reuse `local_path` and relax its git requirement.** Rejected: `local_path`
  is a pinned git clone with trust semantics; overloading it would change an
  existing contract and blur "pinned clone" vs "working-tree dir." A distinct
  kind is clearer and backward-compatible (KTD6).
- **Materialize `.agents/` into a temporary in-memory git tree** to feed the
  existing `*git.Repository` decoder unchanged. Rejected: hacky, adds a synthetic
  commit/object store, and still needs a content producer; the `SourceTree`
  interface is cleaner, testable, and aligns with the repo's DI convention.
- **Build overlay-on-remote now.** Rejected for this PR: it forces an
  engine/ledger redesign (multi-source request, precedence, provenance) that
  the standalone path avoids entirely. Deferred, sketched in Scope Boundaries.

---

## Sources & Research

- Repo architecture, seams, and single-source assumptions: `internal/manifest/`
  (`schema.go:31-42`, `load.go:143-174`), `internal/cli/materialize.go:47-122`,
  `internal/ir/decode.go:35,51-61,63-176`, `internal/engine/request.go:64-70`,
  `internal/engine/target.go:514,555-560`, `internal/ledger/types.go:28-40`,
  `internal/adapter/bundled/codex/emit_reserved.go:13-33`,
  `internal/cli/cmd_init.go:125-131`, `internal/tui/wizard/initconfig.go:16-100`.
- Invariants: `AGENTS.md` (fsroot, pinning/floating, offline-strict, ledger
  orphan authority, freeze-the-wire-frame).
- Learnings: `docs/solutions/best-practices/large-mechanical-rename-of-load-bearing-identifiers.md`,
  `docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`,
  `docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`.
- Existing partial scope: `docs/plans/2026-06-08-007-feat-cli-tui-sync-engine-plan.md`
  (`--floating`, `local_path`, deferred `--defer-resolve`).

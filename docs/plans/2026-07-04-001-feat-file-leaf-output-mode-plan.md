---
title: "feat: file-leaf OutputMode + Cursor/Pi command & Cursor skill parity"
status: active
date: 2026-07-04
type: feat
---

# feat: `file-leaf` OutputMode + Cursor/Pi command & Cursor skill parity

## Summary

Add a fourth engine `OutputMode` — **`file-leaf`** — that lets an adapter own
*individual files* inside a directory agent-sync does **not** own wholesale (a
flat directory shared with the user's own files). Then use it to close real
adapter capability gaps:

- **Cursor `command`** → `.cursor/commands/<id>.md` (flat; works in Cursor IDE
  *and* CLI).
- **Pi `command`** → `.pi/prompts/<id>.md` (unblocked "for free" by the same
  primitive — previously deferred exactly on this gap).
- **Cursor `skill`** → the shared `.agents/skills/` tree (needs **no** engine
  change — clean `shared-subdir`, reuses existing skill machinery).

This brings Cursor to effective full parity (agents-md, rule, skill, command,
mcp) and unblocks Pi command. **Codex requires no code** — its `rule` and
`command` gaps are by-design tool omissions (rules fold into `AGENTS.md`; custom
prompts are deprecated in favor of skills and live only in `~`), and forcing them
would emit files Codex never reads, violating the capability-honesty gate.

The engine work lands in the **data-loss-critical swap/ownership area** governed
by AGENTS.md invariants #6 (two-rename atomic swap) and #7 (ledger authority),
so it is TDD-first and requires a mandatory `ce-code-review` pass on the
swap/ownership changes.

---

## Problem Frame

The engine has three write shapes today (`internal/engine/target.go`
`applyTarget`):

1. **`owned-subdir`** — stage a whole directory, two-rename swap it into place.
   agent-sync owns the entire directory.
2. **`shared-subdir`** — own only the `agent-sync-<id>` *leaf directories* inside
   a shared parent (e.g. `.agents/skills/`); the parent is never swapped, foreign
   sibling *directories* survive.
3. **`tool-owned-entry`** — surgically merge into a user-owned file (JSON pointer
   / TOML path / markdown section).

None can own a **bare file** in a flat directory that also holds the user's own
files. Cursor commands (`.cursor/commands/<name>.md`) and Pi prompts
(`.pi/prompts/<name>.md`) are exactly this shape: a flat dir, one file per
command, co-resident with user-authored files. Cursor command **subdirectories**
(`.cursor/commands/agent-sync/`) render in the Cursor IDE but are **not read by
the Cursor CLI** (verified 2026-07), so the namespaced owned-subdir trick is
IDE-only — a flat, per-file approach is required for both surfaces. This is the
same gap that deferred Pi `command` (see `docs/adapters/pi.md`).

---

## Requirements

- **R1** — A new `file-leaf` OutputMode: an adapter declares a parent directory
  it does *not* own, and owns only the specific direct-child files it emits.
- **R2** — File-leaf writes are atomic (reuse the existing `StagedWrite`
  temp→fsync→rename primitive; no directory swap, no new sentinel machinery).
- **R3** — **Foreign files in the shared parent are never touched, never walked,
  never counted as drift or orphans.** This is the load-bearing safety property.
- **R4** — Drift detection is **per-file** (compare on-disk hash to the ledger
  entry); the engine must not directory-walk the shared parent.
- **R5** — Orphan reclaim is **per-file** via ledger diff (a previously-emitted
  file-leaf path no longer desired is deleted; foreign files are invisible).
- **R6** — Both OutputMode type systems stay in lockstep (adapterkit SDK +
  contract mirror) with parity tests green.
- **R7** — Cursor: `command` and `skill` flip to supported; Pi: `command` flips
  to supported — each capability-honest (real ops emitted, coverage accurate).
- **R8** — Codex stays as-is (agents-md + skill + mcp); document why rule/command
  are unachievable-by-design, not a gap.
- **R9** — Passes `go vet ./... && go test -race ./... && golangci-lint run`,
  plus a real `agent-sync sync` e2e proving foreign-file survival alongside
  agent-sync-owned command/skill files.

---

## Key Technical Decisions

- **KTD1 — Reuse the single-file primitive, don't invent a swap path.** The
  existing single-file branch in `applyTarget` (`fileOutputs`/`fileType` →
  `root.StagedWrite`, used today for the `.agent-sync-managed` sidecar) is already
  atomic and needs no directory swap or sentinels. file-leaf routes into it. The
  net-new logic is **classification**, not a new write mechanism. (R2)
- **KTD2 — File-leaf children enter the `effective` set as their own file paths.**
  Mirror `effectiveOwnedPrefixes`/`leafUnder` (shared-subdir), but the derived
  "leaf" is the **file itself** (`<parent>/<id>.md`), a *direct child only*, not a
  `<parent>/<firstSegment>` directory. Because the file equals its own effective
  prefix, `ownerOf(effective, path) == path` and the existing `o.Path == pre`
  check naturally routes it to `fileOutputs`. The shared parent is never added to
  `effective`, so it is never swapped or walked. (R1, R3)
- **KTD3 — Per-file drift + orphan, never a parent walk.** The at-sync drift gate
  and `planTarget` must treat file-leaf `effective` entries per-file: hash-compare
  the single file against its ledger entry (reuse the `OutOfBand` on-disk-hash
  logic in `plan.go`), and skip the `ScanDrift`/`ScanDriftUnion` directory walk
  for file-type entries (the swap loop already skips `fileType[pre]`). Orphan
  reclaim already works per-file via ledger diff + `DeleteOrphans`. (R4, R5)
- **KTD4 — No ADV-1 co-ownership for file-leaf (first cut).** `.pi/prompts/` and
  `.cursor/commands/` are distinct directories — no two adapters share a flat
  command dir — so the sibling-leaf release-filter complexity does not apply.
  `siblingKnown` guarding stays only where a real overlap exists (none here).
  Documented as an explicit scope boundary so a future shared flat dir revisits it.
- **KTD5 — Path-safety: reject file-leaf ops that name a subdirectory.** A
  file-leaf op path must be a *direct child file* of its declared parent. Add a
  mode-aware branch to `pathInDeclaredOutputs` (or the classification step) that
  rejects `<parent>/sub/dir/file.md` under a file-leaf parent — file-leaf owns
  direct children only, and a nested path would imply directory ownership. (R3)
- **KTD6 — Cursor skill uses the shared `.agents/skills/` tree, not `.cursor/skills/`.**
  Cursor reads both; the shared tree is the established cross-tool convention
  (codex/pi/antigravity co-own it, ADV-1-safe, byte-identical SKILL.md). Reuse the
  existing shared-subdir skill emitter wholesale — zero engine change. (R7)
- **KTD7 — Codex: no code.** rule → author as an `agents-md` node; command →
  author as a `skill`. Both already work. Sharpen the unsupported notes' routing
  guidance only. (R8)

---

## High-Level Technical Design

### Where file-leaf sits among the four modes

```
owned-subdir      agent-sync owns the whole dir      swap unit = directory tree
shared-subdir     owns agent-sync-<id> leaf DIRS     swap unit = leaf directory
file-leaf  (NEW)  owns individual direct-child FILES swap unit = single file (StagedWrite)
tool-owned-entry  merges into a user file            unit = a slice of one file
```

### Ownership classification flow (file-leaf)

```
declared: { Path: ".cursor/commands", Mode: file-leaf }
emitted op: write_file ".cursor/commands/deploy.md"

1. fileLeafParents = [".cursor/commands"]                     (from declared outputs)
2. effective += ".cursor/commands/deploy.md"                  (direct-child file, NOT the parent)
      — parent ".cursor/commands" is NEVER added to effective
3. ownerOf(effective, ".cursor/commands/deploy.md") == ".cursor/commands/deploy.md"
4. o.Path == pre  → fileOutputs (StagedWrite: temp→fsync→rename)   [existing primitive]
5. ledger entry {Path: ".cursor/commands/deploy.md", SHA256, Size}
6. drift: hash-compare that ONE file vs ledger; never walk ".cursor/commands/"
7. orphan: prior ledger file-leaf path not re-emitted → delete that ONE file
   foreign ".cursor/commands/mine.md" → never in effective, never in ledger → invisible
```

The single divergence from the existing single-file path: today a single-file
output requires the *declared prefix to equal the file* (`o.Path == pre` where
`pre` is a declared owned-subdir path). file-leaf makes the file its own effective
prefix even though the *declared* output is the parent dir — everything
downstream (StagedWrite, ledger, orphan) is unchanged.

---

## Implementation Units

> Suggested delivery: **PR 1** = U5 (Cursor skill — independent, no engine change,
> low risk). **PR 2** = U1–U4 + U6 + U7 + U8 (engine file-leaf + its first
> consumers). The engine change (U1–U3) must land with its tests and a
> `ce-code-review` pass before/with any file-leaf consumer. The implementer may
> merge PR boundaries if review prefers one branch; unit dependency order holds
> regardless.

### U1. Add the `file-leaf` OutputMode to both type systems

**Goal:** Introduce `OutputModeFileLeaf` ("file-leaf") in the adapterkit SDK and
the engine-internal contract mirror, kept in lockstep.

**Requirements:** R6
**Dependencies:** none
**Files:** `pkg/adapterkit/types.go`, `internal/adapter/contract/protocol.go`,
`pkg/adapterkit/schema_parity_test.go`, `internal/adapter/contract/protocol_test.go`
**Approach:** Add the constant to both enums with string value `file-leaf`. Extend
the parity tests that assert the two enums match. No behavior yet — this is the
vocabulary. Confirm `DeclaredOutput` needs no new field (file identity comes from
op paths, like shared-subdir).
**Patterns to follow:** the existing `OutputModeSharedSubdir` addition across the
same four files.
**Test scenarios:**
- Parity test: adapterkit and contract enumerations contain identical mode sets
  including `file-leaf`.
- A `DeclaredOutput{Mode: "file-leaf"}` round-trips through JSON decode into
  `contract.DeclaredOutput` with the mode preserved.
**Verification:** Both parity tests green; both packages compile.

### U2. Engine: file-leaf classification, containment, drift & orphan

**Goal:** Teach the engine to own direct-child files of a file-leaf parent —
routing them through the existing atomic single-file write, with per-file drift
and orphan handling and no parent-directory walk.

**Requirements:** R1, R2, R3, R4, R5, KTD1–KTD3, KTD5
**Dependencies:** U1
**Files:** `internal/engine/target.go`, `internal/engine/plan.go`,
`internal/adapter/runtime.go`, possibly `internal/sync/drift.go`
**Approach:**
- Add `fileLeafParents(outputs)` alongside `ownedSubdirs`/`sharedSubdirs`
  (filter `Mode == file-leaf`).
- Extend `effectiveOwnedPrefixes` (or add a parallel derivation) so that, for each
  file-leaf parent, a **direct-child** op path or prior-ledger path is added to
  `effective` **as the file path itself** — never the parent. Reject non-direct
  children (nested subdirs) here and/or in the path-safety gate (KTD5).
- Because the file becomes its own effective prefix, the existing
  `o.Path == pre → fileOutputs` branch and the single-file `StagedWrite` loop
  handle the write unchanged. Confirm `fileType[filePath] = true` so the directory
  swap loop skips it (it already skips `fileType` prefixes).
- **Drift gate (`applyTarget` ~L335-352):** for `effective` entries that are
  file-type, do a per-file hash check instead of `ScanDrift`/`ScanDriftUnion`
  (which walk a directory). Verify how the existing single-file sidecar is
  drift-handled today and extend that path; do **not** walk the file-leaf parent.
- **`planTarget` (`plan.go`):** mirror the same — per-file `WouldCreate`/
  `WouldUpdate`/`OutOfBand` for file-leaf paths; ensure a foreign file in the
  parent never appears as drift or a phantom delete.
- **Orphan:** already correct via `ownerOf(effective, e.Path)` + `newEntries`
  diff → `DeleteOrphans` removes the single file. Confirm the `--expect-deletions`
  count includes file-leaf orphans.
- **Path-safety (`pathInDeclaredOutputs`):** add a mode-aware branch rejecting a
  file-leaf op path that is not a direct-child file of its declared parent.
**Execution note:** Test-first. Write failing engine tests (U3) for foreign-file
survival and per-file drift BEFORE changing `applyTarget`, since this is
data-loss-critical swap/ownership code.
**Technical design:** see the classification-flow sketch in High-Level Technical
Design (directional).
**Patterns to follow:** `effectiveOwnedPrefixes`/`leafUnder`/`ownerOf` and the
`fileOutputs`/`fileType`/`StagedWrite` single-file path in `target.go`; the
`OutOfBand` on-disk hash logic in `plan.go`.
**Test scenarios:** (unit-level for helpers; behavior lives in U3)
- `fileLeafParents` filters only `file-leaf` declared outputs.
- Effective-set derivation: a direct-child op path yields an effective entry equal
  to the file path; the parent dir is absent from `effective`.
- A nested path (`<parent>/sub/x.md`) under a file-leaf parent is rejected.
- `ownerOf` returns the file path for a file-leaf child and "" for a foreign
  sibling not in `effective`.
**Verification:** Helper unit tests green; U3 behavior tests green; no existing
engine/sync test regresses.

### U3. Engine tests: foreign-file survival, drift, orphan, out-of-band

**Goal:** Prove the safety properties of file-leaf against the same adversarial
cases shared-subdir is tested for — at file granularity.

**Requirements:** R3, R4, R5
**Dependencies:** U2 (co-developed; tests written first per U2 execution note)
**Files:** `internal/engine/file_leaf_test.go` (new),
`internal/engine/target_helpers_test.go`, `internal/sync/*_test.go` as needed
**Approach:** Mirror `internal/engine/shared_subdir_test.go`, replacing leaf-dir
scenarios with flat-file scenarios in a shared dir. Use a synthetic adapter or an
existing one (pi/cursor once U4/U6 land) with a file-leaf declared output.
**Patterns to follow:** `shared_subdir_test.go` helpers (`assertFileBytes`,
foreign-skill setup), `internal/sync/drift_test.go`, `orphans_test.go`.
**Test scenarios:**
- **Foreign file survives add:** user has `.cursor/commands/mine.md`; sync emits
  `.cursor/commands/deploy.md`; both exist afterward, `mine.md` byte-identical.
- **Foreign file survives update + remove:** re-sync changing `deploy.md`, then a
  sync that drops `deploy.md` (orphan-deleted) — `mine.md` untouched throughout.
- **Foreign file is NOT drift:** `validate` after a clean sync reports no drift
  despite `mine.md` sitting in the shared parent.
- **Owned file out-of-band edit IS drift:** hand-edit `deploy.md`; `validate`
  flags it `OutOfBand`; the foreign `mine.md` never flagged.
- **Orphan reclaim per-file:** remove the command node; only `deploy.md` deleted;
  `mine.md` and the parent dir remain.
- **`--expect-deletions` counts file-leaf orphans** correctly (mismatch aborts
  before mutation).
- **Atomicity:** interrupted write leaves either old or new `deploy.md`, never a
  truncated file (StagedWrite property; assert via the existing staged-write test
  approach).
**Verification:** All scenarios green under `-race`; `shared_subdir_test.go` and
`swap`/`drift`/`orphans` suites still green.

### U4. Pi `command` via file-leaf (first real consumer)

**Goal:** Emit Pi prompt-template commands to `.pi/prompts/<id>.md` and flip
`command` to supported — proving the primitive end-to-end on a real adapter.

**Requirements:** R7
**Dependencies:** U2
**Files:** `internal/adapter/bundled/pi/emit.go`,
`internal/adapter/bundled/pi/emit_reserved.go` (or a new `emit_command.go`),
`internal/adapter/bundled/pi/capabilities.go`,
`internal/adapter/bundled/pi/capabilities.yaml`,
`internal/adapter/bundled/pi/emit_unsupported.go`,
`internal/coverage/coverage.go`, pi tests + `testdata/ir/`
**Approach:** Add `emitCommand` producing a single `OpWriteFile` at
`.pi/prompts/<id>.md` (managed header prepended; **no** `OpMkdir` — file-leaf owns
files, not the dir). Add `{Path: ".pi/prompts", Mode: file-leaf}` to
`declaredOutputs`. Flip `command` to `Supported` in `conceptKinds` +
`capabilities.yaml`; remove it from the unsupported switch/notes. Route
`ir.KindCommand` to `emitCommand` in dispatch. Update `coverage.go` for command
native-read behavior (`.pi/prompts/` — verify project + user scope; `~/.pi/prompts/`
is the user-global home).
**Patterns to follow:** pi's existing `emitAgentsMD`/`emitSkill` structure;
antigravity/claude command emitters for the header + write shape (minus the mkdir
+ owned-subdir).
**Test scenarios:**
- command node → exactly one `write_file` at `.pi/prompts/<id>.md`, no mkdir, with
  managed header + body.
- capabilities YAML↔code parity holds with command now supported.
- `declaredOutputs` includes `.pi/prompts` as file-leaf at project scope and the
  user-global equivalent at user scope.
- coverage: command native/non-native flags match the verified pi read behavior.
- unsupported-kinds test no longer expects a command warning.
**Verification:** pi package tests green; a real sync (U8) emits the pi command
file and preserves a foreign `.pi/prompts/` file.

### U5. Cursor `skill` via shared-subdir (independent; PR 1)

**Goal:** Emit Cursor skills to the shared `.agents/skills/agent-sync-<id>/` tree
and flip `skill` to supported. No engine change.

**Requirements:** R7, KTD6
**Dependencies:** none (independent of file-leaf)
**Files:** `internal/adapter/bundled/cursor/emit.go`,
`internal/adapter/bundled/cursor/emit_reserved.go` (or new `emit_skill.go`),
`internal/adapter/bundled/cursor/capabilities.go`,
`internal/adapter/bundled/cursor/capabilities.yaml`,
`internal/adapter/bundled/cursor/emit_unsupported.go`,
`internal/coverage/coverage.go`, cursor tests + `testdata/ir/`
**Approach:** Reuse the shared skill emitter (byte-identical to codex/pi/
antigravity via `skillmeta` + shared header). Add
`{Path: ".agents/skills", Mode: shared-subdir}` to `declaredOutputs` (scope-aware:
project `.agents/skills`, user `~/.agents/skills` — both read by Cursor). Flip
`skill` to `Supported`; drop it from the unsupported switch/notes. Update
`coverage.go` (skill now native for cursor where applicable). Confirm the existing
hierarchy-composition/rule behavior (PR #35) is unaffected.
**Patterns to follow:** `codex`/`pi` `emitSkill` + declaredOutputs shared-subdir
entry; `antigravity` skill wiring.
**Test scenarios:**
- skill node with assets → `mkdir .agents/skills/agent-sync-<id>` + `SKILL.md`
  (frontmatter at byte 0) + asset writes; SKILL.md bytes identical to codex/pi.
- description-less skill still emits + degraded warning (shared behavior).
- capabilities YAML↔code parity with skill supported.
- `declaredOutputs` shared-subdir entry present at both scopes; no project-root
  path leaks at user scope.
- coverage: skill native flags correct for cursor.
- **co-ownership:** a sync targeting both cursor and codex co-owns
  `.agents/skills/agent-sync-<id>` without drift/orphan false-positives (ADV-1).
**Verification:** cursor package + engine shared-subdir tests green; real sync (U8)
shows cursor+codex co-owning a skill leaf.

### U6. Cursor `command` via file-leaf

**Goal:** Emit Cursor commands to `.cursor/commands/<id>.md` and flip `command` to
supported (IDE + CLI, flat).

**Requirements:** R7
**Dependencies:** U2 (needs file-leaf), U5 (same package; sequence after to avoid
churn)
**Files:** `internal/adapter/bundled/cursor/emit.go`,
`internal/adapter/bundled/cursor/emit_command.go` (or reuse emit file),
`internal/adapter/bundled/cursor/capabilities.{go,yaml}`,
`internal/adapter/bundled/cursor/emit_unsupported.go`,
`internal/coverage/coverage.go`, cursor tests + `testdata/ir/`
**Approach:** `emitCommand` → single `OpWriteFile` at `.cursor/commands/<id>.md`
(managed header; no mkdir). Add `{Path: ".cursor/commands", Mode: file-leaf}` to
`declaredOutputs`, **scope-aware**: project `.cursor/commands`, user
`.cursor/commands` (→ `~/.cursor/commands`, Cursor's user-global command home —
verify). Flip `command` to `Supported`; remove from unsupported. Update coverage.
**Patterns to follow:** U4 (pi command) — same file-leaf emit shape.
**Test scenarios:**
- command node → one `write_file` at `.cursor/commands/<id>.md`, no mkdir, header
  + body.
- capabilities parity with command supported.
- `declaredOutputs` file-leaf entry at project + user scope.
- coverage flags correct (command native at project and user for cursor).
- foreign `.cursor/commands/mine.md` survives a cursor sync (engine-level, but
  assert via a cursor-targeted sync test).
**Verification:** cursor package tests green; real sync (U8) emits cursor command
+ preserves foreign command file.

### U7. Docs, spec, README, CHANGELOG, Codex note

**Goal:** Document the new mode and the capability changes; correct the Codex
framing.

**Requirements:** R8
**Dependencies:** U4, U5, U6
**Files:** `docs/spec/adapter-protocol-v1.md` (and/or `docs/spec/ir-v1.md`) for
the new OutputMode, `docs/adapters/cursor.md`, `docs/adapters/pi.md`,
`docs/adapters/codex.md`, `README.md`, `CHANGELOG.md`
**Approach:** Spec: define `file-leaf` (semantics, ownership, foreign-file safety,
drift/orphan granularity) alongside the other three modes. cursor.md: skill +
command now supported, with the file-leaf/flat-dir + IDE/CLI notes. pi.md: remove
the command "planned/deferred" note; document `.pi/prompts/` file-leaf. codex.md:
sharpen rule→agents-md and command→skill routing guidance; state explicitly that
codex is at its honest ceiling by design. README: update the Supported-tools table
statuses. CHANGELOG: Unreleased entry.
**Test scenarios:** `Test expectation: none` — docs. Verify README status column
matches the shipped capabilities; verify no broken doc links.
**Verification:** Docs read cleanly; capability claims match code.

### U8. Verification: full gate + real sync e2e

**Goal:** Prove the whole thing end-to-end on the real binary, with emphasis on
the data-loss-critical foreign-file safety property.

**Requirements:** R9
**Dependencies:** U1–U7
**Files:** (no product code) — a scratch canonical repo + workspace, driven by the
built binary
**Approach:** Build `agent-sync`; author a canonical repo with a command node and a
skill node; target cursor + pi (+ codex for skill co-ownership). Pre-create
**foreign** files (`.cursor/commands/mine.md`, `.pi/prompts/mine.md`) in the
workspace. Run `sync`, then `validate`, then a re-sync that drops the command
node.
**Test scenarios (observed on real FS):**
- After sync: agent-sync command files exist at `.cursor/commands/<id>.md` and
  `.pi/prompts/<id>.md`; cursor skill at `.agents/skills/agent-sync-<id>/`; the
  foreign `mine.md` files are **byte-unchanged**.
- `validate` reports no drift (foreign files not flagged).
- Drop the command node + re-sync: agent-sync command file removed; `mine.md`
  survives; parent dirs remain.
- Full gate: `go vet ./... && go test -race ./... && golangci-lint run` all clean.
**Verification:** All observations hold; gate green. Mandatory `ce-code-review` on
the U2 swap/ownership diff before merge (per AGENTS.md invariants #6/#7).

---

## Scope Boundaries

**In scope:** the `file-leaf` OutputMode + engine plumbing; Cursor command+skill;
Pi command; docs; Codex note-sharpening (docs only).

### Deferred to Follow-Up Work

- **ADV-1 co-ownership for file-leaf.** Not needed now (`.pi/prompts/` vs
  `.cursor/commands/` are distinct dirs). If two adapters ever share one flat
  command dir, add the sibling-leaf release-filter path (mirror shared-subdir).
- **Cursor command subdirectory namespacing.** IDE-only; skipped in favor of the
  flat file-leaf approach that works in IDE + CLI.

### Non-goals

- **Codex rule/command as emitted files** — by-design tool omissions; would
  violate the capability-honesty gate. Handled by routing (rule→agents-md,
  command→skill), not new emitters.
- Changing the two-rename directory swap or the ledger schema.

---

## System-Wide Impact

- **Engine swap/ownership (data-loss-critical).** U2 touches `applyTarget`/
  `planTarget`/`pathInDeclaredOutputs`. Risk: a classification bug that adds the
  shared *parent* to `effective` would swap/delete the whole dir including foreign
  files. Mitigation: the effective-set derivation adds only direct-child files;
  U3's foreign-file-survival tests are the guardrail; mandatory ce-code-review.
- **All adapters read the OutputMode enum** via the contract mirror — U1 must keep
  parity or every adapter session breaks. Parity tests enforce this.
- **Coverage reporting** changes for cursor (skill, command) and pi (command);
  users will see fewer degradation warnings.

---

## Risks & Dependencies

- **Top risk: foreign-file data loss** if file-leaf ever escalates to directory
  ownership. Fully mitigated by KTD2 (files-only in `effective`) + U3 tests +
  review. This is the reason for TDD-first and mandatory review.
- **Drift-gate directory walk on a file path.** `ScanDrift` walks a dir; applied
  to a file-leaf entry it must be replaced by a per-file hash check (KTD3). Verify
  how the existing single-file sidecar is drift-handled and extend that, not the
  walk.
- **Cursor user-scope command home** (`~/.cursor/commands/`) and **pi user-scope
  prompt home** (`~/.pi/prompts/`) — verify against current docs during U4/U6;
  default to project-scope-native + coverage-flag if unconfirmed.
- **Dependency:** none external. Builds on the merged shared-subdir/ADV-1 work.

---

## Sources & Research

- Architecture map (staging/swap/ledger/orphan/OutputMode) gathered from the
  codebase this session — `internal/engine/target.go` (`ownedSubdirs`,
  `sharedSubdirs`, `effectiveOwnedPrefixes`, `leafUnder`, `ownerOf`,
  `fileOutputs`/`fileType`/`StagedWrite` single-file path, orphan/drift gates),
  `internal/engine/plan.go` (`planTarget`, `OutOfBand`), `internal/sync/`
  (`Stage`, `Swap`, `ScanDrift`/`ScanDriftUnion`, `Orphans`/`DeleteOrphans`),
  `internal/ledger/types.go`, `internal/adapter/runtime.go`
  (`pathInDeclaredOutputs`), `pkg/adapterkit/types.go` +
  `internal/adapter/contract/protocol.go` (OutputMode + parity tests).
- Cursor commands flat-dir + IDE-vs-CLI subdir behavior: Cursor docs
  (cursor.com/docs/cli/reference/slash-commands) + forum reports
  (forum.cursor.com — subfolders IDE-but-not-CLI), verified 2026-07.
- Cursor skills read `.agents/skills/` + `~/.agents/skills/`, folder-per-SKILL.md:
  cursor.com/docs/context/skills, verified 2026-07.
- Codex rule→AGENTS.md and command→skills (prompts deprecated, home-only):
  developers.openai.com/codex (agents-md guide, skills), verified 2026-07.
- Existing test template: `internal/engine/shared_subdir_test.go`.
- Prior deferral of pi/cursor command on exactly this gap:
  `docs/adapters/pi.md`, and the unit-16 restart notes.

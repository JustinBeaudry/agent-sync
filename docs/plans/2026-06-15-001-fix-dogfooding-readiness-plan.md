---
title: "fix: dogfooding-readiness — claude AGENTS.md sync, tool-owned idempotency, quickstart"
type: fix
status: active
date: 2026-06-15
origin: none (direct investigation — see Problem Frame)
---

# fix: dogfooding-readiness — claude AGENTS.md sync, tool-owned idempotency, quickstart

## Summary

Three verified defects block `agent-sync` from being usable for internal
dogfooding. Two are correctness bugs on the flagship happy path (Claude `sync`
hard-fails on the most basic config; `validate` reports false drift after a
clean `sync`), and one is a documentation gap (the canonical-repo authoring
format is undocumented, so a new user hits a hard decoder rejection in their
first five minutes). This plan fixes the two bugs test-first, locks the whole
init→sync→validate→re-sync happy path with an end-to-end smoke test that would
have caught both, and ships a contract-level quickstart with a copyable example
canonical repo.

Out of scope: `unmanage`/`rollback` commands, the `pi` adapter, ADV-1, the
`gemini` adapter, and the Unit 20 extension SDK. These are v1.0.0-gate items,
not dogfooding-readiness blockers (see Scope Boundaries).

---

## Problem Frame

Verified by building the binary (`go build ./cmd/agent-sync`) and running the
real `init → sync → validate` flow against a local canonical repo. The plumbing
(manifest, discovery, cache, git, trust, IR, skills/rules emit, atomic swap,
cursor adapter) works. Three things break the dogfood experience:

1. **Claude `sync` hard-fails on one `AGENTS.md` + `--target claude`.** Error:
   `engine: merge CLAUDE.md: merge: markdown body contains agent-sync marker
   "<!-- agent-sync:" (the engine owns markers; pass inner body only)`.
   The Claude adapter double-wraps the managed section; the engine owns the
   markers and rejects it. codex and cursor were fixed for this in the
   2026-06-10 handoff; **Claude was never fixed.**

2. **`validate` reports drift immediately after a clean `sync`.**
   `validate --output=json` returns `{"target":"cursor","would_update":["AGENTS.md"]}`
   on an in-sync workspace. This breaks the git-hook / CI gate, whose entire
   purpose is "is this workspace in sync?". Independent of bug 1 — reproduced
   with the cursor adapter, whose emit is already correct.

3. **The canonical authoring format is undocumented.** Canonical sources use
   agent-sync frontmatter (`required`/`targets`/`version`); the IR decoder
   hard-rejects native `name:`/`description:` frontmatter as unknown fields. A
   new user authoring a skill the obvious way hits an immediate decode error
   with no quickstart to consult.

---

## Requirements Trace

- **G1 — Claude flagship path works.** `init --target claude` + an `AGENTS.md`
  in the canonical repo → `sync` exits 0 and writes a valid managed section into
  `CLAUDE.md`. → U1
- **G2 — Sync is idempotent and validate is honest.** After a clean `sync`,
  `validate` reports no drift and a second `sync` reports all targets unchanged,
  for every output mode including tool-owned files (markdown sections, JSON,
  TOML). → U2
- **G3 — The happy path can't silently regress.** An end-to-end test exercises
  init→sync→validate→re-sync across all three bundled adapters with an
  `AGENTS.md`, a rule, and a skill. → U3
- **G4 — A new user can get to a green sync without reading source.** A
  one-page quickstart with a copyable example canonical repo documents the
  authoring format and the init→sync→validate flow. → U4

---

## Scope Boundaries

- In scope: the two correctness bugs (U1, U2), a regression-locking E2E smoke
  test (U3), and the quickstart doc (U4).
- The fix for bug 2 lives in the **validate/plan comparison path**, not by
  relaxing `validate` to tolerate drift. The invariant is "clean sync →
  validate reports zero drift"; fix the comparison to honor it (see origin
  learning: `docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`).

### Deferred to Follow-Up Work

- **`unmanage` / `rollback` commands.** A clean exit hatch is desirable for
  dogfood confidence but is a feature, not a blocker. U4's quickstart documents
  a manual backing-out procedure as an interim. Track as a separate plan.
- **Capture learnings** (`/ce-compound`) for the marker-ownership convention
  and the idempotent-merge comparison, after this lands — neither exists in
  `docs/solutions/` yet.

### Out of Scope (v1.0.0 gate, not dogfooding readiness)

- `pi` adapter (Unit 11.5) and its prerequisite ADV-1 concurrency fix. ADV-1 is
  dormant: the engine iterates targets sequentially (`internal/engine/engine.go:97,148`)
  and no two bundled adapters share a staging parent.
- `gemini` adapter, experimental adapters, and the Unit 20 extension SDK.

---

## Context & Research

### Root causes (verified)

- **Bug 1 — `internal/adapter/bundled/claude/emit_tool_owned.go:107`.**
  `emitAgentsMD` does `wrapped := wrapManagedSection(node.ID, body)` and sends
  `Content: wrapped`. codex (`internal/adapter/bundled/codex/emit_tool_owned.go:105`)
  and cursor (`internal/adapter/bundled/cursor/emit_tool_owned.go:120`) both send
  `Content: body`. The engine merge guard at `internal/merge/markdown.go:28-30`
  rejects marker-bearing content at runtime. The Claude unit test
  `TestEmitAgentsMD_WrapsBodyInSection` (`internal/adapter/bundled/claude/emit_test.go`)
  asserts the *buggy* wrapped output, which is why CI stayed green — no test runs
  the Claude agents-md emit through the real engine merge.

- **Bug 2 — `internal/engine/plan.go:50-57`.** For `OpWriteToolOwned`, the Plan
  (validate) path marks any path already in the ledger as `WouldUpdate`
  unconditionally; it never computes the desired slice hash to compare against
  the stored `ledger.Entry.SHA256`. The write path *does* compute and store the
  slice hash (`internal/engine/target.go:493,498` via `merge.ApplyToFile`), so
  the data exists — the comparison is simply skipped (comment: "a precise slice
  diff is deferred"). The `OpWriteFile` branch (`plan.go:61-68`) shows the
  correct hash-compare pattern to mirror. Slice-hash computation is currently
  embedded inside the mutating merge functions (`internal/merge/markdown.go:49`,
  `json.go:69`, `toml.go:101`) and is not separately callable.

### Institutional learnings applied

- `docs/solutions/best-practices/large-mechanical-rename-of-load-bearing-identifiers.md`
  — markers are a load-bearing on-disk format; verify producer/consumer
  byte-parity across all adapters, and grep for the *doubled/broken* marker form
  (a grep for the correct form is blind to a double-wrap).
- `docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`
  — AGENTS.md/CLAUDE.md/GEMINI.md are distinct IR nodes; the engine carries the
  invariant. Fix the merge/compare to honor "clean sync → zero drift", don't
  relax validate.
- `docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`
  — idempotency byte-instability hazards to check in the merge: trailing
  newlines, CRLF vs LF, path separators. Both operands of any hash/compare must
  use one canonical byte representation.

### Authoring contract reference

`docs/spec/ir-v1.md` is the frozen IR contract: 6 node kinds, canonical repo
layout, recognized frontmatter (`required`/`targets`/`version`, `x-` tolerated,
all else rejected), and skill-id-from-directory-name rule. U4 documents this at
contract level without restating decoder internals.

---

## Implementation Units

### U1. Fix Claude `agents-md` marker double-wrap

**Goal:** Claude `sync` succeeds on an `AGENTS.md`-only canonical repo, writing a
valid managed section into `CLAUDE.md`, matching codex/cursor behavior.

**Requirements:** G1

**Dependencies:** none

**Files:**
- Modify: `internal/adapter/bundled/claude/emit_tool_owned.go` (send inner
  `body`, drop the `wrapManagedSection` call in `emitAgentsMD`; keep the
  marker-injection guard at line 101-106 that rejects bodies *containing* marker
  syntax)
- Modify: `internal/adapter/bundled/claude/emit_test.go` (replace
  `TestEmitAgentsMD_WrapsBodyInSection` — which asserts the buggy wrapped output
  — with an assertion that `Content` is the inner body and contains no marker)
- Modify/Add test: `internal/engine/*_test.go` — engine-level integration test
  that runs the Claude adapter's `agents-md` emit through the real engine merge
  and asserts `sync` succeeds and `CLAUDE.md` contains exactly one well-formed
  `<!-- agent-sync:begin id=... -->` / `end` pair (mirror the existing
  shared-subdir engine tests in `internal/engine/shared_subdir_test.go`)
- Check (possibly remove now-dead): `internal/adapter/bundled/claude/header.go`
  `wrapManagedSection` — if no longer referenced after the fix, delete it; if
  still used elsewhere, leave it

**Approach:** Mirror codex/cursor exactly: the engine owns begin/end markers, the
adapter emits inner body only. This is the same fix already applied to codex and
cursor in the 2026-06-10 handoff; reference those two `emitAgentsMD`
implementations as the canonical shape. After the change, grep all three
bundled adapters to confirm none emit marker-wrapped tool-owned content, and
grep for a doubled marker form as a parity check.

**Execution note:** Test-first. Add the failing engine-level integration test
(it reproduces the runtime merge rejection) before changing the adapter, then
make it green, then update the stale unit test.

**Patterns to follow:** `internal/adapter/bundled/cursor/emit_tool_owned.go:101-120`
and `internal/adapter/bundled/codex/emit_tool_owned.go:87-105`.

**Test scenarios:**
- Happy path (engine integration): canonical repo with one `AGENTS.md`,
  `--target claude` → `sync` exits 0; `CLAUDE.md` contains the body inside one
  begin/end marker pair; no double markers. Covers G1.
- Unit: `emitAgentsMD` produces an `OpWriteToolOwned` whose `Content` equals the
  inner body and contains no `<!-- agent-sync:` substring.
- Error path (guard preserved): a node body that itself contains
  `<!-- agent-sync:` is still rejected with `CodeInvalidParams` (hostile/marker-
  injection bodies must not pass through).
- Parity: a test (or the integration test parametrized over all three adapters)
  asserting claude/cursor/codex all emit inner-body-only for `agents-md`.

**Verification:** `sync --target claude` on an `AGENTS.md`-only repo exits 0 and
the resulting `CLAUDE.md` round-trips through a second sync unchanged (with U2);
gate passes.

---

### U2. Fix tool-owned drift comparison in `validate`/Plan (idempotency)

**Goal:** After a clean `sync`, `validate` reports no drift and a second `sync`
reports unchanged for tool-owned outputs (markdown sections, JSON, TOML), by
comparing the desired slice hash against the stored ledger hash instead of
unconditionally reporting `WouldUpdate`.

**Requirements:** G2

**Dependencies:** none (independent of U1; both are exercised together in U3)

**Files:**
- Modify: `internal/engine/plan.go` (the `OpWriteToolOwned` branch at ~50-57):
  compute the desired slice hash for the op and compare to
  `oldByPath[o.Path].SHA256` — `WouldCreate` if path unknown, `WouldUpdate` only
  on hash mismatch, otherwise unchanged (mirror the `OpWriteFile` branch at
  61-68, including its out-of-band-edit check)
- Modify: `internal/merge/markdown.go`, `internal/merge/json.go`,
  `internal/merge/toml.go` — expose a non-mutating slice-hash computation so the
  Plan path can compute the desired hash without performing the merge write
  (e.g., a `SliceHash`/dry-run variant, or extract the hash step the merge funcs
  already perform at `markdown.go:49`, `json.go:69`, `toml.go:101`). Keep the
  write-path and compare-path hashing identical so they can never diverge
- Add test: `internal/engine/*_test.go` — idempotency regression spanning
  sync→Plan(validate)→sync for tool-owned files
- Add/extend test: `internal/merge/*_test.go` — slice-hash is stable across
  recompute and matches what the write path stores

**Approach:** The write path already stores the correct slice hash in the ledger;
the bug is purely that Plan never reads it. The single source of truth must be a
shared slice-hash function used by both `merge.ApplyToFile` (write) and the new
Plan comparison (read), so a normalization change in one can't desync the other.
Audit the merge for byte-instability before locking the hash: trailing newline,
CRLF vs LF, and any embedded path separators must be canonicalized consistently
(see go-windows-cross-platform learning). If found, normalize in one place that
both write and compare share.

**Execution note:** Test-first — write the failing sync→validate-clean
regression test (it reproduces today's false drift) before touching `plan.go`.

**Patterns to follow:** the `OpWriteFile` comparison in `internal/engine/plan.go`
(known/unknown/hash-mismatch/out-of-band branches).

**Test scenarios:**
- Happy path: canonical repo with `AGENTS.md` + a rule, `--target cursor` →
  `sync`; then `validate` reports `drift_detected:false` and empty
  `would_update`/`would_create`. Covers G2.
- Happy path: second `sync` after a clean sync reports the target unchanged
  (0 written, 0 deleted).
- Tool-owned coverage: idempotency holds for a markdown-section output
  (AGENTS.md), a JSON output (`.cursor/mcp.json` mcp-server-entry), and a TOML
  output (`.codex/config.toml`) — at least one test per merge kind.
- Edge case: after an out-of-band hand edit to the managed section, `validate`
  *does* report drift (the comparison must detect real changes, not just
  suppress all of them).
- Edge case: a genuine content change in the canonical source → `validate`
  reports `would_update` for the affected path.

**Verification:** `sync` then `validate` on an unchanged workspace exits 0 with
no drift across all three output modes; out-of-band edits still surface as drift;
gate passes.

---

### U3. End-to-end dogfood smoke test (regression lock)

**Goal:** Lock the full dogfood happy path so U1/U2 can't silently regress:
init→sync→validate(clean)→re-sync(unchanged) across all three bundled adapters
with an `AGENTS.md`, a rule, and a skill.

**Requirements:** G3

**Dependencies:** U1, U2

**Files:**
- Add test: `internal/engine/e2e_dogfood_test.go` (or the closest existing
  engine-level test home) — builds a temporary canonical git repo, runs the
  engine pipeline for `claude`, `cursor`, and `codex`, and asserts the full
  cycle. Use the existing engine test harness / `internal/gittest` helpers and
  honor `AGENT_SYNC_REQUIRE_GIT`

**Approach:** Construct a realistic canonical repo (one `AGENTS.md`, one
`rules/<id>.md`, one `skills/<id>/SKILL.md` using agent-sync frontmatter), sync
all three targets, then assert: every target sync succeeds (0 failures);
`validate` immediately after reports no drift for every target; a second sync
reports all targets unchanged. This single test would have caught both bugs.

**Patterns to follow:** `internal/engine/shared_subdir_test.go` for the
canonical-repo + engine-run harness shape; `internal/gittest` for the git
fixture and the require-git guard.

**Test scenarios:**
- Happy path: 3 targets, mixed node kinds → all sync OK; post-sync validate
  clean; re-sync unchanged. Covers G3 (and G1, G2 end-to-end).
- Capability-honest: a kind a given adapter declares `unsupported` surfaces as a
  warning, not a failure, and does not break the clean-validate assertion.

**Verification:** the new test passes under
`AGENT_SYNC_REQUIRE_GIT=1 go test -race ./internal/engine/...` and fails if
either U1 or U2 is reverted.

---

### U4. Quickstart + example canonical repo

**Goal:** A new user can author a canonical repo and reach a green
init→sync→validate without reading source, and knows how to back out.

**Requirements:** G4

**Dependencies:** none (content depends on U1/U2 behavior; write last so
examples match fixed behavior)

**Files:**
- Add: `docs/quickstart.md` — the canonical-repo layout, the
  `required`/`targets`/`version` frontmatter (and the explicit "do not use
  native `name:`/`description:`" gotcha), and the `init → sync → validate` flow
  with real commands
- Add: `examples/canonical/` (or `docs/quickstart/example/`) — a copyable
  example canonical repo: `AGENTS.md`, `rules/<id>.md`, `skills/<id>/SKILL.md`,
  each with correct agent-sync frontmatter
- Modify: `README.md` — link the quickstart; correct the adapter status table
  (Codex currently shows "Planned (primary)" but is bundled and merged)

**Approach:** Stay at contract level per the spec-drift learning — describe what
markers/sections/frontmatter mean and point to `docs/spec/ir-v1.md` for the full
contract; do not restate decoder internals (id-derivation, sort order) that the
code enforces, to avoid doc drift. Include a short "Backing out" section
documenting the manual removal procedure (delete the target's reserved prefix
and the `.agent-sync/` state) as the interim until `unmanage` ships.

**Patterns to follow:** `docs/adapters/*.md` tone and structure; the canonical
layout block in `docs/spec/ir-v1.md`.

**Test scenarios:** `Test expectation: none — documentation + static example
files.` Validate by following the quickstart verbatim against the built binary
(or wiring the example repo into U3's fixture so the documented example is the
tested example, preventing doc/behavior drift).

**Verification:** following `docs/quickstart.md` against a clean checkout
produces a green `sync` and a no-drift `validate`; README adapter table is
accurate.

---

## Risks & Dependencies

- **Slice-hash extraction (U2) risk:** pulling the hash computation out of the
  mutating merge functions could subtly change the hashed bytes. Mitigation: one
  shared function used by both write and compare paths; assert in test that the
  write-stored hash equals the recompute. This is the load-bearing, data-adjacent
  change in the plan — review with care.
- **Sequencing:** U1 and U2 are independent and can land in either order; U3
  depends on both; U4 should be written last so documented examples match fixed
  behavior. Reasonable as one PR (cohesive dogfooding-readiness pass) or two
  (bugs, then docs+E2E).
- **Marker parity (U1):** verify with a grep for both the canonical and the
  doubled marker form across `internal/adapter/bundled/*`, per the rename
  learning's completion-grep blind-spot lesson.

## Verification (whole plan)

Gate, run before declaring done:
`go vet ./... && AGENT_SYNC_REQUIRE_GIT=1 go test -race ./... && golangci-lint run`,
80% coverage floor maintained. Plus the manual dogfood check: `init --target
claude --target cursor --target codex` against the example canonical repo →
`sync` exits 0 → `validate` reports no drift → second `sync` reports unchanged.

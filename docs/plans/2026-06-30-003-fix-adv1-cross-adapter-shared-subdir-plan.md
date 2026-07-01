---
title: "fix: ADV-1 cross-adapter shared-subdir co-ownership (enables codex+pi skills)"
status: approved
date: 2026-06-30
type: fix
origin: docs/plans/2026-06-30-002-feat-pi-adapter-plan.md
master_plan: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# fix: ADV-1 — cross-adapter shared-subdir co-ownership

## Summary

Two bundled adapters (codex and pi) both declare the shared `.agents/skills`
tree. When a workspace targets **both**, they co-own the same leaf
`.agents/skills/agent-sync-<id>/`. Today that **fails**: ledgers are per-target
(`.agent-sync/state/<target>.json`), and the shared-subdir drift + orphan checks
consult only the *current* target's ledger — so pi sees codex's file as a
foreign hand-edit and aborts with `ErrMidLifeDrift` (confirmed by the PR #32
validation test).

This plan makes shared-subdir co-ownership correct by having the drift and
orphan logic reason over **all target ledgers** for shared-subdir leaves, then
enables `skill` on the pi adapter. This is a change to the data-loss-critical
drift/swap/orphan path (AGENTS invariants #6/#7) — **mandatory `ce-code-review`**.

Scope note: pi `command` (flat owned files in the shared `.pi/prompts/` dir)
needs a *different* engine change — file-leaf stage/swap — and is a separate
follow-up (PR3), not in this plan.

## Problem Frame (confirmed)

`internal/engine/target.go` `applyTarget`:
- loads only the current target's ledger (`loadLedger(root, target)`, ~222),
- computes `effectiveOwnedPrefixes` including shared-subdir **leaves** derived
  from this target's ops + this target's ledger (~237),
- for each effective prefix calls `syncpkg.ScanDrift(root, pre, oldLedger)` (~243)
  — which flags any file under `pre` not in `oldLedger`.

`internal/sync/orphans.go` `Orphans(old, next)` diffs the current target's old
vs new ledger to pick deletions.

Both are single-target. For an **owned-subdir** prefix (`.claude/...`,
`.cursor/rules/...`) that is correct — only one adapter owns it. For a
**shared-subdir** leaf, a sibling adapter may legitimately own the same leaf, so
single-target reasoning misfires:
- **Drift false positive:** target B's drift scan sees target A's file as
  unmanaged → `ErrMidLifeDrift`, sync fails. (The confirmed ADV-1 symptom.)
- **Orphan cross-delete:** target A removing skill X orphan-deletes the shared
  leaf while target B's ledger still claims it (only observable when a run
  targets A but not B).

## Design

**Principle: for shared-subdir leaves only, the "managed" set is the union of
all targets' ledgers; owned-subdir prefixes are unchanged (single-target).**

1. **Sibling ledger set.** In `applyTarget`, load the ledgers of the other
   targets in `req.Targets` (reuse `loadLedger`; missing ledger = empty, not an
   error). Cheap: a handful of small JSON files, once per target sync.

2. **Drift (shared leaves union-aware).** Add a drift-scan variant that accepts
   extra known paths (or a merged known-set). For a prefix that is a
   shared-subdir leaf (`leafUnder(sharedPrefixes, pre) != ""`), the known set =
   current target's ledger ∪ every sibling ledger's entries under that leaf.
   Owned-subdir prefixes keep the current single-ledger `ScanDrift` exactly.

3. **Orphan (shared leaves union-aware).** When computing deletions, a path
   under a shared-subdir leaf is an orphan only if it is absent from the new
   ledgers of **all** targets that emit into that shared parent — not just the
   current target's next ledger. Concretely: keep a shared-leaf path if any
   sibling target's *current* ledger (or this run's sibling ops, when the run
   targets siblings too) still claims it. Owned-subdir orphans unchanged.

4. **Swap ordering (verify, likely already safe).** When codex and pi both emit
   the same leaf in one run, they swap sequentially with byte-identical content
   (the managed-file header is identical across adapters). The per-leaf sentinel
   (`.state-<leaf>`, PR #24) already scopes recover per leaf. Add a test that
   the second swap + the pre-stage `Recover` for the shared parent handle a
   just-swapped-by-a-sibling leaf cleanly; fix only if the test fails.

5. **Enable pi `skill`.** Flip pi `conceptKinds[skill]` → supported; add the
   `.agents/skills` shared-subdir declared output (scope-aware: same relative
   path at user scope); restore the skill emitter (copy codex's `emitSkill` +
   asset validation + managed header — identical, so codex and pi produce
   byte-identical SKILL.md). Update pi capabilities.yaml + tests.

## Implementation (TDD)

1. **Engine: sibling-ledger plumbing** — `applyTarget` loads sibling ledgers;
   thread them (or a merged known-set) into the drift scan and orphan computation
   for shared-subdir leaves only. Keep owned-subdir behavior byte-for-byte.
2. **`internal/sync` drift/orphan variants** — add union-aware entry points with
   unit tests (foreign file under shared leaf still detected as drift; a
   sibling-owned file under a shared leaf is NOT drift; orphan keeps a
   sibling-claimed shared leaf, deletes a truly-unclaimed one).
3. **Re-add the `[codex, pi]` cross-adapter engine test** (reverted from PR #32):
   distinct ids, same id, add/update/remove, idempotent re-sync, recover clean,
   foreign skill preserved. Must pass.
4. **Enable pi skill** (adapter changes above) + pi adapter tests (skill happy
   path, user-scope, assets).
5. **Four-adapter AGENTS.md + shared-skills round-trip** (claude/cursor/codex/pi)
   — user content byte-identical; shared skill tree co-owned cleanly.
6. **Docs**: `docs/adapters/pi.md` (skill → supported), CHANGELOG, master-plan
   Unit 11.5 cross-adapter test note.
7. **Gate + review**: `go vet ./... && go test -race ./... && golangci-lint run`,
   then **`ce-code-review`** (swap/drift/orphan is data-loss-critical), then a
   real `sync` dogfood targeting `[codex, pi]`.

## Risks

- **Data loss** if orphan coordination is wrong (deleting a leaf a sibling still
  owns) or if drift under-detects (letting a real foreign edit through). Mitigate
  with the union-aware unit tests (both directions) + adversarial review + the
  existing foreign-skill-preservation guards.
- **Scope creep**: keep owned-subdir paths on the exact current code path; the
  union logic applies *only* when `leafUnder` matches a shared prefix.

## Out of Scope

- pi `command` + file-leaf stage/swap (PR3).
- Cross-adapter *dedup* (writing the shared leaf once instead of once-per-adapter)
  — an optimization; byte-identical double-write is correct and simpler. Revisit
  only if it shows up as a perf/QoL issue.
- Hierarchy composition; Gemini/Windsurf/LM Studio adapters.

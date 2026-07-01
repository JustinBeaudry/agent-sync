---
title: "fix: ADV-1 hardening — run-wide sync lock + dropped-target ledger warning"
status: approved
date: 2026-07-01
type: fix
origin: docs/plans/2026-06-30-003-fix-adv1-cross-adapter-shared-subdir-plan.md
master_plan: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# fix: ADV-1 hardening — run-wide sync lock + dropped-target ledger warning

## Summary

PR #33 made codex+pi shared-subdir co-ownership correct for the primary case
(one sequential `sync` process). Its 4-persona review confirmed **no data
loss**, but flagged residual **orphan leaks** (stranded files, never deletions)
that this plan closes:

- **P1 — cross-process cross-delete race.** Co-ownership infers "does a sibling
  still own this leaf?" from the union of per-target ledgers, read without any
  run-wide lock (`engine.Sync` only `Recover`s then loops per target;
  `applyTarget` holds only its own `TargetLock`). Two overlapping processes —
  `sync --target codex` and `sync --target pi`, both removing the shared skill —
  each read the *other's* still-populated ledger, each defer the delete to the
  other, and strand the file with no ledger claiming it.
- **P2 — dropped-target leak.** A target removed from the manifest is never
  visited again (`req.Targets = m.Targets`), so its ledger and any leaf it
  solely owned are stranded — there is no ledger GC.

## Design

**1. Per-workspace run-wide sync lock (fixes P1).**
Acquire one flock-backed lock at the top of `engine.Sync` (after `Recover`,
before the target loop), release at the end. This serializes concurrent syncs on
the same workspace so the cross-target read-decide-write is atomic across
processes: the second process reads the first's *committed* ledgers and the
"last releaser deletes the leaf" convergence holds cross-process. It also makes
concurrent shared-leaf swaps orderly instead of one failing with `ErrStale`.

- Reuse the existing `flock` pattern (`internal/locks`). New primitive
  `locks.NewRunLock(root)` → lock file at `.agent-sync/state/.sync.lock`
  (machine-id-tagged like `TargetLock`, advisory, releases on process death).
- **Unconditional** acquisition (not gated on shared prefixes): the engine can't
  know a target declares a shared prefix without initializing it, and a
  per-workspace sync lock is the correct granularity anyway — concurrent syncs
  on one workspace already contend on the same files. Document the semantics
  change: two `sync --target X` / `--target Y` on one workspace now serialize
  (previously overlapped via per-target locks). This is safer, not a regression
  for any real workflow (no daemon; the CLI is invoked serially in practice).
- On contention: block with the same timeout/`ErrLocked` behavior as
  `TargetLock` (retry then fail loudly), so a wedged holder surfaces rather than
  corrupts.

**2. Dropped-target ledger warning (mitigates P2; full GC deferred).**
At sync start, list the on-disk ledgers under `.agent-sync/state/*.json` and, for
any target NOT in `req.Targets`, emit a one-time warning naming the orphaned
ledger and its owned path count, pointing at the (future) `unmanage` command.
This surfaces the leak without destructive action.

- **Not** silent GC: reclaiming a dropped target's files is exactly
  `unmanage <target>` semantics (delete everything that target owned) — a
  deliberate, destructive operation that belongs in its own command (master-plan
  Unit 24), not an implicit side effect of every sync. Silently deleting a
  dropped target's files on the next unrelated sync would be a surprising,
  data-destroying behavior. A warning is the safe, honest mitigation.

**3. Stale-sibling release (P2, adversarial ADV-STALE):** covered transitively —
the run-wide lock serializes the read-decide-write, and the dropped-target
warning surfaces a sibling ledger that no live target backs. No separate change;
honoring a filtered-out sibling's ownership is intended behavior, not a bug.

## Implementation (TDD)

1. `internal/locks/runlock.go` (+ test) — `NewRunLock(root)` / `Acquire`, mirroring
   `TargetLock` (flock, machine-id tag, timeout→`ErrLocked`, release-on-death).
2. `internal/engine/engine.go` — acquire the run lock in `Sync` after `Recover`,
   `defer release()`. On `ErrLocked`, return a clear "another agent-sync sync is
   running in this workspace" error. Leave `Plan` (validate, read-only) lock-free.
3. `internal/engine/target.go` (or a helper in `Sync`) — scan `.agent-sync/state`
   for `<target>.json` files whose target ∉ `req.Targets`; emit a warning per
   orphaned ledger (path count + `unmanage` pointer). Read-only; never deletes.
4. Tests:
   - run-lock: concurrent `Sync` on one workspace serializes (second blocks/gets
     `ErrLocked` under a zero timeout); a run releases the lock on completion.
   - cross-delete race regression: simulate the sequential-equivalent — codex
     releases then pi (in one locked run) deletes; and assert two *serialized*
     filtered removals converge to the leaf deleted (no strand). (True concurrent
     processes are hard to unit-test; assert the lock is held across the target
     loop + the serialized-convergence property.)
   - dropped-target warning: seed a `pi.json` ledger, sync with `Targets=[codex]`
     (pi dropped), assert the warning fires and NO file is deleted.
5. Docs: `docs/adapters/` / a concurrency note; CHANGELOG `[Unreleased]/Fixed`.
6. Gate: `go vet ./... && go test -race ./... && golangci-lint run`, then
   **`ce-code-review`** (touches the concurrency/locking + sync-entry path), then
   a real dogfood.

## Code review findings (correctness + adversarial, 2026-07-01)

Both verified the core safe: run-lock release-on-every-path (defer + `sync.Once`),
`run → target → file` acquisition order (no deadlock), acquire-before-Recover
correct, byte-equivalent sidecar refactor, and `warnOrphanLedgers` never deletes.
Two real findings, both fixed:

- **P1 (adversarial): the run lock broke the post-merge git-hook yield.** A hook
  sync contending on the *target* lock used to get `StatusBlocked` (clean
  summary, exit 0); the run lock returned `ErrRunLocked` as a hard error, so a
  contended `git pull` would stall then fail (violating AGENTS invariant #3).
  Fixed: on `ErrRunLocked`, `Sync` returns a clean summary with all targets
  `StatusBlocked` (nil error) — the post-merge path yields and exits 0, matching
  the target-lock behavior. Added a short `RunLockTimeout` (post-merge sets 3s)
  so a contended hook yields fast instead of the multi-minute default. Regression
  test: `TestSync_RunLockContendedReturnsBlockedNotError`.
- **P1 (correctness): `warnOrphanLedgers` mis-fired on `capability-report.json`.**
  The state dir also holds non-ledger `.json` files; the scan warned
  "unmanage capability-report" on every sync after the first. Fixed: only warn
  when `ledger.Load` positively identifies a real per-target ledger (`lerr == nil`
  or a populated `ErrLedgerSchemaTooOld`); strict decode rejects
  capability-report.json. Regression tests: `TestSync_SecondSyncNoOrphanWarningForStateFiles`,
  `TestSync_FilteredTargetNoOrphanWarning`.
- **P2/P3 (advisory):** `--break-lock` is not yet CLI-wired for either lock
  (pre-existing) — softened the runlock comment + documented recovery (end the
  holder pid); over-serialization of concurrent same-workspace syncs is the
  accepted cost of closing the race (documented in Risks). Both deferred.

## Out of Scope

- **Ledger GC / reclaiming a dropped target's files** — that is the `unmanage`
  command (Unit 24). This plan warns; it does not delete.
- Divergent co-emission path-granular swap (guarded fail-closed in PR #33).
- pi `command` (file-leaf swap); hierarchy composition; Gemini adapter.

## Risks

- Over-serialization: the run-wide lock serializes concurrent same-workspace
  syncs. Accepted (safer; no real concurrent-sync workflow). Mitigate surprise
  with a clear `ErrLocked` message.
- A stale lock from a crashed process: advisory flock releases on process death,
  so a busy lock means a live holder; the existing `--break-lock` escape hatch
  (per `TargetLock`) should extend to the run lock.

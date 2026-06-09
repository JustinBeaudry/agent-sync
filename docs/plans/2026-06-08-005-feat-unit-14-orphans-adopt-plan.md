---
title: Unit 14 — Orphan deletion + adopt-prefix primitives
type: feat
status: active
date: 2026-06-08
origin: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# Unit 14 — Orphan deletion + `--adopt-prefix` primitives

## Overview

The ledger-diff orphan-deletion primitives, the mid-life drift guard,
and the safe `--adopt-prefix` flow (backup tarball + typed-name
confirmation + ledger adoption). Requirement R10. Depends on Units 12
(ledger) and 13 (atomic swap).

## Scope

**In scope (this PR — primitives):**
- `internal/sync/orphans.go` — `Orphans(old, new)` (ledger-diff only,
  never a filesystem scan), `DeleteOrphans` (post-ledger-durable,
  out-of-band-delete tolerant), `CheckExpectedDeletions` (`--expect-deletions=N` guard).
- `internal/sync/drift.go` — `ScanDrift` → `ErrMidLifeDrift` naming the
  first rogue file; a ledger entry missing on disk is NOT drift.
- `internal/sync/adopt.go` — `Backup` (`.tar.gz` of the prefix to
  `.aienv/state/backups/<target>-<ts>.tar.gz`), `ConfirmAdopt` (exact
  typed target name; `y`/`yes` rejected), `AdoptEntries` (records every
  existing file as-adopted with its current hash), `BackupRel`.
- `internal/sync/walk.go` — shared `walkFiles` / `readFile` over the
  workspace `os.Root` FS.

**Deferred (documented divergence from master plan Unit 14):**
- `internal/cli/cmd_adopt.go` — the adopt subcommand + interactive
  prompt. The Cobra tree does not exist until Unit 16; the CLI wiring
  (and `--adopt-prefix` / `--adopt-prefix-no-backup` / red-warning UX,
  scope-all-vs-one) lands there, consuming these primitives. Consistent
  with how Units 12a/13 shipped primitives ahead of their wiring.

## Key Technical Decisions

1. **Orphan set is the ledger diff only** (`old paths \ new paths`),
   never a filesystem scan — so mid-life drift can never induce a
   phantom orphan deletion.
2. **Delete after the new ledger is durable** (caller-owned ordering),
   so a crash mid-delete is recoverable. `DeleteOrphans` treats an
   already-gone path as a silent no-op (out-of-band delete).
3. **Adoption is destructive-safe**: backup the whole prefix first,
   require the exact typed target name to confirm (`y`/`yes` insufficient,
   mirroring `gh repo delete`), then record existing files as-adopted.
   A later sync's normal orphan deletion removes adopted-but-not-re-emitted
   files; the backup preserves them.
4. **Mid-life drift refuses, never clobbers**: `ScanDrift` fails closed
   with `ErrMidLifeDrift` pointing the user at `--adopt-prefix`.
5. Timestamps and SHAs are caller-stamped (no wall-clock in the core)
   for deterministic tests and reproducible runs.

## Test scenarios (shipped)

- `Orphans` diff (old\new), identical ledgers → none.
- `DeleteOrphans` removes present, no-ops missing.
- `CheckExpectedDeletions` (-1 passes; match passes; mismatch → sentinel).
- `ScanDrift` rogue file → `ErrMidLifeDrift` naming it; all-managed → ok;
  ledger-entry-missing-on-disk → not drift.
- `ConfirmAdopt` exact-name only; `Backup` tarball contents == prefix
  contents; `AdoptEntries` hashes/sizes/paths correct.

## Verification

`go vet`, `go test -race`, `golangci-lint` clean; windows + linux
cross-compile; coverage ≥ 80%. Backup tarball contents match prefix
bytes; orphan deletion is diff-bounded.

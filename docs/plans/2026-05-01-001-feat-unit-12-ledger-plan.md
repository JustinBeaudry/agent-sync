---
title: "feat: Unit 12 -- ledger + per-target lock"
type: feat
status: active
date: 2026-05-01
unit: 12
master_plan: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# feat: Unit 12 — Ledger + per-target lock

## Goal

Implement the per-target ledger (`.aienv/state/<target>.json`) with content
hashes, schema versioning, and `gofrs/flock`-backed locking with stale-PID
detection. Also implement the per-external-file flock registry that serializes
concurrent `write_tool_owned` operations on shared files like `AGENTS.md` and
`.mcp.json`.

## Sub-units

- [x] **12.1** `internal/ledger/types.go` — `Entry`, `Ledger` types; sentinel errors
- [x] **12.2** `internal/ledger/load.go` — load from `.aienv/state/<target>.json`; corrupted/missing → `ErrLedgerCorrupted`/`ErrLedgerNotFound`
- [x] **12.3** `internal/ledger/write.go` — atomic write via `fsroot.StagedWrite`; MkdirAll for state dir
- [x] **12.4** `internal/ledger/migrate.go` — schema-version upgrade; requires `--migrate-state` flag or interactive confirm
- [x] **12.5** `internal/locks/flock.go` — `AcquireTarget` with bounded `TryLockContext`; PID+host sidecar; stale-PID detection; `--break-lock` path
- [x] **12.6** `internal/locks/filelock.go` — per-external-file registry keyed by canonical absolute path; `AcquireFile`
- [x] **12.7** Tests: `roundtrip_test.go`, `migrate_test.go`, `flock_test.go`, `filelock_test.go`

## Key invariants

- Ledger lives at `<workspace>/.aienv/state/<target>.json` — outside the
  reserved prefix so a user nuking the prefix doesn't destroy orphan-detection
  signal (plan decision #13).
- Content hash per entry is SHA-256 of emitted bytes at emission time (not
  re-hashed on read).
- Missing or JSON-corrupted ledger → `ErrLedgerCorrupted` / `ErrLedgerNotFound`;
  callers route to first-sync-guard path (plan decision #13).
- Schema migration is gated: callers must pass `MigrateState: true` or the
  load returns `ErrSchemaVersionMismatch` with explicit guidance.
- Lock file: `.aienv/state/<target>.lock`; sidecar `.aienv/state/<target>.lock.pid`
  records `{pid, host, started_at}`.
- Stale-lock break: PID dead (signal-0 fail) AND older than 2× timeout →
  auto-break with stderr notice; otherwise `ErrLockHeldByLiveProcess`.
- Per-file registry uses `sync.Mutex`-guarded map + `gofrs/flock` on the
  real filesystem path; keyed by `filepath.Clean(filepath.Abs(path))`.

## Files created

```
internal/ledger/
  types.go
  load.go
  write.go
  migrate.go
  roundtrip_test.go
  migrate_test.go
internal/locks/
  flock.go
  flock_unix.go    (signal-0 PID check)
  flock_windows.go (OpenProcess PID check)
  filelock.go
  flock_test.go
  filelock_test.go
```

## Dependencies

- `internal/fsroot` (StagedWrite, OpenWorkspaceRoot) — Unit 1 ✓
- `internal/workspace` (state dir path helper) — Unit 3 ✓
- `github.com/gofrs/flock` — already in go.mod ✓

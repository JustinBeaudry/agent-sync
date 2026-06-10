# Atomic swap — operations & recovery

`agent-sync` updates a reserved-subdirectory target (e.g. `.claude/rules/agent-sync/`)
**atomically**: a sync either fully lands its new generation or leaves
the previous one byte-intact. This is done with a two-rename swap under
the single workspace `os.Root`, a persisted sentinel, and a startup
recovery reconciler. A crash at any point is recoverable to a clean
pre-sync-or-post-sync state — never a torn tree.

## How a swap works

For a target prefix `<workspace>/<parent>/agent-sync/` the new generation is
staged at `<parent>/.agent-sync-staging/<timestamp>-<sha>/agent-sync/`, then
promoted:

1. Write sentinel `.state = intend`.
2. `Rename(prefix → prefix.old)` — move the live generation aside (skipped on a first sync).
3. Sentinel → `step1_done`.
4. `Rename(staging/agent-sync → prefix)` — promote the new generation.
5. Sentinel → `step2_done`.
6. `RemoveAll(prefix.old)` (best-effort) and delete the sentinel.

Both rename operands are relative to the workspace root, so the swap can
never cross filesystems and never renames a directory whose handle it
holds open.

## Recovery states

Run `agent-sync sync --recover` (or it runs automatically at sync start) to
reconcile any half-completed swap. The reconciler scans
`<parent>/.agent-sync-staging/*/.state`:

| Sentinel | On-disk shape | Action |
|----------|---------------|--------|
| none | prefix present | clean — nothing to do |
| `intend` | no `.old` | crash before step 1 → discard the staging generation |
| `intend` | `.old` present | **impossible** — logged, requires operator intervention |
| `step1_done` | prefix absent, `.old` + staging present | crash between steps → complete the promotion |
| `step1_done` | prefix + `.old` both present | **impossible** — logged, requires operator intervention |
| `step2_done` | `.old` present | crash before cleanup → remove `.old`, drop sentinel |

The reconciler is idempotent. The two "impossible" rows are defensive —
they are never guessed at; they are surfaced for a human.

## Pre-flight refusal (`ErrStale`)

If a leftover `prefix.old` or a sentinel at `intend`/`step1_done` is
found and `--recover` was not just run, the sync refuses with `ErrStale`
and recommends `agent-sync sync --recover`. This stops a second sync from
stomping a half-completed one.

## Retention & scratch cleanup

The last 3 staging generations per target are kept for forensics; older
ones are pruned automatically. `agent-sync sync --clean-scratch` force-clears
all staging directories for a target.

## Error taxonomy

| Error | Cause | Behavior |
|-------|-------|----------|
| `ErrLocked` | Windows sharing/access violation on rename | bounded retry (~4s, 5 attempts), then surfaced with the retry count |
| `ErrCrossVolume` | rename crossed filesystems (`EXDEV`) | abort; only reachable if a bind-mount/submount lives inside the workspace |
| `ErrStale` | leftover half-sync at startup | run `--recover` |
| `ErrPermission` | ACL/permission denial | abort, non-retryable |

## Windows: blocked renames

If a sync repeatedly fails with `ErrLocked`, an editor, terminal, or
agent likely holds a file in the target open. Close it and retry. The
opt-in `--diagnose` flag is reserved for read-only Restart-Manager
process enumeration (not yet implemented); for now, check which tool has
the path open.

## Antivirus

Third-party AV occasionally locks files mid-rename, surfacing as
recurring `ErrLocked`. If needed, add a narrow exclusion for the
**`.agent-sync-staging/`** scratch directory only — never exclude the live
reserved prefix. Windows Defender is generally fine without exclusions.

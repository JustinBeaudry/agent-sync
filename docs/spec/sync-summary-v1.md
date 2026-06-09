# Sync summary — v1 (`--output=json` schema)

Stable, versioned schema for `agent-sync sync` output. Built by
`internal/report`. CI consumers gate on `schema_version`.

## Per-target status tokens

ASCII tokens (color is a rendering concern; the token is identical
colored or not):

| Token | Meaning |
|-------|---------|
| `ok` | target synced, changes applied |
| `unchanged` | target synced, no changes |
| `failed` | target failed (best-effort: isolated; reported per-target) |
| `skipped` | target not attempted (e.g. disabled) |
| `rolled-back` | atomic-mode: another target failed, this one's swap was reverted |
| `blocked` | target's lock was held by another process |

There is **no** per-target `partial` — partial is a top-line concept.

## Top-line outcome

- `OK <counts>` — all targets `ok`/`unchanged`; exit 0.
- `PARTIAL <counts>` — best-effort with a mix of success and `failed`; exit 1.
- `FAIL <counts>` — atomic rollback (`rolled-back > 0`), or zero successes, or any `blocked`; exit 1.

`<counts>` lists only non-zero tallies in a fixed order: ok, unchanged,
failed, skipped, rolled-back, blocked.

## `--output=json` document

```json
{
  "schema_version": 1,
  "workspace": "<abs path>",
  "commit": "<sha or empty>",
  "generated_at": "<RFC3339, caller-stamped>",
  "mode": "atomic | best-effort",
  "targets": [
    {
      "target": "claude",
      "status": "ok",
      "counts": { "written": 3, "deleted": 0, "unchanged": 0, "warnings": 0 },
      "duration_ms": 12,
      "paths": [".claude/rules/aienvs/a.mdc"],
      "error": "<present only when failed/blocked>"
    }
  ],
  "summary": {
    "line": "OK 1 ok",
    "ok": 1, "failed": 0, "skipped": 0, "unchanged": 0,
    "rolled_back": 0, "blocked": 0, "exit_code": 0
  }
}
```

## Determinism

`targets` is sorted by target name and nil slices serialize as `[]`
(never `null`), so the same inputs produce byte-identical output across
platforms. The caller stamps `generated_at` (the package holds no
wall-clock).

## Modes

- **atomic** (default): all targets' swaps land or none do; any failure
  rolls back the rest (`rolled-back`).
- **best-effort** (`--best-effort`): each target swaps independently; a
  failure is isolated and reported per-target.

A required-unmet capability fails the sync in **both** modes; the
capability report (see `capability-report-v1.md`) is still written.

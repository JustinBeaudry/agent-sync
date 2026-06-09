# `aienvs validate` output (v1)

Status: stable · Added: 2026-06-09 · Plan: `docs/plans/2026-06-08-007-feat-cli-tui-sync-engine-plan.md` (U4)

`aienvs validate` is a dry run: it computes what a sync *would* change
without mutating the workspace. It is the CI drift guard.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | No drift — every target is up to date |
| 1 | Drift detected — at least one target has pending changes or an out-of-band modification |
| 2+ | Operational error (manifest load, materialize, adapter failure) |

## JSON contract (`--output=json`)

```jsonc
{
  "schema_version": 1,
  "workspace": "/abs/path/to/workspace",
  "commit": "<resolved canonical SHA>",
  "drift_detected": true,
  "targets": [
    {
      "target": "claude",
      "would_create": [".claude/rules/aienvs/no-fri.md"],
      "would_update": [],
      "would_delete": [],
      "out_of_band_modified": [],
      "warnings": [],
      "error": ""              // present only when this target failed to plan
    }
  ]
}
```

- All list fields are always present (empty arrays, never null).
- `out_of_band_modified` lists managed files whose on-disk sha256 diverges
  from the ledger — a hand-edit inside a reserved prefix.
- `drift_detected` is true when any target has a non-empty change set or
  out-of-band modification.

## Text output

When there is no drift: `No drift: all targets are up to date.`
Otherwise, per target, one line per change labeled `create` / `update` /
`delete` / `out-of-band-modified` / `warning`.

## Deferred

- `--diff` (unified-diff bodies for text files) is planned but not yet
  implemented — it requires threading op content through the dry-run plan.
  Tracked as residual follow-up work.
- `--no-fetch` / `--fetch` network-posture overrides follow sync's flags
  once the URL fetch path grows them.

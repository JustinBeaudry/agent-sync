# Capability report — v1

Persisted at `.aienv/state/capability-report.json` during staging
(before the swap), so it survives an atomic rollback (decision #21).
`agent-sync validate` computes the same shape in memory without writing.
Built by `internal/report`.

## Schema

```json
{
  "schema_version": 1,
  "generated_at": "<RFC3339, caller-stamped>",
  "targets": [
    {
      "target": "claude",
      "concept_kinds": { "rule": "supported", "skill": "unsupported" },
      "write_tool_owned": true,
      "progress": false,
      "required_unmet": ["skill"]
    }
  ]
}
```

## Fields

- `concept_kinds` — each IR concept kind the target reports, mapped to
  its tri-state level: `supported` | `partial` | `unsupported`.
- `write_tool_owned`, `progress` — adapter capability flags.
- `required_unmet` — the concept kinds the IR **requires** of this target
  that are not `supported` (unsupported, partial, or unreported). A
  non-empty `required_unmet` on any target fails the sync in both atomic
  and best-effort modes.

## Determinism

`targets` and each `required_unmet` are sorted; nil slices serialize as
`[]`. Same inputs → byte-identical output. The caller stamps
`generated_at`.

# Emit op-content channel (v1, additive)

Status: stable · Added: 2026-06-08 · Plan: `docs/plans/2026-06-08-007-feat-cli-tui-sync-engine-plan.md` (U0)

## Why

Invariant #2 (`AGENTS.md`): *adapters never write files directly; the CLI core
performs the actual writes.* The original v1 `emit` response carried only an
`ops_performed` summary (`{op, path}`) — the op **content** was computed by the
adapter and discarded. That made invariant #2 unsatisfiable: the CLI had nothing
to write. This was surfaced while wiring the sync engine and resolved by adding a
content channel rather than letting adapters write files themselves.

## The field

`EmitResult` gains one **additive, optional** field:

```jsonc
{
  "ops_performed": [ { "op": "write_file", "path": ".claude/rules/aienvs/r.md" } ],
  "ops": [
    { "op": "write_file", "path": ".claude/rules/aienvs/r.md",
      "mode": 420, "encoding": "utf8", "content": "..." }
  ]
}
```

- `ops` is an array of **full op envelopes**, one per performed op, in the **same
  order** as `ops_performed`.
- Each envelope is the existing frozen per-op wire form (`write_file`,
  `write_tool_owned`, `mkdir`, `delete`, `warning`) and is decoded with
  `contract.DecodeOp`. No new op shapes are introduced.
- Content encoding (`utf8` / `base64`) and the `MaxOpPayloadBytes` cap are the
  existing op-envelope rules — they ride along unchanged.

## Compatibility

This is additive under the **"freeze the wire frame, grow capabilities"** policy,
so it does **not** bump the protocol version string (`aienvs/v1`):

- A producer that omits `ops` decodes to `Ops == nil` (legacy behavior; the CLI
  simply has no content to write for that adapter).
- A consumer that ignores `ops` is unaffected — `ops_performed` is unchanged and
  remains the basis for the declared-outputs and capability-lied gates.

## Producer/consumer contract

- **Adapters** (producers): set `ops` to the marshaled envelopes for every op they
  perform, in `ops_performed` order. The bundled `claude` and `cursor` adapters do
  this via `emittedOps.wireOps()`.
- **CLI core** (consumer): decode each `ops[i]` with `contract.DecodeOp`, then
  apply: `write_file` → `fsroot.Root.StagedWrite`; `write_tool_owned` →
  `merge.ApplyToFile`; `mkdir` → directory create; `delete`/`warning` → recorded.
  `ops_performed` stays the summary used for reporting and the integrity gates.

---
title: agent-sync `cursor` adapter — concept→destination reference
status: active
date: 2026-06-08
adapter: cursor
contract_version: agent-sync/v1
---

# `cursor` adapter

The bundled `cursor` adapter maps the v1 IR concept set to Cursor's
actual on-disk layout. Implementation lives in
[`internal/adapter/bundled/cursor/`](../../internal/adapter/bundled/cursor/);
the per-kind support declaration is in
[`capabilities.yaml`](../../internal/adapter/bundled/cursor/capabilities.yaml).

The adapter owns:

- The **`.cursor/rules/agent-sync/` reserved subtree** for rules, emitted
  as `.mdc` files.
- A **per-entry slice** of `.cursor/mcp.json` for `mcp-server-entry`
  IR nodes (under `/mcpServers/agentsync_<id>` JSON pointers), plus a
  `.cursor/.agent-sync-managed` sidecar marker.
- A **managed section** of the workspace-root `AGENTS.md` for
  `agents-md` IR nodes.

The adapter does **not** own:

- `command` or `plugin-reference` — Cursor reads neither at the project
  level in a way agent-sync can own today, so they are declared
  `unsupported` (see "Unsupported kinds" below). (`skill` is supported via
  the shared `.agents/skills/` tree — see the table.)
- The legacy `.cursorrules` file — it is never written, migrated, or
  mutated (see "Legacy `.cursorrules`" below).
- The whole of `AGENTS.md` — the adapter writes exactly one
  agent-sync-marked section; user content and other adapters' sections
  are preserved by the ledger-driven merge (Unit 12a).
- Anything outside the paths in the table; user-authored files inside
  the same parent directories — orphan detection is scoped to
  agent-sync-emitted paths only (Unit 14 of the master plan).

## Concept → destination

| IR kind | Status | Destination | Locator | Notes |
|---|---|---|---|---|
| `agents-md` | supported | workspace-root `AGENTS.md` | section `agent-sync:<id>` | Section markers `<!-- agent-sync:begin id=<id> -->` … `<!-- agent-sync:end id=<id> -->`. User content outside the section is preserved. Cursor reads `AGENTS.md` directly (no companion file). |
| `rule` | supported | `.cursor/rules/agent-sync/<id>.mdc` | n/a | Managed-file header prepended. v1 emits frontmatter-less `.mdc` (the IR strips frontmatter at decode); the rule behaves as a manual / agent-requested rule until IR frontmatter exposure lands. No `paths:` ward — that is a Claude Code bug with no Cursor equivalent. |
| `mcp-server-entry` | supported | `.cursor/mcp.json` | json-pointer `/mcpServers/agentsync_<id>` | A sidecar `.cursor/.agent-sync-managed` file is written alongside `.cursor/mcp.json` because JSON has no comment syntax. |
| `skill` | supported | `.agents/skills/agent-sync-<id>/SKILL.md` (+assets) | n/a | Shared cross-tool tree (co-owned with codex/pi/antigravity; ADV-1-safe, byte-identical `SKILL.md`). Cursor reads `.agents/skills/` and `~/.agents/skills/`. Scope-invariant: the relative path resolves under `$HOME` at user scope. |
| `command` | unsupported | n/a | n/a | Cursor commands live in a **flat** `.cursor/commands/` dir (project) / `~/.cursor/commands/` (user), co-resident with user files. Owning individual files there needs `file-leaf` engine support (planned); subdirectory namespacing works in the Cursor IDE but not the CLI. Until then, targeting `command` surfaces a degradation warning and emits no files. |
| `plugin-reference` | unsupported | n/a | n/a | Cursor does not load project-level plugin registries. Targeting `plugin-reference` at `cursor` surfaces a degradation warning and emits no files. |

## Path-verification notes

The destinations above are based on Cursor's documented project-level
discovery rules. If a future Cursor release moves or renames any of
these paths, the adapter and this table need to be updated together.

- **Rules** — Cursor reads project rules from `.cursor/rules/` as
  `.mdc` (Markdown-with-frontmatter) files and supports nested
  subdirectories, which is what makes `.cursor/rules/agent-sync/<id>.mdc`
  a valid owned subdirectory. See
  [Cursor rules](https://docs.cursor.com/context/rules).
- **`.cursor/mcp.json`** — Project-scoped MCP servers live in a flat
  `.cursor/mcp.json` file (a file, not a directory) with the
  `{"mcpServers": {"<name>": {...}}}` shape — the same shape as
  Claude's `.mcp.json`, at a different location. See
  [Cursor MCP](https://docs.cursor.com/context/mcp).
- **`AGENTS.md`** — Cursor reads an `AGENTS.md` file at the project
  root (the cross-tool [agents.md](https://agents.md) convention).

## Unsupported kinds

Rather than emit dead files into directories Cursor cannot own cleanly,
the adapter declares these kinds `unsupported` and surfaces a
degradation `warning` op so the sync report names the gap honestly. No
files are written for these kinds.

- **`command`** — Cursor commands live in a **flat** `.cursor/commands/`
  directory (project) and `~/.cursor/commands/` (user), co-resident with
  the user's own command files. Owning individual files in a shared flat
  dir requires the engine's `file-leaf` OutputMode (planned — see the
  file-leaf plan). Subdirectory namespacing (`.cursor/commands/agent-sync/`)
  is read by the Cursor IDE but **not** the Cursor CLI, so it is not a
  portable option. `command` flips to `supported` once `file-leaf` lands.
- **`plugin-reference`** — Cursor has no project-level plugin manifest
  that agent-sync can populate.

> **`skill` is now supported** (was unsupported in earlier releases):
> Cursor reads the shared `.agents/skills/` tree, so the adapter co-owns
> it with codex/pi/antigravity. See the concept table above.

## Legacy `.cursorrules`

`.cursorrules` (a single root file) is the superseded predecessor of
the `.cursor/rules/` directory. The `cursor` adapter never writes,
migrates, or mutates `.cursorrules`.

Detecting a pre-existing `.cursorrules` and warning the user to
migrate is **not** the adapter's job: the adapter performs zero
filesystem I/O and cannot inspect the workspace (the project's
safe-filesystem layer, `internal/fsroot`, is the single enforcement
point for touching user paths). That legacy-detection warning is owned
by the sync engine (Unit 13 of the master plan), which walks the
workspace through `internal/fsroot`.

## Why `AGENTS.md` directly (not a companion)?

Cursor reads `AGENTS.md` at the project root natively, so the
`agents-md` IR kind targeted at `cursor` writes a managed section into
`AGENTS.md` itself — unlike the `claude` adapter, which routes
`agents-md` into a `CLAUDE.md` companion because Claude Code does not
read project-level `AGENTS.md`.

## User scope (`sync --user`)

The adapter is scope-aware. At user scope the scope root is `$HOME`, and the
destinations change because Cursor's user-global config differs from its
project config:

| IR kind | User-scope behavior |
|---|---|
| `mcp-server-entry` | Emitted to `~/.cursor/mcp.json` — Cursor's user-global MCP config, the same `{"mcpServers": {...}}` shape as the project file. The strict-JSON sidecar (`.agent-sync-managed`) is **suppressed**: `~/.cursor/mcp.json` is Cursor's own shared file, not an agent-sync-owned one. |
| `skill` | Emitted to `.agents/skills/agent-sync-<id>/` (→ `~/.agents/skills/…`), which Cursor reads at user scope. Scope-invariant — no remap needed. |
| `rule` | **Skipped.** Cursor has no file-addressable user-global rules location — "User Rules" live in Cursor's settings/cloud (Customize → Rules), not a writable file. |
| `agents-md` | **Skipped.** Cursor reads `AGENTS.md` at the project root and in subdirectories only; there is no user-global `~/AGENTS.md` or `~/.cursor/AGENTS.md`. |

Skipped kinds are not silently lost: `internal/coverage` reports a per-scope
warning ("cursor has no user-global location for <kind>; sync it at project
scope instead") in the sync output and the JSON `coverage_warnings`. To apply
rules or agents-md content broadly, sync them at **project** scope. (Verified
against Cursor docs + a Cursor-staff statement, 2026-06-30; Cursor is reworking
this area in 3.9 "Customize", so a future native `~/.cursor/rules/` could change
this.)

## Multi-adapter ownership notes

- `AGENTS.md` is shared by `cursor`, `codex`, and `pi`. The
  section-marker scheme (`<!-- agent-sync:begin id=<id> -->` …
  `<!-- agent-sync:end id=<id> -->`) is identical across those adapters so
  their sections coexist. The ledger-driven merge (Unit 12a) combines
  them with user content; the per-external-file lock (Unit 12)
  serializes concurrent writers. This adapter emits exactly one
  marked section and assumes nothing about sole ownership.
- `.cursor/mcp.json` is a Cursor-only file — it is not the same file
  as Claude's workspace-root `.mcp.json`. Each tool's MCP config lives
  under its own path.
- `.cursor/rules/agent-sync/` is a Cursor-only reserved subtree.

## Exit path

To unbind agent-sync from a workspace and remove every file the `cursor`
adapter has emitted:

```bash
agent-sync unmanage cursor
```

The command (Unit 24 of the master plan, not yet shipped) uses the
ledger to remove emitted files cleanly. User-authored files inside
the same directories are preserved.

## Authoritative references

- Implementation: [`internal/adapter/bundled/cursor/`](../../internal/adapter/bundled/cursor/)
- Capability declaration: [`internal/adapter/bundled/cursor/capabilities.yaml`](../../internal/adapter/bundled/cursor/capabilities.yaml)
- Spec — adapter protocol: [`docs/spec/adapter-protocol-v1.md`](../spec/adapter-protocol-v1.md)
- Spec — IR: [`docs/spec/ir-v1.md`](../spec/ir-v1.md)
- Plan: [`docs/plans/2026-06-08-001-feat-unit-10-cursor-adapter-plan.md`](../plans/2026-06-08-001-feat-unit-10-cursor-adapter-plan.md)
- Sibling adapter (template): [`docs/adapters/claude.md`](claude.md)
- Master plan (Unit 10 entry): [`docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md`](../plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md)
- Cursor rules: https://docs.cursor.com/context/rules
- Cursor MCP: https://docs.cursor.com/context/mcp
- AGENTS.md standard: https://agents.md

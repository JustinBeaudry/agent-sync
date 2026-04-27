---
title: aienvs `claude` adapter — concept→destination reference
status: active
date: 2026-04-27
adapter: claude
contract_version: aienvs/v1
---

# `claude` adapter

The bundled `claude` adapter maps the v1 IR concept set to Claude
Code's actual on-disk layout. Implementation lives in
[`internal/adapter/bundled/claude/`](../../internal/adapter/bundled/claude/);
the per-kind support declaration is in
[`capabilities.yaml`](../../internal/adapter/bundled/claude/capabilities.yaml).

The adapter owns:

- The **`.claude/` reserved subtree** for rules, commands, and skills
  (under per-subsystem `aienvs/` namespaces or `aienvs-*` folder
  prefixes).
- A **managed section** of the workspace-root `CLAUDE.md` for the
  `agents-md` companion overlay.
- A **per-entry slice** of the workspace-root `.mcp.json` for
  `mcp-server-entry` IR nodes (under `/mcpServers/aienvs_<id>` JSON
  pointers).

The adapter does **not** own:

- `AGENTS.md` at the workspace root (Claude Code does not read it at
  the project level — see "Why no AGENTS.md?" below).
- Anything outside the paths in the table.
- User-authored files inside the same parent directories — orphan
  detection is scoped to aienvs-emitted paths only (Unit 14 of the
  master plan).

## Concept → destination

| IR kind | Status | Destination | Locator | Notes |
|---|---|---|---|---|
| `agents-md` (claude overlay) | supported | workspace-root `CLAUDE.md` | section `aienvs:<id>` | Section markers `<!-- aienvs:begin id=<id> -->` … `<!-- aienvs:end id=<id> -->`. User content outside the section is preserved. |
| `rule` | supported | `.claude/rules/aienvs/<id>.md` | n/a | Managed-file header prepended. Bodies opening with `paths:` frontmatter trigger a degradation warning (see [Bug ward](#bug-ward-paths-frontmatter)). |
| `skill` | supported | `.claude/skills/aienvs-<id>/SKILL.md` plus assets | n/a | Folder name MUST equal skill name; the `aienvs-` prefix gives ownership isolation while honoring [Claude's skill-discovery constraint](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills). |
| `command` | supported | `.claude/commands/aienvs/<id>.md` | n/a | Available as `/aienvs:<id>` slash command without colliding with user commands. |
| `mcp-server-entry` | supported | workspace-root `.mcp.json` | json-pointer `/mcpServers/aienvs_<id>` | A sidecar `.aienvs-managed` file is written alongside `.mcp.json` because JSON has no comment syntax. |
| `plugin-reference` | unsupported | n/a | n/a | Claude Code does not load project-level plugin registries. Targeting `plugin-reference` at `claude` surfaces a degradation warning and emits no files. |

## Path-verification notes

The destinations above are based on Claude Code's documented
project-level discovery rules. If a future Claude Code release moves
or renames any of these paths, the adapter and this table need to
be updated together.

- **Rules / commands** — Claude Code reads project-local rules and
  commands from `.claude/rules/` and `.claude/commands/`
  (subdirectory recursion supported). The `aienvs/` subfolder is a
  free-namespacing convention; nothing in Claude Code requires it.
- **Skills** — `.claude/skills/<name>/SKILL.md` is the required
  shape per
  [Equipping agents for the real world with Agent Skills](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills).
  `<name>` in the folder must equal the `name:` value declared in
  `SKILL.md`'s frontmatter. The adapter preserves the IR body
  verbatim apart from the managed header, so the IR content **must**
  include whatever Claude-required frontmatter is needed — including
  a `name:` value that matches the emitted `aienvs-<id>` folder
  name. The adapter does not inject or rewrite frontmatter; that is
  the canonical-repo author's responsibility.
- **`.mcp.json`** — Workspace-root project-local MCP config per
  [Claude Code MCP docs](https://docs.claude.com/en/docs/claude-code/mcp).
  Not `.claude/mcp.json`.
- **`CLAUDE.md`** — Project-level memory file Claude Code loads
  automatically.

## Why no AGENTS.md?

Claude Code does not read project-level `AGENTS.md` as of 2026-04.
Other agents (Cursor, Codex, Pi) do. The aienvs convention is that
the `agents-md` IR kind targeted at `claude` writes a managed
section into **`CLAUDE.md`**, not `AGENTS.md`, so the content is
visible to Claude Code without writing to a file Claude Code
ignores.

If you also want the same content visible to Cursor or Codex, target
the IR node at multiple adapters (`targets: [claude, cursor]`); each
adapter writes to its tool's actual discovery path.

## Bug ward: `paths:` frontmatter

Claude Code's rule activation has known issues with `paths:`
frontmatter as of 2026-04 — rules are loaded but activation behavior
is inconsistent. The adapter still writes the rule, but emits a
`warning` op so the sync report surfaces the degradation. Authors
who depend on path-scoped rules should either remove the `paths:`
field or accept inconsistent activation.

The detection is best-effort: only frontmatter blocks where `paths:`
is the first non-blank key are caught. Frontmatter with `paths:`
after other keys passes silently in v1; tracking issue is filed
against the IR (full frontmatter exposure is a v1.x change).

## Multi-adapter ownership notes

- `.mcp.json` is also touched by the `cursor` adapter (under its own
  pointer base) and possibly future adapters. The merge step is
  ledger-driven (Unit 12a); each adapter's entries live under a
  distinct pointer prefix (`/mcpServers/aienvs_<id>` here vs.
  `/mcpServers/<other-prefix>_<id>` elsewhere) and are independently
  removable.
- `CLAUDE.md` is a Claude-only file — no other adapter writes to it.
- `AGENTS.md` is shared by `cursor`, `codex`, and `pi`; the section-
  marker scheme is identical across those adapters so they coexist.

## Exit path

To unbind aienvs from a workspace and remove every file the `claude`
adapter has emitted:

```bash
aienvs unmanage claude
```

The command (Unit 24 of the master plan, not yet shipped) uses the
ledger to remove emitted files cleanly. User-authored files inside
the same directories are preserved.

## Authoritative references

- Implementation: [`internal/adapter/bundled/claude/`](../../internal/adapter/bundled/claude/)
- Capability declaration: [`internal/adapter/bundled/claude/capabilities.yaml`](../../internal/adapter/bundled/claude/capabilities.yaml)
- Spec — adapter protocol: [`docs/spec/adapter-protocol-v1.md`](../spec/adapter-protocol-v1.md)
- Spec — IR: [`docs/spec/ir-v1.md`](../spec/ir-v1.md)
- Plan: [`docs/plans/2026-04-27-001-feat-unit-9-claude-adapter-plan.md`](../plans/2026-04-27-001-feat-unit-9-claude-adapter-plan.md)
- Master plan (Unit 9 entry): [`docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md`](../plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md)
- Claude Code skills: https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills
- Claude Code MCP: https://docs.claude.com/en/docs/claude-code/mcp

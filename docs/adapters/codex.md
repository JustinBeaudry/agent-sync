# Codex CLI adapter

The `codex` adapter compiles the agent-sync IR into the files **Codex CLI**
actually reads. Mapping validated against the official Codex docs in June 2026.

## ⚠️ Path reality vs. the original assumption

The original design assumed a `.codex/agent-sync/` reserved subdirectory symmetric
with `.claude/rules/agent-sync/`. **That subdirectory does not exist in v1.** Codex
CLI has no single per-tool reserved tree; instead its inputs live in three
distinct places, so the `codex` adapter owns no dedicated reserved prefix of its
own:

- **Skills** live under the **shared** `.agents/skills/` tree (a cross-tool
  convention Codex reads by walking from the cwd up to the repo root) — not
  under `.codex/`.
- **MCP servers** live **inside** the tool-owned `.codex/config.toml`.
- **Prose** lives in the **shared** workspace-root `AGENTS.md`.

If you are looking for a `.codex/agent-sync/` folder after a sync, there isn't one —
this is expected.

## Concept → destination mapping

| IR kind | Codex destination | How |
|---------|-------------------|-----|
| `agents-md` | workspace-root `AGENTS.md` | managed section between `<!-- agent-sync:begin id=<id> -->` / `<!-- agent-sync:end id=<id> -->` markers (shared with the `cursor` adapter) |
| `skill` | `.agents/skills/agent-sync-<id>/SKILL.md` (+ assets) | name-prefixed folder under the shared agents-skills tree |
| `mcp-server-entry` | `.codex/config.toml`, table `[mcp_servers.agentsync_<id>]` | `write_tool_owned` with a `toml-path` locator; user content elsewhere in the file is preserved |
| `rule` | — | **unsupported**: Codex has no per-tool rule concept; consolidate into `agents-md` or a skill |
| `command` | — | **unsupported**: Codex custom prompts are deprecated **and** user-home-only (`~/.codex/prompts/`), so they aren't repo-shareable; use a skill |
| `plugin-reference` | — | **unsupported**: no project-level plugin registry |

Unsupported kinds surface as honest degradation warnings in the capability
report — agent-sync never emits dead files into directories Codex doesn't read.

## Caveats (version-dependent, June 2026)

- **AGENTS.md size cap.** Codex stops adding context once the combined AGENTS.md
  size reaches `project_doc_max_bytes` (default **32 KiB**), in lookup order. A
  very large managed section can push past that limit.
- **AGENTS.override.md precedence.** When an `AGENTS.override.md` exists, Codex
  reads it *instead of* `AGENTS.md` in that directory — your synced section will
  not be loaded until the override is removed or merged.
- **Project-local MCP loading.** `.codex/config.toml` MCP entries are supported
  at the project scope, but if a particular Codex version does not load them,
  lift the emitted `[mcp_servers.agentsync_<id>]` table into the user-level
  `~/.codex/config.toml` — the table body is identical.

## Coexistence with other adapters

`codex`, `cursor`, and (later) `pi` all section-merge into the same workspace-root
`AGENTS.md`, each keyed by its node `id`. The marker syntax is byte-identical
across adapters, so sections coexist and are independently removable without
touching user prose or each other — see
`internal/merge` (`TestMergeMarkdown_CodexCursorCoexistenceAndIndependentRemoval`).

## Sources

- AGENTS.md discovery, size cap, override precedence: <https://developers.openai.com/codex/guides/agents-md>, <https://developers.openai.com/codex/config-advanced>
- Project-local config + `mcp_servers`: <https://developers.openai.com/codex/config-reference>, <https://developers.openai.com/codex/mcp>
- Skills under `.agents/skills`: <https://developers.openai.com/codex/skills>
- Custom prompts deprecated / user-home-only: <https://developers.openai.com/codex/custom-prompts>

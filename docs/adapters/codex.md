# Codex CLI adapter

The `codex` adapter compiles the agent-sync IR into the files **Codex CLI**
actually reads. Mapping last checked against the official Codex docs in July
2026.

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

## Managed Native Fragments

Codex supports the first native-fragment surfaces. Native fragments are authored
under `.agents/configs/codex/...`, resolved through the same hierarchy as
portable assets, and applied by core allowlists rather than by adapters writing
arbitrary tool config.

### Feature Flags

Author feature flags under `.agents/configs/codex/features/<id>/`:

```yaml
id: hooks
target: codex
path: .codex/config.toml
merge: toml-key
locator: features.hooks
visibility: team
inheritance: descendants
safety: passive
payload: payload.toml
```

```toml
[features]
hooks = true
```

agent-sync preserves existing `config.toml` content and only inserts or replaces
the requested `[features].<key>` entry. Codex currently documents feature flags
under the `[features]` table; `hooks` is stable and defaults to `true`.

### Lifecycle Hooks

Author lifecycle hooks under `.agents/configs/codex/hooks/<id>/`:

```yaml
id: pre-tool-policy
target: codex
path: .codex/hooks.json
merge: codex-hooks
locator: PreToolUse/pre-tool-policy
visibility: team
inheritance: descendants
safety: executable
payload: payload.json
```

```json
{
  "matcher": "Bash",
  "hooks": [
    {
      "type": "command",
      "command": "python3 .codex/hooks/pre_tool_use_policy.py",
      "statusMessage": "Checking Bash command"
    }
  ]
}
```

agent-sync generates `.codex/hooks.json` as an agent-sync-owned file. If an
unmanaged `.codex/hooks.json` already exists, sync fails closed until the user
moves or deletes that file. Codex remains responsible for project-local hook
trust review; agent-sync writes configuration, it does not bypass Codex's
`/hooks` trust flow.

## User scope (`sync --user`)

The adapter is scope-aware. At user scope the scope root is `$HOME`:

| IR kind | User-scope destination |
|---|---|
| `agents-md` | `~/.codex/AGENTS.md` — Codex's user-global instructions path. This is a genuine **remap**, not a directory coincidence: Codex does *not* read `~/AGENTS.md` at the user level. (Analog of Claude's `CLAUDE.md → ~/.claude/CLAUDE.md`.) |
| `mcp-server-entry` | `~/.codex/config.toml` — already the relative path `.codex/config.toml`, and in fact Codex's *primary* config location. Unchanged. |
| `skill` | `~/.agents/skills/agent-sync-<id>/SKILL.md` — the official user-global skills tree. Unchanged. |

Caveats specific to user scope (not handled by the adapter — documented so you
are not surprised):

- **`AGENTS.override.md` precedence.** Codex prefers `~/.codex/AGENTS.override.md`
  over `~/.codex/AGENTS.md`. If a stale override file exists, the synced managed
  section is shadowed until it is removed or merged.
- **`CODEX_HOME` relocation.** agent-sync assumes the default `~/.codex` (the
  hierarchy layer sets the user scope root to `$HOME`). If you relocate Codex's
  home via `CODEX_HOME`, the `config.toml` and `AGENTS.md` writes will not follow
  it. The skills tree at `~/.agents/skills/` is unaffected by `CODEX_HOME`.

(Verified against Codex official docs, 2026-06-30.)

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
- Feature flags and lifecycle hooks: <https://developers.openai.com/codex/config-basic#feature-flags>, <https://developers.openai.com/codex/hooks>
- Skills under `.agents/skills`: <https://developers.openai.com/codex/skills>
- Custom prompts deprecated / user-home-only: <https://developers.openai.com/codex/custom-prompts>

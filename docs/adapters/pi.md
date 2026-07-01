# `pi` adapter

The bundled `pi` adapter maps the v1 IR concept set to
[Pi](https://pi.dev)'s (`@mariozechner/pi-coding-agent`, repo
`earendil-works/pi`) on-disk layout. Implementation lives in
[`internal/adapter/bundled/pi/`](../../internal/adapter/bundled/pi/); the
per-kind support declaration is in
[`capabilities.yaml`](../../internal/adapter/bundled/pi/capabilities.yaml).

Pi is a structural sibling of the [`codex` adapter](codex.md): both
section-merge into the workspace-root `AGENTS.md`, and (in a future release)
both place skills in the shared `.agents/skills/` tree.

## Concept → destination

| IR kind | Status | Project destination | User-scope (`sync --user`) | Notes |
|---|---|---|---|---|
| `agents-md` | ✅ supported | `AGENTS.md` managed section `agent-sync:<id>` | `.pi/agent/AGENTS.md` (→ `~/.pi/agent/AGENTS.md`) | Pi reads `AGENTS.md` (root + parent-walk; `CLAUDE.md` accepted as an alias) and, at user scope, `~/.pi/agent/AGENTS.md`. Section markers `<!-- agent-sync:begin id=<id> -->` … `end`; user content outside the section is preserved. |
| `skill` | ✅ supported | `.agents/skills/agent-sync-<id>/SKILL.md` (+assets) | same relative path (→ `~/.agents/skills/…`) | Shared cross-tool tree (same as codex): one emitted skill serves both tools. codex+pi co-ownership of a leaf is made safe by the engine's union-aware drift/orphan checks (ADV-1). SKILL.md content is byte-identical to codex's. |
| `command` | ⏳ unsupported (planned) | — | — | Pi runs `/<name>` prompt templates from `.pi/prompts/<name>.md` (flat, non-recursive). Deferred: that dir is shared with user prompts, so owning individual command files needs file-leaf swap support. Surfaces a degradation warning today. |
| `mcp-server-entry` | ❌ unsupported (by design) | — | — | **Pi does not load MCP servers by design** — see the [no-MCP rationale](https://mariozechner.at/posts/2025-11-02-what-if-you-dont-need-mcp/). Not a gap. Surfaces a degradation warning; build/install a Pi extension to wrap the capability. |
| `rule` | ❌ unsupported | — | — | Pi has no per-tool rule concept distinct from `AGENTS.md`. Consolidate rule content into an `agents-md` node. |
| `plugin-reference` | ❌ unsupported | — | — | Pi packages install via `pi install` (npm/git), not a project-level registry file. |

Unsupported kinds surface as honest degradation warnings in the capability
report — agent-sync never emits dead files into paths Pi doesn't read. The
MCP entry is the canonical *deliberate exclusion* example (a tool that chooses
not to support a concept, versus failing to).

## User scope (`sync --user`)

`agents-md` is scope-aware: at user scope Pi reads user-global instructions from
`~/.pi/agent/AGENTS.md` (not `~/AGENTS.md`), so the adapter targets
`.pi/agent/AGENTS.md` — the direct analog of Claude's `CLAUDE.md →
~/.claude/CLAUDE.md` and Codex's `AGENTS.md → ~/.codex/AGENTS.md` remaps.
`skill` needs no remap: `.agents/skills/…` already resolves to
`~/.agents/skills/…` under the home scope root, which Pi reads.

## Caveats (version-dependent, verified 2026-06-30)

- **Project trust gating (Pi v0.79.0).** Pi loads project-local `.pi/…`
  resources (and project skills/prompts) only after the user grants trust to
  the folder (`~/.pi/agent/trust.json`). Files agent-sync writes at project
  scope are silently ignored until the user runs Pi in the project and trusts
  it. `AGENTS.md` at the repo root is not `.pi/`-scoped and is unaffected.
- **`PI_CODING_AGENT_DIR`.** The user-global base defaults to `~/.pi/agent/` but
  can be relocated via `PI_CODING_AGENT_DIR`. agent-sync assumes the default
  (the hierarchy layer sets the user scope root to `$HOME`), like Codex's
  `CODEX_HOME`.
- **`APPEND_SYSTEM.md` doc trap (issue #748).** Pi documents
  `.pi/APPEND_SYSTEM.md` but does not auto-discover it as of early 2026.
  agent-sync does not write system-prompt override files regardless.

## Coexistence with other adapters

`pi`, `codex`, and `cursor` all section-merge into the same workspace-root
`AGENTS.md`, each keyed by its node `id`. The marker syntax is byte-identical
across adapters, so sections coexist and are independently removable without
touching user prose or each other (see `internal/merge`).

## Authoritative references

- Implementation: [`internal/adapter/bundled/pi/`](../../internal/adapter/bundled/pi/)
- Capability declaration: [`internal/adapter/bundled/pi/capabilities.yaml`](../../internal/adapter/bundled/pi/capabilities.yaml)
- Plan: [`docs/plans/2026-06-30-002-feat-pi-adapter-plan.md`](../plans/2026-06-30-002-feat-pi-adapter-plan.md)
- Sibling adapter (template): [`docs/adapters/codex.md`](codex.md)
- Pi docs: <https://pi.dev/docs/latest> · Context files, Skills, Prompt templates
- Pi "No MCP" rationale: <https://mariozechner.at/posts/2025-11-02-what-if-you-dont-need-mcp/>

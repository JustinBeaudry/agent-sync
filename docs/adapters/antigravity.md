# `antigravity` adapter

The bundled `antigravity` adapter maps the v1 IR concept set to
[Google Antigravity](https://antigravity.google) 2.0's on-disk layout. The
Antigravity IDE and CLI share one config surface under `~/.gemini/`. Antigravity
replaced the retired Gemini CLI (2026-06-18) and reads the same `GEMINI.md`
overlay; the IR decoder already routes a root `GEMINI.md` overlay to the
`antigravity` target
([`internal/ir/decode.go`](../../internal/ir/decode.go), PR #36). Implementation
lives in
[`internal/adapter/bundled/antigravity/`](../../internal/adapter/bundled/antigravity/);
the per-kind support declaration is in
[`capabilities.yaml`](../../internal/adapter/bundled/antigravity/capabilities.yaml).

Antigravity is a **full-parity** adapter — every IR kind except
`plugin-reference` is supported.

## The `.agent` vs `.agents` split (read this first)

Antigravity is internally inconsistent about its config directory name, and the
adapter **reproduces this faithfully** rather than normalizing it (a normalized
path would be inert — Antigravity would not read it):

- **Rules and workflows use `.agent/` (singular)** — inherited from Antigravity's
  Windsurf/Codeium lineage.
- **Skills and MCP use `.agents/` (plural)** — the cross-tool AGENTS.md-ecosystem
  convention, shared with the `codex`/`pi` adapters.

This is not a typo. It is confirmed across Google's own codelabs
(`.agents/skills/`, `.agents/workflows/` in the pipelines codelab) and Google
DevRel posts (`.agent/rules/`, `.agent/workflows/`). See **Authoritative
references**.

## Concept → destination

| IR kind | Status | Project destination | User-scope (`sync --user`) | Notes |
|---|---|---|---|---|
| `agents-md` | ✅ supported | `GEMINI.md` managed section (id `agent-sync`) | `.gemini/GEMINI.md` (→ `~/.gemini/GEMINI.md`) | `GEMINI.md` is Antigravity's highest-precedence, tool-specific instruction file. The fixed section id is `agent-sync`; the per-node `<id>` lives in the markers `<!-- agent-sync:begin id=<id> -->` … `end`. User content outside the section is preserved. **`GEMINI.md` only — not `AGENTS.md`** (see below). |
| `rule` | ✅ supported | `.agent/rules/agent-sync/<id>.md` (**singular** `.agent`) | *(no user-global home — inert; coverage warns)* | agent-sync owns the whole `agent-sync/` subdir (owned-subdir, swapped wholesale). Managed-file header prepended. |
| `command` | ✅ supported | `.agent/workflows/agent-sync/<id>.md` (**singular** `.agent`) | *(no user-global home — inert; coverage warns)* | Antigravity "workflows" are `/`-triggered saved prompts — the closest native shape to an agent-sync command. |
| `skill` | ✅ supported | `.agents/skills/agent-sync-<id>/SKILL.md` (+assets) (**plural** `.agents`) | `.gemini/skills/agent-sync-<id>/…` | Shared cross-tool tree (same as codex/pi): one emitted skill serves all three. Co-ownership of a leaf is made safe by the engine's union-aware drift/orphan checks (ADV-1). SKILL.md bytes are identical to codex/pi. |
| `mcp-server-entry` | ✅ supported | `.agents/mcp_config.json` (**plural** `.agents`) | `.gemini/config/mcp_config.json` (→ `~/.gemini/config/mcp_config.json`) | JSON pointer `/mcpServers/agentsync_<id>`; other entries preserved. Top-level key `mcpServers`. Antigravity 2.0 (IDE + CLI) reads this file. No `.agent-sync-managed` sidecar (unlike claude) — the file is a shared tool-owned merge target, not an agent-sync-owned strict-JSON file. |
| `plugin-reference` | ❌ unsupported | — | — | Antigravity does not load a project-level plugin-reference registry file. Surfaces a degradation warning and emits no files. |

## Why `GEMINI.md` only, not `AGENTS.md`?

Antigravity reads both `GEMINI.md` (highest priority, tool-specific) and
`AGENTS.md` (cross-tool, since v1.20.3). The adapter writes the `agents-md`
overlay to **`GEMINI.md` only**: `AGENTS.md` is already owned by the `codex` and
`pi` adapters, and a second writer merging into the same file at the same scope
would collide. Targeting `GEMINI.md` gives Antigravity Antigravity-specific,
highest-precedence guidance with clean separation.

If you also want the same content in `AGENTS.md` (visible to Cursor/Codex/Pi),
target the IR node at those adapters (`targets: [antigravity, codex]`); each
adapter writes to its own discovery path.

## User scope (`sync --user`)

Three kinds are scope-aware because Antigravity has a genuine user-global home
for them, all under `~/.gemini/`:

- `agents-md` → `.gemini/GEMINI.md` (→ `~/.gemini/GEMINI.md`)
- `mcp-server-entry` → `.gemini/config/mcp_config.json`
- `skill` → `.gemini/skills/…` (**not** `~/.agents/skills/…`, which Antigravity
  does not read at the user level — a genuine remap)

`rule` and `command` have **no documented Antigravity user-global directory**
(global rules fold into `~/.gemini/GEMINI.md`; global workflows live at a
different, untargeted path). They keep their project-relative `.agent/` paths at
all scopes, and [`internal/coverage`](../../internal/coverage/coverage.go) flags
them as inert at user scope so `sync --user` reports the gap honestly. Sync rules
and workflows at project scope instead.

## Caveats (version-dependent — verify before relying)

The `.agent`/`.agents` paths are sourced from Google codelabs, the Gemini API
docs, and Google DevRel posts, not a single machine-readable spec (the official
`antigravity.google/docs` pages are a client-rendered SPA). If a future
Antigravity release moves or renames any path, the adapter and this table need to
be updated together — the change is localized to `resolvePathSet` /
`declaredOutputs` / the subdir constants in `emit_reserved.go`.

- Workspace `mcp_config.json` at `.agents/mcp_config.json` is the least
  corroborated path; the global `~/.gemini/config/mcp_config.json` is
  well-attested.
- `AGENTS.md` support landed in Antigravity v1.20.3 (2026-03-05); `GEMINI.md`
  takes precedence when both exist.

## Coexistence with other adapters

- `GEMINI.md` is Antigravity-specific — no other adapter writes to it.
- `.agents/skills/` is the shared cross-tool skills tree co-owned with `codex`
  and `pi`; SKILL.md bytes are identical across the three, so a co-emitted skill
  is a content no-op on the second swap (ADV-1).
- `.agents/mcp_config.json` entries live under the distinct
  `/mcpServers/agentsync_<id>` pointer prefix and are independently removable.

## Exit path

To unbind agent-sync from a workspace and remove every file the `antigravity`
adapter has emitted:

```bash
agent-sync unmanage antigravity
```

## Authoritative references

- Implementation: [`internal/adapter/bundled/antigravity/`](../../internal/adapter/bundled/antigravity/)
- Capability declaration: [`internal/adapter/bundled/antigravity/capabilities.yaml`](../../internal/adapter/bundled/antigravity/capabilities.yaml)
- Plan: [`docs/plans/2026-07-03-001-feat-antigravity-adapter-plan.md`](../plans/2026-07-03-001-feat-antigravity-adapter-plan.md)
- IR overlay target (PR #36): [`internal/ir/decode.go`](../../internal/ir/decode.go), [`docs/spec/ir-v1.md`](../spec/ir-v1.md)
- Antigravity docs: <https://antigravity.google/docs> · MCP: <https://antigravity.google/docs/mcp> · Rules & Workflows: <https://antigravity.google/docs/rules-workflows>
- Skills (Google codelab): <https://codelabs.developers.google.com/autonomous-ai-developer-pipelines-antigravity>
- Rules/workflows paths (Google DevRel): <https://atamel.dev/posts/2025/11-25_customize_antigravity_rules_workflows/>

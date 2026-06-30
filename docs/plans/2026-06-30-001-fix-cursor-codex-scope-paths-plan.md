---
title: "fix: scope-aware Cursor and Codex adapter output paths (sync --user)"
status: approved
date: 2026-06-30
type: fix
origin: docs/plans/2026-06-18-001-fix-claude-user-scope-paths-plan.md
master_plan: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# fix: scope-aware Cursor and Codex adapter output paths (sync --user)

## Summary

PR #30 made the **Claude** adapter scope-aware so `sync --user` targets
Claude Code's real user-config paths. The Cursor and Codex adapters were left
behind: they still declare and emit workspace-relative paths regardless of
scope. `sync --user` is now a first-class feature, but only one of the three
bundled adapters honors it correctly.

This plan extends the #30 scope-aware pattern (`scope` captured at
`initialize` → `declaredOutputs(scope)` + `handleEmit(..., scope)` →
`resolvePathSet`) to Cursor and Codex.

The research-grounded finding (official docs, 2026-06-30) reframes the work:
at user scope the scope root **is** `$HOME`, so any project path already
under a dot-dir resolves correctly (`.cursor/mcp.json` → `~/.cursor/mcp.json`,
`.codex/config.toml` → `~/.codex/config.toml`, `.agents/skills/...` →
`~/.agents/skills/...`). The defects are narrow and specific:

- **Cursor** has **no** user-global home for *rules* (User Rules live in app
  settings, not a writable file) or *agents-md* (`AGENTS.md` is project-only).
  At user scope those two outputs are inert and must be **suppressed**, not
  remapped. The sidecar is suppressed too (the global `~/.cursor/mcp.json` is
  Cursor's own shared file, exactly like Claude's `~/.claude.json`).
- **Codex** reads user-global agent instructions from `~/.codex/AGENTS.md`,
  **not** `~/AGENTS.md`. The agents-md output needs a **remap** at user scope.
  MCP (`~/.codex/config.toml`) and skills (`~/.agents/skills/`) are already
  correct.

Project and directory scope behavior is unchanged for both adapters.

---

## Problem Frame

`sync --user` sets the scope root to `$HOME`
(`internal/hierarchy/discover.go:108`, `Root = home`). The path-safety gate
(`internal/adapter/runtime.go` `pathInDeclaredOutputs`) accepts an emitted op
only if its path is contained in a declared output, so declared outputs and
emitted paths must move together.

### Cursor — current static behavior (`internal/adapter/bundled/cursor/`)

`declaredOutputs()` (capabilities.go) and the emit constants
(emit_tool_owned.go) are scope-blind:

| Concept | Path | User-scope (`$HOME` root) | Correct? |
|---|---|---|---|
| mcp-server-entry | `.cursor/mcp.json` | `~/.cursor/mcp.json` | ✅ already correct — Cursor reads global MCP here, same `mcpServers` JSON shape |
| sidecar | `.cursor/.agent-sync-managed` | `~/.cursor/.agent-sync-managed` | ⚠️ writes a stray marker next to Cursor's own global file → **suppress** |
| rule | `.cursor/rules/agent-sync/<id>.md` | `~/.cursor/rules/agent-sync/...` | ❌ Cursor has **no** user-global rules file → **suppress + warn** |
| agents-md | `AGENTS.md` | `~/AGENTS.md` | ❌ Cursor has **no** user-global AGENTS.md → **suppress + warn** |

### Codex — current static behavior (`internal/adapter/bundled/codex/`)

| Concept | Path | User-scope (`$HOME` root) | Correct? |
|---|---|---|---|
| mcp-server-entry | `.codex/config.toml` | `~/.codex/config.toml` | ✅ already correct — this is Codex's *primary* config location |
| skill | `.agents/skills/agent-sync-<id>/SKILL.md` | `~/.agents/skills/agent-sync-<id>/SKILL.md` | ✅ already correct — official user-global skills tree |
| agents-md | `AGENTS.md` | `~/AGENTS.md` | ❌ Codex reads `~/.codex/AGENTS.md` → **remap to `.codex/AGENTS.md`** |

Net effect today: `sync --user` for Cursor writes inert rules/AGENTS.md and a
stray sidecar; for Codex it writes an inert `~/AGENTS.md`. Both report success.

---

## Design Decisions

Research (official Cursor docs + a Cursor-staff statement, 2026-06-30) settled
two questions:

- **Cursor has no file-addressable user-global home for rules or AGENTS.md.**
  User Rules are cloud/app-settings only; there is no `~/.cursor/rules/` and no
  `~/.cursor/AGENTS.md`. `~/.cursor/mcp.json` is the *sole* file-addressable
  global config. So at user scope those two concepts are inert and must not be
  written.
- **"Combine global + project rules" is deferred.** Making global Cursor rules
  take effect means merging the user rule-layer into each project's
  `.cursor/rules/` — but agent-sync's scopes are **independent silos**
  (`runHierarchySync` syncs each scope to its own root), so that is a new
  engine-level *hierarchy-composition* feature, not part of scope-aware paths.
  Recorded as a future feature (see Out of Scope). Cursor is also actively
  reworking this area (3.9 "Customize"), so a native `~/.cursor/rules/` could
  land and obviate it.

1. **Mirror the #30 threading.** Add `scopeUser = "user"` const and a
   `resolvePathSet(scope)` to each adapter; thread scope from `OnInitialize`
   into `declaredOutputs(scope)` and `handleEmit(..., scope)`; store the
   resolved set on `emitState`. Capabilities stay static.

2. **Cursor: MCP-only at user scope; the adapter skips the inert kinds.**
   `declaredOutputs(user)` returns only `.cursor/mcp.json` (drops the rules dir,
   `AGENTS.md`, and the sidecar). At user scope `dispatchNode` **silently skips**
   `rule` and `agents-md` nodes (emits no op — silent skip is already this
   function's pattern for non-targeted nodes), and `emitMCPServerEntry` emits the
   entry to `~/.cursor/mcp.json` with **no sidecar** (the global mcp.json is
   Cursor's own shared file, same reasoning as Claude's `~/.claude.json`).
   Capabilities stay static (rule/agents-md remain `supported` — they *are*, at
   project scope).

3. **Coverage owns the user-facing warning — not the adapter.** `internal/coverage`
   is the designed, documented home for "a scope emits a kind the target won't
   read natively at this level" (its `Warning` carries a `Level`; its table is
   explicitly "correct here if a tool's behavior is verified to differ"). Its
   current assumption that *project/user levels are always native* is the stale
   `#30`-class assumption. Add a user-level gap: **cursor rule + agents-md are
   non-native at user scope**. This surfaces the honest warning through the
   existing per-scope pipeline (text `warning:` lines + JSON `coverage_warnings`),
   computed from the manifest, with no double-warning from the adapter. Claude
   and Codex have no user-scope gap (every supported kind has a user-global home,
   Codex's after the agents-md remap), so they get no entry.

4. **Codex: remap agents-md only.** `resolvePathSet(user)` sets
   `agentsMD = ".codex/AGENTS.md"`; project/directory keep `AGENTS.md`. MCP
   (`.codex/config.toml`) and skills (`.agents/skills/`) are unconditional —
   already correct at every scope (their relative paths land under `$HOME` at
   user scope). No coverage change for Codex.

5. **Documented limitations (not handled now), recorded in `docs/adapters/`:**
   - **Codex `AGENTS.override.md` precedence.** Codex prefers
     `~/.codex/AGENTS.override.md` over `~/.codex/AGENTS.md`; a stale override
     shadows our managed section. We write canonical `AGENTS.md`; owning the
     override is out of scope. Document it.
   - **`CODEX_HOME` relocation.** We assume the default `~/.codex` (the
     hierarchy layer sets the scope root to `$HOME`). Honoring `CODEX_HOME`
     would require the scope root to move — a larger change. Document the
     assumption. (Skills at `~/.agents/skills/` are unaffected by `CODEX_HOME`.)

6. **No protocol change.** The `scope` field already exists on the wire (#30).
   This plan only consumes it in two more bundled adapters + coverage.

---

## Implementation (TDD per step)

### Coverage (do first — both adapters' user-scope behavior asserts against it)

1. **coverage.go** — add `nonNativeAtUser` table (`cursor: {rule, agents-md}`);
   extend `Analyze` to emit user-level warnings from it (project still nil;
   directory unchanged). Update the package/table doc comments that currently
   claim "project/user levels are always native."
2. **coverage_test.go** — add: cursor rule+agents-md at user → 2 warnings;
   cursor mcp at user → 0; claude/codex at user → 0 (existing test still green);
   deterministic ordering.

### Cursor

3. **emit_tool_owned.go** — add `scopeUser` const, `pathSet{mcpJSON, sidecar}`,
   `resolvePathSet(scope)`: user ⇒ `{".cursor/mcp.json", sidecar: ""}`; else ⇒
   `{".cursor/mcp.json", ".cursor/.agent-sync-managed"}`. Route
   `emitMCPServerEntry` through `state.paths` (path + sidecar gate) and use it in
   the error message, like Claude.
4. **capabilities.go** — `declaredOutputs(scope)`: at user scope return only the
   `.cursor/mcp.json` tool-owned-entry; otherwise the full current list.
5. **emit.go** — thread `scope` into `handleEmit`; add `paths pathSet` to
   `emitState`; in `dispatchNode`, at user scope skip `rule` and `agents-md`
   (return nil, no op) with a comment pointing to coverage as the warning channel.
6. **bundled.go** — capture `scope` in `OnInitialize`; pass to
   `declaredOutputs(scope)` and `handleEmit(..., scope)`.
7. **Tests** — `capabilities_test.go` (user-scope declared-outputs = MCP only);
   `emit_test.go` (update `handleEmit` call sites to pass `"project"`; add a
   user-scope test: MCP → `.cursor/mcp.json`, no sidecar, rule/agents-md produce
   no ops).
8. **docs/adapters/cursor.md** — document user-scope behavior (MCP only; rules &
   AGENTS.md have no user-global home, flagged by coverage).

### Codex

9. **emit_tool_owned.go** — add `scopeUser` const, `pathSet{agentsMD}`,
   `resolvePathSet(scope)`: user ⇒ `agentsMD = ".codex/AGENTS.md"`; else
   `agentsMD = "AGENTS.md"`. (MCP `codexConfigPath` and the skills tree are
   unconditional.)
10. **capabilities.go** — `declaredOutputs(scope)`: swap the `AGENTS.md`
    tool-owned-entry path to `.codex/AGENTS.md` at user scope; MCP + skills
    unchanged.
11. **emit.go / emit_tool_owned.go** — thread `scope`; add `paths pathSet` to
    `emitState`; `emitAgentsMD` writes to `state.paths.agentsMD`.
12. **bundled.go** — capture `scope` in `OnInitialize`; pass through.
13. **Tests** — `capabilities_test.go` (user-scope AGENTS.md path);
    `emit_test.go` (update call sites; add user-scope test: agents-md →
    `.codex/AGENTS.md`, MCP → `.codex/config.toml`, skills → `.agents/skills/`).
14. **docs/adapters/codex.md** — document user-scope behavior + the
    `AGENTS.override.md` / `CODEX_HOME` caveats.

### Cross-cutting

15. **E2E** — extend `internal/cli/hierarchy_sync_test.go` (or a sibling) with
    a `sync --user` case asserting Cursor MCP → `~/.cursor/mcp.json` (foreign
    keys preserved, no sidecar) and Codex agents-md → `~/.codex/AGENTS.md`.
16. **CHANGELOG.md** — add to `[Unreleased] / Fixed`.
17. **Verification gate** — `go vet ./... && go test -race ./... && golangci-lint run`.

---

## Out of Scope

- **Hierarchy composition / "combine global + project rules"** (merging the
  user rule-layer into each project's `.cursor/rules/`). This is the real way to
  make global Cursor rules take effect, but it is an engine-level feature on the
  independent-scope model — its own plan. Candidate follow-up; may be obviated
  if Cursor ships a native `~/.cursor/rules/` read path.
- Cursor global "User Rules" (app-settings/cloud, not file-addressable).
- Codex `AGENTS.override.md` ownership and `CODEX_HOME` relocation (documented
  caveats only).
- Per-scope capability matrix changes (capabilities stay static).
- Gemini / Windsurf / LM Studio adapters (not yet built).

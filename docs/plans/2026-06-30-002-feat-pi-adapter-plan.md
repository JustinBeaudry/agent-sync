---
title: "feat: bundled pi adapter (Unit 11.5) + ADV-1 shared-skills validation"
status: approved
date: 2026-06-30
type: feat
origin: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
master_plan: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# feat: bundled `pi` adapter (Unit 11.5) + ADV-1 shared-skills validation

## Summary

Ship the fourth bundled adapter, **`pi`** (`@mariozechner/pi-coding-agent`,
repo `earendil-works/pi`, docs pi.dev). Pi is the master plan's "primary"
planned tier. It is a structural sibling of the **codex** adapter: both
section-merge into the workspace-root `AGENTS.md` and both place skills in the
shared `.agents/skills/` tree. Pi adds prompt-template *commands* and honestly
declares the concepts Pi excludes by design (MCP, rules, plugin-references).

The adapter is **scope-aware from day one** (the pattern established by #30/#31):
project and user scope both resolve correctly, so we don't ship a project-only
adapter and immediately follow with a scope PR.

A second phase **validates ADV-1** — the dormant concern that `codex` and `pi`
both declaring the shared `.agents/skills` parent could collide on the shared
`.agent-sync-staging` tree. Evidence-first: PR #24's per-leaf sentinel work
(`ed0a87e`) likely already covers it, so we add a `[codex, pi]` cross-adapter
test and only implement a fix if it actually collides.

---

## Verified Pi layout (research 2026-06-30, corrects the April Unit 11.5 spec)

Official sources: Pi README (earendil-works/pi), pi.dev/docs/latest,
CHANGELOG through v0.79.0 (2026-06-08), mariozechner.at no-MCP post.

| Concept | Project read path | User-global read path | vs April plan |
|---|---|---|---|
| agent instructions | `AGENTS.md` (root + parent-walk; `CLAUDE.md` alias) | `~/.pi/agent/AGENTS.md` | global base is `~/.pi/agent/`, **not** `~/.pi/` |
| skills | `.agents/skills/<name>/SKILL.md` (parent-walked) **and** `.pi/skills/` (flat) | `~/.agents/skills/` and `~/.pi/agent/skills/` | confirmed; `SKILL.md` exact case; Pi allows name≠dir |
| prompt templates (commands) | `.pi/prompts/<name>.md` → `/<name>`, `{{var}}` interpolation | `~/.pi/agent/prompts/` | **recursion into subdirs UNVERIFIED** — see Open Decision |
| MCP | not supported (by design) | — | unchanged stance, confirmed |
| rules | none (use `AGENTS.md`) | — | confirmed |
| plugins/extensions | `.pi/settings.json` `packages[]` + `.pi/extensions/*.ts` | `~/.pi/agent/...` | registry is settings.json, not a standalone file |
| system prompt | `.pi/SYSTEM.md` / `.pi/APPEND_SYSTEM.md` | `~/.pi/agent/...` | advisory only; agent-sync does not manage |

**Watch-outs (documented, not handled):**
- **Project trust gating (v0.79.0).** Pi loads project-local `.pi/...` resources
  (and project skills/prompts) only after the user grants trust to the folder
  (`~/.pi/agent/trust.json`). Files agent-sync writes are silently ignored until
  the user runs Pi in the project and trusts it. Document in `docs/adapters/pi.md`.
- **`APPEND_SYSTEM.md` doc trap (issue #748).** Documented but not auto-discovered
  as of early 2026. We don't write it anyway (advisory-only).
- **`PI_CODING_AGENT_DIR`** can relocate `~/.pi/agent/`; like Codex's `CODEX_HOME`
  we assume the default and document the assumption.

---

## Concept → destination (agent-sync `pi` adapter)

Reserved prefix: `.pi`. Skills use the shared `.agents/skills` tree (sharedSubdir).

| IR kind | Status (PR1) | Project destination | User-scope destination |
|---|---|---|---|
| `agents-md` | **supported** | `AGENTS.md` section `agent-sync:<id>` (root) | `.pi/agent/AGENTS.md` (→ `~/.pi/agent/AGENTS.md`) — **remap**, like Codex |
| `skill` | **unsupported in PR1 (planned, PR2)** | (target: `.agents/skills/agent-sync-<id>/SKILL.md`) | same relative path |
| `command` | **unsupported in PR1 (planned, PR2)** | (target: flat `.pi/prompts/agent-sync-<id>.md` → `/agent-sync-<id>`) | `.pi/agent/prompts/...` |
| `mcp-server-entry` | **unsupported (by design)** | one-time warning, no file | same |
| `rule` | unsupported | one-time warning (note: use `agents-md`) | same |
| `plugin-reference` | unsupported | one-time warning | same |

**Why `skill` is deferred to PR2 (confirmed, not speculative):** the ADV-1
validation test (Phase 2) proved codex+pi sharing `.agents/skills` **collides**.
Root cause: ledgers are per-target (`.agent-sync/state/<target>.json`), and
`sync.ScanDrift` (the mid-life-drift data-loss guard) checks a leaf's files
against only the *current* target's ledger. After codex writes
`.agents/skills/agent-sync-<id>/SKILL.md` (recorded in `codex.json`), pi's drift
scan sees that file as an unmanaged foreign file and fails with
`ErrMidLifeDrift`. Safe co-ownership needs the shared-subdir drift/orphan check
to union across all targets' ledgers — a change to the data-loss-critical
drift/swap/orphan path (requires ce-code-review). Deferred to PR2 with `command`.

**Why `command` is deferred to PR2 (not a guess — verified):** Pi's prompt
loader is **flat, non-recursive** (`readdirSync` + basename only, confirmed in
`prompt-templates.ts` and pi.dev docs), so the only discoverable form is a flat
file `.pi/prompts/agent-sync-<id>.md`. But `.pi/prompts/` is **shared** with the
user's own prompt files, so agent-sync can't own/swap it wholesale (that would
delete user prompts — violates the swap model). And the existing shared-subdir
machinery only supports **directory** leaves: `sync.Stage` does `MkdirAll(<leaf>)`
(`staging.go:39`), so a *file* leaf doesn't fit. Supporting `command` therefore
needs new engine support for **agent-sync-owned files within a shared directory**
(file-leaf stage/swap + prefix-scoped orphan deletion) — a change to the
data-loss-critical swap core (AGENTS.md invariants #6/#7, requires ce-code-review).
That belongs in its own focused PR, not bundled with a new adapter.

In PR1 the pi adapter declares `command` **unsupported** with an honest note —
"agent-sync's pi adapter does not yet emit prompt-template commands (planned);
Pi itself supports them at `.pi/prompts/`." This keeps the capability-lie gate
satisfied (see [[capability-lie-gate-scope]]) and is honest about *adapter
coverage* (not a Pi limitation).

The `agents-md` section markers, managed-file headers, and shared-subdir leaf
machinery are identical to codex/cursor so all four adapters coexist in one
`AGENTS.md` and one `.agents/skills/` tree.

---

## Design Decisions

1. **Mirror the codex adapter.** `pi` is codex's closest sibling (shared
   `.agents/skills` + `AGENTS.md` section). Copy its package shape:
   `bundled.go` (lifecycle + scope capture), `capabilities.go` +
   `capabilities.yaml` (conceptKinds mirror + parity test), `emit.go` (dispatch,
   dedup, wire), `emit_tool_owned.go` (agents-md section + `pathSet`/`resolvePathSet`),
   a skills emitter (shared-subdir, copy codex's `emitSkill`), and a new commands
   emitter. Register in `internal/cli/setup.go`.

2. **Scope-aware from day one.** `resolvePathSet(scope)` remaps `agents-md` →
   `.pi/agent/AGENTS.md` at user scope; `skill` (`.agents/skills`) is unconditional
   (already correct under `$HOME`). `capabilitiesForWire(scope)` + `declaredOutputs(scope)`
   thread scope exactly as cursor/codex now do. No new user-scope coverage gap:
   every PR1-supported pi kind has a user-global home (unlike cursor's rule/agents-md).
   (When `command` lands in PR2, its prompts dir remaps `.pi/prompts/` →
   `.pi/agent/prompts/` at user scope.)

3. **MCP = honest deliberate exclusion, not degradation.** Declare
   `mcp-server-entry` unsupported. On an in-target MCP node emit a one-time
   `warning` op (deduped per emit): "Pi does not load MCP servers by design — see
   https://mariozechner.at/posts/2025-11-02-what-if-you-dont-need-mcp/. Build/install
   a Pi extension to wrap the capability." This is the master plan's canonical
   capability-matrix-honesty example. (`rule`, `plugin-reference` likewise
   unsupported with their own notes.) Because these are declared unsupported,
   the runtime capability-lied gate is satisfied — see [[capability-lie-gate-scope]];
   a pi-only manifest of just MCP entries is an honest warning, not a sync failure.

4. **Advisories (warnings, files untouched).** If `.pi/SYSTEM.md` /
   `.pi/APPEND_SYSTEM.md` exist, emit a one-time advisory that agent-sync does not
   manage system-prompt overrides and `agents-md` loads as a context file. If
   `.pi/extensions/` exists, an informational note. Never write or modify these.
   *(MVP may defer the filesystem-probe advisories if the adapter has no read
   access to the workspace at emit time — confirm during implementation; the
   no-MCP/rule/plugin warnings are the must-haves.)*

5. **`required: true` unmet → error, don't paper over.** A `required`
   `mcp-server-entry`/`rule` targeting pi surfaces `required_unmet` in atomic
   mode (existing engine behavior for unsupported required kinds — verify it
   already does this; add a test).

---

## Implementation (TDD per phase)

### Phase 1 — Pi adapter (agents-md only), project + user scope ✅ DONE

1. `internal/adapter/bundled/pi/bundled.go` — lifecycle, scope capture in
   `OnInitialize`, `capabilitiesForWire()` + `declaredOutputs(scope)`,
   `handleEmit(..., scope)`. Reserved prefix `.pi`.
2. `capabilities.yaml` + `capabilities.go` — conceptKinds: agents-md supported;
   skill/command/mcp-server-entry/rule/plugin-reference unsupported. Parity test.
   `declaredOutputs(scope)`: `AGENTS.md` / `.pi/agent/AGENTS.md` (tool-owned-entry
   section) — scope-aware.
3. `emit_tool_owned.go` — `pathSet{agentsMD}` + `resolvePathSet(scope)`;
   `emitAgentsMD` (section markers, inner-body-only, marker-injection guard — copy codex).
4. `emit.go` — dispatch + wire (copy codex); `emitUnsupportedWarning` with
   pi-specific notes (no-MCP rationale URL; rule → use agents-md; skill/command → planned).
5. `internal/cli/setup.go` — register `pi.Bundled()`.
6. Tests: `emit_test.go`, `capabilities_test.go`, `emit_errors_test.go`, `testdata/` —
   agents-md happy path, user-scope agents-md remap, no-MCP warning, skill/rule/command/plugin
   unsupported, capability-lie-gate regression (unsupported-only pi emit succeeds).
7. `docs/adapters/pi.md` — concept→destination (project + user), no-MCP callout,
   skill/command-planned notes, trust-gating + `PI_CODING_AGENT_DIR` caveats.

### Phase 2 — ADV-1 shared-skills validation (codex + pi) ✅ DONE (confirmed collision)

8. Added a `[codex, pi]` shared-`.agents/skills` test. **Result: it collides**
   (`ErrMidLifeDrift`) — see the "Why skill is deferred" note above. ADV-1 is a
   real, data-loss-adjacent drift/ledger fix, not covered by the per-leaf
   sentinels. The validation test was reverted from PR1 (it exercises unbuilt
   skill support); it returns in PR2 alongside the fix.

### PR2 (separate plan + ce-code-review) — skill + command + ADV-1 fix

- Engine: shared-subdir `ScanDrift`/orphan check unions across all targets'
  ledgers so codex and pi co-own `.agents/skills/agent-sync-<id>` leaves safely
  (validate add/update/remove/recover). Then flip pi `skill` → supported.
- Engine: file-leaf stage/swap for owned files in a shared dir; then flip pi
  `command` → supported (`.pi/prompts/agent-sync-<id>.md`, scope-aware).
- Re-add the `[codex, pi]` cross-adapter validation test (must pass).

### Cross-cutting

10. `README.md` adapter table: Pi `Planned (primary)` → `✅ Bundled`.
11. `CHANGELOG.md` `[Unreleased] / Added`.
12. Four-adapter `AGENTS.md` round-trip test (claude/cursor/codex/pi coexist;
    user content byte-identical) per master plan verification.
13. Gate: `go vet ./... && go test -race ./... && golangci-lint run`.

---

## PR2 outline (command support — separate plan + ce-code-review)

Pi's prompt dir is flat (verified), and the only discoverable form is
`.pi/prompts/agent-sync-<id>.md` (→ `/agent-sync-<id>`), a flat file in a dir
shared with the user's prompts. PR2 adds engine support for **agent-sync-owned
files within a shared directory**:

- A file-leaf mode (or extend shared-subdir so a leaf can be a single file):
  `sync.Stage`/`Swap` currently assume a directory leaf (`MkdirAll`), so the
  stage/swap + recover path needs a file-leaf variant.
- Prefix-scoped orphan deletion within the shared dir (delete
  `.pi/prompts/agent-sync-*.md` no longer emitted; never touch user prompts).
- Then flip the pi adapter's `command` to supported (`emitCommand`), scope-aware
  prompts dir (`.pi/prompts/` → `.pi/agent/prompts/` at user scope), and verbatim
  `{{var}}` passthrough.

This is a swap-core change (data-loss-critical), so it gets its own plan and a
mandatory ce-code-review, isolated from the adapter.

---

## Out of Scope

- Hierarchy composition / "combine" (the next, separate piece of work).
- Pi packages/extensions registry (`.pi/settings.json` `packages[]`) and
  `plugin-reference` emission — declared unsupported; revisit if demand appears.
- System-prompt override management (`.pi/SYSTEM.md` etc.) — advisory only.
- `PI_CODING_AGENT_DIR` relocation and `APPEND_SYSTEM.md` writing (documented caveats).
- Gemini / Windsurf / LM Studio adapters.

---
title: "feat: Add full-parity Antigravity bundled adapter"
status: active
date: 2026-07-03
type: feat
origin: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# feat: Add full-parity Antigravity bundled adapter

## Summary

Add a new bundled adapter, `antigravity`, that translates agent-sync IR into
Google Antigravity 2.0's on-disk layout (IDE + CLI share the same config under
`~/.gemini/`). Antigravity replaced the retired Gemini CLI (2026-06-18) and reads
the same `GEMINI.md` overlay. The IR side is already wired — PR #36 routed the
root `GEMINI.md` overlay to the `antigravity` target in `internal/ir/decode.go`.
This plan builds the missing `internal/adapter/bundled/antigravity/` package,
registers it, records its coverage behavior, and documents it.

The adapter is **full-parity**: it supports `agents-md`, `rule`, `skill`,
`command`, and `mcp-server-entry`, and declines `plugin-reference` with a warning
(matching `claude`/`pi`). `claude` is the structural reference (it is the only
existing full-parity adapter); `pi` is the reference for the shared `.agents/`
skills tree and scope-invariant capability set.

---

## Problem Frame

agent-sync renders team-standard agent guidance into each tool's native files.
Antigravity is a supported-tier target on the roadmap
(`docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md:57`) but has no
adapter — the only Antigravity presence today is the IR decoder's overlay-target
string. Users who author a canonical repo cannot currently sync to Antigravity.

The defining complication is that **Antigravity is internally inconsistent about
its config directory name**, and the adapter must reproduce this faithfully
rather than normalize it:

- **Rules and workflows** use `.agent/` (singular) — inherited from Antigravity's
  Windsurf/Codeium lineage.
- **Skills and MCP** use `.agents/` (plural) — the cross-tool AGENTS.md-ecosystem
  convention, shared with `codex`/`pi`.

This split is confirmed across Google's own codelabs (`.agents/skills/`,
`.agents/workflows` in the pipelines codelab) and Google DevRel posts
(`.agent/rules/`, `.agent/workflows/` — Mete Atamel, atamel.dev). See
**Sources & Research**.

---

## Requirements

- **R1** — New `antigravity` bundled adapter emitting the full IR v1 op vocabulary
  for Antigravity's real on-disk layout (see the mapping table below).
- **R2** — `agents-md` overlay targets **`GEMINI.md` only** (managed markdown
  section). Do **not** write `AGENTS.md` — that file is owned by `codex`/`pi` and
  a second writer at the same scope would collide. (Decision, see origin PR #36.)
- **R3** — Faithfully reproduce the `.agent/` (rules, workflows) vs `.agents/`
  (skills, mcp) directory split.
- **R4** — Scope-aware paths (project vs `--user`) resolved from a single
  `resolvePathSet`, so declared and emitted paths never drift (path-safety gate).
- **R5** — `capabilities.yaml` and the in-code `conceptKinds` map stay in parity
  (enforced by `capabilities_test.go`).
- **R6** — Registered in the single registration site and reflected in the
  coverage native-read table with honest user-scope gaps.
- **R7** — Passes `go vet ./... && go test -race ./... && golangci-lint run`.
- **R8** — Documented (`docs/adapters/antigravity.md`, README table, CHANGELOG)
  and the roadmap plan updated to mark the adapter landed.

---

## IR-kind → Antigravity path mapping (the core decision surface)

| IR kind | Project scope | User scope (`--user`, root = `$HOME`) | OutputMode | Notes |
|---|---|---|---|---|
| `agents-md` | `GEMINI.md` | `.gemini/GEMINI.md` | `tool-owned-entry` (markdown section `agent-sync`) | GEMINI.md only; highest Antigravity precedence |
| `rule` | `.agent/rules/agent-sync/<id>.md` | *(no Antigravity global rules dir — inert)* | `owned-subdir` | singular `.agent`; agent-sync-exclusive subdir |
| `command` | `.agent/workflows/agent-sync/<id>.md` | *(global workflows live elsewhere — inert in v1)* | `owned-subdir` | singular `.agent`; Antigravity "workflows" = slash commands |
| `skill` | `.agents/skills/agent-sync-<id>/SKILL.md` (+assets) | `.gemini/skills/agent-sync-<id>/…` | `shared-subdir` | plural `.agents`; co-owned tree (codex/pi) |
| `mcp-server-entry` | `.agents/mcp_config.json` | `.gemini/config/mcp_config.json` | `tool-owned-entry` (JSON pointer `/mcpServers/agentsync_<id>`) | plural `.agents`; `{"mcpServers": {...}}` |
| `plugin-reference` | — | — | *(unsupported)* | Warning, matching claude/pi |

**Reserved prefix:** `.agent` (singular) — the Antigravity-exclusive native
directory. Skills and MCP live outside the reserved prefix (`.agents/…`),
declared separately as `shared-subdir` / `tool-owned-entry`. This mirrors `pi`,
whose reserved prefix is `.pi` yet emits skills to `.agents/skills`.

**User-scope resolution decision:** `agents-md`, `mcp-server-entry`, and `skill`
have genuine Antigravity user-global homes and are scope-aware. `rule` and
`command` have **no documented Antigravity global directory** (global rules fold
into `~/.gemini/GEMINI.md`; global workflows use a different path we are not
targeting in v1). Following agent-sync's existing honesty model (emit to our
location, let coverage flag non-native reads), `rule` and `command` keep their
project-relative `.agent/` paths at all scopes and are recorded in
`coverage.nonNativeAtUser` so `sync --user` warns they are inert. Capabilities do
**not** vary by scope (like `pi`); only paths and coverage do.

---

## Key Technical Decisions

- **KTD1 — GEMINI.md-only overlay.** Owning `GEMINI.md` gives Antigravity-specific,
  highest-precedence guidance and avoids fighting `codex`/`pi` over `AGENTS.md`.
  Confirmed with the user. (R2)
- **KTD2 — Faithful `.agent`/`.agents` split, not normalized.** The directory
  names are Antigravity's actual read paths; normalizing either would make emitted
  content inert. Expressed via distinct `DeclaredOutput` entries. (R3)
- **KTD3 — `claude` is the code template, `pi` the shared-tree reference.** Copy
  claude's file structure and emit dispatch wholesale (it is the only full-parity
  adapter), then take pi's scope-invariant `capabilitiesForWire` shape and its
  `.agents/skills` `shared-subdir` handling. Rename `adapterName`/`reservedPrefix`
  and rewrite `resolvePathSet` + `declaredOutputs` for Antigravity's paths.
- **KTD4 — Scope-awareness only where a user-global home exists.** agents-md, mcp,
  skill are scope-aware; rule, command are project-only + coverage-flagged. Keeps
  the adapter honest without inventing undocumented Antigravity paths. (R4)
- **KTD5 — MCP body validation reused verbatim.** Reuse claude's two-stage
  JSON-object validation (`json.Valid` + `isJSONObject`) before writing into
  `mcp_config.json` — a non-object body silently breaks every MCP load.

---

## Output Structure

```
internal/adapter/bundled/antigravity/
  bundled.go              # Bundled(), run(), manifest, adapterName/reservedPrefix
  capabilities.go         # conceptKinds map, capabilitiesForWire, declaredOutputs
  capabilities.yaml       # embedded per-kind declaration (parity-tested)
  emit.go                 # handleEmit, dispatchNode, irNode wire shape, helpers
  emit_reserved.go        # emitRule, emitCommand, emitSkill (owned/shared subdirs)
  emit_tool_owned.go      # resolvePathSet, emitAgentsMD, emitMCPServerEntry
  emit_unsupported.go     # emitPluginReferenceWarning
  header.go               # managed-file header/marker rendering
  bundled_test.go
  capabilities_test.go
  emit_test.go
  header_test.go
  testdata/ir/
    agents-md-only.json
    rule-only.json
    skill-with-assets.json
    mcp-only.json
    mixed-everything.json
    plugin-reference-warn.json
docs/adapters/antigravity.md
```

---

## Implementation Units

### U1. Scaffold the antigravity package (manifest + lifecycle)

**Goal:** Stand up `bundled.go` with `Bundled()`, `run()`, the manifest, and the
`adapterName`/`adapterVersion`/`reservedPrefix` constants, wiring the
adapterkit lifecycle (`OnInitialize` → capabilities + declared outputs;
`OnEmit` → handleEmit).

**Requirements:** R1, R4
**Dependencies:** none
**Files:** `internal/adapter/bundled/antigravity/bundled.go`
**Approach:** Copy `internal/adapter/bundled/claude/bundled.go` structure verbatim;
set `adapterName = "antigravity"`, `reservedPrefix = ".agent"`. Keep the bundled
magic-cookie/`bundledGetenv` scaffolding unchanged. Capture `scope`, `sourceURL`,
`sourceCommit` at initialize; pass to `handleEmit`. Package doc comment must state
the full kind→path mapping (mirror claude's doc block) including the
`.agent`/`.agents` split so the surprise is documented at the top of the file.
**Patterns to follow:** `internal/adapter/bundled/claude/bundled.go`.
**Test scenarios:** `Test expectation: none` here — behavior is covered by U2–U6
emit tests and U7 wiring tests. (Scaffolding unit.)
**Verification:** Package compiles; `Bundled()` returns a manifest with
`Name: "antigravity"`, `ContractVersion: v1`, non-empty `Command` placeholder.

### U2. Capabilities: conceptKinds map + capabilities.yaml (full parity)

**Goal:** Declare all five supported kinds + `plugin-reference` unsupported, in
both the in-code map and the embedded YAML, with accurate notes describing the
`.agent`/`.agents` split and the GEMINI.md-only overlay.

**Requirements:** R1, R2, R3, R5
**Dependencies:** U1
**Files:**
`internal/adapter/bundled/antigravity/capabilities.go`,
`internal/adapter/bundled/antigravity/capabilities.yaml`,
`internal/adapter/bundled/antigravity/capabilities_test.go`
**Approach:** Mirror `claude/capabilities.go`. `conceptKinds`: `agents-md`,
`rule`, `skill`, `command`, `mcp-server-entry` = `Supported`; `plugin-reference` =
`Unsupported`. `capabilitiesForWire()` sets `WithWriteToolOwned(true)` (agents-md
+ mcp both emit `write_tool_owned`) and does not vary by scope (pi shape).
`declaredOutputs(scope)` calls `resolvePathSet` (U4) and returns:
`.agent/rules/agent-sync` (owned-subdir), `.agent/workflows/agent-sync`
(owned-subdir), `.agents/skills` (shared-subdir), `paths.mcpConfig`
(tool-owned-entry, JSON pointer `/mcpServers`), `paths.geminiMD` (tool-owned-entry,
section `agent-sync`). YAML notes must document each path and the singular/plural
split verbatim.
**Patterns to follow:** `claude/capabilities.go`, `pi/capabilities.yaml` (note
style), `pi/capabilities.go` (scope-invariant `capabilitiesForWire`).
**Test scenarios:**
- `TestCapabilitiesYAML_MatchesCodeMap` — YAML `concept_kinds` equals the in-code
  `conceptKinds` map kind-for-kind (copy claude's parity test).
- `TestCapabilitiesForWire_ExposesAllKinds` — wire capabilities list all five
  supported + plugin-reference unsupported; `WriteToolOwned` is true.
- `declaredOutputs` at project scope returns exactly the six declared outputs with
  the correct `OutputMode` and locator (JSON pointer for mcp, section id for
  agents-md).
- `declaredOutputs` at user scope returns `.gemini/GEMINI.md`,
  `.gemini/config/mcp_config.json`, `.gemini/skills` for the scope-aware three,
  and the unchanged `.agent/…` paths for rule/command.
**Verification:** Parity test green; declared outputs match the mapping table at
both scopes.

### U3. resolvePathSet + agents-md/mcp tool-owned emitters (scope-aware)

**Goal:** Implement scope resolution and the two `write_tool_owned` emitters
(GEMINI.md markdown section, mcp_config.json JSON pointer).

**Requirements:** R1, R2, R4, R5 (KTD5)
**Dependencies:** U1
**Files:** `internal/adapter/bundled/antigravity/emit_tool_owned.go`
**Approach:** Define `pathSet{geminiMD, mcpConfig string}` and
`resolvePathSet(scope)`: user scope → `{.gemini/GEMINI.md, .gemini/config/mcp_config.json}`;
otherwise → `{GEMINI.md, .agents/mcp_config.json}`. `emitAgentsMD` emits
`OpWriteToolOwned{Kind: MarkdownSection, Locator: "agent-sync:"+id, Path: geminiMD}`
after rejecting bodies containing the marker opener (reuse claude's guard).
`emitMCPServerEntry` reuses claude's two-stage JSON-object validation and emits
`OpWriteToolOwned{Kind: JSONPointer, Locator: "/mcpServers/agentsync_"+id, Path: mcpConfig}`.
**No `.agent-sync-managed` sidecar** — Antigravity's `mcp_config.json` is a
tool-owned merge target, not an agent-sync-owned strict-JSON file, so the sidecar
(claude's strict-JSON ownership marker) does not apply. Confirm the merge engine
does not require it for JSON-pointer tool-owned entries (it does not for claude at
user scope, where the sidecar is likewise suppressed).
**Patterns to follow:** `claude/emit_tool_owned.go` (`resolvePathSet`,
`emitAgentsMD`, `emitMCPServerEntry`, `isJSONObject`, `markerOpenBytes`).
**Test scenarios:**
- agents-md node → one `write_tool_owned` at `GEMINI.md` (project) /
  `.gemini/GEMINI.md` (user), locator `agent-sync:<id>`, inner body only.
- agents-md body containing `<!-- agent-sync:` → `InvalidParams`, no op.
- mcp-server-entry with a JSON-object body → one `write_tool_owned` at
  `.agents/mcp_config.json` (project) / `.gemini/config/mcp_config.json` (user),
  locator `/mcpServers/agentsync_<id>`.
- mcp body that is valid JSON but not an object (array/number/string) →
  `InvalidParams`, no op.
- mcp body that is not valid JSON → `InvalidParams`, no op.
**Verification:** Tool-owned ops land at the correct scope-resolved paths; invalid
bodies fail closed.

### U4. Reserved/shared subdir emitters: rule, command, skill

**Goal:** Emit rules and workflows into agent-sync-owned subdirs under `.agent/`,
and skills into the shared `.agents/skills/` tree.

**Requirements:** R1, R3
**Dependencies:** U1
**Files:** `internal/adapter/bundled/antigravity/emit_reserved.go`,
`internal/adapter/bundled/antigravity/header.go`
**Approach:** Mirror claude's `emitRule`/`emitCommand`/`emitSkill` and its
per-subdir `mkdir` + `README.md` dedup (`readmeEmitted`) and per-path dedup
(`emittedFilePath`). Rewrite the destination roots: rule →
`.agent/rules/agent-sync/<id>.md`; command → `.agent/workflows/agent-sync/<id>.md`;
skill → `.agents/skills/agent-sync-<id>/SKILL.md` (+ assets under the leaf dir).
Skill leaf dirs are the only managed nodes inside the shared `.agents/skills`
parent (shared-subdir — never touch the parent or sibling leaves). Reuse claude's
managed-file header rendering (`header.go`) for the README/rule/command files.
**Patterns to follow:** `claude/emit_reserved.go`, `claude/header.go`,
`pi` skill emission into `.agents/skills`.
**Test scenarios:**
- rule node → `mkdir .agent/rules/agent-sync` + `README.md` + `<id>.md`
  (`write_file`), correct `OpRecord` order.
- command node → `mkdir .agent/workflows/agent-sync` + `README.md` + `<id>.md`.
- skill node with assets → `mkdir .agents/skills/agent-sync-<id>` + `SKILL.md` +
  each asset `write_file` at its `rel_path`; no op touches the `.agents/skills`
  parent.
- two skills → two independent leaf dirs, README dedup does not apply across
  skills (each leaf is self-contained), no duplicate `write_file` path error.
- asset `rel_path` colliding with `SKILL.md` → `InvalidParams` (path dedup).
**Verification:** Subdir ops land under the correct singular/plural roots; shared
tree parent is never emitted.

### U5. Emit dispatch + plugin-reference warning + wire plumbing

**Goal:** Wire `handleEmit`, `dispatchNode`, the `irNode` wire shape, dedup/sort,
node-targeting filter, and the unsupported `plugin-reference` warning.

**Requirements:** R1
**Dependencies:** U3, U4
**Files:** `internal/adapter/bundled/antigravity/emit.go`,
`internal/adapter/bundled/antigravity/emit_unsupported.go`
**Approach:** Copy claude's `emit.go` wholesale (irNode/irDocument shapes,
`emittedOps`, `records`, `wireOps`, `handleEmit`, `rejectDuplicateNodes`,
deterministic sort, `decodeBodyOrPassthrough`, `wrapBodyErr`/`wrapOpErr`,
`recordWritePath`). `nodeTargetsAntigravity` filters on `adapterName`.
`dispatchNode` routes rule/command/skill → U4 emitters, agents-md/mcp → U3
emitters, plugin-reference → `emitPluginReferenceWarning` (OpWarning,
`WarningStatusDegraded`, note: Antigravity has no project-level plugin-reference
registry). `emitState` drops claude's `sidecarEmitted` field (no sidecar).
**Patterns to follow:** `claude/emit.go`, `claude/emit_tool_owned.go`
(`emitPluginReferenceWarning`).
**Test scenarios:**
- `mixed-everything.json` (one of each supported kind) → the full expected
  `OpRecord` list in deterministic order.
- plugin-reference node → single `OpWarning`, status degraded, no file ops.
- node targeted at a different adapter (`targets: ["claude"]`) → silent skip.
- duplicate `(kind, id)` in payload → `InvalidParams`.
- unknown kind → `InvalidParams`.
**Verification:** `emitFixture` over each testdata doc yields the expected records;
mixed doc exercises every emitter.

### U6. Test fixtures

**Goal:** Provide the input-only IR JSON fixtures the emit tests read.

**Requirements:** R7
**Dependencies:** U2–U5 (co-developed with the tests referencing them)
**Files:** `internal/adapter/bundled/antigravity/testdata/ir/*.json`
(`agents-md-only`, `rule-only`, `skill-with-assets`, `mcp-only`,
`mixed-everything`, `plugin-reference-warn`)
**Approach:** Adapt claude's / pi's fixtures. Keep node ids and bodies minimal but
realistic (mcp body a real `{"command": …}` object; skill with one asset).
**Patterns to follow:** `claude/testdata/ir/`, `pi/testdata/ir/`.
**Test scenarios:** `Test expectation: none` — fixtures are test inputs, exercised
by U2–U5.
**Verification:** All emit tests referencing these fixtures pass.

### U7. Register the adapter + coverage native-read entries

**Goal:** Compile the adapter into the bundled set and record its native-read
behavior so `sync` warns accurately.

**Requirements:** R6
**Dependencies:** U1–U5
**Files:** `internal/cli/setup.go`, `internal/coverage/coverage.go`
**Approach:** Add the import + `antigravityadapter.Bundled()` line to
`bundledAdapters()`. In `coverage.go`:
- `nativeAtDirectory["antigravity"]`: `{agents-md: true}` — Antigravity walks
  nested `GEMINI.md`/`AGENTS.md`; it does not read nested `.agent/rules`,
  `.agent/workflows`, `.agents/skills`, or `.agents/mcp_config.json`. (Verify the
  nested-read assumption for rules/workflows against current docs during
  implementation; default to non-native = warn if unconfirmed.)
- `nonNativeAtUser["antigravity"]`: `{rule: true, command: true}` — no Antigravity
  global directory for these (global rules fold into `~/.gemini/GEMINI.md`; global
  workflows use an untargeted path). agents-md, skill, mcp all resolve under
  `~/.gemini/…` so they are native at user scope (no entry).
**Patterns to follow:** existing `setup.go` slice, `coverage.go` claude/codex/
cursor entries + their documented-assumptions comments.
**Test scenarios:**
- Registration: a `bundledAdapters()`/discovery test (or existing adapter-count
  test) sees `antigravity` in the set. Check for an existing count assertion and
  update it.
- `coverage.Analyze(LevelDirectory, [rule, agents-md], ["antigravity"])` → one
  warning for `rule`, none for `agents-md`.
- `coverage.Analyze(LevelUser, [rule, command, skill], ["antigravity"])` → warnings
  for `rule` and `command`, none for `skill`.
**Verification:** `antigravity` is discoverable; coverage warnings match the
user-scope gap decision (KTD4).

### U8. Documentation + roadmap update

**Goal:** Document the adapter and mark it landed.

**Requirements:** R8
**Dependencies:** U1–U7
**Files:** `docs/adapters/antigravity.md`, `README.md`, `CHANGELOG.md`,
`docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md`
**Approach:** Write `docs/adapters/antigravity.md` mirroring
`docs/adapters/{claude,pi}.md`: the kind→path table, the `.agent`/`.agents` split
rationale with sources, GEMINI.md-only decision, user-scope coverage gaps. Update
the README planned-adapter table row (Antigravity: Planned → shipped/supported;
bump the bundled count "Four" → "Five" everywhere it appears). Add a CHANGELOG
entry. In the roadmap plan, update the deferred-adapter note (`:57`) to record the
Antigravity adapter as landed with the PR reference.
**Patterns to follow:** `docs/adapters/pi.md`, existing README table, CHANGELOG
stamping style (recent commits `docs: stamp CHANGELOG …`).
**Test scenarios:** `Test expectation: none` — docs. Verify the README bundled
count is consistent across all mentions (there is a count in the adapter table and
possibly prose).
**Verification:** Docs build/read cleanly; README count consistent; roadmap
reflects landed status.

---

## Scope Boundaries

**In scope:** the five supported kinds at project + user scope, coverage entries,
registration, docs.

### Deferred to Follow-Up Work

- **User-scope rule/command native homes.** If a future Antigravity release
  documents a global `.agent/rules`/workflows directory (or we decide to target
  `~/.gemini/antigravity/global_workflows/` for commands), make those kinds
  scope-aware and drop them from `nonNativeAtUser`.
- **Nested-directory runtime mapping.** Coverage warns that non-agents-md kinds are
  not read from nested dirs; per-tool nested runtime mapping is a separate,
  cross-adapter effort already noted in the coverage package.
- **`AGENTS.md` participation.** Antigravity also reads `AGENTS.md`; deliberately
  left to `codex`/`pi` to avoid multi-writer collision (KTD1). Revisit only if the
  overlay-ownership model changes.

### Non-goals

- Changing the IR decoder (already done in PR #36).
- Antigravity IDE-plugin or extension packaging (`plugin-reference` stays
  unsupported).

---

## Risks & Dependencies

- **Directory-name accuracy is the top risk.** The `.agent`/`.agents` split is
  sourced from Google codelabs + DevRel posts, not a single machine-readable spec
  (the official `antigravity.google/docs` pages are an SPA that resisted
  extraction). Mitigation: the mapping is documented with sources in
  `docs/adapters/antigravity.md`; if a path is later found wrong it is a localized
  change in `resolvePathSet`/`declaredOutputs`/`emit_reserved.go`. Confirm paths
  once more against a live Antigravity install before merge if one is available.
- **Workspace `mcp_config.json` path** (`.agents/mcp_config.json`) is
  single-sourced; the global path (`~/.gemini/config/mcp_config.json`) is
  well-corroborated. Low blast radius (one `resolvePathSet` line).
- **Adapter-count test drift.** Adding the fifth adapter may break an existing
  count assertion; U7 accounts for locating and updating it.
- **Dependency:** none external — IR wiring is already merged (PR #36).

---

## Sources & Research

- Antigravity MCP config (`~/.gemini/config/mcp_config.json`, workspace
  `.agents/mcp_config.json`, `{"mcpServers": …}`): Google Workspace MCP codelab;
  Composio "How to connect MCP servers with Google Antigravity".
- Skills `.agents/skills/`, `.agents/workflows/`, `agents.md`: Google codelab
  "Build Autonomous Developer Pipelines using agents.md and skills.md in
  Antigravity"; Gemini API "Antigravity Agent" doc (`.agents/skills/`).
- Rules/workflows `.agent/rules/`, `.agent/workflows/` (singular): Mete Atamel
  (Google DevRel), atamel.dev "Customize Google Antigravity with rules and
  workflows"; agentpedia.codes user-rules guide; zenn.dev Antigravity rules
  article.
- GEMINI.md vs AGENTS.md precedence + `AGENTS.md` added in v1.20.3 (2026-03-05),
  global `~/.gemini/GEMINI.md`: agentpedia.codes; gemini-cli issue #16058.
- Global skills `~/.gemini/skills/`: Medium (Dazbo) "Configuring MCP Servers and
  Skills for Antigravity CLI and IDE".
- Origin: `docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md:57`
  (deferred supported-tier `antigravity` adapter); `internal/ir/decode.go`
  (`GEMINI.md` → `antigravity` overlay target, PR #36).

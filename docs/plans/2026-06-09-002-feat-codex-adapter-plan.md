---
title: "feat: bundled Codex CLI adapter (Unit 11)"
status: completed
date: 2026-06-09
type: feat
origin: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
master_plan: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# feat: bundled Codex CLI adapter (Unit 11)

## Summary

Ship the third bundled adapter, `codex`, for **Codex CLI**. It compiles the
tool-agnostic IR into the files Codex CLI actually reads, declaring everything
else `unsupported` honestly. This realizes master-plan **Unit 11** and brings the
README's "Codex CLI — primary" promise in line with reality.

The work mirrors the existing `claude` and `cursor` bundled adapters
(`internal/adapter/bundled/{claude,cursor}/`) in structure, emit-op style,
managed-file headers, and test shape. The one genuinely new concern is that
`codex` and `cursor` **both** section-merge into the workspace-root `AGENTS.md`,
so per-`id` marker isolation and multi-adapter coexistence must be proven by test.

---

## Problem Frame

The README advertises Codex CLI as a primary-tier target, but no `codex` adapter
is bundled or wired — only `claude` and `cursor` are. A user who lists `codex` in
their manifest today gets nothing useful.

The master plan's Unit 11 was authored in early 2026 and made assumptions about
Codex's file layout that needed verification against the current (June 2026)
product. Research against the official Codex docs confirms the mapping holds, with
small refinements folded into the decisions below (see Sources & Research).

---

## Requirements Trace

From the master plan (`origin`) and the product requirements it derives from:

- **R3** — translate the IR into a target tool's native conventions via a
  per-tool adapter. → U1–U4
- **R11** — capability-honest translation; lossy/unsupported mappings are made
  visible in the capability report, never silently dropped. → U1, U4
- **R12** — adapters emit declarative ops only; the CLI core performs writes
  (AGENTS.md invariant #2). → U2, U3, U4

Master-plan unit mapping: this plan is master Unit 11 in full. Unit 11.5 (`pi`)
and the Gemini/experimental adapters are explicitly out of scope.

Success criteria: conformance passes for `codex`; a manifest targeting `codex`
emits the correct files for `agents-md` + `skill` + `mcp-server-entry`; `rule`,
`command`, and `plugin-reference` surface as honest `unsupported` translations in
the capability report; `codex` and `cursor` coexist in one `AGENTS.md` without
clobbering each other or user content.

---

## Concept → Destination Mapping (validated June 2026)

| IR kind | Codex destination | Op mode | Status |
|---------|-------------------|---------|--------|
| `agents-md` | workspace-root `AGENTS.md`, section between `<!-- aienvs:begin id=<id> -->` / `<!-- aienvs:end id=<id> -->` | `write_tool_owned`, `markdown-section` locator | supported (shared with `cursor`) |
| `skill` | `.agents/skills/aienvs-<id>/SKILL.md` (+ assets) | reserved owned-subdir, name-prefix isolation | supported (shared cross-tool) |
| `mcp-server-entry` | `.codex/config.toml`, table `[mcp_servers.aienvs_<id>]` | `write_tool_owned`, `toml-path` locator `mcp_servers.aienvs_<id>` | supported |
| `rule` | — | — | `unsupported` (no per-tool rule concept; folded into AGENTS.md) |
| `command` | — | — | `unsupported` (custom prompts deprecated **and** user-home-only `~/.codex/prompts/`, not repo-shareable; point to skills) |
| `plugin-reference` | — | — | `unsupported` (no project-level plugin registry) |

Key path reality (the "origin asymmetry" to document prominently): **there is no
`.codex/aienvs/` reserved subdirectory** the way `.claude/rules/aienvs/` exists
for Claude. Codex skills live under the shared `.agents/skills/` tree, MCP entries
live inside the tool-owned `.codex/config.toml`, and prose lives in the shared
`AGENTS.md`. The `codex` adapter therefore owns *no* dedicated reserved prefix of
its own — a structural difference from `claude`/`cursor` that the adapter manifest
and docs must state plainly.

---

## Key Technical Decisions

- **Mirror the `cursor` adapter, not `claude`.** `cursor` is the closer template:
  it already does workspace-root `AGENTS.md` section-merge (`emit_tool_owned.go`)
  and honest `unsupported` declarations (`emit_unsupported.go`, `emit_reserved.go`).
  Follow its file split, header format, and test layout.
- **Reuse the existing `<!-- aienvs:begin/end id=<id> -->` marker scheme** for the
  AGENTS.md section so `codex` and `cursor` produce byte-compatible markers. This
  is what makes per-`id` coexistence work: each adapter's section is keyed by node
  `id`, independently locatable and deletable. Do not invent a codex-specific
  marker syntax.
- **MCP via `toml-path` tool-owned merge**, locator `mcp_servers.aienvs_<id>`,
  reusing `internal/merge`'s TOML surgical-merge path (the same `write_tool_owned`
  machinery `cursor` uses for `.cursor/mcp.json` via `json-pointer`). User content
  elsewhere in `.codex/config.toml` (e.g. top-level keys, non-aienvs tables) must
  be preserved across read/write.
- **Project-local `.codex/config.toml` is the emit target** (confirmed supported;
  `mcp_servers` is not a project-scope-restricted key). Emit a one-time advisory
  alongside MCP writes: if a user's Codex version doesn't load project-local MCP
  servers, the same table can be lifted to `~/.codex/config.toml`. This is a
  softened version of the master plan's "known bug" note — framed as a
  version-dependent fallback, not a defect we're shipping around.
- **`command` is `unsupported`, not best-effort.** Custom prompts are deprecated
  *and* live only in `~/.codex/prompts/` (not repo-shareable), so there is no
  project-scoped destination aienvs could own. The capability note points users at
  skills instead.
- **No dedicated reserved prefix.** Unlike `claude`/`cursor`, the `codex` adapter
  manifest declares its owned outputs as the shared `.agents/skills/aienvs-*`
  folders, the tool-owned `AGENTS.md` sections, and the tool-owned
  `.codex/config.toml` tables — there is no `.codex/aienvs/` subdir. The ledger
  tracks these three shapes (per-folder, per-section, per-table).

---

## Output Structure

```
internal/adapter/bundled/codex/
  bundled.go              # Bundled() constructor + adapter.AdapterManifest
  capabilities.go         # capability matrix loader (embeds capabilities.yaml)
  capabilities.yaml       # supported/unsupported per IR kind + notes
  header.go               # managed-file header + marker construction
  emit.go                 # Emit entrypoint: dispatch by IR kind
  emit_reserved.go        # skill -> .agents/skills/aienvs-<id>/SKILL.md (+assets)
  emit_tool_owned.go      # agents-md -> AGENTS.md section; mcp -> .codex/config.toml
  emit_unsupported.go     # rule/command/plugin-reference -> warnings
  capabilities_test.go
  header_test.go
  emit_test.go
  emit_opcontent_test.go  # (if claude's op-content coverage pattern applies)
  testdata/ir/*.json      # IR fixtures mirroring cursor's testdata set
docs/adapters/codex.md    # concept->destination table + asymmetry callout
```

`internal/cli/setup.go` is modified (not created) to register the adapter.

---

## Implementation Units

### U1. Adapter scaffold, manifest, and capability matrix

**Goal:** Stand up the `codex` adapter package with its `BundledAdapter`
constructor, `AdapterManifest`, capability matrix, and managed-file header — the
non-emit skeleton, so the adapter is discoverable and declares honest capabilities.

**Requirements:** R3, R11.

**Dependencies:** none (the adapter contract, IR, and bundled-adapter machinery
already exist).

**Files:**
- Create: `internal/adapter/bundled/codex/bundled.go`,
  `internal/adapter/bundled/codex/capabilities.go`,
  `internal/adapter/bundled/codex/capabilities.yaml`,
  `internal/adapter/bundled/codex/header.go`
- Test: `internal/adapter/bundled/codex/capabilities_test.go`,
  `internal/adapter/bundled/codex/header_test.go`

**Approach:** Copy the shape of `internal/adapter/bundled/cursor/{bundled,
capabilities,header}.go` and `capabilities.yaml`. The capability matrix declares
`agents-md`, `skill`, `mcp-server-entry` as `supported`; `rule`, `command`,
`plugin-reference` as `unsupported`, each with a `note` (command's note points to
skills; rule's to consolidating into agents-md; plugin-reference's explains no
project registry). The manifest declares the adapter name `codex` and the owned
output shapes — crucially, **no `.codex/aienvs/` reserved prefix** (see KTD).

**Patterns to follow:** `internal/adapter/bundled/cursor/bundled.go`,
`capabilities.go`, `capabilities.yaml`, `header.go`; the manifest-name id grammar
guard in `cursor/header.go`.

**Test scenarios:**
- `capabilities.yaml` parses and the built matrix reports exactly the six kinds
  with the statuses in the mapping table (supported: agents-md, skill,
  mcp-server-entry; unsupported: rule, command, plugin-reference).
- `capabilities_test.go` asserts every `ir.AllKinds()` kind has an explicit entry
  (no kind silently defaulting).
- Header construction produces the shared `<!-- aienvs:begin id=<id> -->` /
  `<!-- aienvs:end id=<id> -->` markers byte-identical to cursor's for the same id.
- `header_test.go`: invalid id reaching marker construction is rejected upstream
  (mirror cursor's guard test).

**Verification:** package builds; `capabilities_test`/`header_test` pass; the
matrix matches the mapping table.

### U2. Emit `skill` → `.agents/skills/aienvs-<id>/SKILL.md`

**Goal:** Emit skills into the shared `.agents/skills/` tree with name-prefix
isolation, including skill assets.

**Requirements:** R3, R12.

**Dependencies:** U1.

**Files:**
- Create: `internal/adapter/bundled/codex/emit.go`,
  `internal/adapter/bundled/codex/emit_reserved.go`
- Test: `internal/adapter/bundled/codex/emit_test.go` (skill cases),
  `internal/adapter/bundled/codex/testdata/ir/skill-with-assets.json`,
  `.../testdata/ir/skill-only.json`

**Approach:** `emit.go` is the dispatch entrypoint (decode IR doc, route each node
by kind). For `skill`, emit a `mkdir` for `.agents/skills/aienvs-<id>/`, a
`write_file` for `SKILL.md`, and a `write_file` per asset at its `rel_path` under
the skill folder. Paths are forward-slash on the wire regardless of host OS (see
`docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`,
Rule 3). Mirror `claude/emit.go`'s `emitSkill` shape (Claude is the existing skill
emitter; cursor declares skill unsupported, so claude is the reference here).

**Patterns to follow:** `internal/adapter/bundled/claude/emit.go` `emitSkill` +
reserved-subdir README emission; `claude/emit_reserved.go`.

**Test scenarios:**
- Happy path: IR with one `skill` node (no assets) emits `mkdir .agents/skills/
  aienvs-<id>` + `write_file .agents/skills/aienvs-<id>/SKILL.md` with the node
  body as content.
- Happy path: `skill` with two assets emits the SKILL.md plus one `write_file`
  per asset at `.agents/skills/aienvs-<id>/<rel_path>`, forward-slash paths.
- Edge case: skill id with subdirectory-shaped asset rel_path (e.g.
  `templates/x.txt`) nests correctly under the skill folder.
- Edge case: empty skill body still emits a SKILL.md (no panic, content may be
  empty/minimal).
- Op paths are all under `.agents/skills/aienvs-<id>/` (no escape).

**Verification:** `emit_test` skill cases pass; golden op list matches expected
paths and kinds.

### U3. Emit `agents-md` → AGENTS.md section and `mcp-server-entry` → `.codex/config.toml`

**Goal:** Emit the two tool-owned-file mappings via `write_tool_owned`, preserving
unrelated user content in both shared files.

**Requirements:** R3, R12.

**Dependencies:** U1.

**Files:**
- Create: `internal/adapter/bundled/codex/emit_tool_owned.go`
- Test: extend `internal/adapter/bundled/codex/emit_test.go` (agents-md + mcp
  cases); `.../testdata/ir/agents-md-only.json`,
  `.../testdata/ir/mcp-server-entry-only.json`,
  `.../testdata/ir/mixed-everything.json`

**Approach:** For `agents-md`, emit a `write_tool_owned` op targeting workspace-root
`AGENTS.md` with a `markdown-section` locator keyed by node id (begin/end markers
from U1's header). For `mcp-server-entry`, emit a `write_tool_owned` op targeting
`.codex/config.toml` with a `toml-path` locator `mcp_servers.aienvs_<id>` carrying
the normalized server entry. Mirror `cursor/emit_tool_owned.go` (which does the
AGENTS.md markdown-section path) and the TOML path machinery in `internal/merge`.
Attach the MCP project-local-config advisory as an `op_warning` (or capability
note) per the KTD.

**Patterns to follow:** `internal/adapter/bundled/cursor/emit_tool_owned.go`
(AGENTS.md markdown-section); `internal/merge/toml.go` + `cursor`'s mcp emission
for the tool-owned TOML/JSON locator shape; `docs/spec/tool-owned-merge-v1.md`.

**Test scenarios:**
- Happy path: `agents-md` node emits one `write_tool_owned` op, file `AGENTS.md`,
  markdown-section locator keyed by id, content = node body.
- Happy path: `mcp-server-entry` node emits one `write_tool_owned` op, file
  `.codex/config.toml`, toml-path locator `mcp_servers.aienvs_<id>`.
- Happy path: `mixed-everything.json` (agents-md + skill + mcp) emits the union:
  AGENTS.md section, `.agents/skills/...`, `.codex/config.toml` table.
- Edge case: MCP emission carries the one-time project-local-config advisory
  exactly once per sync, not once per entry.
- Integration (deferred to apply-time, asserted in U5 coexistence test): unrelated
  user content in `.codex/config.toml` and `AGENTS.md` survives a real merge.

**Verification:** `emit_test` agents-md + mcp cases pass; op shapes match the
tool-owned-merge spec.

### U4. Unsupported handling + register the adapter

**Goal:** Emit honest `unsupported` warnings for `rule`/`command`/
`plugin-reference`, and wire `codex` into the bundled adapter set so it is
discoverable end to end.

**Requirements:** R11, R12.

**Dependencies:** U1, U2, U3.

**Files:**
- Create: `internal/adapter/bundled/codex/emit_unsupported.go`
- Modify: `internal/cli/setup.go` (add `codexadapter.Bundled()` to
  `bundledAdapters()`)
- Test: extend `internal/adapter/bundled/codex/emit_test.go` (unsupported cases);
  `.../testdata/ir/command-unsupported.json`,
  `.../testdata/ir/rule-unsupported.json`,
  `.../testdata/ir/plugin-reference-unsupported.json`

**Approach:** Mirror `cursor/emit_unsupported.go`: each unsupported kind produces an
`op_warning` with the capability note, and the node is not emitted as a file. A
`required: true` node of an unsupported kind must surface as `required_unmet`
(fail in atomic mode) rather than a silent skip — reuse the existing
required-unmet path the other adapters use. Then register in `setup.go`.

**Patterns to follow:** `internal/adapter/bundled/cursor/emit_unsupported.go`;
`internal/cli/setup.go` `bundledAdapters()` (claude + cursor registration).

**Test scenarios:**
- `rule` node → warn-and-skip, no file op, capability report records unsupported.
- `command` node → warn-and-skip with the skills-pointer note.
- `plugin-reference` node → warn-and-skip with the no-registry note.
- Edge case: `required: true` rule targeting codex → `required_unmet` (surfaced,
  not papered over).
- After registration, `bundledAdapters()` returns three adapters and discovery
  resolves `codex` (a thin assertion in `internal/cli` setup or discovery test).

**Verification:** `emit_test` unsupported cases pass; `setup.go` change compiles;
discovering `codex` works in a wired sync (smoke).

### U5. Cross-adapter AGENTS.md coexistence test + `docs/adapters/codex.md`

**Goal:** Prove `codex` and `cursor` coexist in one workspace-root `AGENTS.md`, and
document the path-reality asymmetry.

**Requirements:** R11 (capability honesty surfaced in docs), R3.

**Dependencies:** U2, U3, U4.

**Files:**
- Create: `docs/adapters/codex.md`
- Test: a cross-adapter coexistence test. Preferred location: alongside the merge
  apply tests (`internal/merge/`) or a focused `internal/sync`/`internal/engine`
  test that applies both adapters' AGENTS.md sections to one file. (Implementer
  picks the seam that exercises real `write_tool_owned` apply, not just op
  emission.)

**Approach:** The coexistence test seeds an `AGENTS.md` containing user prose, then
applies a `codex` section (id A) and a `cursor` section (id B) via the real
tool-owned markdown-section merge. Assert: both sections present, each keyed by its
id, user prose outside markers byte-identical, and deleting one section (orphan
removal of id A) leaves the other section and user prose intact. `docs/adapters/
codex.md` carries the concept→destination table from this plan and a prominent
callout that there is **no `.codex/aienvs/` subdirectory** in v1, plus the AGENTS.md
32 KiB cap and `AGENTS.override.md` precedence caveats.

**Patterns to follow:** `internal/merge/markdown_test.go` (section merge);
`docs/adapters/cursor.md`, `docs/adapters/claude.md` (doc shape).

**Test scenarios:**
- Two adapters, two ids, one `AGENTS.md`: both sections present after applying
  both; user content outside markers unchanged.
- Delete (orphan) the codex section by id → cursor section + user prose remain
  byte-identical; the codex markers and content are gone.
- Re-applying the same codex section is idempotent (no duplicate markers).
- `docs/adapters/codex.md` exists and contains the asymmetry callout (assert by
  presence in a docs-lint or simple test if the repo has one; otherwise this is a
  documentation deliverable verified by review).

**Verification:** coexistence test passes; `docs/adapters/codex.md` documents the
mapping and asymmetry; full `go test -race ./...` green.

---

## Risks & Dependencies

- **Risk: shared `AGENTS.md` clobbering.** The headline risk — two adapters writing
  one file. Mitigated by reusing cursor's exact id-keyed marker scheme and proving
  coexistence in U5 before this ships. If the markdown-section merge can't isolate
  by id, that's a blocker surfaced at U5, not after merge.
- **Risk: TOML merge fidelity.** `.codex/config.toml` may contain arbitrary user
  tables; the `toml-path` merge must preserve them. Mitigated by the existing
  `internal/merge` TOML path (already fuzz-tested) and the U3 preservation case.
- **Risk: Codex convention drift.** The mapping is validated as of June 2026; Codex
  is fast-moving. `docs/adapters/codex.md` records the validation date and the two
  version-dependent caveats (project-local MCP loading, AGENTS.override shadowing)
  so future drift is traceable.
- **Dependency:** none blocking — all required primitives (IR, adapter contract,
  tool-owned merge, ledger, conformance harness) are merged.

---

## Sources & Research (validated June 2026)

- AGENTS.md discovery (repo-root walk, 32 KiB `project_doc_max_bytes`,
  `AGENTS.override.md` precedence): https://developers.openai.com/codex/guides/agents-md,
  https://developers.openai.com/codex/config-advanced
- Project-local `.codex/config.toml` supported; `mcp_servers` not restricted at
  project scope: https://developers.openai.com/codex/config-reference,
  https://developers.openai.com/codex/mcp
- Skills scanned from `.agents/skills` (cwd → repo root); SKILL.md + frontmatter:
  https://developers.openai.com/codex/skills
- Custom prompts deprecated and user-home-only (`~/.codex/prompts/`), skills are
  the recommended replacement: https://developers.openai.com/codex/custom-prompts

These findings confirmed the master plan's Unit 11 mapping and refined two
advisories (MCP project-local framing; command-unsupported rationale).

---

## Scope Boundaries

### Deferred to Follow-Up Work
- Unit 11.5: the `pi` adapter (shares the `.agents/skills/` tree — coordinate the
  four-way AGENTS.md coexistence test then).

### Out of scope
- Gemini and experimental-tier adapters.
- The extension-SDK CLI (Unit 20).
- `rollback`/`unmanage` commands.

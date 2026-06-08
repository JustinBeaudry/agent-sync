---
title: Unit 10 — Bundled `cursor` adapter (IR → Cursor ops)
type: feat
status: active
date: 2026-06-08
origin: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# Unit 10 — Bundled `cursor` adapter

## Overview

Ship the second bundled adapter (`cursor`) that consumes IR v1 nodes
and emits the v1 op vocabulary (`write_file`, `write_tool_owned`,
`mkdir`, `delete`, `warning`) targeted at Cursor's actual on-disk
layout. The adapter is a faithful structural copy of the Unit 9
`claude` adapter (`internal/adapter/bundled/claude/`): same SDK-backed
`Bundled()` constructor, same in-process `adapterkit.Server` wiring,
same managed-file-header / section-marker / per-subdir-README helpers,
same "summary-only `OpsPerformed`" wire behavior, same zero-file-I/O
invariant. Only the **destination mapping and the capability matrix
differ** — and those differences are the whole point of the unit.

Cursor's layout diverges from Claude's in five concrete ways, each of
which drives a decision below:

- `rule` → `.cursor/rules/aienvs/<id>.mdc` (note `.mdc`, not `.md`).
- `mcp-server-entry` → `.cursor/mcp.json` (under `.cursor/`, **not**
  workspace root like Claude's `.mcp.json`).
- `agents-md` → workspace-root `AGENTS.md` (**not** `CLAUDE.md`).
- `skill`, `command`, `plugin-reference` → `unsupported` (Cursor reads
  none of these at the project level — see Key Technical Decisions).
- The `paths:` frontmatter ward is **dropped** (it is a Claude Code
  bug; `.mdc` frontmatter has no equivalent issue). The Cursor-specific
  legacy concern is a pre-existing `.cursorrules` file — but detecting
  it requires filesystem access the adapter does not have, so that
  check is deferred to the sync engine (KTD 7).

As with Unit 9, "done" means the adapter emits the right ops with the
right content for every **supported** IR kind, declares its capability
matrix honestly, surfaces honest `warning` ops for every
**unsupported** kind, and passes a cursor-specific conformance corpus
driven in-process via `pkg/adapterkit/testing.RunInprocServer`. Actual
file writes still happen later in the sync engine (Units 12, 12a, 13).

## Problem Frame

Unit 9 proved the framework end-to-end against one tool. Unit 10's job
is to prove the adapter *shape* generalizes to a second tool whose
layout, file extension, MCP-config location, and supported-concept set
all differ from Claude's. It is the first real test of the "copy the
claude package shape" promise made in Unit 9's System-Wide Impact
section, and it is where the **honest-`unsupported`** philosophy earns
its keep: Cursor genuinely does not have skills, project commands, or a
plugin registry, so the adapter must decline them out loud rather than
emit dead files into directories Cursor never reads.

It also surfaces the first cross-adapter shared-file concern:
`AGENTS.md` at the workspace root will eventually be written by
`cursor`, `codex`, and `pi` (Units 10, 11, 11.5). This unit writes
only a single aienvs-marked section into `AGENTS.md` and does not yet
coordinate with other adapters — the per-external-file lock and
multi-section merge live in Units 12/12a. This unit must not assume it
is the sole writer of `AGENTS.md`.

## Requirements Trace

- **R3.** Bundled `cursor` adapter present in v1 (second primary
  adapter alongside `claude`).
- **R11.** Adapter respects v1 IR concept set (closed kinds; honest
  capability declarations — three kinds declared `unsupported`).
- **R12.** Per-tool ownership model — mix of reserved subdirectory
  (`rule`), tool-owned-file ops (`mcp-server-entry`, `agents-md`), and
  honest `unsupported` for the rest.
- **Master plan decision #25** — encoded mappings in
  `docs/adapters/cursor.md` and `capabilities.yaml`.
- **Master plan decision #23** — managed-file headers + per-reserved-
  subdir README.
- **Master plan Unit 10** (lines 755–786 of the origin plan) — this
  plan refines that unit, with two documented divergences (KTD 6
  `skill: unsupported`; KTD 7 `.cursorrules` detection deferred to the
  sync engine).

## Scope Boundaries

- **In scope:** the bundled `cursor` adapter, its capabilities
  declaration (`capabilities.yaml` + in-code mirror), its emission
  logic for every supported IR kind (`rule`, `mcp-server-entry`,
  `agents-md`), honest `warning` + skip handling for unsupported kinds
  (`skill`, `command`, `plugin-reference`), the cursor-specific
  conformance corpus, and `docs/adapters/cursor.md`.
- **Out of scope (explicit non-goals):**
  - Orphan deletion logic (Unit 14).
  - Tool-owned-file merging (Unit 12a — the adapter only emits the
    `write_tool_owned` op; the merge that combines aienvs's `AGENTS.md`
    section with user content and with other adapters' sections lives
    in the sync engine).
  - Per-external-file lock for the shared `AGENTS.md` (Unit 12).
  - Ledger persistence (Unit 12), atomic swap (Unit 13).
  - CLI registration into the cobra tree (Unit 16).
  - Capability-report rendering (Unit 15).
  - Real values for the managed-file-header `{source-url}@{short-sha}`
    placeholders (Unit 13 `_meta` plumbing; v1 ships placeholders, same
    as Unit 9).

### Deferred to Follow-Up Work

- **`.cursorrules` legacy detection + warning.** The master plan Unit
  10 text asks the adapter to detect a pre-existing `.cursorrules`
  file and warn. The adapter performs **zero filesystem I/O** (Unit 9
  invariant) and the project's `CLAUDE.md` forbids reaching outside
  `internal/fsroot` to touch user paths. A bundled adapter has neither
  fsroot access nor a sanctioned stat path, so it cannot detect the
  file. This check is therefore deferred to the **sync engine** (the
  layer that owns `internal/fsroot` and walks the workspace), most
  naturally as a pre-sync workspace lint in Unit 13 or a dedicated
  legacy-detection step. The cursor adapter's `docs/adapters/cursor.md`
  documents the legacy status of `.cursorrules`; the *runtime* warning
  is the sync engine's job. **Action:** update master plan Unit 10 to
  record this divergence in the same PR (project `CLAUDE.md` rule:
  "If a decision must diverge from the plan, update the plan first."),
  **and add a tracked checklist item with its own acceptance criterion
  to the sync-engine unit (Unit 13)** so the `.cursorrules` detection +
  warning obligation has a committed home and an owner — not a floating
  "most naturally in Unit 13" note that no unit is accountable for and
  that therefore risks shipping never.
- **Skill support if Cursor ships a skills concept.** KTD 6 declares
  `skill: unsupported` because Cursor has no `.cursor/skills/` /
  folder-per-skill convention today. If a future Cursor release adds
  one, flipping the declaration to `supported` is a v1.x change that
  copies Unit 9's `emitSkill` shape with the `.cursor/skills` parent.

## Context & Research

### Relevant Code and Patterns

The cursor adapter is a structural copy of the claude adapter. Every
file below has a direct claude counterpart to mirror:

- **`internal/adapter/bundled/claude/bundled.go`** — `Bundled()
  *adapter.BundledAdapter` constructor; `run(ctx, stdin, stdout)`
  in-process entry point; `bundledGetenv` cookie placeholder. Copy
  verbatim, swapping `claude`→`cursor`, `.claude`→`.cursor`.
- **`internal/adapter/bundled/claude/capabilities.go`** —
  `//go:embed capabilities.yaml`, `conceptKinds` in-code map,
  `capabilitiesForWire()`, `declaredOutputs()`, `parseCapabilitiesYAML`,
  `loadDeclaration`. The `conceptKinds` map and `declaredOutputs()`
  body are where cursor diverges.
- **`internal/adapter/bundled/claude/capabilities.yaml`** — the
  authoritative per-kind declaration; cursor's version flips three
  kinds to `unsupported` and changes the supported kinds' notes.
- **`internal/adapter/bundled/claude/emit.go`** — `irNode`/`irDocument`
  wire shapes, `handleEmit`, `dispatchNode`, `decodeIRDocument`,
  `nodeTargetsClaude` (→ `nodeTargetsCursor`), `rejectDuplicateNodes`,
  `decodeBodyOrPassthrough`, `emitState`. The dispatch switch changes:
  cursor routes `rule`/`mcp-server-entry`/`agents-md` to emitters and
  `skill`/`command`/`plugin-reference` to a warn-and-skip branch.
- **`internal/adapter/bundled/claude/emit_reserved.go`** — `emitRule`
  (mirror under `.cursor/rules/aienvs` with `.mdc` extension; **drop**
  the `paths:` ward), `ensureSubdir`, `prependHeader`, asset-path
  validation (not needed — cursor has no skill emission). Note the
  Rule-of-Three comment at `emit_reserved.go:30` — cursor adds a
  *single* reserved-subdir kind (`rule`), so do **not** extract a
  shared helper yet; copy the claude shape.
- **`internal/adapter/bundled/claude/emit_tool_owned.go`** —
  `emitMCPServerEntry` (mirror; path `.cursor/mcp.json`, same
  `/mcpServers/aienvs_<id>` pointer, same JSON-object validation,
  same `.aienvs-managed` sidecar next to the JSON file under
  `.cursor/`), `emitAgentsMD` (mirror; path `AGENTS.md`, same
  `markdown-section` locator and marker-injection guard),
  `emitPluginReferenceWarning` (generalize to all three unsupported
  kinds), `isJSONObject`.
- **`internal/adapter/bundled/claude/header.go`** — `markdownHeader`,
  `jsonSidecarMarker`, `sectionMarkerBegin/End`, `wrapManagedSection`,
  `readmeForSubdir`, `mustValidID`. Copy verbatim; swap the literal
  `claude` strings in `readmeForSubdir` and `jsonSidecarBody` to
  `cursor` and `aienvs unmanage cursor`.
- **`internal/adapter/bundled/claude/bundled_test.go`,
  `emit_test.go`, `capabilities_test.go`, `header_test.go`** — test
  shapes to mirror, including the in-process `RunInprocServer` end-to-
  end driver and the capabilities-YAML-vs-code parity test.
- **`internal/adapter/bundled/claude/testdata/{ir,expected}/`** —
  fixture pair shape (IR JSON in `ir/`, `[]OpRecord` summary +
  golden content in `expected/`).

Shared infrastructure (unchanged, consumed as-is):

- **`pkg/adapterkit/types.go`** — `OpWriteFile`, `OpWriteToolOwned`
  (`ToolOwnedKind` ∈ {`json-pointer`, `toml-path`, `markdown-section`}),
  `OpMkdir`, `OpDelete`, `OpWarning`, `DeclaredOutput`, `Capabilities`,
  `OutputMode` ∈ {`owned-subdir`, `tool-owned-entry`}, `EmitParams`
  (carries `target` + `ir` only — **no workspace-file context**),
  `InitializeParams` (carries `WorkspaceRoot` string, but the adapter
  must not stat it — see KTD 7).
- **`pkg/adapterkit/server.go`** — `NewServer`, `OnInitialize`,
  `OnEmit`, `Run`.
- **`pkg/adapterkit/testing.go`** — `RunInprocServer` + `Client`.
- **`internal/adapter/discover.go`** — `BundledAdapter{Manifest, Run}`.
- **`internal/ir/types.go`** — `Kind` constants, `IsValidID`.
- **`internal/capmatrix/types.go`** — `CapabilityStatus`.
- **`internal/adapter/runtime.go`** — `pathInDeclaredOutputs` gate and
  the capability-lied check (only fires for `supported` kinds — this is
  why warning-only emission for `unsupported` kinds is safe).

### Institutional Learnings

- **`docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`**
  Rule 3: wire-protocol paths are forward-slash regardless of host OS;
  build op paths with string concatenation using `/`, never
  `filepath.Join`. Mirrored from the claude adapter.
- **`docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`**
  Update `docs/adapters/cursor.md` and `capabilities.yaml` in the same
  PR as `emit.go` — split-PR drift is a known repeat-cost.

### External References

- **Cursor project rules — `.cursor/rules/*.mdc`:** Cursor reads
  project rules from `.cursor/rules/` as `.mdc` (Markdown-with-
  frontmatter) files and supports nested subdirectories under
  `.cursor/rules/`, which is what makes `.cursor/rules/aienvs/<id>.mdc`
  a valid owned subdirectory. `.cursorrules` (single root file) is the
  legacy form, superseded by `.cursor/rules/`.
  <https://docs.cursor.com/context/rules>
- **Cursor MCP config — `.cursor/mcp.json`:** project-scoped MCP
  servers live in a flat `.cursor/mcp.json` file with the
  `{"mcpServers": {"<name>": {...}}}` shape (same shape as Claude's
  `.mcp.json`, different location). <https://docs.cursor.com/context/mcp>
- **`AGENTS.md` standard:** Cursor reads an `AGENTS.md` file at the
  project root (the cross-tool agents.md convention).
  <https://agents.md>
- **NOTE — web verification was not run for this plan** (the planning
  web-research step was declined). The path/extension/shape facts above
  reflect the planner's existing knowledge and the master plan's
  authoritative mappings. Unit 10.7 (`docs/adapters/cursor.md`)
  includes a path-verification subsection; the implementer should
  confirm each path against current Cursor docs at implementation time
  and adjust if a fact has changed. The two capability *downgrades*
  (KTD 6, KTD 7) are the conservative, fail-safe direction — if a fact
  turns out more permissive than assumed, the result is a missing
  feature, not a corrupted file.

## Key Technical Decisions

1. **Structural copy of the claude adapter, not a new shape.** The
   package mirrors `internal/adapter/bundled/claude/` file-for-file.
   This is deliberate duplication: with only two adapters, extracting a
   shared "bundled adapter kit" would be premature (Rule of Three; the
   third adapter is codex/pi in Units 11/11.5). Keeping the cursor
   package a near-verbatim copy makes the *diff* between adapters the
   reviewable artifact and keeps each adapter's destination mapping
   self-contained.

2. **`reserved_prefix` is `.cursor`.** The shared parent for the
   adapter's one owned subdirectory (`.cursor/rules/aienvs`).
   `declared_outputs` enumerates the actual subpaths. The
   `reserved_prefix` field feeds Unit 8's cross-adapter nested-prefix
   conflict detection; `.cursor/mcp.json` sits *under* `.cursor` and is
   declared as a `tool-owned-entry` (the path-safety gate accepts it;
   the nested-prefix check is about owned-subdir overlap, not tool-
   owned files).

3. **`.mcp.json` lives under `.cursor/`, `AGENTS.md` at workspace
   root.** Cursor reads MCP config from `.cursor/mcp.json` (unlike
   Claude's workspace-root `.mcp.json`) and reads `AGENTS.md` from the
   workspace root (unlike Claude's `CLAUDE.md` companion). The
   `.aienvs-managed` JSON sidecar for the strict-JSON file is written
   next to `.cursor/mcp.json` — i.e. at `.cursor/.aienvs-managed`, not
   workspace-root `.aienvs-managed`. `declared_outputs`:
   - `{path: ".cursor/rules/aienvs", mode: "owned-subdir"}`
   - `{path: ".cursor/mcp.json", mode: "tool-owned-entry", json_pointer: "/mcpServers"}`
   - `{path: "AGENTS.md", mode: "tool-owned-entry", section_id: "aienvs"}`
   - `{path: ".cursor/.aienvs-managed", mode: "owned-subdir"}` (exact-
     path declaration for the sidecar, mirroring claude's approach of
     declaring the sidecar by exact path rather than over-broadly
     authorizing a directory).

4. **Capabilities declared in `capabilities.yaml`, mirrored in code,
   parity-tested.** Same single-source-of-drift-detection pattern as
   Unit 9.1: the YAML is the authoritative human-readable declaration,
   the in-code `conceptKinds` map is the runtime authority, and a unit
   test parses the YAML and asserts kind-for-kind agreement. Cursor's
   matrix:

   | Kind | Status | Notes |
   |---|---|---|
   | `agents-md` | `supported` | Section-merged into workspace-root `AGENTS.md` between aienvs markers. |
   | `rule` | `supported` | `.cursor/rules/aienvs/<id>.mdc` with managed-file header. |
   | `mcp-server-entry` | `supported` | `.cursor/mcp.json` under `/mcpServers/aienvs_<id>`; `.cursor/.aienvs-managed` sidecar. |
   | `skill` | `unsupported` | Cursor has no folder-per-skill `SKILL.md` concept. |
   | `command` | `unsupported` | Cursor has no project-level custom slash-command concept. |
   | `plugin-reference` | `unsupported` | Cursor has no project-level plugin registry. |

5. **`agents-md` is supported and writes `AGENTS.md` directly** (not a
   companion overlay into a different file like Claude's `CLAUDE.md`).
   Cursor reads `AGENTS.md` natively, so the IR `agents-md` kind maps
   to its namesake file. Content is `wrapManagedSection(node.ID, body)`
   — begin/end markers wrap the body — emitted as
   `OpWriteToolOwned{Path: "AGENTS.md", Kind: markdown-section,
   Locator: "aienvs:" + node.ID}`. **This adapter does not assume it
   owns the whole file**: the merge step (Unit 12a) combines this
   section with user content *and* with sections other adapters
   (`codex`, `pi`) write to the same `AGENTS.md`. The adapter emits
   exactly one marked section and nothing else.

6. **`skill: unsupported` — cross-check resolved.** The master plan
   Unit 10 listed `skill` under "name-prefix isolation" *with an
   explicit cross-check escape hatch* ("if Cursor does not yet support
   subdirectory-based skills at all, declare `skill: unsupported`;
   don't silently degrade"). Cursor has no `.cursor/skills/` /
   folder-per-skill / `SKILL.md` convention — skills are a Claude Code
   concept. Emitting skill files into a directory Cursor never reads
   would create dead files and violate the codebase's honest-mapping
   philosophy (master plan decision #25). Therefore `skill` is declared
   `unsupported` and produces a `warning` op. This is within the master
   plan's authorized envelope, not a divergence from it. (If web
   verification at implementation time surfaces a real Cursor skills
   convention, flip to `supported` by copying Unit 9's `emitSkill`.)

7. **`.cursorrules` legacy detection deferred to the sync engine.** The
   adapter cannot detect a pre-existing `.cursorrules` file: it does
   zero filesystem I/O (Unit 9 invariant), `EmitParams` carries no
   workspace-file context, and the project `CLAUDE.md` forbids reaching
   outside `internal/fsroot` to stat user paths. `InitializeParams`
   carries a `WorkspaceRoot` *string*, but statting it from the adapter
   would breach both invariants. The legacy check belongs in the sync
   engine, which owns `internal/fsroot` and already walks the
   workspace. This is a **documented divergence** from the master plan
   Unit 10 text and must be reflected back into the master plan in the
   same PR. The cursor adapter's responsibility is limited to
   *documenting* `.cursorrules`'s legacy status in
   `docs/adapters/cursor.md`.

8. **Drop the `paths:` frontmatter ward.** Claude's `emitRule` warns
   when a rule body opens with `paths:` frontmatter (a Claude Code
   activation bug). Cursor's `.mdc` format supports `globs` /
   `description` / `alwaysApply` frontmatter natively with no such bug,
   so the ward is omitted from cursor's `emitRule`. This is a deletion
   relative to the claude shape, not an addition.

9. **`.mdc` rules are emitted frontmatter-less in v1.** The IR strips
   frontmatter at decode (Unit 9, decision #9 — the adapter sees only
   `Required`, `Targets`, `Version` plus the raw body). Cursor's `.mdc`
   activation metadata (`globs`, `alwaysApply`, `description`) is not
   carried through the v1 IR, so the emitted `.mdc` is
   `managedHeader + body` with no synthesized frontmatter. A
   frontmatter-less `.mdc` is valid and behaves as a manual / agent-
   requested rule in Cursor. The managed-file header is the same
   leading HTML-comment banner as claude; because there is no
   frontmatter in v1, a leading comment is safe (it does not displace a
   `---` frontmatter opener off line 1). Carrying real `.mdc`
   frontmatter is the same v1.x IR-frontmatter-exposure change Unit 9
   already flagged.

10. **Summary-only `OpsPerformed`; zero file I/O; no protocol change.**
    Identical to Unit 9: ops are built with content (validated via
    `NewOpWriteFile` / `OpWriteToolOwned`) and only `[]OpRecord`
    summaries survive on the wire. The adapter touches no files and
    extends no `pkg/adapterkit` surface.

## Open Questions

### Resolved During Planning

- **Is `skill` supported?** No — `unsupported` (KTD 6). Cursor has no
  skills concept.
- **Where does `.mcp.json` live for Cursor?** `.cursor/mcp.json`
  (KTD 3), not workspace root.
- **Which file does `agents-md` write?** Workspace-root `AGENTS.md`
  directly (KTD 5), not a `CLAUDE.md`-style companion.
- **Can the adapter detect `.cursorrules`?** No (KTD 7) — deferred to
  the sync engine.
- **Does cursor need the `paths:` ward?** No (KTD 8) — dropped.
- **Where does the `.aienvs-managed` sidecar go?** Next to the JSON
  file it marks: `.cursor/.aienvs-managed` (KTD 3).

### Deferred to Implementation

- **Real values for the managed-file-header `{source-url}@{short-sha}`
  placeholders.** Plumbed via `_meta` once the Unit 13 sync engine
  wires IR-decode context through `EmitParams`. v1 ships placeholders
  (same as Unit 9).
- **Exact wording of the three `unsupported` warning notes.** Drafted
  in `capabilities.yaml` notes; final phrasing tuned during testing to
  match the claude adapter's tone.
- **Whether to verify each Cursor path against live docs.** The
  implementer confirms the `.mdc` / `.cursor/mcp.json` / `AGENTS.md`
  facts at implementation time (web verification was declined during
  planning) and adjusts `docs/adapters/cursor.md` plus the emit paths
  if anything has shifted. Per the 10.3/10.4 execution notes, this
  verification happens *before* the paths are baked into emit
  constants, not in the doc-only 10.7.
- **Whether the Unit 12a `AGENTS.md` merge contract needs per-section
  metadata** (originating-adapter id, ordering hint) that the
  `write_tool_owned` markdown-section op emitted here does not carry.
  If 12a's merge needs it, the op shape frozen in this unit may need
  revision when 12a lands. Surfaced as a cross-unit watch item, not a
  v1 blocker — this unit emits a single well-formed section and the
  conformance corpus can only assert op shape, not cross-adapter merge
  composition (no merge code exists yet to test against).

## Output Structure

```
internal/adapter/bundled/
  cursor/
    bundled.go        # Bundled() *adapter.BundledAdapter; in-process run().
    capabilities.go   # In-code capability matrix; mirrors capabilities.yaml.
    capabilities.yaml # Authoritative per-kind support declaration; go:embed.
    capabilities_test.go
    header.go         # managed-file header + section markers + per-subdir README.
    header_test.go
    emit.go           # IR → ops dispatch; wire shapes; targets filter.
    emit_reserved.go  # rule → .cursor/rules/aienvs/<id>.mdc (no paths: ward).
    emit_tool_owned.go# mcp-server-entry → .cursor/mcp.json; agents-md → AGENTS.md;
                      #   warn-and-skip for skill/command/plugin-reference.
    emit_test.go
    bundled_test.go   # End-to-end via pkg/adapterkit/testing.RunInprocServer.

internal/adapter/bundled/cursor/testdata/
  ir/
    rule-only.json
    mcp-server-entry-only.json
    agents-md-only.json
    mixed-everything.json          # rule + mcp + agents-md + one unsupported kind
    targeted-other.json            # IR with targets:[claude] — adapter must skip
    skill-unsupported.json         # skill node — adapter must warn-and-skip
    command-unsupported.json       # command node — adapter must warn-and-skip
    plugin-reference-unsupported.json
  expected/
    <one .json per ir/ fixture>    # []OpRecord summaries
    content/<fixture>/<path>       # golden file/section content
  conformance_cases.go             # table of conformance.Case fixtures (Unit 10.6)

docs/adapters/
  cursor.md   # Authoritative concept→destination table + path verification + legacy notes.
```

The per-unit `**Files:**` sections below are authoritative for what
each implementation unit creates or modifies. The implementer may
adjust the layout if implementation reveals a better one.

## Implementation Units

### Unit 10.1: Capability declaration + adapter scaffold

**Goal:** Stand up the package shape: `Bundled()` constructor, manifest
metadata, in-code capability matrix with three `unsupported` kinds, and
the `capabilities.yaml` declaration the adapter ships alongside. No
emission logic yet.

**Requirements:** R3, R11, R12.

**Dependencies:** Unit 7 (IR types), Unit 8 (`adapter.BundledAdapter`,
`pkg/adapterkit`). No dependency on Unit 9 code (intentional copy, not
import).

**Files:**
- Create: `internal/adapter/bundled/cursor/bundled.go`
- Create: `internal/adapter/bundled/cursor/capabilities.go`
- Create: `internal/adapter/bundled/cursor/capabilities.yaml`
- Test: `internal/adapter/bundled/cursor/capabilities_test.go`

**Approach:**
- Copy `claude/bundled.go`; swap `adapterName = "cursor"`,
  `reservedPrefix = ".cursor"`, the placeholder `Command` slice name,
  and the package doc comment's destination table.
- Copy `claude/capabilities.go`; change the `conceptKinds` map so
  `skill`, `command`, `plugin-reference` are `capmatrix.Unsupported`
  and `agents-md`, `rule`, `mcp-server-entry` are `capmatrix.Supported`;
  rewrite `declaredOutputs()` per KTD 3 (the four entries:
  `.cursor/rules/aienvs`, `.cursor/mcp.json`, `AGENTS.md`,
  `.cursor/.aienvs-managed`).
- Author `capabilities.yaml` with `name: cursor`, `reserved_prefix:
  .cursor`, and the six-kind `concept_kinds` block matching KTD 4's
  table (three supported, three unsupported, each with a note).
- `Capabilities.WriteToolOwned: true` (emits `write_tool_owned` for
  `.cursor/mcp.json` and `AGENTS.md`). `Progress: false`.

**Patterns to follow:** `internal/adapter/bundled/claude/bundled.go`,
`internal/adapter/bundled/claude/capabilities.go`,
`internal/adapter/bundled/claude/capabilities.yaml` (verbatim shape).

**Test scenarios:**
- Happy path: `Bundled()` returns a non-nil `BundledAdapter` whose
  `Manifest.Name == "cursor"` and `Manifest.ReservedPrefix == ".cursor"`.
- Happy path: in-process initialize via `RunInprocServer` returns an
  `InitializeResult` whose `Capabilities.ConceptKinds` matches the YAML
  kind-for-kind (three `supported`, three `unsupported`), and whose
  `DeclaredOutputs` contains exactly the four KTD-3 entries (compared
  as an order-insensitive set).
- Edge case: `capabilities.yaml` declares a kind not in the code (or
  vice versa) → the parity test fails naming the divergent kind
  (verify with a forked YAML byte buffer in a sub-test).
- Edge case: `WriteToolOwned: true` is reported in the capabilities
  block (otherwise the runtime rejects `write_tool_owned` ops).
- Edge case: every one of the six v1 kinds is present in the
  declaration — no kind silently defaults to `unsupported` by omission.

**Verification:**
- `go test ./internal/adapter/bundled/cursor/...` passes.
- `Bundled()` is the only export the package needs to expose.

### Unit 10.2: Managed-file header + per-subdir README helpers

**Goal:** Provide the formatting helpers cursor's emission needs:
managed-file header for `.mdc`/markdown, JSON sidecar marker, section
markers for the `AGENTS.md` managed section, and the per-subdir README
body. Near-verbatim copy of claude's `header.go` with cursor-specific
strings.

**Requirements:** Master plan decision #23.

**Dependencies:** Unit 10.1.

**Files:**
- Create: `internal/adapter/bundled/cursor/header.go`
- Test: `internal/adapter/bundled/cursor/header_test.go`

**Approach:**
- Copy `claude/header.go`. Keep `markdownHeader`, `jsonSidecarMarker`,
  `sectionMarkerBegin/End`, `wrapManagedSection`, `mustValidID`
  unchanged (the marker scheme is identical across adapters).
- `readmeForSubdir`: swap the literal `claude` references to `cursor`
  and the exit command to `aienvs unmanage cursor`.
- `jsonSidecarBody`: swap to reference `.cursor/mcp.json` and
  `aienvs unmanage cursor`.
- All helpers stay pure (`[]byte` returns, no I/O, no package-level
  mutable state).

**Patterns to follow:** `internal/adapter/bundled/claude/header.go`.

**Test scenarios:**
- Happy path: `markdownHeader()` includes the literal substring
  `"Managed by aienvs"` and ends with `"\n"`.
- Happy path: `sectionMarkerBegin("foo")` returns
  `<!-- aienvs:begin id=foo -->`; `sectionMarkerEnd("foo")` the
  matching end marker.
- Happy path: `wrapManagedSection("foo", []byte("body"))` wraps body
  between begin/end markers with a trailing newline.
- Edge case: `readmeForSubdir(".cursor/rules/aienvs")` mentions the
  subdir path and `aienvs unmanage cursor`.
- Edge case: `jsonSidecarMarker()` references `.cursor/mcp.json`.
- Edge case: `sectionMarkerBegin` panics on an id violating the IR id
  grammar (caller has already validated; an invalid id here is a
  programming error).

**Verification:**
- `go vet ./internal/adapter/bundled/cursor/...` clean.
- Helpers cover every formatting site `emit_reserved.go` /
  `emit_tool_owned.go` need in 10.3–10.4.

### Unit 10.3: Reserved-subdirectory emission (rule → `.mdc`)

**Goal:** Implement IR-to-op mapping for `rule`, the only kind Cursor
reads from a reserved subdirectory. Emission produces one `mkdir` for
the parent + the per-subdir README + one `write_file` per rule node at
`.cursor/rules/aienvs/<id>.mdc` with the managed-file header. **No
`paths:` ward** (KTD 8).

**Requirements:** R3, R11, R12.

**Dependencies:** Unit 10.1, 10.2.

**Files:**
- Modify: `internal/adapter/bundled/cursor/bundled.go` (wire `OnEmit`)
- Create: `internal/adapter/bundled/cursor/emit.go`
- Create: `internal/adapter/bundled/cursor/emit_reserved.go`
- Test: `internal/adapter/bundled/cursor/emit_test.go`
- Create: `internal/adapter/bundled/cursor/testdata/ir/rule-only.json`
- Create: `internal/adapter/bundled/cursor/testdata/expected/rule-only.json`
  (+ golden content under `expected/content/rule-only/`)

**Approach:**
- Copy `claude/emit.go`'s `irNode`/`irProvenance`/`irAsset`/
  `irDocument` wire shapes, `emittedOps`, `emitState`, `handleEmit`,
  `decodeIRDocument`, `rejectDuplicateNodes`, `decodeBodyOrPassthrough`,
  `wrapBodyErr`, `wrapOpErr` verbatim.
- Rename `nodeTargetsClaude` → `nodeTargetsCursor` (compares against
  `adapterName == "cursor"`).
- `dispatchNode` switch routes:
  - `ir.KindRule` → `emitRule`
  - `ir.KindMCPServerEntry` → `emitMCPServerEntry` (Unit 10.4)
  - `ir.KindAgentsMD` → `emitAgentsMD` (Unit 10.4)
  - `ir.KindSkill`, `ir.KindCommand`, `ir.KindPluginReference` →
    `emitUnsupportedWarning` (Unit 10.5)
- `emit_reserved.go`: copy claude's `emitRule` but:
  - subdir constant `rulesSubdir = ".cursor/rules/aienvs"`.
  - emitted file path `rulesSubdir + "/" + node.ID + ".mdc"` (note
    `.mdc`).
  - **delete** the `hasPathsFrontmatter` warning branch and its helpers
    (`hasPathsFrontmatter`, `stripFrontmatterOpener`) — not needed.
  - keep `ensureSubdir` (mkdir + per-emit README dedup) and
    `prependHeader`.
- Path construction: forward-slash string concatenation; no
  `filepath.Join`.

**Execution note:** before baking `.cursor/rules/aienvs` and the
`.mdc` extension into the emit constants (and before writing the
goldens), confirm the rules path + extension against live Cursor docs —
web verification was declined during planning. A stale *supported*-kind
path emits confidently-wrong ops that the declared-outputs gate still
passes (it checks containment, not real-world correctness), producing
dead or misplaced files rather than a no-op. Verify here, not in the
doc-only Unit 10.7.

**Patterns to follow:** `internal/adapter/bundled/claude/emit.go`,
`internal/adapter/bundled/claude/emit_reserved.go` (`emitRule`,
`ensureSubdir`, `prependHeader`).

**Test scenarios:**
- Happy path (`rule-only.json`): one rule node id `no-fri`, body
  `"No PRs on Friday."` →
  `[mkdir(.cursor/rules/aienvs), write_file(.cursor/rules/aienvs/README.md), write_file(.cursor/rules/aienvs/no-fri.mdc)]`.
  The `.mdc` content is `managedHeader + body`; the README body matches
  `readmeForSubdir`.
- Happy path (multi-rule): two rule nodes → one `mkdir`, one README,
  two `.mdc` writes, ordered deterministically (sorted by id).
- Edge case: rule body opens with `---\npaths:\n` frontmatter → emitted
  as a normal `.mdc` write with **no** warning op (confirms the ward
  was dropped; this is the explicit behavioral difference from claude).
- Edge case: rule node id literally `README` → its emitted path would
  collide with the per-subdir `README.md`; `recordWritePath` returns
  `CodeInvalidParams` (inherited from the copied `ensureSubdir`/
  `recordWritePath` guard). Note the path differs (`README.mdc` vs
  `README.md`) so confirm whether the collision actually fires; if the
  extensions differ the paths do not collide and both write — assert
  the actual emitted set either way.
- Edge case: node id violates IR grammar → `CodeInvalidParams` error.
- Integration: the declared-outputs gate (exercised in 10.6's
  `bundled_test.go`) accepts every emitted `.cursor/rules/aienvs/*`
  path.

**Verification:**
- `testdata/expected/rule-only.json` golden comparison passes
  byte-for-byte on op `path` strings (`.mdc` extension included).
- `go test -race ./internal/adapter/bundled/cursor/...` clean.

### Unit 10.4: Tool-owned-file emission (mcp-server-entry, agents-md)

**Goal:** Map the two IR kinds whose output lands inside tool-owned
files — `.cursor/mcp.json` (JSON-pointer locator) and workspace-root
`AGENTS.md` (markdown-section locator).

**Requirements:** R3, R11, R12, master plan decision #25.

**Dependencies:** Unit 10.1, 10.2.

**Files:**
- Create: `internal/adapter/bundled/cursor/emit_tool_owned.go`
- Modify: `internal/adapter/bundled/cursor/emit.go` (dispatch already
  wired in 10.3; this unit fills in the emitters)
- Modify: `internal/adapter/bundled/cursor/emit_test.go`
- Create: `internal/adapter/bundled/cursor/testdata/ir/mcp-server-entry-only.json`
- Create: `internal/adapter/bundled/cursor/testdata/ir/agents-md-only.json`
- Create: `internal/adapter/bundled/cursor/testdata/expected/mcp-server-entry-only.json`
- Create: `internal/adapter/bundled/cursor/testdata/expected/agents-md-only.json`

**Approach:**
- Copy claude's `emit_tool_owned.go`. Change constants:
  - `mcpJSONPath = ".cursor/mcp.json"`
  - `mcpSidecarPath = ".cursor/.aienvs-managed"` (next to the JSON file,
    per KTD 3 — claude used workspace-root `.aienvs-managed`; cursor's
    sidecar lives under `.cursor/`)
  - `agentsMDPath = "AGENTS.md"` (replaces claude's `CLAUDE.md`)
  - keep `mcpJSONPointerBase = "/mcpServers/aienvs_"` and
    `sectionIDPrefix = "aienvs:"` unchanged.
- `emitMCPServerEntry`: identical logic to claude — `decodeBodyOr
  Passthrough`, `json.Valid` + `isJSONObject` validation (refuse to
  corrupt strict JSON), emit `OpWriteToolOwned{Path: ".cursor/mcp.json",
  Kind: json-pointer, Locator: "/mcpServers/aienvs_<id>"}`, plus the
  once-per-emit `.cursor/.aienvs-managed` sidecar `write_file`.
- `emitAgentsMD`: identical logic to claude's — reject bodies
  containing the `<!-- aienvs:` marker opener (anti-injection guard),
  `wrapManagedSection(node.ID, body)`, emit
  `OpWriteToolOwned{Path: "AGENTS.md", Kind: markdown-section,
  Locator: "aienvs:" + node.ID}`. No managed-file header inside the
  section (markers are the ownership advertisement).
- Keep `isJSONObject` and `markerOpenBytes` verbatim.

**Execution note:** confirm `.cursor/mcp.json` (flat file, not a
directory) and workspace-root `AGENTS.md` against live Cursor docs
before baking them into the emit constants and goldens — same
unverified-path risk as 10.3, and these are *supported* kinds whose
wrong paths the declared-outputs gate will pass silently.

**Patterns to follow:** `internal/adapter/bundled/claude/emit_tool_owned.go`
(`emitMCPServerEntry`, `emitAgentsMD`, `isJSONObject`).

**Test scenarios:**
- Happy path (`mcp-server-entry-only.json`): node id `lsp`, body
  `{"command":"node","args":["server.js"]}` →
  `[write_tool_owned(.cursor/mcp.json), write_file(.cursor/.aienvs-managed)]`.
  `OpWriteToolOwned.Locator == "/mcpServers/aienvs_lsp"`.
- Happy path (`agents-md-only.json`): node id `team`, body
  `"## Conventions\n..."` → `[write_tool_owned(AGENTS.md)]`. Locator
  `"aienvs:team"`; content wraps the body in begin/end markers.
- Edge case: `mcp-server-entry` body is not valid JSON →
  `CodeInvalidParams`; no ops emitted for that node; the whole emit
  aborts (v1 one-failed-node-fails-the-emit behavior, inherited).
- Edge case: `mcp-server-entry` body is valid JSON but not an object
  (e.g. `["a"]` or `42`) → `CodeInvalidParams` (would corrupt
  `/mcpServers/<key>`).
- Edge case: `agents-md` body contains `<!-- aienvs:end id=x -->` →
  `CodeInvalidParams` (anti-injection guard prevents `AGENTS.md`
  section corruption).
- Edge case: two `mcp-server-entry` nodes in one emit → two
  `write_tool_owned` ops but only **one** `.cursor/.aienvs-managed`
  sidecar (per-emit dedup via `emitState.sidecarEmitted`).
- Integration: declared-outputs gate accepts `.cursor/mcp.json`,
  `AGENTS.md`, and `.cursor/.aienvs-managed`.

**Verification:**
- Golden fixtures match byte-for-byte on `OpsPerformed`.
- `OpWriteToolOwned.Kind` and `Locator` round-trip through
  `json.Marshal`/`Unmarshal`.

### Unit 10.5: Honest-unsupported handling (skill, command, plugin-reference, off-target)

**Goal:** When the IR contains a kind cursor cannot honestly emit
(`skill`, `command`, `plugin-reference`), surface a `warning` op and
skip emission. When a node is targeted at a different adapter, skip it
silently. Generalize claude's single-kind `emitPluginReferenceWarning`
into a shared `emitUnsupportedWarning` covering all three kinds with
kind-specific notes.

**Requirements:** R11, R12, master plan decision #25 (honest
capability-matrix handling).

**Dependencies:** Unit 10.1, 10.3, 10.4.

**Files:**
- Modify: `internal/adapter/bundled/cursor/emit_tool_owned.go` (or a
  small `emit_unsupported.go`) — the warn-and-skip emitter
- Modify: `internal/adapter/bundled/cursor/emit_test.go`
- Create: `internal/adapter/bundled/cursor/testdata/ir/skill-unsupported.json`
- Create: `internal/adapter/bundled/cursor/testdata/ir/command-unsupported.json`
- Create: `internal/adapter/bundled/cursor/testdata/ir/plugin-reference-unsupported.json`
- Create: `internal/adapter/bundled/cursor/testdata/ir/targeted-other.json`
- Create: `internal/adapter/bundled/cursor/testdata/expected/skill-unsupported.json`
- Create: `internal/adapter/bundled/cursor/testdata/expected/command-unsupported.json`
- Create: `internal/adapter/bundled/cursor/testdata/expected/plugin-reference-unsupported.json`
- Create: `internal/adapter/bundled/cursor/testdata/expected/targeted-other.json`

**Approach:**
- `emitUnsupportedWarning(emitted, node)` emits
  `OpWarning{ConceptID: node.ID, Status: WarningStatusDegraded, Note: <kind-specific>}`
  and no `write_file`. Notes:
  - `skill`: "Cursor has no folder-per-skill concept; skills are not
    installed at the project level."
  - `command`: "Cursor has no project-level custom command concept; the
    command was not installed."
  - `plugin-reference`: "Cursor does not load project-level plugin
    references; the reference was not installed."
- `targets:` filter: `nodeTargetsCursor` (already in `handleEmit` from
  10.3) skips off-target nodes before kind dispatch — no warning, no op.
- Capability-lied avoidance: all three warned kinds are declared
  `unsupported` in `capabilities.yaml`, so warning-only emission does
  not trip the runtime's capability-lied check (which fires only for
  `supported` kinds — `internal/adapter/runtime.go`).

**Patterns to follow:**
`internal/adapter/bundled/claude/emit_tool_owned.go`
(`emitPluginReferenceWarning`) and claude's `targeted-other.json` /
`plugin-reference-warn.json` fixtures.

**Test scenarios:**
- Happy path (`skill-unsupported.json`): one `skill` node id `coder`
  (with assets) → `[warning(coder)]`, zero writes. Assets are ignored.
- Happy path (`command-unsupported.json`): one `command` node id
  `deploy` → `[warning(deploy)]`, zero writes.
- Happy path (`plugin-reference-unsupported.json`): one
  `plugin-reference` node id `linter` → `[warning(linter)]`, zero
  writes.
- Happy path (`targeted-other.json`): one rule node `targets:[claude]`
  → empty `OpsPerformed` (zero ops; filtered before dispatch — no
  warning).
- Edge case: one supported `rule` for cursor + one `skill` with
  `required: true` for cursor → adapter emits the rule's ops + the
  skill warning. Runtime-side `required_unmet` enforcement is **not**
  this unit's scope (the framework handles it from the `unsupported`
  declaration).
- Edge case: a `skill` node targeted `[claude]` → zero ops (targets
  filter wins before kind dispatch, so no warning either).

**Verification:**
- `OpWarning` ops appear in `OpsPerformed` with the right `concept_id`
  (verified via marshalled JSON, since `OpRecord` carries only `op` +
  `path`).
- No `write_file` / `write_tool_owned` accidentally emitted for a
  warned or off-target node.

### Unit 10.6: End-to-end inproc + cursor conformance corpus

**Goal:** Drive the adapter through the real `pkg/adapterkit` client
over `io.Pipe` and assert recorded `OpsPerformed` matches a cursor-
specific conformance corpus — proving the adapter and runtime agree on
the wire shape end-to-end without a subprocess. **Must pass with zero
warnings** for the all-supported fixtures, and with exactly the
expected warnings for the unsupported-kind fixtures.

**Requirements:** R3, R11, R12.

**Dependencies:** 10.1–10.5.

**Files:**
- Create: `internal/adapter/bundled/cursor/bundled_test.go`
- Create: `internal/adapter/bundled/cursor/testdata/ir/mixed-everything.json`
- Create: `internal/adapter/bundled/cursor/testdata/expected/mixed-everything.json`
- Create: `internal/adapter/bundled/cursor/testdata/conformance_cases.go`
  (table of `conformance.Case`-shaped fixtures pointing at
  `testdata/ir` + `testdata/expected`)

**Approach:**
- Use `adapterkit.RunInprocServer` to drive an in-memory client against
  the adapter's SDK server. For each fixture:
  1. `client.Initialize(...)`, assert capability matrix + the four
     declared outputs.
  2. `client.Initialized(...)`.
  3. `client.Emit(target: "cursor", ir: <fixture>)`, assert
     `EmitResult.OpsPerformed` equals the expected slice (deep-equal on
     `[]OpRecord`; warnings compared via the same `op` + `concept_id`
     helper claude's test uses).
  4. `client.Shutdown(...)`.
- `mixed-everything.json` exercises `rule` + `mcp-server-entry` +
  `agents-md` + one unsupported kind (e.g. `skill`) together to surface
  ordering / dedup interactions in one shot. Expected ordering (sorted
  by kind then id, matching `handleEmit`'s sort):
  `[write_tool_owned(AGENTS.md), write_tool_owned(.cursor/mcp.json), write_file(.cursor/.aienvs-managed), mkdir(.cursor/rules/aienvs), write_file(.cursor/rules/aienvs/README.md), write_file(.cursor/rules/aienvs/<rule-id>.mdc), warning(<skill-id>)]`.
  (Confirm the exact order against the kind-sort + per-emitter op order
  during implementation; the fixture is the source of truth.)
- Conformance harness CLI wiring is deferred to Unit 16; this in-proc
  test uses the same `adapterkit.Client` the harness uses internally.

**Test scenarios:**
- Happy path: `mixed-everything.json` produces the expected op sequence
  above; the all-supported subset produces **zero** `warning` ops.
- Happy path: clean shutdown is acknowledged; no goroutine / pending-
  request leak (manual cleanup assertion as in claude's test).
- Error path: `client.Initialize` after `client.Shutdown` returns an
  error (server is stateful, single-lifecycle).
- Integration: every fixture's emitted paths pass the runtime's
  declared-outputs gate (no path escapes the four declared outputs).

**Verification:**
- `go test -race ./internal/adapter/bundled/cursor/...` clean.
- All fixtures pass byte-comparison on `OpsPerformed`.
- The all-supported corpus emits zero warnings (the master plan's
  "passes the shared conformance harness with zero warnings" bar, read
  against the supported fixtures).

### Unit 10.7: `docs/adapters/cursor.md`

**Goal:** Authoritative human-readable reference: per-kind destination
table, capability declarations (including the three `unsupported`
kinds and why), path-verification notes, and the `.cursorrules` legacy
note. Mirrors `docs/adapters/claude.md`.

**Requirements:** Master plan decision #25.

**Dependencies:** 10.1–10.6 (doc reflects shipped behavior).

**Files:**
- Create: `docs/adapters/cursor.md`

**Approach:**
- One-paragraph summary: what the `cursor` adapter owns
  (`.cursor/rules/aienvs/*.mdc`, a `/mcpServers/aienvs_*` section of
  `.cursor/mcp.json`, an aienvs-marked section of workspace-root
  `AGENTS.md`) and what it declines (`skill`, `command`,
  `plugin-reference`).
- Concept→destination table mirroring `capabilities.yaml`, including
  the three `unsupported` rows with the user-facing remediation note
  from 10.5's warning text.
- Path-verification subsection citing current Cursor docs for the
  rules (`.cursor/rules/*.mdc`), MCP (`.cursor/mcp.json`), and
  `AGENTS.md` layout (one-line citation each, links from Context &
  Research). **The implementer verifies each path against live docs at
  implementation time** (web verification was declined during
  planning).
- `.cursorrules` legacy note: explain that `.cursorrules` is the
  superseded single-file form, that aienvs does **not** write or
  migrate it, and that detecting a pre-existing `.cursorrules` and
  warning is handled by the sync engine (KTD 7), not this adapter.
- "Exit is easy" subsection: `aienvs unmanage cursor` removes every
  emitted file using the ledger (master plan Unit 24; named so users
  know the exit path).

**Test scenarios:**
- Test expectation: none — documentation-only unit. The drift detector
  is the `capabilities.yaml`-vs-code parity test in 10.1; the doc is
  reviewed by hand.

**Verification:**
- The concept→destination table matches the in-code capability map
  kind-for-kind (manual diff during code review).
- Every cited link resolves at review time.

## System-Wide Impact

- **Interaction graph:** The bundled `cursor` adapter is wired into the
  runtime via `adapter.DiscoverOptions.Bundled` at the composition root
  (alongside `claude`). Until Unit 16 wires it, it is exercised only by
  this package's tests. No `cmd/` or `internal/cli/` callers change in
  this PR.
- **Error propagation:** Adapter errors flow as `adapterkit.Error`
  values from `OnEmit`; the runtime wraps them with the right
  `error_class`. No new error classes.
- **State lifecycle:** The adapter holds no state between emits;
  `handleEmit` is pure with respect to `EmitParams`. No temp files, no
  side effects, zero file I/O.
- **Shared-file concern (`AGENTS.md`):** This is the first adapter to
  write workspace-root `AGENTS.md`. It emits exactly one aienvs-marked
  section and must not assume sole ownership — `codex` (Unit 11) and
  `pi` (Unit 11.5) will write their own sections to the same file, and
  the per-external-file lock + multi-section merge (Units 12, 12a)
  serialize and combine them. Nothing in this unit coordinates that;
  it only emits a single well-formed `write_tool_owned` markdown-
  section op.
- **API surface parity:** Unit 11 (codex) and Unit 11.5 (pi) will copy
  this package's shape just as it copied claude's. Keep the public
  surface narrow (`Bundled()` only). When codex/pi land, re-evaluate
  the Rule-of-Three extraction of a shared bundled-adapter kit
  (`emitRule`/`ensureSubdir`/`prependHeader`, the wire shapes, the
  `targets` filter, the JSON-object validation) — three copies is the
  trigger, not two.
- **Unchanged invariants:**
  - Summary-only `OpsPerformed` (Unit 8 PR3 spec freeze). Content built,
    discarded at the wire.
  - `internal/fsroot` untouched; the adapter performs zero file I/O.
  - `pkg/adapterkit` public surface unchanged.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Cursor path/extension facts (`.mdc`, `.cursor/mcp.json`, `AGENTS.md`) drift from assumption — web verification was declined during planning | Implementer verifies each path against live Cursor docs in 10.7; both capability downgrades (KTD 6, 7) are fail-safe (a missing feature, never a corrupted file); paths are isolated to constants in `emit_reserved.go` / `emit_tool_owned.go` for a one-line fix |
| `skill: unsupported` is wrong if Cursor ships a skills concept | Declared honestly per the master plan's explicit cross-check escape hatch; flipping to `supported` is a localized v1.x change copying Unit 9's `emitSkill` |
| Master plan Unit 10 says the adapter detects `.cursorrules`; this plan defers it to the sync engine | Documented divergence (KTD 7); master plan Unit 10 updated in the same PR per project `CLAUDE.md`; detection lands in the sync engine where fsroot lives |
| Drift between `capabilities.yaml` and the in-code matrix | 10.1 parity test fails CI on any divergence (same guard as Unit 9.1) |
| Path strings differ across Windows / Unix | Forward-slash string concatenation everywhere; no `filepath.Join` (cross-platform learning) |
| Premature shared-adapter abstraction with only two adapters | Explicit copy-not-extract decision (KTD 1); Rule-of-Three extraction deferred to the third adapter (codex/pi) |
| `.cursor/.aienvs-managed` sidecar path differs from claude's workspace-root sidecar | Declared by exact path in `declaredOutputs()`; covered by a 10.4 integration assertion |

## Documentation / Operational Notes

- `docs/adapters/cursor.md` ships in 10.7 (authoritative).
- Master plan Unit 10 text updated in the same PR to record the two
  divergences (KTD 6 `skill: unsupported`; KTD 7 `.cursorrules`
  detection deferred to the sync engine).
- No CHANGELOG entry yet (changelog is introduced in Unit 21).
- No CLI documentation impact — Unit 16 wires registration when the
  cobra tree lands.
- No new dependencies. Re-uses `pkg/adapterkit`, `internal/adapter`,
  `internal/ir`, `internal/capmatrix`, and `github.com/goccy/go-yaml`
  (already a dependency via the claude adapter).

## Sources & References

- **Origin / parent plan:** [docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md](2026-04-21-001-feat-aienvs-workspace-cli-plan.md) (Unit 10 section, lines 755–786)
- **Sibling plan (template):** [docs/plans/2026-04-27-001-feat-unit-9-claude-adapter-plan.md](2026-04-27-001-feat-unit-9-claude-adapter-plan.md)
- **Reference adapter to copy:** `internal/adapter/bundled/claude/`
- **Spec — protocol:** [docs/spec/adapter-protocol-v1.md](../spec/adapter-protocol-v1.md)
- **Spec — IR:** [docs/spec/ir-v1.md](../spec/ir-v1.md)
- **SDK:** `pkg/adapterkit/`
- **Runtime path-safety gate:** `internal/adapter/runtime.go` (`pathInDeclaredOutputs`)
- **Cross-platform path-handling rule:** `docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`
- **Spec-vs-impl drift learning:** `docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`
- **Cursor rules (`.cursor/rules/*.mdc`):** https://docs.cursor.com/context/rules
- **Cursor MCP (`.cursor/mcp.json`):** https://docs.cursor.com/context/mcp
- **AGENTS.md standard:** https://agents.md

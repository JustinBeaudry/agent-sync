---
title: Unit 9 — Bundled `claude` adapter (IR → Claude Code ops)
type: feat
status: active
date: 2026-04-27
origin: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# Unit 9 — Bundled `claude` adapter

## Overview

Ship the first bundled adapter (`claude`) that consumes IR v1 nodes and
emits the v1 op vocabulary (`write_file`, `write_tool_owned`, `mkdir`,
`delete`, `warning`) targeted at Claude Code's actual on-disk layout.
The adapter runs in-process via `internal/adapter`'s `BundledAdapter`
hook, speaking the same wire protocol as a subprocess adapter but
wrapped by `pkg/adapterkit`'s SDK so the implementation is shaped like
the reference echo adapter at `conformance/echo/`.

The adapter is purely a **wire-protocol producer**: it builds ops with
content (validating shape via `adapterkit.NewOpWriteFile` and
`OpWriteToolOwned`) and returns op-record summaries in
`EmitResult.OpsPerformed`. Actual file writes happen later, in the
sync engine (Units 12, 12a, 13). For Unit 9, "done" means the adapter
emits the right ops with the right content for every supported IR
kind, declares its capability matrix honestly, and passes a
claude-specific conformance corpus.

## Problem Frame

Without a bundled adapter, the v1 protocol is spec-only — there's
nothing to demonstrate IR-to-target mapping for a real tool. Unit 9
proves the framework end-to-end against a tool whose layout is known
to diverge from the "every concept under `.<tool>/aienvs/`" symmetry
the origin doc assumed (see master plan decision #25). It also locks
the ownership-mode pattern that Units 10, 11, and 11.5 will mirror:

- **Reserved-subdirectory mode** for `rule` and `command` (Claude reads
  `.claude/rules/` and `.claude/commands/`; an `aienvs/` subfolder gives
  free namespacing).
- **Name-prefix isolation** for `skill` (Claude requires the SKILL
  folder name to equal the skill name; prefix the folder with
  `aienvs-` to distinguish ownership).
- **Tool-owned-file mode** for `mcp-server-entry` (Claude reads
  `.mcp.json` at workspace root) and the `agents-md` companion overlay
  for Claude (`CLAUDE.md` at workspace root, section-merged).
- **Honest `unsupported`** for IR kinds Claude does not read at the
  project level (`plugin-reference`, project-level `agents-md` qua
  `AGENTS.md`).

## Requirements Trace

- **R3.** Bundled `claude` adapter present in v1.
- **R11.** Adapter respects v1 IR concept set (closed kinds; honest
  capability declarations).
- **R12.** Per-tool ownership model — mix of reserved subdirectories,
  prefix isolation, and tool-owned-file ops.
- **Master plan decision #25** — encoded mappings in
  `docs/adapters/claude.md` and `capabilities.yaml`.
- **Master plan decision #23** — managed-file headers + per-reserved-
  subdir README.

## Scope Boundaries

- **In scope:** the bundled `claude` adapter, its capabilities
  declaration, its emission logic for every supported IR kind, the
  managed-file header / managed-section marker / first-emit-README
  helpers it needs, the claude-specific conformance corpus, and
  `docs/adapters/claude.md`.
- **Out of scope (explicit non-goals):** orphan deletion logic
  (Unit 14), tool-owned-file merging (Unit 12a — adapter only emits
  the `write_tool_owned` op; merge behavior lives in the sync engine),
  ledger persistence (Unit 12), atomic swap (Unit 13), CLI registration
  (Unit 16), capability-report rendering (Unit 15).

### Deferred to Separate Tasks

- **Adapter registration into the CLI.** Unit 16 wires bundled
  adapters into the cobra tree. This unit exposes a `Bundled()`
  constructor; nothing imports it yet.
- **Conformance against actual `aienvs adapter conformance-test`
  CLI.** Unit 8 PR3 ships the harness; Unit 16 ships the CLI command.
  Unit 9 exercises the adapter against the harness via a Go test, not
  via the CLI.
- **Real-world write verification.** Until Units 12a + 13 land, the
  adapter's ops are validated by inspecting the marshalled
  `OpWriteFile` / `OpWriteToolOwned` payload (content + locator),
  not by inspecting files on disk after a sync.

## Context & Research

### Relevant Code and Patterns

- **`conformance/echo/main.go`** — reference SDK-based adapter; same
  shape as the bundled `claude` adapter, only difference is that the
  bundled version wires its `Run` into `BundledAdapter.Run` instead
  of `main()`.
- **`pkg/adapterkit/server.go`** — `NewServer`, `OnInitialize`,
  `OnEmit`, `OnShutdown`, `Run`. The bundled adapter constructs a
  server with caller-supplied stdin/stdout pipes and registers the
  three handlers.
- **`pkg/adapterkit/types.go`** — `OpWriteFile`, `OpWriteToolOwned`
  (with `ToolOwnedKind` ∈ {`json-pointer`, `toml-path`,
  `markdown-section`}), `OpMkdir`, `OpDelete`, `OpWarning`,
  `DeclaredOutput`, `Capabilities`, `OutputMode` ∈ {`owned-subdir`,
  `tool-owned-entry`}.
- **`pkg/adapterkit/testing.go`** — `RunInprocServer` + `Client` for
  driving the adapter in-process from tests without a subprocess.
- **`internal/adapter/discover.go`** — `BundledAdapter{Manifest, Run}`
  shape; `Run func(ctx, stdin io.Reader, stdout io.Writer) error`.
- **`internal/adapter/conformance/`** — harness, corpus loader,
  `Case`/`Expected` types. Corpus fixtures are JSON files under
  `corpus/` declaring IR + expected ops; the harness drives an
  adapter binary or process and asserts the recorded `ops_performed`
  matches.
- **`internal/ir/types.go`** — `Node`, `Kind` constants
  (`KindAgentsMD`, `KindRule`, `KindSkill`, `KindCommand`,
  `KindPluginReference`, `KindMCPServerEntry`), `Skill` (Node +
  Assets), `Provenance`.
- **`internal/capmatrix/types.go`** — `CapabilityStatus` ∈
  {`Supported`, `Partial`, `Unsupported`}.

### Institutional Learnings

- **`docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`**
  Rule 3: wire-protocol paths are forward-slash regardless of host OS;
  do not call `filepath.Join` when building op paths. Use string
  concatenation with `/` (mirrors the reference echo adapter).
- **`docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`**
  Update `docs/adapters/claude.md` and `capabilities.yaml` in the same
  PR as `emit.go` — split-PR drift is a known repeat-cost.

### External References

- [Claude Code skills directory structure](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills) —
  the skill-folder-name must equal skill-name constraint that drives
  the `aienvs-<id>/` naming.
- [Claude Code project-local `.mcp.json`](https://docs.claude.com/en/docs/claude-code/mcp) —
  workspace-root MCP convention (not `.claude/mcp.json`).
- [Claude Code rules / `paths:` frontmatter known issue (2026-04)](https://docs.claude.com/en/docs/claude-code/rules) —
  motivates the rule-frontmatter ward warning.

## Key Technical Decisions

1. **Adapter implementation pattern: SDK-backed `BundledAdapter`.**
   The package exposes a `Bundled() *adapter.BundledAdapter`
   constructor that builds an `adapterkit.Server` over the supplied
   pipes and runs it. Mirrors the structure of `conformance/echo/main.go`
   so future adapters (`cursor`, `codex`, `pi`) copy a known shape.

2. **Capabilities declared in code, mirrored in `capabilities.yaml`.**
   The per-kind support table ships in `capabilities.yaml` (the
   authoritative human-readable declaration) and is built in code via
   `adapterkit.NewCapabilities()` for the wire response. Both must
   agree; a unit test reads the YAML and asserts it matches the
   in-code map. Single source of truth lives in `capabilities.yaml`
   for documentation purposes; code is the runtime authority.

3. **`reserved_prefix` is `.claude`.** The shared parent for the
   adapter's owned subdirectories. `declared_outputs` enumerates the
   actual subpaths the adapter writes to (`.claude/rules/aienvs`,
   `.claude/commands/aienvs`, `.claude/skills`, plus tool-owned-entry
   declarations for `.mcp.json` and `CLAUDE.md`). The
   `reserved_prefix` field is for ownership-conflict detection across
   adapters (Unit 8's nested-prefix check); `declared_outputs` is the
   path-safety gate.

4. **`.mcp.json` and `CLAUDE.md` are declared at workspace root, not
   under `.claude`.** Both are tool-owned files Claude Code reads from
   the workspace root, not the `.claude/` subtree.
   `declared_outputs[*]` includes:
   - `{path: ".mcp.json", mode: "tool-owned-entry", json_pointer: "/mcpServers"}`
   - `{path: "CLAUDE.md", mode: "tool-owned-entry", section_id: "aienvs"}`
   These do **not** trigger the cross-adapter nested-prefix check
   because they're not under any `reserved_prefix` (which is `.claude`).

5. **Skills declared at `.claude/skills` (owned-subdir).** The adapter
   only emits writes under `.claude/skills/aienvs-<id>/`, but the
   declared output is the parent `.claude/skills` so the path-safety
   gate accepts the writes. Orphan-detection prefix scoping
   (`aienvs-*` only) is the sync engine's responsibility (Unit 14),
   not declared_outputs'.

6. **Companion `agents-md` overlay for `claude` lives in this
   adapter, not a separate side-adapter.** The master plan unit text
   names this as in-scope: when an IR `agents-md` node targets
   `claude`, emit a `write_tool_owned` op with `kind: "markdown-section"`
   into `CLAUDE.md`. Locator format: `aienvs:<node-id>` (mirrors the
   `<!-- aienvs:begin id=<id> --> ... <!-- aienvs:end id=<id> -->`
   marker scheme).

7. **Managed-file header is shared across reserved-subdir emissions.**
   Implemented as a small helper (`headerForMarkdown`,
   `headerForJSON`) inside `internal/adapter/bundled/claude/header.go`.
   Other adapters (Units 10, 11, 11.5) will need the same — extract to
   a shared package only after the third adapter (Rule of Three).
   Format per master plan decision #23:
   `# Managed by aienvs — do not edit. Source: <url>@<short-sha>. Regenerate: aienvs sync`.
   For v1, `<url>@<short-sha>` is rendered as a placeholder
   (`{source-url}@{short-sha}`) — wiring real values into the emission
   context is part of Unit 13 (sync engine supplies them via
   `EmitParams._meta`). The header is **emitted with the placeholder
   in v1**; a follow-up replaces it with real values once `_meta`
   plumbing exists.

8. **Per-reserved-subdir README is emitted on first emit.** The
   adapter does not know whether this is "first emit" — that's
   ledger state. v1 behavior: emit the README every time as a
   `write_file` op. The sync engine treats the README as part of the
   ledger; if it already exists with the same content the write is a
   no-op at the disk level. Cost: one extra op per reserved subdir
   per emit. Acceptable.

9. **`paths:` frontmatter ward.** The IR doesn't expose `paths:` —
   it strips frontmatter at decode (`internal/ir/kinds.go`'s
   `extractMarkdownFrontmatter`). The adapter sees only `Required`,
   `Targets`, `Version` from the recognized field set. The ward
   therefore fires only when `extra` (x-prefixed) frontmatter contains
   an `x-paths` key OR when the body itself starts with a markdown
   frontmatter block (re-emitted by the user). v1 implementation:
   detect `^---\npaths:` at the start of the rule body and emit a
   `warning` op alongside the `write_file`. This is best-effort; full
   handling depends on the IR exposing original frontmatter, which is
   a v1.x change.

## Open Questions

### Resolved During Planning

- **Where does the adapter run?** In-process, via
  `BundledAdapter.Run`. Wired by the SDK's `adapterkit.Server` over
  the runtime-supplied pipes.
- **How do we declare multiple disjoint output paths?**
  `DeclaredOutputs` is a list — one entry per subpath. The shared
  parent (`reserved_prefix`) is metadata for cross-adapter conflict
  detection only.
- **How do we handle skills with assets?** Each asset becomes a
  separate `write_file` op under the same `aienvs-<id>/` folder,
  preserving the relative path from `Skill.Assets[*].RelPath`.
- **Should we emit `mkdir` ops?** Yes — the reference echo adapter
  emits `mkdir` for the parent of every `write_file`. Mirror that;
  the sync engine treats `mkdir` as idempotent.

### Deferred to Implementation

- **Real values for the managed-file header `<source-url>` and
  `<short-sha>`.** Plumbed via `_meta` once Unit 13 sync engine wires
  the IR-decode context through `EmitParams`. v1 ships placeholders.
- **Whether to deduplicate the per-subdir `mkdir` op when multiple
  nodes share a parent.** Echo emits one `mkdir` per `emit` call up
  front; we'll mirror that. Deduplication of repeated `mkdir`s for
  the same path within one emit is a v1.x cleanup if it surfaces as
  a noise issue in conformance output.
- **Exact wording of warnings for `unsupported` kinds.** Drafted in
  `capabilities.yaml` notes; final phrasing tuned during testing.

## Output Structure

```
internal/adapter/bundled/
  claude/
    bundled.go        # Bundled() *adapter.BundledAdapter constructor; ties Manifest + Run.
    capabilities.go   # In-code capability matrix; mirrors capabilities.yaml.
    capabilities.yaml # Authoritative per-kind support declaration; embedded via go:embed.
    capabilities_test.go
    header.go         # managed-file headers + section markers + per-subdir README body.
    header_test.go
    emit.go           # IR → ops mapping.
    emit_test.go
    bundled_test.go   # End-to-end via pkg/adapterkit/testing.RunInprocServer.

internal/adapter/bundled/claude/testdata/
  ir/
    rule-only.json
    rule-with-paths-frontmatter.md  # body with leading `paths:` frontmatter for the ward
    command-only.json
    skill-with-assets.json
    mcp-server-entry-only.json
    agents-md-companion.json
    mixed-everything.json
    targeted-other.json             # IR with targets:[cursor] — adapter must skip
    plugin-reference-warn.json      # plugin-reference node — adapter must warn-and-skip
    rule-required-but-unsupported.json  # not applicable for claude (rule is supported);
                                        # use plugin-reference required:true instead
  expected/
    <one .json per ir/ fixture>     # contract.OpRecord array (summaries)
    <plus golden content under content/<fixture>/<path>>

docs/adapters/
  claude.md   # Authoritative concept→destination table + path verification + warnings.
```

The per-unit `**Files:**` sections below are authoritative for what
each implementation unit creates or modifies. The implementer may
adjust the layout if implementation reveals a better one.

## Implementation Units

- [x] **Unit 9.1: Capability declaration + adapter scaffold**

**Goal:** Stand up the package shape: `Bundled()` constructor, manifest
metadata, in-code capability matrix, and the `capabilities.yaml`
declaration the adapter ships alongside. No emission logic yet.

**Requirements:** R3, R11, R12

**Dependencies:** Unit 7 (IR types), Unit 8 (`adapter.BundledAdapter`,
`pkg/adapterkit`).

**Files:**
- Create: `internal/adapter/bundled/claude/bundled.go`
- Create: `internal/adapter/bundled/claude/capabilities.go`
- Create: `internal/adapter/bundled/claude/capabilities.yaml`
- Test: `internal/adapter/bundled/claude/capabilities_test.go`

**Approach:**
- `Bundled() *adapter.BundledAdapter` returns a fully-formed
  `BundledAdapter{Manifest: claudeManifest, Run: runFn}`. Manifest
  fields: `Name: "claude"`, `Version: "0.1"`, `ContractVersion:
  ContractVersionV1`, `Command: nil` (bundled adapters don't spawn),
  `ReservedPrefix: ".claude"`.
- `runFn` constructs an `adapterkit.NewServer` with the supplied
  stdin/stdout pipes, registers `OnInitialize`, `OnEmit`,
  `OnShutdown`, then calls `server.Run(ctx)`. Initialize handler
  builds the `InitializeResult` from the in-code capability table.
- `capabilities.yaml` is embedded via `//go:embed`. Unit test parses
  the YAML and asserts the in-code declaration matches it kind-for-
  kind. This is the single source of drift detection for the kind
  set — if a kind is added to one and not the other, the test fails.
- Capability table for `claude`:
  | Kind | Status | Notes |
  |---|---|---|
  | `agents-md` | `supported` | Companion overlay into `CLAUDE.md` (workspace root). |
  | `rule` | `supported` | `.claude/rules/aienvs/<id>.md`. |
  | `skill` | `supported` | `.claude/skills/aienvs-<id>/SKILL.md` + assets. |
  | `command` | `supported` | `.claude/commands/aienvs/<id>.md`. |
  | `mcp-server-entry` | `supported` | `.mcp.json` workspace-root, json-pointer locator. |
  | `plugin-reference` | `unsupported` | Claude Code does not read project-level plugin registries. |
- `DeclaredOutputs` returned from `OnInitialize`:
  1. `{path: ".claude/rules/aienvs", mode: "owned-subdir"}`
  2. `{path: ".claude/commands/aienvs", mode: "owned-subdir"}`
  3. `{path: ".claude/skills", mode: "owned-subdir"}`
  4. `{path: ".mcp.json", mode: "tool-owned-entry", json_pointer: "/mcpServers"}`
  5. `{path: "CLAUDE.md", mode: "tool-owned-entry", section_id: "aienvs"}`
- `Capabilities.WriteToolOwned: true`. `Progress: false` (Unit 8b).

**Patterns to follow:** `conformance/echo/main.go` (SDK shape),
`pkg/adapterkit/example_test.go` (server construction).

**Test scenarios:**
- Happy path: `Bundled()` returns a non-nil `BundledAdapter` whose
  `Manifest.Name` is `"claude"` and `Manifest.ReservedPrefix` is
  `".claude"`.
- Happy path: in-process initialize via `RunInprocServer` returns an
  `InitializeResult` whose `Capabilities.ConceptKinds` matches the
  YAML, and whose `DeclaredOutputs` contains exactly the five entries
  above (compared as a set; order-insensitive).
- Edge case: `capabilities.yaml` declares a kind not in the code
  (or vice versa) → drift test fails with a message naming the
  divergent kind. Verify by adding a temporary mismatch in a sub-
  test using a forked YAML byte buffer.
- Edge case: `WriteToolOwned: true` is reported in the capabilities
  block (otherwise the runtime rejects `write_tool_owned` ops).

**Verification:**
- `go test ./internal/adapter/bundled/claude/...` passes.
- The adapter declares all six v1 kinds (no missing kind silently
  defaults to `unsupported`).
- `Bundled()` is the only export from the package needed by callers;
  internal helpers stay unexported.

- [x] **Unit 9.2: Managed-file header + per-subdir README helpers**

**Goal:** Implement the small set of formatting helpers every
emission needs: managed-file header for markdown, JSON sidecar marker,
section markers for tool-owned markdown, and the per-subdir README body.

**Requirements:** Master plan decision #23.

**Dependencies:** Unit 9.1.

**Files:**
- Create: `internal/adapter/bundled/claude/header.go`
- Test: `internal/adapter/bundled/claude/header_test.go`

**Approach:**
- `markdownHeader() []byte` returns the canonical managed-file header
  with placeholder source/SHA values (`{source-url}@{short-sha}`).
- `jsonSidecarMarker() []byte` returns the body for an
  `.aienvs-managed` file written next to a strict-JSON tool-owned
  file. Used per master plan decision #23 for `.mcp.json`.
- `sectionMarkerBegin(id string) []byte` /
  `sectionMarkerEnd(id string) []byte` return
  `<!-- aienvs:begin id=<id> -->` / `<!-- aienvs:end id=<id> -->`.
- `readmeForSubdir(subdir string) []byte` returns the README body
  emitted on every sync into reserved subdirs; explains ownership and
  the `aienvs unmanage <target>` exit path.
- All helpers are pure functions returning `[]byte`. No I/O. No
  package-level mutable state.

**Test scenarios:**
- Happy path: `markdownHeader()` includes the literal substring
  `"Managed by aienvs"` and ends with `"\n"`.
- Happy path: `sectionMarkerBegin("foo")` returns
  `<!-- aienvs:begin id=foo -->` (no trailing newline; caller wraps).
- Edge case: `readmeForSubdir("rules/aienvs")` mentions the subdir
  path and the unmanage command in the body.
- Edge case: `sectionMarkerBegin` rejects ids that violate the IR
  id grammar (`[a-z0-9][a-z0-9-_]{0,63}`) — fail-fast, return an
  error or panic to surface adapter bugs early. (Pick `panic` since
  the caller has already validated the id; an invalid id here is a
  programming error, not a data error.)

**Verification:**
- `go vet ./internal/adapter/bundled/claude/...` clean.
- Helpers cover every formatting site `emit.go` needs in 9.3+.

- [x] **Unit 9.3: Reserved-subdirectory emission (rule, command,
  skill)**

**Goal:** Implement IR-to-op mapping for the three kinds Claude reads
from reserved subdirectories. Each emission produces one `mkdir` per
parent + one `write_file` per node + the per-subdir README +
managed-file header. Skill emission additionally walks
`Skill.Assets[*]`.

**Requirements:** R3, R11, R12

**Dependencies:** Unit 9.1, 9.2.

**Files:**
- Modify: `internal/adapter/bundled/claude/bundled.go` (wire `OnEmit`)
- Create: `internal/adapter/bundled/claude/emit.go`
- Create: `internal/adapter/bundled/claude/emit_reserved.go`
- Test: `internal/adapter/bundled/claude/emit_test.go`
- Create: `internal/adapter/bundled/claude/testdata/ir/rule-only.json`
- Create: `internal/adapter/bundled/claude/testdata/ir/command-only.json`
- Create: `internal/adapter/bundled/claude/testdata/ir/skill-with-assets.json`
- Create: `internal/adapter/bundled/claude/testdata/expected/rule-only.json`
- Create: `internal/adapter/bundled/claude/testdata/expected/command-only.json`
- Create: `internal/adapter/bundled/claude/testdata/expected/skill-with-assets.json`

**Approach:**
- The adapter package defines a small `irDocument` shape
  (`{nodes: []irNode}` plus skill-asset extension) for unmarshaling
  `EmitParams.IR`. It does not import `internal/ir` at runtime — the
  IR comes over the wire as JSON. (Importing the package is fine for
  type aliases and id grammar reuse; just not for in-process method
  calls.)
- For each node, dispatch on `Kind`:
  - `rule` → emit_reserved.emitRule(node) → `mkdir(.claude/rules/aienvs)`
    + `write_file(.claude/rules/aienvs/<id>.md, headerForMarkdown() + body)`
  - `command` → emit_reserved.emitCommand(node) → analogous under
    `.claude/commands/aienvs`.
  - `skill` → emit_reserved.emitSkill(node) → `mkdir(.claude/skills/aienvs-<id>)`
    + `write_file(.claude/skills/aienvs-<id>/SKILL.md, ...)` + one
    `write_file` per `Skill.Assets[*].RelPath` under the same folder.
- Each reserved subdir gets exactly one extra `write_file` for its
  README on every emit (no per-node deduplication; the sync engine is
  the dedup authority).
- Path construction: forward-slash string concatenation (per learning
  on cross-platform path handling). No `filepath.Join`.
- `OpsPerformed` returns `OpRecord` summaries built from the
  marshalled ops. The full op (with content) is constructed via
  `NewOpWriteFile(...)` and round-tripped through `json.Marshal` for
  shape validation, then discarded — only the summary survives in the
  v1 wire result.

**Patterns to follow:** `conformance/echo/main.go`'s `handleEmit`
(loop shape + summary recording).

**Test scenarios:**
- Happy path (`rule-only.json`): one rule node id `no-fri`, body
  `"No PRs on Friday."` →
  `[mkdir(.claude/rules/aienvs), write_file(.claude/rules/aienvs/README.md), write_file(.claude/rules/aienvs/no-fri.md)]`.
  README body matches `readmeForSubdir`. Rule body has the
  managed-file header prepended.
- Happy path (`command-only.json`): same structure under
  `.claude/commands/aienvs`.
- Happy path (`skill-with-assets.json`): skill `coder` with one asset
  `templates/foo.txt` →
  `[mkdir(.claude/skills/aienvs-coder), write_file(.claude/skills/aienvs-coder/SKILL.md), write_file(.claude/skills/aienvs-coder/templates/foo.txt)]`.
  Note: `aienvs-coder` skill folder gets no `README.md` (the
  per-subdir README is only for the parent reserved directory; each
  skill folder is its own scope and a README inside it would clash
  with skill-discovery semantics).
- Happy path (multi-node): IR with one rule + one command + one skill
  emits the union of the above with the right ordering (alphabetical
  by output path, deterministic).
- Edge case: rule body starts with `---\npaths:\n` →
  `write_file` emitted as normal **plus** `warning` op with
  `concept_id: <node-id>, status: degraded, note: "rule has known
  paths: frontmatter — Claude Code activation behavior is inconsistent"`.
- Edge case: skill with zero assets → only `SKILL.md` emitted, no
  asset writes.
- Edge case: node id violates IR grammar → adapter returns
  `adapterkit.Error{Code: CodeInvalidParams, Message: ...}` (mirrors
  the echo reference).
- Integration: declared-outputs gate (runtime side, in
  `bundled_test.go`) accepts every emitted path — the adapter never
  emits a path outside its declared outputs.

**Verification:**
- All `testdata/expected/*.json` golden comparisons pass.
- `go test -race ./internal/adapter/bundled/claude/...` clean.
- Op `path` strings match the exact strings in the expected fixtures
  byte-for-byte.

- [x] **Unit 9.4: Tool-owned-file emission (mcp-server-entry,
  agents-md companion)**

**Goal:** Map the two IR kinds whose output lands inside files the
tool itself owns — `.mcp.json` (JSON pointer locator) and `CLAUDE.md`
(markdown-section locator).

**Requirements:** R3, R11, R12, master plan decision #25.

**Dependencies:** Unit 9.1, 9.2.

**Files:**
- Create: `internal/adapter/bundled/claude/emit_tool_owned.go`
- Modify: `internal/adapter/bundled/claude/emit.go` (dispatch the two
  kinds)
- Modify: `internal/adapter/bundled/claude/emit_test.go`
- Create: `internal/adapter/bundled/claude/testdata/ir/mcp-server-entry-only.json`
- Create: `internal/adapter/bundled/claude/testdata/ir/agents-md-companion.json`
- Create: `internal/adapter/bundled/claude/testdata/expected/mcp-server-entry-only.json`
- Create: `internal/adapter/bundled/claude/testdata/expected/agents-md-companion.json`

**Approach:**
- `mcp-server-entry`:
  - Build `OpWriteToolOwned{Path: ".mcp.json", Kind:
    ToolOwnedKindJSONPointer, Locator: "/mcpServers/aienvs_<id>",
    Content: <node body bytes>}`.
  - Sidecar marker: also emit `OpWriteFile{Path: ".aienvs-managed",
    Mode: 0o644, Content: jsonSidecarMarker()}` to advertise
    aienvs-managed status next to the strict-JSON file. The sidecar
    is at workspace root (next to `.mcp.json`).
  - **Validate node body parses as JSON** before emitting; on parse
    failure return `adapterkit.Error{CodeInvalidParams, ...}` —
    `.mcp.json` is strict JSON; emitting an invalid entry would
    corrupt the merged file.
- `agents-md` companion:
  - Construct content as
    `sectionMarkerBegin(node.ID) + "\n" + node.Body + "\n" + sectionMarkerEnd(node.ID) + "\n"`.
  - Build `OpWriteToolOwned{Path: "CLAUDE.md", Kind:
    ToolOwnedKindMarkdownSection, Locator: "aienvs:" + node.ID,
    Content: <wrapped body>}`.
  - No header on the CLAUDE.md content — the section markers serve as
    the equivalent ownership advertisement, and a managed-file header
    inside a user-owned markdown file would be visually noisy.

**Test scenarios:**
- Happy path (`mcp-server-entry-only.json`): one node id `lsp` with
  body `{"command": "node", "args": ["server.js"]}` →
  `[write_tool_owned(.mcp.json), write_file(.aienvs-managed)]`.
  `OpWriteToolOwned.Locator` is `"/mcpServers/aienvs_lsp"`.
- Happy path (`agents-md-companion.json`): one `agents-md` node id
  `claude` with body `"## Build commands\n..."` →
  `[write_tool_owned(CLAUDE.md)]`. Locator is `"aienvs:claude"`.
  Content includes both begin and end markers.
- Edge case: `mcp-server-entry` body is not valid JSON →
  `CodeInvalidParams` error returned; no ops emitted for this node;
  other nodes in the same IR still emit normally. Specifically: the
  emit handler returns the error immediately, aborting the whole
  emit call (mirror echo's invalid-id behavior). Deferred until
  Unit 8b: per-node skip-with-warning. v1: any single node failure
  fails the whole emit.
- Edge case: `agents-md` node id contains characters disallowed in
  the section marker (e.g., `>`) → the id grammar already restricts
  this set, so this is unreachable in well-formed IR; defend with a
  panic in `sectionMarkerBegin` (per 9.2's edge case).
- Integration: declared-outputs gate accepts `.mcp.json` and
  `CLAUDE.md` via the `tool-owned-entry` declared outputs.

**Verification:**
- Golden fixtures match byte-for-byte.
- `OpWriteToolOwned.Kind` and `Locator` round-trip correctly through
  `json.Marshal`/`Unmarshal` (validated by an `adapterkit` types
  test, but checked here too via the marshalled wire output).

- [x] **Unit 9.5: Honest-unsupported handling
  (`plugin-reference`, off-target nodes)**

**Goal:** When the IR contains nodes the adapter cannot honestly
emit, surface the right `warning` ops and skip emission. Required
nodes mapped to `unsupported` are the runtime's responsibility
(capability-lied / required_unmet checks); the adapter just declares
the support level honestly and emits a `warning` for non-required
nodes.

**Requirements:** R11, R12, master plan decision #25 (honest
capability-matrix handling).

**Dependencies:** Unit 9.1, 9.3, 9.4.

**Files:**
- Modify: `internal/adapter/bundled/claude/emit.go` (warn-and-skip
  branch)
- Modify: `internal/adapter/bundled/claude/emit_test.go`
- Create: `internal/adapter/bundled/claude/testdata/ir/plugin-reference-warn.json`
- Create: `internal/adapter/bundled/claude/testdata/ir/targeted-other.json`
- Create: `internal/adapter/bundled/claude/testdata/expected/plugin-reference-warn.json`
- Create: `internal/adapter/bundled/claude/testdata/expected/targeted-other.json`

**Approach:**
- `plugin-reference` node → emit `OpWarning{ConceptID: <node-id>,
  Status: WarningStatusDegraded, Note: "Claude Code does not load
  project-level plugin references; install plugins via Claude Code's
  own plugin command."}`. No `write_file` emitted.
- `targets:` filter: if `node.Targets` is non-empty and does not
  include `"claude"`, skip the node entirely (no warning, no op). The
  adapter is not the target — silence is correct.
- Capability-lied avoidance: the adapter declares
  `plugin-reference: unsupported`, so emitting a `warning` (no
  non-warning ops) for a plugin-reference-only IR will not trigger
  the runtime's capability-lied check (which only fires on
  `supported` kinds, per `internal/adapter/runtime.go:197`).

**Test scenarios:**
- Happy path (`plugin-reference-warn.json`): IR with one
  `plugin-reference` node id `linter` → `[warning(linter)]`. No
  writes.
- Happy path (`targeted-other.json`): IR with one rule node
  `targets: [cursor]` → empty `OpsPerformed` (zero ops). Runtime's
  capability-lied check is not triggered because the rule node was
  filtered out before kind dispatch (no `supported` kind made it past
  the targets filter).
- Edge case: IR with one rule for `claude` + one plugin-reference
  with `required: true` for `claude` → adapter emits the rule's ops
  + the warning. Runtime-side `required_unmet` enforcement is **not
  in this unit's scope** — that's the framework's job after seeing
  the unsupported declaration in the capability matrix.
- Edge case: IR with one plugin-reference targeted at `[cursor]` →
  zero ops (targets filter wins before kind dispatch).

**Verification:**
- `OpWarning` ops are reported in `OpsPerformed` with the right
  `concept_id` (verified through marshalled JSON, since `OpRecord`
  itself only carries `op` + `path`).
- No `write_file`/`write_tool_owned` op accidentally emitted for a
  warned/skipped node.

- [x] **Unit 9.6: End-to-end inproc + claude-conformance corpus**

**Goal:** Drive the adapter through the actual `pkg/adapterkit`
client over io.Pipe and assert the recorded `OpsPerformed` matches a
claude-specific conformance corpus. This proves the adapter and the
runtime agree on the wire shape end-to-end without going through a
subprocess.

**Requirements:** R3, R11, R12.

**Dependencies:** 9.1–9.5.

**Files:**
- Create: `internal/adapter/bundled/claude/bundled_test.go`
- Create: `internal/adapter/bundled/claude/testdata/ir/mixed-everything.json`
- Create: `internal/adapter/bundled/claude/testdata/expected/mixed-everything.json`
- Create: `internal/adapter/bundled/claude/testdata/conformance_cases.go`
  (or `corpus.go`) — table of `conformance.Case`-shaped fixtures
  pointing at the `testdata/ir` + `testdata/expected` files.

**Approach:**
- Use `adapterkit.RunInprocServer` to drive an in-memory client
  against the adapter's SDK server (the same `Server` the bundled
  `Run` callback would create).
- For each fixture in the table:
  1. `client.Initialize(...)`, assert capability + declared_outputs.
  2. `client.Initialized(...)`.
  3. `client.Emit(target: "claude", ir: <fixture body>)`, assert
     `EmitResult.OpsPerformed` equals the expected slice (deep-equal
     on `[]OpRecord` only; warnings carry no path so the comparison
     uses `op` + `concept_id` for warnings via a small helper).
  4. `client.Shutdown(...)`.
- Comparison helper canonicalizes `OpRecord` ordering (the conformance
  harness's existing comparison treats ordering as stable; mirror
  that).
- The `mixed-everything.json` fixture exercises every kind together
  to surface ordering / dedup interactions one-shot.
- **Conformance harness wiring is deferred to Unit 16** (the CLI
  command). Until then this end-to-end test is the closest the
  adapter gets to running through the actual harness; it uses the
  same `adapterkit.Client` shape the harness uses internally.

**Test scenarios:**
- Happy path: `mixed-everything.json` →
  `[mkdir(.claude/rules/aienvs), write_file(.claude/rules/aienvs/README.md), write_file(.claude/rules/aienvs/<rule-id>.md), mkdir(.claude/commands/aienvs), write_file(.claude/commands/aienvs/README.md), write_file(.claude/commands/aienvs/<cmd-id>.md), mkdir(.claude/skills/aienvs-<skill-id>), write_file(.claude/skills/aienvs-<skill-id>/SKILL.md), write_tool_owned(.mcp.json), write_file(.aienvs-managed), write_tool_owned(CLAUDE.md), warning(<plugin-id>)]`.
- Happy path: shutdown is acknowledged; no goroutine leak (verified
  via `goleak`-style check or manual cleanup assertion).
- Error path: `client.Emit` against a target other than `"claude"`
  (e.g., `"cursor"`) — expected behavior is **emit succeeds** because
  the adapter doesn't validate the target string in v1; the framework
  uses the target name only for routing. Document this in the test
  with a comment so a future reader doesn't expect rejection.
- Integration: re-init after shutdown is forbidden (server is
  stateful) — verify `client.Initialize` after `Shutdown` returns
  an error.

**Verification:**
- `go test -race ./internal/adapter/bundled/claude/...` clean.
- All fixtures pass byte-comparison on `OpsPerformed`.
- The adapter survives a clean shutdown and reports no pending
  request leaks.

- [x] **Unit 9.7: `docs/adapters/claude.md`**

**Goal:** Authoritative human-readable reference: per-kind destination
table, capability declarations, deprecation/bug notes, and the
adapter's exit path.

**Requirements:** Master plan decision #25 (authoritative concept→
destination mapping per adapter).

**Dependencies:** 9.1–9.6 (so the doc reflects shipped behavior, not
intended).

**Files:**
- Create: `docs/adapters/claude.md`

**Approach:**
- Open with a one-paragraph summary: what `claude` adapter is, what
  it owns, what it declines.
- Concept→destination table mirroring `capabilities.yaml`. Include
  the `unsupported` rows with the user-facing remediation note from
  9.5's warning text.
- Path-verification subsection citing the upstream Claude Code docs
  for the rules / commands / skills / MCP layout (links from
  Context & Research above; one-line citation each).
- Deprecation / bug notes:
  - `paths:` frontmatter ward (links the upstream issue).
  - Project-level `AGENTS.md` (qua `agents-md` plain) is `unsupported`
    by Claude Code at the project level — users should rely on the
    `CLAUDE.md` companion overlay instead. Note that targeting
    `agents-md` at `claude` does NOT write to `AGENTS.md`; it writes
    to `CLAUDE.md`. This is intentional asymmetry per master plan.
- "Exit is easy" subsection: `aienvs unmanage claude` removes every
  emitted file using the ledger (Unit 24 of the master plan; not
  shipped yet, but the doc names it so users know what's coming).

**Test scenarios:**
- Test expectation: none -- documentation-only unit. The drift
  detector is the `capabilities.yaml`-vs-code test in 9.1; the doc
  is reviewed by hand.

**Verification:**
- The concept→destination table in the doc matches the in-code
  capability map kind-for-kind (manual diff during code review).
- Every link cited in the doc resolves at review time (not
  programmatically tested in v1).

## System-Wide Impact

- **Interaction graph:** The bundled `claude` adapter is wired into
  the runtime via `adapter.Adapter{Bundled: claude.Bundled()}` at the
  composition root. Until Unit 16 wires it, it's only used in this
  package's tests. No callers in `cmd/` or `internal/cli/` change in
  this PR.
- **Error propagation:** Adapter errors flow as
  `adapterkit.Error` values from `OnEmit`; the runtime wraps them in
  `RuntimeError` with the right `error_class`. No new error classes
  introduced.
- **State lifecycle risks:** The adapter holds no state between
  emits — `OnEmit` is pure with respect to its `EmitParams`. Skill
  asset traversal is in-memory; no temp files, no side effects. The
  per-emit `mkdir`+`README` op pair is idempotent at the disk level
  once the sync engine writes them.
- **API surface parity:** Unit 10 (cursor) and Unit 11 (codex) will
  copy this package's shape — keep the public surface narrow
  (`Bundled()` only) so the duplication is small and the contract is
  obvious.
- **Integration coverage:** The end-to-end inproc test in 9.6 is the
  primary cross-layer assertion. Subprocess parity is implicit
  (bundled adapters speak the same wire protocol as subprocess ones,
  proven by Unit 8 PR2's `inproc_test.go`).
- **Unchanged invariants:**
  - Adapter still emits **summary-only** `OpsPerformed` (Unit 8 PR3
    spec freeze). Content is built but discarded at the wire — Unit
    9 does not change the v1 protocol.
  - `internal/fsroot` is not touched. The adapter performs zero
    file I/O.
  - `pkg/adapterkit` public surface is not extended; the adapter
    consumes the existing API.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Drift between `capabilities.yaml` and the in-code matrix during edits | Unit 9.1 ships a parity test that fails CI on any divergence |
| Path strings differ across Windows / Unix | Forward-slash string concatenation everywhere; per cross-platform learning |
| `paths:` frontmatter detection misses cases not at the top of the body | Documented as best-effort; v1.x adds true frontmatter exposure via the IR |
| Per-emit README causing unnecessary writes | Acceptable for v1 (sync engine deduplicates at disk level); revisit if conformance noise becomes a complaint |
| Companion `agents-md` overlay collides with future `cursor`+`codex` overlays sharing `AGENTS.md` | This unit only touches `CLAUDE.md`, not `AGENTS.md`; cross-adapter `AGENTS.md` coordination is Unit 10/11/11.5's concern |
| Conformance harness CLI not yet wired (Unit 16) | End-to-end test in 9.6 uses `adapterkit.RunInprocServer` to exercise the same client interface the harness uses |

## Documentation / Operational Notes

- `docs/adapters/claude.md` ships in 9.7 (authoritative).
- No CHANGELOG entry yet (changelog is introduced in Unit 21).
- No CLI documentation impact — Unit 16 wires the registration when
  the cobra tree lands.
- No new dependencies. Re-uses `pkg/adapterkit`, `internal/adapter`,
  and `internal/ir` (for type aliases and id grammar).

## Sources & References

- **Origin / parent plan:** [docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md](2026-04-21-001-feat-aienvs-workspace-cli-plan.md) (Unit 9 section)
- **Spec — protocol:** [docs/spec/adapter-protocol-v1.md](../spec/adapter-protocol-v1.md)
- **Spec — IR:** [docs/spec/ir-v1.md](../spec/ir-v1.md)
- **Reference adapter:** `conformance/echo/main.go`
- **SDK:** `pkg/adapterkit/`
- **Runtime path-safety gate:** `internal/adapter/runtime.go` (function
  `pathInDeclaredOutputs`)
- **Cross-platform path-handling rule:** `docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`
- **Spec-vs-impl drift learning:** `docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`
- **Claude Code skills directory structure:** https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills
- **Claude Code project-local `.mcp.json`:** https://docs.claude.com/en/docs/claude-code/mcp

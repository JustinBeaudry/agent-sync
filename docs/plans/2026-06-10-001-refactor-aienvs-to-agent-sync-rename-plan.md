---
title: "refactor: rename legacy aienvs/aienv identifiers to agent-sync"
type: refactor
status: active
date: 2026-06-10
plan_depth: deep
breaking: true
---

# refactor: Rename legacy `aienvs`/`aienv` to `agent-sync`

## Summary

The project was renamed to **agent-sync** (module `github.com/agent-sync/agent-sync`,
binary `agent-sync`), but the legacy name `aienvs`/`aienv` still survives in three
places: a stale golangci config bug, prose/comments throughout the tree, and a set
of **load-bearing on-disk and wire-format identifiers** that adapters write into
user files and the engine reads back. This plan renames all of them.

The on-disk/wire changes are a **breaking change** with **no migration and no
legacy detection** (explicit user decision): the binary simply adopts the new
names. A pre-existing `.aienv`-format workspace will, on its next `sync`, be
treated as unmanaged — the new binary looks only at `.agent-sync/state/`, finds no
ledger, and takes the first-sync path. Old `aienvs:` managed blocks left inside
user files are not cleaned up; new `agent-sync:` blocks are added alongside. This
is accepted because no production workspaces are known to exist on disk.

The work preserves AGENTS.md **invariant #6** (two-rename atomic swap) and
**invariant #7** (ledger authority) — only the *names* of the reserved state
directory, markers, and key prefixes change, never the swap/recover/ledger
mechanics.

---

## Problem Frame

`aienvs` (the original "AI environments" working name) leaks into surfaces a user
or contributor sees and into the format adapters emit. Concretely, `grep -ri
aienv` across the repo returns ~130 files. They fall into three buckets with very
different risk:

1. **Stale config bug.** `.golangci.yml` sets goimports `local-prefixes:
   github.com/aienvs/aienvs`, but the real module is
   `github.com/agent-sync/agent-sync`. The prefix never matches, so import
   grouping is silently wrong. Pure win to fix; no behavior risk.

2. **Docs & comments.** README, AGENTS.md, CLAUDE.md prose, and Go doc comments
   refer to the product as `aienvs` and describe `.aienv.yaml`/emitted paths under
   the old names. Cosmetic, but user-facing.

3. **Load-bearing on-disk / wire-format identifiers** (breaking): the manifest
   filename, the reserved state directory, the markdown managed-block markers, the
   MCP key/table prefix, the IR frontmatter keys, the cache directory, the
   accessibility env var, the emitted skill-folder prefix, and the strict-JSON
   sidecar marker filename. Renaming any of these changes what gets written to
   disk and into user-owned files (`AGENTS.md`, `.mcp.json`, `.codex/config.toml`),
   which is the data-loss-critical merge/swap surface.

The challenge is doing bucket 3 without breaking the per-commit-green build and
without disturbing invariants #6/#7.

---

## Scope Boundaries

**In scope**
- Every `aienvs`/`aienv` identifier in production code, tests, and the one
  testdata fixture (`testdata/manifest/valid-pinned.yaml`).
- The golangci local-prefix bug.
- Prose/comment references to the current product name and emitted paths.

**Out of scope / non-goals**
- **Any migration or legacy-format detection.** No auto-rename, no fail-loud
  guard, no ledger schema bump. (Explicit user decision: "clean break, no
  detection.")
- **Module path / binary name.** Already `agent-sync`; no `go.mod` change.

### Deferred to Follow-Up Work
- **Dated historical doc filenames** that contain `aienvs`
  (`docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md`,
  `docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md`) are left as
  historical artifacts. Links to them stay valid. Only the *current-name prose*
  around those links is updated. Renaming the files would churn git history and
  break existing references for no functional gain.

---

## Key Technical Decisions

### KTD-1: Split naming rule — hyphenated for paths, non-hyphenated for keys

Two target spellings, chosen by the *context* each identifier lives in:

- **`agent-sync` (hyphenated)** for filesystem paths, directory names, the env-var
  family, and human-facing prose. Hyphens are safe in filenames and consistent
  with the existing `AGENT_SYNC_REQUIRE_GIT` env var and the `agent-sync` binary.
- **`agentsync` (non-hyphenated)** for identifier-context tokens that must be a
  valid **TOML bare key** and a clean JSON key. `mcp_servers.agent-sync_<id>`
  would force TOML key-quoting and is fragile; `__agent-sync_*` JSON keys are
  ugly and inconsistent. The `aienvs_`/`__aienvs_` tokens use an underscore
  separator today, so `agentsync_`/`__agentsync_` is the minimal, safe analogue.

The id *suffix* after a prefix may still contain hyphens (e.g.
`mcp_servers.agentsync_my-server`, skill dir `agent-sync-my-skill`) — ids already
permit hyphens and underscores in `internal/merge/locator.go`.

### KTD-2: Naming map (authoritative)

| Old identifier | New identifier | Context | Unit |
|---|---|---|---|
| `github.com/aienvs/aienvs` | `github.com/agent-sync/agent-sync` | golangci goimports local-prefix | U1 |
| `.aienv.yaml` | `.agent-sync.yaml` | workspace manifest filename | U2 |
| `.aienv/`, `.aienv/state/` | `.agent-sync/`, `.agent-sync/state/` | reserved state dir (ledger, locks, machine-id, filelocks) | U3 |
| `<!-- aienvs:begin/end ... -->` | `<!-- agent-sync:begin/end ... -->` | markdown managed-block markers | U4 |
| `aienvs:` (`sectionIDPrefix`, markdown locator) | `agent-sync:` | markdown section-id prefix | U4 |
| `"aienvs"` (`claudeMDSection`/`agentsMDSection`) | `"agent-sync"` | emitted AGENTS.md/CLAUDE.md section id value | U4 |
| `aienvs_` (`mcpJSONPointerBase`, `mcpTOMLPathBase`, `aienvsKeyPrefix`) | `agentsync_` | MCP key / TOML bare table prefix | U5 |
| `__aienvs_required/targets/version` | `__agentsync_required/targets/version` | IR frontmatter JSON keys | U5 |
| `aienvs-` (`skillPrefix`) | `agent-sync-` | emitted skill leaf-dir prefix (`.claude/skills/agent-sync-<id>`, `.agents/skills/agent-sync-<id>`) | U6 |
| `.aienvs-managed` | `.agent-sync-managed` | strict-JSON sidecar marker filename | U6 |
| `aienvs/repos` (`cache.DirName`) | `agent-sync/repos` | shared cache directory | U7 |
| `AIENVS_ACCESSIBLE` | `AGENT_SYNC_ACCESSIBLE` | TUI accessibility env var | U7 |

### KTD-3: No ledger schema bump, no detection

The ledger already carries `SchemaVersionCurrent` with too-old/too-new handling
(`internal/ledger/types.go`). We deliberately do **not** bump it. Because the
state dir itself moves (`.aienv/state/` → `.agent-sync/state/`), a legacy ledger
is simply never opened by the new binary — it sees no ledger and runs the
first-sync path. Bumping the version would do nothing useful without detection
logic (which is out of scope) and would only mislead a future reader into thinking
a migration seam exists for this rename.

### KTD-4: Per-commit-green via identifier grouping

Each unit renames **one identifier group end-to-end** — production constants,
every test, and every affected golden/testdata line — in the same change. A single
golden assertion (e.g. a codex "mixed-everything" expected output) may be touched
by several units, each editing only the lines for its identifier; after each unit
both the production output and the golden change in lockstep, so every commit
stays green. Implementers must not split a production-constant change from its
test/golden update across units.

---

## Implementation Units

> **Verification convention for every unit below:** the unit is complete when
> `go vet ./...`, `AGENT_SYNC_REQUIRE_GIT=1 go test -race ./...`, and
> `golangci-lint run` are all clean, and `grep -ri "<the old token(s) for this
> unit>"` returns no hits outside the deferred historical doc filenames.

### U1. Fix the stale golangci local-prefix

**Goal:** Correct the goimports local-prefix so import grouping matches the real
module path.
**Requirements:** Bucket 1 (stale config bug).
**Dependencies:** none.
**Files:** `.golangci.yml`.
**Approach:** Replace `github.com/aienvs/aienvs` with
`github.com/agent-sync/agent-sync` under `formatters.settings.goimports.local-prefixes`.
Also fix the incidental `aienvs` mention in the `.golangci.yml` G304 comment
(line ~29) while here.
**Patterns to follow:** value must equal `module` line in `go.mod`.
**Test scenarios:** Test expectation: none — config-only. Verification is
`golangci-lint run` clean plus a spot check that goimports now groups
`github.com/agent-sync/...` imports as local in a touched file.
**Verification:** `golangci-lint run` clean; no `aienvs` left in `.golangci.yml`.

---

### U2. Rename workspace manifest `.aienv.yaml` → `.agent-sync.yaml`

**Goal:** The workspace anchor file and all references to it use the new name.
**Requirements:** Bucket 3 (manifest identifier).
**Dependencies:** none.
**Files:**
- `internal/workspace/workspace.go` (`ManifestName` const, `ErrNotFound` message,
  doc comments).
- `internal/manifest/load.go`, `internal/manifest/schema.go` (any references in
  errors/comments).
- `internal/tui/wizard/initconfig.go` (wizard copy referencing the manifest).
- `internal/cli/cmd_init.go` and any other `cmd_*.go` referencing `.aienv.yaml`.
- `testdata/manifest/valid-pinned.yaml` (header comment).
- Co-located `*_test.go` for the above (notably workspace + manifest + cli tests
  that construct or assert on `.aienv.yaml`).
**Approach:** Pure constant + string rename. `ManifestName` is the single source
of truth for the filename; ensure callers reference the constant rather than a
literal where practical (do not refactor broadly — only fix what this rename
touches).
**Patterns to follow:** `internal/workspace/workspace.go` already centralizes
`ManifestName`; keep that the only literal.
**Test scenarios:**
- Happy path: workspace discovery finds a repo anchored by `.agent-sync.yaml`.
- Edge: discovery from a nested subdir still resolves upward to the manifest.
- Error: no manifest anywhere → `ErrNotFound` whose message names
  `.agent-sync.yaml` (assert the new string).
**Verification:** gate green; `grep -ri "aienv.yaml\|\.aienv\b"` shows no manifest
references (state-dir `.aienv/` handled in U3).

---

### U3. Rename reserved state directory `.aienv/` → `.agent-sync/`

**Goal:** Move the reserved state tree (ledger, locks, machine-id, filelocks,
staging sentinels) to `.agent-sync/` while preserving invariants #6 and #7.
**Requirements:** Bucket 3 (state-dir identifier); AGENTS.md invariants #6, #7.
**Dependencies:** none (independent of U2, but both touch CLI text — land in any
order).
**Files:**
- `internal/ledger/load.go` (`.aienv/state/<target>.json` path builder),
  `internal/ledger/write.go` (`stateDir` const, `ensureStateDir`),
  `internal/ledger/types.go` (path doc comments).
- `internal/locks/flock.go` (`stateDirRel`, segment guard `[]string{".aienv",
  stateDirRel}`), `internal/locks/filelock.go` (`fileLocksDirRel`),
  `internal/locks/machineid.go` (`machineIDRel`), `internal/locks/errors.go`
  (`ErrUnsafeStatePrefix` message + comments).
- `internal/fsroot/root.go`, `internal/fsroot/safewrite.go` (any `.aienv`
  references in reserved-prefix handling / comments).
- `internal/sync/staging.go`, `internal/sync/recover.go`, `internal/sync/swap.go`,
  `internal/sync/adopt.go`, `internal/sync/state_sentinel.go` (state-path
  references).
- `internal/engine/target.go` (reserved-prefix derivation referencing the state
  dir).
- Co-located `*_test.go` across `ledger`, `locks`, `fsroot`, `sync`, `engine`.
**Approach:** This is the highest-risk unit. The literal `.aienv` appears as
**two distinct segments** in the symlink-safety guards: the top-level `.aienv`
component and the `.aienv/state` prefix. Both must move to `.agent-sync` /
`.agent-sync/state` together, including the `segs := append([]string{".aienv",
stateDirRel}, extra...)` guard in `flock.go`. Do **not** alter the two-rename swap
sequence, the per-leaf `.state-<leaf>` sentinel scheme, or the recover state
machine — only the parent directory name changes. Verify the symlink-traversal
guards still resolve the new path components.
**Patterns to follow:** keep each package's existing path-constant as the single
source (e.g. `stateDir`, `stateDirRel`, `machineIDRel`); change the constant, not
scattered literals.
**Execution note:** Characterization-first — before changing constants, confirm
the existing `internal/sync` recover/swap and `internal/locks` symlink-guard tests
pass, so any post-rename failure is unambiguously a path-wiring slip, not a
pre-existing gap.
**Test scenarios:**
- Happy path: a full stage→swap→reconcile cycle writes ledger + sentinels under
  `.agent-sync/state/` and leaves no `.aienv/` directory.
- Invariant #6: two-rename atomic swap still performs `<prefix>` → `<prefix>.old`
  → staged-in, with both renames under one `os.Root`, after the dir rename.
- Invariant #7: ledger at `.agent-sync/state/<target>.json` remains authoritative
  for ownership; a second sync reads it back and reconciles correctly.
- Edge (symlink guard): a symlinked `.agent-sync` or `.agent-sync/state` path
  component is rejected with `ErrUnsafeStatePrefix` (assert the guard fires on the
  renamed components).
- Edge: per-leaf sentinel `.state-<leaf>` collision behavior is unchanged for
  shared-subdir leaves.
- Error: stale sentinel from a wedged prior run still routes to recovery, not
  silent overwrite.
**Verification:** gate green (especially `internal/sync`, `internal/locks`,
`internal/engine` with `-race`); a real `init` + two consecutive `sync` runs
create and reuse `.agent-sync/state/`; no `.aienv/` path literal remains.

---

### U4. Rename markdown markers + section-id prefix/value

**Goal:** Managed markdown blocks are delimited by `<!-- agent-sync:begin/end -->`
and identified by the `agent-sync:` prefix / `agent-sync` section id, consistently
across the merge engine and all three bundled adapters.
**Requirements:** Bucket 3 (markdown wire format).
**Dependencies:** none, but **must change merge engine and adapters together** —
the engine writes the markers and the adapters emit the locators; a split would
desync them.
**Files:**
- `internal/merge/markdown.go` (`markerOpen = "<!-- aienvs:"`, the partial-managed
  header text, `parseBeginMarker`/`parseEndMarker`/`cutMarker` namespace).
- `internal/merge/locator.go` (markdown locator `aienvs:<id>` parsing + error
  messages).
- `internal/adapter/bundled/claude/emit_tool_owned.go`,
  `.../cursor/emit_tool_owned.go`, `.../codex/emit_tool_owned.go`
  (`sectionIDPrefix = "aienvs:"`).
- `internal/adapter/bundled/claude/capabilities.go`, `.../cursor/capabilities.go`,
  `.../codex/capabilities.go` (`claudeMDSection`/`agentsMDSection := "aienvs"`).
- Co-located `*_test.go` and inline golden assertions in `internal/merge` and the
  three adapter packages (notably `markdown_test.go`, adapter `emit_test.go`,
  `capabilities_test.go`).
**Approach:** Change the marker namespace constant in `markdown.go` and the
matching `sectionIDPrefix` constants in the adapters in one unit. The section-id
*value* `"aienvs"` becomes `"agent-sync"` (a hyphenated id is fine — the begin
marker parser already accepts hyphenated ids). Update the human-readable
partial-managed header string (it currently says "edit outside the aienvs:begin /
aienvs:end markers").
**Patterns to follow:** marker grammar in `internal/merge/markdown.go` is the
single definition; adapters only need the prefix string to match.
**Test scenarios:**
- Covers AE: round-trip — emit a managed AGENTS.md section, re-parse it, recover
  the same id and body using `agent-sync:` markers.
- Happy path: `parseBeginMarker` accepts `<!-- agent-sync:begin id=agent-sync -->`
  (hyphenated namespace AND hyphenated id).
- Edge: a user block that happens to contain the literal text `aienvs` but no
  marker is left untouched (no accidental match).
- Edge: mixed file with a foreign `<!-- something:begin -->` block is not claimed.
- Error: malformed marker (`agent-sync:begin` with no id) → parse error, not a
  silent claim.
- Integration: end-to-end adapter emit produces markers the merge engine locates
  and updates idempotently on a second sync (no duplicate blocks).
**Verification:** gate green; emitted AGENTS.md/CLAUDE.md sections use
`agent-sync:` markers; `grep -ri "aienvs:"` returns nothing outside historical
docs.

---

### U5. Rename MCP key/table prefix + IR frontmatter keys

**Goal:** Managed MCP entries are keyed `agentsync_<id>` (JSON pointer and TOML
bare table) and IR frontmatter uses `__agentsync_*`.
**Requirements:** Bucket 3 (MCP + frontmatter wire format).
**Dependencies:** none; change merge locator/json/toml and adapters together (same
desync reasoning as U4).
**Files:**
- `internal/merge/locator.go` (`aienvsKeyPrefix = "aienvs_"`, `validateAienvsSeg`,
  locator doc comments — rename the symbol too, e.g. `agentsyncKeyPrefix`).
- `internal/merge/json.go` (`/mcpServers/aienvs_` pointer examples + escaping
  logic comments).
- `internal/merge/toml.go` (`[mcp_servers.aienvs_<id>]` header rendering,
  `renderAienvsTable` → `renderAgentsyncTable`).
- `internal/merge/errors.go` (comment referencing aienvs-managed entries).
- `internal/ir/kinds.go` (`__aienvs_required`/`__aienvs_targets`/`__aienvs_version`
  JSON tags + the parse/delete logic for each), `internal/ir/types.go` (any
  references).
- `internal/adapter/bundled/claude/emit_tool_owned.go`,
  `.../cursor/emit_tool_owned.go` (`mcpJSONPointerBase = "/mcpServers/aienvs_"`),
  `.../codex/emit_tool_owned.go` (`mcpTOMLPathBase = "mcp_servers.aienvs_"`).
- Co-located `*_test.go` + inline goldens in `internal/merge`, `internal/ir`, and
  the three adapter packages.
**Approach:** Rename both the string values and the Go symbols (`aienvsKeyPrefix`,
`validateAienvsSeg`, `renderAienvsTable`) for consistency. The `__agentsync_*`
keys are internal IR transport keys (stripped before user-facing frontmatter is
re-emitted), so they don't appear in user output — but they are a wire contract
between IR producers and consumers and must change in lockstep.
**Patterns to follow:** `aienvsKeyPrefix` in `locator.go` is the prefix source of
truth; `internal/ir/kinds.go` owns the `__*` key constants.
**Test scenarios:**
- Happy path: emit an MCP server entry → JSON merge writes `/mcpServers/agentsync_<id>`;
  TOML merge writes `[mcp_servers.agentsync_<id>]`.
- Edge: id containing hyphens and underscores (`agentsync_my-server_v2`) parses
  back to the correct id via `validateAgentsyncSeg`.
- Edge: TOML bare-key validity — the rendered `[mcp_servers.agentsync_<id>]`
  header needs no quoting (assert it parses as a bare key).
- Edge: a user-owned MCP entry NOT prefixed `agentsync_` is preserved untouched on
  merge (ownership boundary).
- Error: malformed locator (segment missing the `agentsync_` prefix) → error
  message names the new prefix.
- Frontmatter: IR parse of `__agentsync_required/targets/version` populates
  `Frontmatter`; wrong types still error; keys are stripped from re-emitted body.
**Verification:** gate green; emitted `.mcp.json`/`.codex/config.toml` use
`agentsync_`; `grep -ri "aienvs_\|__aienvs"` returns nothing outside historical
docs.

---

### U6. Rename emitted skill-dir prefix + sidecar marker filename

**Goal:** Emitted skill folders are `agent-sync-<id>` and the strict-JSON sidecar
marker is `.agent-sync-managed`.
**Requirements:** Bucket 3 (emitted filesystem names).
**Dependencies:** U3 conceptually (state-dir naming), but independently landable;
no shared constants with U3.
**Files:**
- `internal/adapter/bundled/claude/emit_reserved.go`,
  `.../codex/emit_reserved.go` (`skillPrefix = "aienvs-"` + path comments).
- `internal/adapter/bundled/claude/emit_tool_owned.go`
  (`claudeMDSidecar = ".aienvs-managed"`), `.../claude/emit.go` +
  `.../cursor/emit.go` (sidecar dedup comments),
  `.../cursor/emit_tool_owned.go` (`mcpSidecarPath = ".cursor/.aienvs-managed"`).
- `internal/adapter/bundled/claude/capabilities.go` (`{Path: ".aienvs-managed"}`),
  `.../cursor/capabilities.go` (`{Path: ".cursor/.aienvs-managed"}`), and the
  `.claude/skills` / `.agents/skills` ownership comments in all three
  `capabilities.go`.
- Co-located `*_test.go` + inline goldens in the three adapter packages.
**Approach:** Rename `skillPrefix` and the sidecar path constants. The skill-dir
prefix is what isolates agent-sync-owned skill folders from foreign ones under the
shared `.claude/skills` / `.agents/skills` parents — the prefix must be unique and
stable; `agent-sync-` satisfies that. Update the path-traversal-guard comments
that reference `../aienvs-victim/SKILL.md`.
**Patterns to follow:** each adapter's `skillPrefix` const is the source of truth;
the shared-subdir leaf-ownership logic keys on this prefix.
**Test scenarios:**
- Happy path: a skill IR emits `.claude/skills/agent-sync-<id>/SKILL.md` (claude)
  and `.agents/skills/agent-sync-<id>/SKILL.md` (codex).
- Edge: a foreign skill folder NOT matching `agent-sync-` under the shared parent
  survives a sync (shared-subdir leaf ownership, AGENTS invariant for shared
  parents).
- Edge: path-traversal attempt via a crafted id (`../agent-sync-victim`) is
  rejected by the existing `leafUnder`/traversal guard.
- Sidecar: `.agent-sync-managed` (claude) / `.cursor/.agent-sync-managed` (cursor)
  is written exactly once per emit (dedup) and marks managed JSON entries.
- Integration: second sync reuses the same skill leaf dirs without orphaning.
**Verification:** gate green; emitted skill dirs and sidecars use the new names;
`grep -ri "aienvs-\|aienvs-managed"` returns nothing outside historical docs.

---

### U7. Rename cache directory + accessibility env var

**Goal:** Shared cache lives under `agent-sync/repos` and the accessibility toggle
is `AGENT_SYNC_ACCESSIBLE`.
**Requirements:** Bucket 3 (cache dir, env var).
**Dependencies:** none.
**Files:**
- `internal/cache/location.go` (`DirName = "aienvs/repos"` + path-example
  comments).
- `internal/tui/accessible.go` (`AIENVS_ACCESSIBLE` env lookup + comment).
- Co-located `*_test.go` in `internal/cache` and `internal/tui`.
**Approach:** Two independent constant renames. Align the env var with the
existing `AGENT_SYNC_REQUIRE_GIT` family (prefix `AGENT_SYNC_`).
**Patterns to follow:** existing `AGENT_SYNC_REQUIRE_GIT` env naming;
`cache.DirName` as the single cache-path source.
**Test scenarios:**
- Happy path (cache): cache root resolves to `<XDG_CACHE>/agent-sync/repos`.
- Edge (cache): audit path and per-key dir derive from the renamed `DirName`.
- Happy path (env): `AGENT_SYNC_ACCESSIBLE=1` activates accessible TUI mode;
  unset/`0` does not.
- Edge (env): legacy `AIENVS_ACCESSIBLE` is NOT read (assert no fallback — clean
  break).
**Verification:** gate green; `grep -ri "AIENVS_ACCESSIBLE\|aienvs/repos"` empty.

---

### U8. Docs & comments sweep + final verification grep

**Goal:** All current-name prose and remaining Go comments say `agent-sync`; the
only surviving `aienv` strings are the deferred historical doc filenames/links.
**Requirements:** Bucket 2 (docs/comments).
**Dependencies:** U1–U7 (so prose describes the final names and the verification
grep is meaningful).
**Files:**
- `README.md` (product-name prose; `.aienv.yaml` → `.agent-sync.yaml`; emitted
  path examples on ~line 26 updated to the real new paths, e.g.
  `.claude/skills/agent-sync-<id>/`, `.agents/skills/agent-sync-<id>/`; **keep**
  the dated plan/brainstorm links).
- `AGENTS.md` (prose; keep the two dated doc links in the reading list).
- `CLAUDE.md` (the plan-path reference — keep the link, fix surrounding prose if
  it names the product `aienvs`).
- `cmd/agent-sync/main.go` (doc-comment plan-path reference — keep path).
- Any remaining Go doc comments across the files touched in U2–U7 not already
  caught (sweep with the grep below).
**Approach:** This is a prose/comment pass. Update the *product name* and *path
examples* to match the now-renamed code. Do **not** edit the dated historical doc
filenames or the links pointing at them. Finish with the repo-wide verification
grep and confirm every remaining hit is an intentional historical reference.
**Patterns to follow:** README adapter table / path conventions already established
in the repo.
**Test scenarios:** Test expectation: none — docs/comments only. Verification is
the grep below plus a human read of README/AGENTS for naming consistency.
**Verification:**
`grep -rni "aienv" . --exclude-dir=.git` returns ONLY:
(a) the two dated filenames under `docs/brainstorms/` and `docs/plans/`, and
(b) links/path-references to those two files. Any other hit is a miss — fix it.
Full gate green one final time.

---

## System-Wide Impact

- **Users with an existing `.aienv` workspace** (if any exist): next `sync` treats
  the workspace as unmanaged, leaves old `aienvs:` blocks orphaned in their files,
  and adds new `agent-sync:` blocks alongside. Accepted per the clean-break
  decision; called out in the breaking-change note for the release.
- **CI:** the coverage gate (80% floor) must still pass — renames are
  behavior-preserving, so coverage should be unchanged; watch for any test
  accidentally dropped during a rename.
- **Release notes:** this is a breaking on-disk format change and should be flagged
  as such (e.g. a major/minor bump per the project's versioning) even though no
  code migration ships.
- **Data-loss-critical surface:** U3 touches the swap/ledger/lock layer. The
  AGENTS.md invariants #6/#7 are the acceptance bar; U3's test scenarios assert
  them directly. Per the handoff guidance, run `ce-code-review` on U3 (and the
  branch overall) before opening the PR — the prior shared-subdir and per-leaf
  sentinel P1s were caught by review, not the local gate.

---

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| U3 mis-wires a symlink-safety guard (two `.aienv` segments), weakening the reserved-prefix protection | Characterization-first execution note; explicit symlink-guard test scenario on the renamed components; `ce-code-review` pass |
| A golden file is half-renamed (one identifier updated, another stale) leaving a flaky/failing assert | KTD-4: each unit updates production + all affected golden lines for its identifier in the same commit; per-unit `-race` gate |
| Hyphenated section id or marker namespace breaks the marker parser | U4 scenario explicitly asserts hyphenated namespace + hyphenated id parse; ids already permit hyphens in `locator.go` |
| TOML bare-key invalidity from a hyphen creeping into the MCP prefix | KTD-1 fixes the prefix as non-hyphenated `agentsync_`; U5 scenario asserts bare-key validity |
| A literal `aienv` string missed in a comment or rarely-exercised path | U8 final repo-wide grep with an explicit allowlist of historical doc references |
| Accidental test deletion during rename drops coverage below the floor | CI coverage gate; final full gate run in U8 |

---

## Dependencies / Sequencing

```
U1 (golangci)        ── independent, do first (trivial, unblocks clean lint)
U2 (manifest)        ── independent
U3 (state dir)       ── independent, highest risk; review-gated
U4 (markers)         ── merge engine + adapters together
U5 (mcp/frontmatter) ── merge engine + adapters together
U6 (skill dirs)      ── adapters
U7 (cache/env)       ── independent
U8 (docs sweep)      ── LAST; depends on U1–U7 for accurate prose + grep
```

U2–U7 are mutually independent and may land in any order or in parallel; only U8
must come last. Recommended order: U1 → U3 (get the risky one reviewed early) →
U2, U7 (easy wins) → U4 → U5 → U6 → U8.

---

## Verification Strategy

Per the repo gate (CLAUDE.md / AGENTS.md), every unit must pass before it's
declared done:

```
go vet ./...
AGENT_SYNC_REQUIRE_GIT=1 go test -race ./...
golangci-lint run
```

Plus, end-to-end after U2–U6: a real `init --target claude --target cursor
--target codex && sync` that confirms `.agent-sync.yaml`, `.agent-sync/state/`,
`agent-sync:` markers, `agentsync_` MCP keys, and `agent-sync-<id>` skill dirs all
appear, exit 0, and a second `sync` is idempotent (no duplicate managed blocks).

Final gate in U8: the repo-wide `grep -rni aienv` allowlist check.

# Changelog

All notable changes to `agent-sync` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the project is pre-1.0, minor version bumps may include breaking changes;
the adapter wire protocol follows its own "freeze the frame, grow capabilities"
compatibility policy documented in `docs/spec/adapter-protocol-v1.md`.

## [Unreleased]

### Changed

- **Gemini CLI → Antigravity CLI.** Google retired Gemini CLI on 2026-06-18
  in favor of Antigravity CLI (`agy`); only enterprise Gemini Code Assist
  licenses retain Gemini CLI access. The planned-adapter roadmap entry is now
  Antigravity CLI, and the `GEMINI.md` root overlay in canonical sources now
  scopes its `agents-md` node to the `antigravity` target instead of the
  never-shipped `gemini` one (Antigravity still reads `GEMINI.md` as its
  tool-specific overlay, so the recognized filename is unchanged; the node id
  stays `gemini`, filename-derived). No behavior change for existing
  workspaces — neither target has a bundled adapter yet, so the overlay was
  and remains inert until the Antigravity adapter lands.

## [0.4.0] - 2026-07-02

### Added

- **User-scope Cursor rule composition**
  (`compose.cursor-rules-from-user`, opt-in). A project manifest can fold the
  user-scope (`~`) manifest's Cursor `rule` nodes into its own sync, so global
  Cursor rules take effect through the project's `.cursor/rules/` — Cursor has
  no file-addressable user-global rules home, and those nodes were previously
  surfaced only as inert-at-user-scope warnings. Composed rules are owned by
  the project ledger, so dropping the rule (or the opt-in) reclaims them via
  the normal orphan path. Id collisions resolve project-wins with a per-id
  shadow warning. Composition is best-effort: a missing or malformed user
  manifest never fails the project sync, and remote user canonicals are never
  fetched during composition. `validate`, `watch`, and `sync --workspace` see
  the same composed desired state, so composed rules are neither reported as
  drift nor deleted; when composition fires, the now-misleading "rule is inert
  at user scope" coverage warning is suppressed.

- **Cross-adapter shared-subdir co-ownership ("ADV-1")** + **pi `skill`
  support**. codex and pi both read the shared `.agents/skills/` tree; with
  per-target ledgers, a workspace targeting both previously failed because each
  adapter's drift guard saw the other's skill file as a foreign hand-edit
  (`ErrMidLifeDrift`). The engine's drift and orphan checks are now union-aware
  for shared-subdir leaves: a file claimed by any configured target's ledger is
  treated as managed, and a shared leaf a sibling still owns is neither
  swap-emptied nor orphan-deleted when a target releases it (verified across
  add / idempotent re-sync / full-remove, and a `--target`-filtered removal that
  leaves the sibling's copy intact). Owned-subdir prefixes keep the exact
  single-target behavior. The pi adapter now emits `skill` to
  `.agents/skills/agent-sync-<id>/SKILL.md`, byte-identical to codex so a
  co-emitted skill matches.

- Bundled **`pi` adapter** (`@mariozechner/pi-coding-agent`), the fourth bundled
  adapter. This first cut supports `agents-md`, section-merged into the
  workspace-root `AGENTS.md` (and `~/.pi/agent/AGENTS.md` at user scope) — it
  coexists with the codex/cursor sections in the same file. Pi's deliberate
  no-MCP stance is surfaced honestly: `mcp-server-entry` targeting pi emits a
  degradation warning citing Pi's rationale, never a dead file. `rule` and
  `plugin-reference` are unsupported (no Pi concept). `skill` is now supported
  (see the co-ownership entry above). `command` remains unsupported-but-planned:
  it needs owned-file-in-a-shared-dir swap support for Pi's flat `.pi/prompts/`
  tree — tracked as a follow-up. See `docs/adapters/pi.md`.

### Fixed

- **ADV-1 hardening: per-workspace run lock + dropped-target warning.** `sync`
  now takes one flock-backed run lock (`.agent-sync/state/.sync.lock`) held
  across the whole run, so concurrent syncs on a workspace serialize. This
  closes a cross-process race in shared-subdir co-ownership where two overlapping
  `--target`-filtered syncs could each defer a co-owned-leaf delete to the other
  and strand the file, and makes concurrent shared-leaf swaps orderly instead of
  one failing with `ErrStale`. `validate` (read-only) stays lock-free. Run-lock
  contention yields a clean *blocked* result (never a hard error), so a
  `--post-merge` git-hook sync still exits 0 and never breaks `git pull` (the hook
  caps its wait at a few seconds so a contended pull yields fast). Separately,
  a sync now warns when an on-disk ledger exists for a target no longer in the
  manifest (its files are stranded until `agent-sync unmanage` reclaims them) —
  the warning is non-destructive; sync never deletes a dropped target's files.

- `sync --user` now targets Claude Code's real user-config paths. The Claude
  adapter previously emitted its agents-md overlay to `~/CLAUDE.md` and its MCP
  servers to `~/.mcp.json` at user scope — neither of which Claude Code reads —
  so user-scope syncs were silently inert. The adapter is now scope-aware: at
  user scope it writes the managed section to `~/.claude/CLAUDE.md` and merges
  MCP entries into `~/.claude.json` (preserving all foreign keys), and suppresses
  the `.agent-sync-managed` sidecar (the MCP target is Claude's own shared file,
  not an agent-sync-owned one). Project and directory scope are unchanged.
- `sync --user` is now scope-aware for the Cursor and Codex adapters too. Codex
  agents-md targets `~/.codex/AGENTS.md` (Codex's user-global instructions path,
  not the inert `~/AGENTS.md`); its MCP (`~/.codex/config.toml`) and skills
  (`~/.agents/skills/`) were already correct. Cursor syncs user-global MCP
  servers to `~/.cursor/mcp.json` (sidecar suppressed); its `rule` and
  `agents-md` outputs are skipped at user scope because Cursor has no
  file-addressable user-global home for them (User Rules are app-settings/cloud;
  there is no global `AGENTS.md`). The skipped Cursor kinds are surfaced as
  per-scope coverage warnings rather than dead-path writes. Project and directory
  scope are unchanged.

### Changed

- Adapter protocol: the `initialize` params carry a new additive, optional
  `scope` field (`"user"` | `"project"` | `"directory"`; absent ⇒ `project`).
  Adapters use it to choose scope-appropriate output paths. Additive under the
  "freeze the frame, grow capabilities" policy — no protocol version bump; an
  adapter that ignores it behaves exactly as before.

## [0.3.0] - 2026-06-17

### Added

- Hierarchy-aware manifests. `agent-sync` discovers a `.agent-sync.yaml` at
  multiple filesystem scopes by walking up from the current directory — project
  (the nearest `.git` ancestor), any intermediate directories, and the user home
  (`~`) — and emits each manifest to its own scope. Precedence is resolved by
  each target tool's native config hierarchy (Claude Code, Codex's `AGENTS.md`
  walk, Cursor's nested `.cursor/rules`); agent-sync never merges across levels.
  New `internal/hierarchy` package.
- `sync` is multi-scope: it emits every manifest from the current directory up
  to the project root, with continue-and-report across scopes (one failing scope
  no longer blocks the others, and per-scope operational/trust failures keep
  their specific exit codes). The new `--user` flag also emits the user-level
  (`~`) manifest; a plain repo `sync` never writes under the home directory, and
  `--user` is mutually exclusive with `--workspace`.
- Coverage warnings (`internal/coverage`). `sync` warns when a scope emits a
  node kind a target tool will not read natively at that level (e.g. a
  nested-directory skill for Claude), so silently-ineffective output is surfaced
  rather than hidden. Unknown targets/kinds default to native (no false
  warnings).
- Hierarchy-aware `status`. It lists every discovered scope (user/project/
  directory) with its level, source, and per-target managed-file counts, and
  surfaces the watch-failure banner per scope.

### Notes

- `validate` and `watch` remain single-scope (nearest workspace) for now.
- The runtime filesystem-mapping fallback for tools that cannot layer a kind
  natively is deferred behind a future explicit flag; the sync engine is
  unchanged by this release.

## [0.2.0] - 2026-06-16

### Added

- In-repo local skill source (`canonical.local_dir`). A workspace can author
  skills, rules, commands, AGENTS.md, mcp, and plugins under a workspace-relative
  directory (e.g. `.agents`) and compile them straight from the working tree —
  no git, no clone, no commit pin. The source kind is a third, mutually-exclusive
  option alongside `url`/`local_path`, exempt from pinning, trust (TOFU), and
  offline-strict. Authored skills coexist with agent-sync's own emitted skills
  in the shared `.agents/skills` tree via the reserved `agent-sync-` id prefix.
  Configure with `agent-sync init --local-dir .agents`. The decoder now reads
  through an `ir.SourceTree` interface satisfied by both `git.Repository` and a
  new `internal/worktree.Reader`.

### Fixed

- `agent-sync --version` now reports the release tag injected via
  `-ldflags -X main.version` instead of fang's `unknown (built from source)`
  placeholder. `fang.Execute` overwrites `root.Version` from build info, so the
  value is now passed back through `fang.WithVersion`.

## [0.1.0] - 2026-06-16

### Added

- Apache-2.0 `LICENSE` and `NOTICE` files; license declared in `README`.
- `docs/threat-model.md` — supply-chain and filesystem-safety threat model.
- `.goreleaser.yaml` and `.github/workflows/release.yml` — reproducible
  multi-platform release packaging (darwin/amd64, darwin/arm64, linux/amd64,
  linux/arm64, windows/amd64) with SHA-256 checksums and optional minisign
  signatures.
- CI coverage gate: total statement coverage is enforced against the 80% floor
  mandated by CLAUDE.md and fails the build on a drop. Added behavioral tests
  for previously-untested packages (`internal/validate`, `conformance/echo`,
  `cmd/agent-sync`) plus the adapter op round-trip, watch sync, trust store,
  and conformance assertion paths to clear it.
- Fixed a nil-pointer panic in `adapterkit.ExitError.Error()` (the `e == nil`
  guard dereferenced `e.Code` in the same branch), surfaced by the new tests.
- CI now fails loud if the real-git/real-filesystem end-to-end tests are
  skipped, via `AGENT_SYNC_REQUIRE_GIT=1`.

### Changed

- `README` tool-support table now distinguishes bundled adapters (Claude Code,
  Cursor, Codex CLI) from planned ones, instead of advertising unimplemented tiers.

### Removed

- Roo Code from the planned tool list (decommissioned upstream).

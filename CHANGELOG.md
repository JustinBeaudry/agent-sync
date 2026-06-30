# Changelog

All notable changes to `agent-sync` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the project is pre-1.0, minor version bumps may include breaking changes;
the adapter wire protocol follows its own "freeze the frame, grow capabilities"
compatibility policy documented in `docs/spec/adapter-protocol-v1.md`.

## [Unreleased]

### Fixed

- `sync --user` now targets Claude Code's real user-config paths. The Claude
  adapter previously emitted its agents-md overlay to `~/CLAUDE.md` and its MCP
  servers to `~/.mcp.json` at user scope — neither of which Claude Code reads —
  so user-scope syncs were silently inert. The adapter is now scope-aware: at
  user scope it writes the managed section to `~/.claude/CLAUDE.md` and merges
  MCP entries into `~/.claude.json` (preserving all foreign keys), and suppresses
  the `.agent-sync-managed` sidecar (the MCP target is Claude's own shared file,
  not an agent-sync-owned one). Project and directory scope are unchanged.

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

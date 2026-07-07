# Changelog

All notable changes to `agent-sync` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the project is pre-1.0, minor version bumps may include breaking changes;
the adapter wire protocol follows its own "freeze the frame, grow capabilities"
compatibility policy documented in `docs/spec/adapter-protocol-v1.md`.

## [Unreleased]

## [0.8.0] - 2026-07-07

### Added

- **User → workspace → project/repo hierarchy.** Manifests now declare
  `scope: user | workspace | project`, `agent-sync init --user` writes the
  user manifest under `~`, `--workspace` writes a workspace manifest, and
  project/repo manifests remain the closest working-tree scope. Sync walks the
  hierarchy and emits each discovered scope to its own safe root.
- **Workspace activation roots.** A workspace manifest can set
  `activation_root: true`; discovery stops there for project syncs, so working
  inside a workspace such as `~/ActualReality` uses that workspace's agent
  configuration as the root instead of falling back to the user-home manifest.
- **Agent harness native fragments.** Canonical sources can now author
  managed native configuration fragments for Codex feature flags and lifecycle
  hooks. Native fragments merge deterministically across hierarchy layers,
  preserve unmanaged TOML/JSON content, and fail closed rather than overwriting
  unmanaged generated files.
- **Zero-flag `init`.** `agent-sync init` no longer requires a source or
  targets: with no source flag the canonical source defaults to the in-repo
  `.agents` directory (created when missing, so the first sync soft-lands on
  the zero-emit hint instead of a missing-source error), and with no
  `--target` the target list is **discovered** from the workspace's tool
  footprints (`.claude/` → claude, `.cursor/` → cursor, `.codex/` → codex,
  `.pi/` → pi, `.agent/` → antigravity) and snapshotted into `targets:`,
  sorted. Zero footprints still writes the manifest (empty `targets:` is the
  spec-valid "not yet configured" state) with a hint naming `--target`. The
  success line announces every inference —
  `wrote .agent-sync.yaml (source: .agents [default]; targets: claude [discovered])` —
  and detected-but-not-enabled footprints when explicit `--target` flags
  override discovery. PATH adapters are never auto-discovered.
- **Wizard defaults.** The init wizard accepts an empty Enter at the source
  prompt as the `.agents` in-repo source (skipping the ref question) and
  preselects discovered targets (all targets when none were discovered).
- **User-scope sync offer + skipped-user notice.** An interactive plain
  `sync` that discovers `~/.agent-sync.yaml` now asks
  `Also sync the user-level manifest at ~/.agent-sync.yaml? [Y/n]` instead of
  silently skipping it. Any run that skips an existing user manifest (e.g.
  non-interactive) ends with
  `note: user-level manifest at ... was not synced; pass --user to include it`
  in text output and in the additive JSON `notice` field — previously the
  hint fired only when there was nothing else to sync. Declining the prompt
  suppresses the note for that run; `--post-merge` (git-hook) syncs never
  prompt.

### Fixed

- **No more duplicated `<name>: started` lines during sync.** Bundled
  in-process adapters were printing the adapterkit session banner (subprocess
  proof-of-life for the runtime's stderr ring) straight to the CLI's stderr,
  once per adapter session — so every target printed it twice per sync.
  Bundled adapters now discard that banner; subprocess adapters are
  unchanged.

### Changed

- **`init --target <name>` on a TTY no longer launches the wizard** — with
  the defaulted `.agents` source the invocation is fully specified. Bare
  `agent-sync init` still runs the wizard on a terminal.
- **`agent-sync init --non-interactive` with no flags now succeeds** (it
  previously failed asking for `--source/--local-path/--local-dir`); scripts
  relying on that failure now get a defaulted manifest instead.
- **Pin flags without a source are rejected explicitly:** `--ref`,
  `--commit`, or `--floating` with no `--source`/`--local-path` now fail with
  an error naming the conflict (the defaulted `.agents` source is unpinned).

## [0.7.0] - 2026-07-04

### Added

- **Cursor skills.** The Cursor adapter now supports the `skill` IR kind,
  emitting to the shared `.agents/skills/agent-sync-<id>/` tree that Cursor reads
  (project and `~/.agents/skills/` at user scope) — the same tree codex, pi, and
  antigravity co-own, so a skill authored once serves every tool with
  byte-identical `SKILL.md`. See [`docs/adapters/cursor.md`](docs/adapters/cursor.md).
- **`file-leaf` OutputMode + Cursor & Pi commands.** A new engine ownership mode
  lets an adapter own individual files inside a flat directory it shares with the
  user (never the directory, never foreign files; per-file atomic write, drift,
  and orphan reclaim). A pre-existing unmanaged file at an exact target path fails
  closed rather than being clobbered (adoptable via `--adopt-prefix`). Built on
  it: **Cursor `command`** → `.cursor/commands/<id>.md` (project) /
  `~/.cursor/commands/<id>.md` (user), and **Pi `command`** → `.pi/prompts/<id>.md`
  — both flip from unsupported to supported. Cursor now has effective full parity
  (agents-md, rule, skill, command, mcp). See
  [`docs/adapters/cursor.md`](docs/adapters/cursor.md),
  [`docs/adapters/pi.md`](docs/adapters/pi.md), and the `file-leaf` row in
  [`docs/spec/adapter-protocol-v1.md`](docs/spec/adapter-protocol-v1.md).

## [0.6.0] - 2026-07-03

### Added

- **Antigravity adapter (bundled, full parity).** agent-sync now syncs to
  [Google Antigravity](https://antigravity.google) 2.0 (IDE + CLI), which
  replaced the retired Gemini CLI and reads the same `GEMINI.md` overlay. The
  adapter supports every IR kind except `plugin-reference`: `agents-md` →
  `GEMINI.md` managed section (not `AGENTS.md`, to avoid colliding with the
  codex/pi adapters), `rule` → `.agent/rules/agent-sync/`, `command` →
  `.agent/workflows/agent-sync/`, `skill` → the shared `.agents/skills/` tree,
  and `mcp-server-entry` → `.agents/mcp_config.json`. It faithfully reproduces
  Antigravity's own `.agent` (rules/workflows) vs `.agents` (skills/mcp)
  directory split. `agents-md`, `skill`, and `mcp-server-entry` are scope-aware
  (`sync --user` targets `~/.gemini/`); `rule` and `command` have no Antigravity
  user-global home and are reported as inert at user scope. See
  [`docs/adapters/antigravity.md`](docs/adapters/antigravity.md).

## [0.5.0] - 2026-07-03

### Added

- **Skills now carry real descriptions.** Emitted `SKILL.md` files open with a
  YAML frontmatter block (`name:` plus `description:`) at byte 0, so Claude
  Code and other Agent-Skills consumers list the authored description instead
  of falling back to the managed-header comment. Author `description:` in your
  canonical `skills/<id>/SKILL.md` frontmatter (a new recognized IR frontmatter
  field); a skill with no description still emits — with a deterministic
  placeholder — and raises a degraded warning so the gap is visible. The
  claude, codex, and pi adapters render the block through one shared helper so
  co-emitted skills stay byte-identical. **Compatibility:** canonical repos that
  adopt `description:` require agent-sync ≥ 0.5.0 — older binaries reject the
  unknown frontmatter key.
- **`agent-sync update`** moves the canonical pin forward safely. It fetches the
  remote, resolves the manifest's `ref` (or the remote default), shows the
  old→new SHAs plus a commit change-summary, then re-pins `commit` +
  `trusted_sha` and re-syncs — all under one workspace run lock so a concurrent
  sync cannot interleave. Fast-forward-only: a rewritten upstream history is
  refused unless `--accept-rewritten-history=<sha>` is passed; routine moves in
  non-interactive mode require `--accept-update=<sha>` (else exit 4). If the
  post-re-pin sync fails, the command exits 6 with a "pin moved, files did not
  land — re-run `agent-sync sync`" remediation. `--user`, `--workspace`,
  `--offline`, `local_dir`, and `local_path` are all handled.
- **Managed-file headers now record provenance.** Every emitted file's
  "do not edit" header shows `Source: <url>@<short-sha>` (git sources) or the
  local source path — the `{source-url}@{short-sha}` template placeholder is
  gone. Composed user-scope Cursor rules carry their own source, not the
  project's. Source URLs are always the credential-stripped canonical form.

### Fixed

- **A zero-emit `sync` now explains itself instead of printing an empty
  report.** Running plain `sync` where the only discovered manifest is the
  user-home one (e.g. from `~` itself, or any directory with no project
  manifest) silently emitted nothing — exit 0, `"scopes":[]`, no hint — because
  the user scope is read-only without `--user` (the plain-sync-never-writes-home
  invariant) and non-emit scopes were dropped without a word. The run now
  carries an advisory notice (additive `notice` field in the JSON document,
  `nothing to sync: …` line in text mode): it points at `agent-sync sync
  --user` when a user manifest was found, or at `agent-sync init` when no
  manifest exists at all. Exit code and write behavior are unchanged.

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

---
date: 2026-04-21
topic: aienvs-agent-workspace
---

# Agent workspace CLI (aienvs): sync and cross-tool configuration

## Problem Frame

Developers who use multiple coding agents and related tools (Codex, Claude, Gemini, Cursor, Windsurf, LM Studio, Roo, and others) accumulate duplicated, divergent configuration: skills, plugins, commands, prompts, and context rules. Copies drift across machines and repos. There is no single, reviewable manifest that stays aligned with Git while emitting each tool’s native layout. The affected users are the developers maintaining these environments; the outcome is consistent agent behavior, less manual copying, and a workspace-local way to tie a project root to a canonical config source.

## Simple mental model

**One manifest ↔ one Git repo ↔ one workspace directory.** Each `.aienv.yaml` points at **exactly one** canonical Git repository (optional local path for hacking). That repo holds the portable config (skills, prompts, shared rules). The CLI **materializes** it (clone/pull), **compiles** to a tool-agnostic intermediate representation (IR), then **emits** native files **into reserved subdirectories inside the target tool's own config locations, rooted at the workspace directory** (e.g. `~/ActualReality/.aienv.yaml` produces `~/ActualReality/.claude/aienvs/*`, `~/ActualReality/.cursor/aienvs/*`, etc.). Emitted files are **build artifacts**: edit the Git-backed source or the manifest, not the outputs. **`sync`** refreshes the clone and re-emits; **`validate`** shows diffs without writing. Optional hooks/watch only automate calling **`sync`**.

**aienvs operates on ONE explicit workspace per invocation.** Users may keep multiple workspaces on disk — typically one per target-tool config **scope**: a user-level workspace at `~/` for global/user defaults, a project-level workspace at `~/Projects/X/` for project-specific rules, and so on. Each is an independent 1:1 manifest↔repo with its own output tree inside *that directory's* tool config paths. aienvs **does not merge** across workspaces; layering across global/user/project scopes is delivered by the **target tools themselves at runtime** (Claude, Cursor, Codex, etc. already merge their own global + user + project configs — aienvs just feeds each level cleanly). Running `sync` always targets exactly one resolved workspace (cwd's nearest `.aienv.yaml`, or an explicit `--workspace <path>`). There is no cross-workspace sync operation in v1.

## Requirements

**Workspace and manifest**

- R1. The CLI can create or adopt a **workspace** rooted at a directory the user chooses (typically a project, a user-home directory, or a dedicated dotfiles-style repo). The workspace is defined by a manifest file named `.aienv.yaml` at that root. **Every invocation operates on exactly one resolved workspace**: default resolution walks up from cwd and selects the nearest `.aienv.yaml`; user may override with `--workspace <path>`. Users may keep multiple `.aienv.yaml` files on disk (e.g. `~/.aienv.yaml` and `~/Projects/X/.aienv.yaml`) — each is an **independent workspace** with its own Git repo and its own output tree. **aienvs does not sync, merge, or cascade across workspaces**; layering across user/project/global scopes is delivered by the target tools' own runtime (see Scope Boundaries).
- R2. The manifest records at minimum: workspace identity, **exactly one canonical configuration Git URL** (and optional branch/ref or commit pin), or a documented **local path** substitute for offline development, plus fields needed to map inputs to adapters including **scope hints** (global / user / project) that inform per-target emission paths (exact schema in planning). **Relationship is 1:1**: one `.aienv.yaml` ↔ one backing repo (or path), not a composite of multiple remotes in v1.
- R3. A workspace can declare which **targets** are active for generation. Targets are organized in **tiers**: **primary** (v1: `claude`, `cursor`, `codex`) — full capability matrix, tested, stable adapter contract, v1-blocker for coverage; **supported** (`gemini`) — best-effort but may ship with known capability gaps; **experimental** (`windsurf`, `lmstudio`, `roo`) — opt-in only (off by default); shipped behind an `experimental: true` flag with explicit warnings that layout or semantics may change between releases. The adapter framework is a v1-stable extension contract so the community can ship out-of-tree adapters from day one. Inactive targets produce no files. Each adapter carries its own version so users can pin an individual adapter independently of the aienvs release.

**Synchronization**

- R4. **Default sync model is explicit and simple**: a `sync` (or equivalent) command updates the **workspace’s** clone of its configured Git remote (fetch/checkout semantics are a planning detail) and **regenerates** per-tool artifacts **under that workspace root**. No daemon is required for the product to be useful.
- R5. **Network / fetch failure**: if the Git remote cannot be updated, **default `sync` fails** and does **not** regenerate from a potentially stale local cache. **Exception — pinned-cached-succeeds**: when the manifest is `commit:`-pinned AND the pinned SHA is already present in the deterministic cache, `sync` succeeds **without network**; no new freshness risk exists in this path. Floating-ref manifests and pins whose SHA is not yet cached still fail on network error. An explicit opt-in flag (e.g. `--offline` / `--use-cache`) may force use of the last materialized clone with **prominent warnings**; exact flag name and exit codes are planning details. (User decision: strict default + pinned-cached exception.)
- R6. **Pinning is the default**: `init` **resolves** the configured ref to a **commit SHA** and writes `commit: <sha>` into `.aienv.yaml`; the symbolic `ref:` may be stored alongside as provenance. Floating refs are **opt-in, not opt-out**: they require **both** `--floating` at `init`/`sync` **and** `floating: true` in the manifest as an explicit acknowledgement. Floating-ref use warns on every invocation (non-interactive: stderr; interactive: stronger prompt). Whether floating is additionally blocked in a strict mode is a planning detail, not a v1 gate. (User decision: resolve at init + opt-in floating.)
- R7. **Clone / cache location**: by default, materialized canonical repos live under a **user cache directory** (XDG-style on Linux, equivalent conventions on macOS/Windows) keyed deterministically so duplicates can be reused. The manifest may optionally override with a **workspace-local** clone path (e.g. for air-gapped or “single directory portability”). Exact path formula and override field name are planning details (user decision: default cache + optional local override).
- R8. **Symlink handling** (v1): discovery of `.aienv.yaml` walks the **logical path** (symlinks preserved as the user navigated them). Writes to emitted outputs are **safely resolved in v1** — `O_NOFOLLOW`-style semantics on POSIX (equivalent on Windows), workspace-root containment check rejecting any resolved target that escapes the workspace root, and refusal to follow a symlink when overwriting a previously managed file. Symlink **cycle** detection and **Windows junction** behavior stay deferred to planning. (User decision: logical discovery, safe writes promoted to v1.)
- R9. **Optional automation with explicit install**: the tool may install Git hooks (post-merge / post-checkout) and/or run a **watch** mode that re-runs generation when manifest or canonical sources change. Hook install requires interactive confirmation **or** an explicit `--install-hooks` flag; silent install is forbidden. v1 hook scripts are **locally generated CLI wrappers** (e.g. `exec aienvs sync --workspace <path>`) — never verbatim content copied from the canonical repo. Installed hooks are **workspace-scoped**, owner-only executable (0700/0755 on POSIX; equivalent restrictive ACL on Windows). **GUI-client gap**: many Git GUIs skip `.git/hooks/*` silently; `aienvs install-hooks` documents this and emits a warning that `aienvs sync` must be run manually after GUI-driven Git operations. Detection (best-effort) of common GUI clients and recommended per-client remediation is a planning detail. **Watch-mode concurrency**: watch and hook invocations coordinate via a **per-workspace lock file** (e.g. `.aienv/state/sync.lock`, PID + timestamp); concurrent `sync` invocations against the same workspace detect the lock, wait up to a bounded timeout, then fail with a clear "another sync in progress" error. Watch mode uses **debounced** re-runs (default ~500ms after last change; configurable in planning). A long-running **daemon** is out of scope for v1 unless deferred items explicitly promote it.
- R10. **Reserved-subdirectory ownership** (generated outputs are ephemeral): adapters write **only into reserved prefixes they own** (e.g. `.claude/aienvs/`, `.cursor/aienvs/`, `.codex/aienvs/`; exact layout per target in planning) and must never read, mutate, or delete anything outside those prefixes. Reserved prefixes **must not nest** across adapters (the framework validates no prefix is an ancestor of another at startup and refuses to run on conflict). Within a reserved subtree, each `sync` overwrites managed files and **deletes orphans** — paths this adapter emitted previously but no longer produces — driven by a per-target **emitted-path ledger** (e.g. `.aienv/state/<target>.json`). **First-sync guard**: when the reserved prefix already exists with content not in any ledger (likely user files predating aienvs), sync refuses and prints an explicit opt-in (`--adopt-prefix` or interactive confirm) that records the existing contents as ledger-tracked before overwriting. **Rename/move** of an IR concept is modeled as delete-at-old-path + create-at-new-path via the ledger — no special-case preservation of editor cursor/state. Agent-written state co-located in the parent directory (session logs, `settings.local.json`, plugin caches) is **out of bounds** for adapters. Hand-edits inside reserved prefixes are not preserved; documentation and CLI messaging must state this clearly. Git-level conflicts in the canonical repo are still surfaced with normal Git errors. (User decision: reserved subdirs + ledger-driven cleanup.)

**Cross-tool translation**

- R11. The system maintains a **tool-agnostic intermediate representation (IR)** for portable concepts and **adapters** that emit native files per target tool. Where a concept has no native equivalent, the adapter documents the gap and applies a **degraded but safe** mapping (skip, stub, or emit a comment warning) rather than silently corrupting config. (The IR is an in-memory compile target; the *canonical source* remains the Git-backed repo referenced by the manifest.) **v1 concept set** (minimum): `agents-md` (root + per-target overlays), `rule` (named prose/markdown chunks addressable by target), `skill` (name + metadata + body + optional assets), `command` (name + prompt/invocation metadata), `plugin-reference` (identifier + version, no install-side-effect in v1), `mcp-server-entry` (name + transport + config). Each IR node carries: stable `id`, `kind`, `version`, source-path provenance, `required: bool`, and a `targets:` allow/deny list. Exact schema (JSON Schema / Go struct definitions) is a planning artifact but the concept set is frozen for v1 capability-matrix purposes. **Executable-output restriction**: v1 adapters emit **declarative content only** (markdown, YAML, JSON, plain text). Emitting executable files (shell scripts, binaries, files with exec bits) requires an explicit per-target justification recorded in that adapter's capability matrix entry; until then, such emissions are rejected by the adapter framework.
- R12. For the initial targets, generation covers the user’s stated pattern: a root `AGENTS.md` (or equivalent shared doc) plus tool-specific overlays (e.g. `CLAUDE.md`, `GEMINI.md`) that **import or include** shared content and add overrides. Exact include mechanism (literal import syntax vs duplicate generation) is deferred to planning if it is purely mechanical.

**CLI and ergonomics**

- R13. The CLI is implemented in **Go** and uses **Bubble Tea** for interactive flows. Wizards cover `init` and `target selection`. Each wizard is **fully specified** in planning: entry preconditions, every branching state (fresh directory / existing manifest / ancestor workspace detected / remote unreachable / local-path mode / zero targets / pre-existing target outputs), the text shown, validation rules, the equivalent non-interactive flag or env var for every prompt, and per-state exit codes. **Every interactive prompt has a corresponding flag**: a master `--yes` / `--non-interactive` mode fails fast when any decision lacks an explicit value rather than picking a silent default. No conflict-resolution wizard is required because R10's reserved-subdir ownership + ledger model removes the conflict classes it would have handled.
- R14. Commands are discoverable via `--help`; common flows are: initialize workspace, link remote, select targets, sync, validate (dry-run diff of what would change), and optionally install hooks / run watch. CLI accessibility basics: honor `NO_COLOR` / auto-detect TTY; status tokens use ASCII text prefixes (OK/FAIL/SKIP) alongside any color; every wizard supports keyboard-only navigation; no reliance on Unicode for core status; reflow at 80 columns minimum.

**Cross-platform**

- R15. The same CLI behavior works on **macOS, Windows, and Linux** for path handling, file permissions, and hook installation paths appropriate to each OS.

**Reliability and reporting**

- R16. **Atomic sync by default**: `sync` **stages all adapter output to a scratch area and swaps into place on full success**; if any enabled adapter fails, no target's output tree is mutated. `--best-effort` is an explicit opt-in that reverts to per-target independence: successful targets keep updated outputs, failed targets are skipped, non-zero exit on any failure, per-target summary printed so mixed state is intentional and visible. In both modes the CLI prints a **per-target summary** (status token: ok / failed / skipped / unchanged; file-change counts; elapsed time) with failure-cause taxonomy aligned to Git / merge state / adapter mapping; `--output=json` provides a machine-readable contract for CI. The summary includes each target's **resolved absolute output paths** so users know exactly where files landed (especially after symlink-logical discovery). (User decision: atomic default + best-effort opt-in.)

**Trust and safety**

- R17. **Trust-on-first-use (TOFU) for canonical sources**: before the first sync that materializes a given `(canonical URL, commit SHA)` pair, `sync` requires **interactive confirmation** *or* an explicit `--accept-new-source` flag (non-interactive). Trusted pairs are persisted in a **per-OS-user trust record** under the user cache/config directory. Subsequent syncs that resolve to a SHA **outside the previously trusted range** for that URL re-prompt; a URL change re-prompts. The trust record is **per OS user** (not per workspace), so a teammate opening a shared repo faces the gate even though the manifest is already in Git. Local-path canonical sources are only accepted if the path is owned by the invoking user; otherwise `sync` refuses. (User decision: TOFU on URL+SHA.)

## Success Criteria

- A developer can point the CLI at a repo root, run `sync`, and get **up-to-date native configs** for all enabled targets from one Git-backed manifest without hand-copying skills between tools.
- Drift is **obvious**: `validate`/diff shows what would change before writing; sync failures explain whether the problem is Git, merge state, or adapter mapping.
- **Capability presence, not file presence**: for every portable concept declared in the canonical repo, each enabled target **either exposes an invocable/loadable equivalent after `sync`, or is reported by the adapter's capability matrix as explicitly unsupported** for that concept. Every `sync` emits a machine-readable **capability report** (e.g. `.aienv/state/capability-report.json`) with shape `{workspace, commit, generated_at, targets: [{target, version, concepts: [{id, kind, status: mapped|degraded|skipped|unsupported, output_paths: [], notes}], required_unmet: []}]}`; `sync` **fails for any enabled target with non-empty `required_unmet`**, preventing false parity.
- A commit-pinned canonical repo resyncs on another machine to a byte-identical set of adapter outputs, given the same enabled-targets list.

## Scope Boundaries

- Not a full **secrets manager**; handling of API keys and tokens stays documented integration patterns only unless explicitly added later.
- Not a replacement for **Git**; hosting, PR workflow, and code review happen in normal Git tooling.
- **Bi-directional import** from existing messy per-tool configs into the canonical model is optional/nice-to-have for v1 — forward generation from canonical → native is the must-have.
- **Real-time multi-user collaboration** on live config is out of scope; polling or hooks are sufficient.
- **Layering across scopes is delegated to target tools**: aienvs does not merge content across global / user / project workspaces. Users who want "org baseline + personal overlays" install them as separate workspaces at the matching filesystem scope; the target tool (Claude, Cursor, Codex, …) does the runtime merge as part of its own config resolution. Composable, in-aienvs manifest layering remains deferred past v1.
- **Aienvs does not sync across workspaces**: there is no multi-workspace, `--cascade`, or ancestor-sync operation in v1. Each workspace is synced by its own explicit invocation.

## Key Decisions

- **Sync simplicity**: Prefer **explicit `sync` + optional hooks/watch** over a mandatory daemon. Revisit only if users need sub-minute propagation without running a command.
- **One explicit workspace per invocation**: no cross-workspace sync, no `--cascade`. Target tools do the global/user/project layering at runtime; aienvs feeds each scope cleanly (user decision).
- **Reserved-subdirectory ownership**: adapters write only into dedicated `aienvs/` prefixes inside each tool's config dir; sync overwrites managed files **and deletes orphans** via a per-target emitted-path ledger; agent-written state outside the reserved prefix is out of bounds (user decision).
- **1:1 manifest and remote**: Each `.aienv.yaml` references **one** Git repository (or local path); outputs install **next to that manifest** (user decision).
- **Adapter tiers**: primary (claude, cursor, codex) with full capability matrix as v1 blocker; supported (gemini); experimental and opt-in (windsurf, lmstudio, roo); v1-stable extension contract so community adapters can ship out-of-tree (user decision).
- **Offline default strict with pinned-cached exception**: fetch failure fails `sync` unless the manifest is `commit:`-pinned AND the SHA is already cached (then no network is required); `--offline` opt-in for explicit cache-only runs (user decision).
- **Pinning is the default**: `init` resolves the configured ref to a commit SHA and writes it into `.aienv.yaml`; floating refs require both a `--floating` flag and an explicit manifest acknowledgement (user decision).
- **First-use trust (TOFU)**: a new `(URL, SHA)` pair requires interactive confirmation or `--accept-new-source`; trust is persisted per OS user; teammates reusing a shared manifest face the gate independently (user decision).
- **Safe automation**: optional hook install is gated on explicit confirm/flag; hook scripts are locally generated CLI wrappers only; adapter output is declarative-only in v1 (no executables without per-target justification) (user decision).
- **Clone storage**: **User cache by default**; optional **workspace-local override** in manifest (user decision).
- **Symlinks**: logical-path discovery for `.aienv.yaml`; **safe write-path resolution in v1** (no-follow writes, workspace-root containment); cycle detection and Windows junctions remain deferred (user decision).
- **Atomic sync by default**: stage + swap; `--best-effort` opt-in for per-target independence (user decision).
- **Capability presence over file presence**: success is measured by per-concept capability reported by each adapter, not by file existence; canonical repo may mark concepts `required: true` and sync fails if any enabled target cannot handle them (user decision).
- **Canonical vs native**: Treat **Git-tracked canonical sources** as the durable truth; native files are **generated outputs**.
- **Translation strategy**: Adapters per target with explicit capability matrices (what maps, what degrades) to avoid false parity between ecosystems.

## Dependencies / Assumptions

- Users are comfortable with Git and YAML manifests.
- Each target tool’s config locations and formats are stable enough to wrap; where vendors change layouts, adapters version independently (assumption for planning).

## Outstanding Questions

### Resolve Before Planning

_(empty — ready for planning)_

### Deferred to Planning

- [Affects R8][Technical] **Symlink cycle detection and Windows junctions**: safe writes are v1; cycle detection during discovery and junction behavior on Windows remain planning items.
- [Affects R12][Technical] Exact **include/import** mechanism for `AGENTS.md` → `CLAUDE.md` / `GEMINI.md` (symlink, codegen header, or tool-native feature).
- [Affects R3, R11][Needs research] Per-target **capability matrix** for primary adapters (claude, cursor, codex) is a v1 blocker; matrices for supported (gemini) and experimental (windsurf, lmstudio, roo) tiers are planning items.
- [Affects R9][Technical] Hook installation strategy per OS and Git client (including GUI-client detection and recommended per-client remediation messaging).
- [Affects R9][Technical] Watch-mode debounce window and lock-file format/timeout defaults.
- [Affects R2][Technical] Exact manifest schema including `scope:` hints (global / user / project), `floating:` ack, per-adapter version pins, and the TOFU trust-record format.
- [Affects R7][Technical] Deterministic cache-key formula (inputs: canonical URL canonicalization, case handling, auth-stripping, trailing-slash normalization).
- [Affects R11][Technical] Final JSON Schema / Go struct definitions for the IR concept set (the frozen concept list in R11 is sufficient for planning).
- [Affects R16][Technical] Exact `--output=json` schema for the per-target sync summary and its stability contract across versions.

## Next Steps

`Resolve Before Planning` is empty → `/ce:plan` for structured implementation planning.

## Alternatives Considered

- **Daemon-first sync**: Rejected for v1 due to complexity; optional watch covers local edit feedback.
- **Native files as source of truth**: Rejected as default because it preserves duplication; optional import path could exist later.
- **Multiple Git remotes per manifest (org + personal layers)**: Deferred past v1 in favor of **1:1 manifest ↔ repo** plus **target-tool runtime layering** across separate workspaces at global/user/project scopes. May revisit if users need composable baselines inside a single output tree.
- **Symlink-only dotfiles managers (stow / chezmoi / yadm) + AGENTS.md convention**: considered as the null hypothesis. Rejected because (a) translation across heterogeneous per-tool directory structures (`.claude/skills/`, `.cursor/rules/`, codex skills layout, …) is beyond what symlink managers express, (b) overwrite-safety, orphan deletion, TOFU, and capability reporting are out of scope for general-purpose dotfiles managers, and (c) the AGENTS.md convention only addresses the *shared prose* surface, not skills/commands/plugins. Aienvs is scoped where the convention ends.

## Document review notes (inline pass)

- **Scope**: Forward generation emphasized; reverse import deferred — aligns with Scope Boundaries.
- **Security**: Supply-chain hardening is now a v1 requirement, not a hardening pass — pin-at-init (R6), TOFU on (URL, SHA) (R17), declarative-only adapter output (R11), gated hook install with locally generated wrappers (R9), reserved-subdir ownership (R10).

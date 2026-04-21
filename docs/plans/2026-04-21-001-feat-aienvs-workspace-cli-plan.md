---
title: "feat: aienvs agent-workspace CLI v1"
type: feat
status: active
date: 2026-04-21
deepened: 2026-04-21
origin: docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md
---

# feat: aienvs agent-workspace CLI v1

## Overview

Build the v1 of `aienvs` — a Go CLI that keeps AI-agent configuration for multiple tools (v1 primary: Claude, Cursor, Codex) in sync from a single Git-backed manifest (`.aienv.yaml`). This plan turns the approved requirements doc into concrete, dependency-ordered implementation units for a greenfield repository.

The architectural essence is a four-stage pipeline — **materialize → compile → stage → swap** — with a **two-mode ownership model** (reserved-subdirectory ownership where the target tool supports it + per-entry ledgered merges into tool-owned files where it doesn't), ledger-driven orphan deletion in both modes, a **two-tier trust model** (committed `trusted_sha:` in `.aienv.yaml` for CI plus per-user `trust.jsonl` TOFU history for interactive work), and a v1-stable adapter extension contract — **LSP-framed JSON-RPC 2.0 over stdio** with initialize/initialized lifecycle, capabilities, declared outputs, cancel/shutdown, progress tokens, per-op timeouts, structured errors, and a magic-cookie handshake — so the three primary adapters and any out-of-tree third-party adapter (Go, Python, or otherwise) speak the same protocol from day one.

## Problem Frame

See origin doc (`docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md`). Summary: developers maintaining multiple coding agents accumulate duplicated, drifting per-tool config. aienvs provides one manifest, one Git repo, one workspace → native files in each tool's reserved subdirectory, with supply-chain-hardened sync and capability-honest translation.

## Requirements Trace

Traceability back to origin `R#`:

- R1 → Units 2, 3, 16, 17 (workspace definition, discovery, CLI + wizard surface)
- R2 → Unit 2 (manifest schema + writer)
- R3 → Units 8, 9, 10, 11, 20 (tiered adapters + extension contract)
- R4, R5 → Units 5, 13, 18 (sync model, offline strict, pinned-cached-succeeds)
- R6 → Unit 5 (pin-at-init, floating opt-in)
- R7 → Unit 4 (cache + URL canonicalization)
- R8 → Units 1, 3, 13 (safe writes, logical discovery, cycle-safe walk)
- R9 → Unit 19 (hooks + watch with explicit install)
- R10 → Units 12, 13, 14 (reserved-subdir ownership, ledger, orphan/adopt flow)
- R11 → Units 7, 8 (IR + declarative-only adapter output)
- R12 → Units 9, 10, 11 (AGENTS.md + per-tool overlays)
- R13 → Units 16, 17 (Go + Bubble Tea, fully spec'd wizards, flag parity)
- R14 → Unit 16 (command surface + accessibility)
- R15 → Units 1, 13, 21 (cross-platform via `os.Root` + CI matrix)
- R16 → Unit 15 (atomic default, `--best-effort`, per-target summary, JSON contract)
- R17 → Unit 6 (TOFU trust store)

Success criteria: covered in unit 15 (capability report artifact), plus unit 18 (validate diff).

## Scope Boundaries

- Not a secrets manager; secret references stay documented integration patterns (origin: Scope Boundaries).
- Not a Git replacement; canonical repo hosting and PR workflow live in normal Git tooling.
- No reverse import from messy per-tool configs into canonical form in v1.
- No cross-workspace sync, `--cascade`, or ancestor-sync in v1 (origin: Scope Boundaries). Target tools do global/user/project layering at runtime.
- No runtime daemon; watch + hooks only (origin: R4, R9).
- No in-process Go `plugin` package; adapters are subprocesses with a JSON contract (see Key Technical Decisions).
- No on-the-wire encryption, signed-commit enforcement, or sigstore attestation verification in v1 (design hook only).

### Deferred to Separate Tasks

- Supported-tier `gemini` adapter and experimental-tier `windsurf`, `lmstudio`, `roo` adapters: separate PRs after primary adapters land. Their capability matrices are planning items per origin Deferred list.
- Central adapter registry: defer past v1; discovery stays GitHub-topic-based (`aienvs-adapter`).
- `--cascade` / ancestor sync: explicitly out of scope.
- Symlink cycle detection beyond a bounded hop limit, and Windows-junction deep handling: origin Deferred; v1 ships with a bounded walk as the safety net.

## Context & Research

### Relevant Code and Patterns

Greenfield repo — no prior internal patterns to follow. External patterns to mirror:

- Capistrano + nix-community/home-manager: stage-to-scratch + two-rename swap for atomic deploy.
- OpenSSH `known_hosts`: append-only per-user TOFU store with hard-block-on-mismatch.
- Terraform provider plugin protocol: separately versioned subprocess RPC contract.
- LSP `ServerCapabilities` + CSI capability RPCs: explicit capability matrices per adapter.
- Helm plugins + gh CLI extensions: subprocess adapter contract with declared `adapter.yaml` manifest.
- Terraform `plan`/`apply`: read-only preview as the primary debugging surface.
- Cobra + Fang integration pattern used by Charm's own CLIs.
- Go cmd/go's `codehost.WorkDir` (`SHA256("git:" + canonical_url)`) for deterministic cache keys.

### Institutional Learnings

None (no prior `docs/solutions/`). Any learnings generated during implementation land here.

### External References

- [Go 1.24+ `os.Root`](https://pkg.go.dev/os#Root) — containment-checked filesystem operations; v1 depends on it.
- [go-git v5.17.x](https://pkg.go.dev/github.com/go-git/go-git/v5) — read-only layer for cached clones.
- [Charm v2 stack](https://charm.land/) (`bubbletea/v2`, `bubbles/v2`, `lipgloss/v2`, `huh/v2`, `fang/v2`).
- [goccy/go-yaml](https://github.com/goccy/go-yaml) — strict parsing + comment-preserving round-trip writer.
- [gofrs/flock](https://github.com/gofrs/flock) — cross-platform file locks.
- [adrg/xdg](https://github.com/adrg/xdg) — XDG-style paths on Linux/macOS/Windows.
- [Terraform plugin protocol](https://developer.hashicorp.com/terraform/plugin/terraform-plugin-protocol) — reference for the versioned adapter contract.
- [Renovate `config:best-practices`](https://docs.renovatebot.com/upgrade-best-practices/) — SHA-pinning guidance.
- CVE-2025-0913 / Go 1.25 safe-create semantics on Windows symlink targets.

## Key Technical Decisions

The requirements doc left several technical details to planning. All resolved here; nothing is a hidden unknown. *Decisions marked [deepened] were tightened during the 2026-04-21 confidence pass with cited research — see Sources & References.*

1. **Go 1.25 minimum.** `os.Root` + CVE-2025-0913 fix + cross-platform reparse-point detection require it. Rationale: filesystem safety is the highest-risk surface; accepting an older toolchain means rebuilding this from scratch.
2. **CLI framework: Cobra v1.9 + Fang v2.** Largest ecosystem, shell completions + man pages for free, pairs cleanly with Bubble Tea. Kong and urfave/cli evaluated; Cobra wins on ecosystem depth for a 10+ subcommand surface.
3. **TUI: Bubble Tea v2 + bubbles v2 + lipgloss v2 + huh v2** for wizards, with a small hand-rolled stack navigator for the multi-screen init flow. Accessibility: `huh.WithAccessible` + honor `NO_COLOR`.
4. **Git materialization: hybrid.** Shell out to `git` for all network operations (auth, credential helpers, SSH config, LFS, sparse checkout — things go-git can't do reliably). Use go-git v5.17 only for read-only operations on the cached clone. Keep the seam narrow so pure-go can replace the shell-out later if go-git's credential story improves.
5. **YAML: goccy/go-yaml v1.19.x** with `DisallowUnknownField()` + `AllowFieldPrefixes("x-")` for forward-compat extension keys. `CommentMap` used when `init` writes the resolved SHA back into the manifest.
6. **Filesystem safety — `os.Root` at the workspace-parent level [deepened].** Use **one `os.Root` scoped at a common ancestor** (typically `<workspace>/.claude/`, `<workspace>/.cursor/`, etc. — the parent of the reserved prefix) rather than one `os.Root` at the reserved prefix itself. Rationale (from Go 1.25 release notes + [Traversal-resistant file APIs](https://go.dev/blog/osroot)): on Windows, an open `os.Root` *handle holds its directory against rename* — a per-reserved-prefix `os.Root` would block its own swap. A parent-root also lets the staging directory live as a sibling (`<workspace>/.claude/.aienv-staging/<ts>/`) so step 1 (`aienvs/ → aienvs.old`) and step 2 (`.aienv-staging/<ts>/claude → aienvs`) happen inside a single root; Go 1.25 has no cross-root rename primitive ([proposal #73041](https://github.com/golang/go/issues/73041)). Generations root accordingly moves under each target's parent (see decision #20). Post-swap, per-reserved-prefix read-only `os.Root` handles are fine — the constraint applies only during the swap window.
7. **File locking: `gofrs/flock` v0.13** with `TryLockContext` + bounded timeout. One lock per (workspace, target); cache directory has its own.
8. **Adapter extension contract [deepened]: LSP-style `Content-Length` framing + JSON-RPC 2.0 body** over stdin/stdout, protocol identifier `aienvs/v1` carried in `Content-Type: application/aienvs-v1+json; charset=utf-8`. Rationale ([LSP 3.17 base protocol](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/), [MCP 2025-11-25 lifecycle](https://modelcontextprotocol.io/specification/latest/basic/lifecycle)): length-prefixed headers are 8-bit-clean, debug-friendly (`cat`/`tee`/`tail`), survive partial writes, and are the de-facto standard adopted by every JSON-over-stdio protocol shipped by Microsoft, Anthropic, and HashiCorp-ish successors. Bundled adapters (claude/cursor/codex) still run via in-process shim but speak the same wire protocol for parity. **Required v1 surface before freeze** (each item sourced from prior-art gap analysis, see Sources):
    - Transport: `Content-Length:`/`Content-Type:` headers + JSON-RPC 2.0 request/response/notification envelope.
    - **`initialize` → `initialized`** lifecycle (LSP/MCP): server must not emit real ops before receiving `initialized` notification.
    - **Server counter-proposes version** on mismatch (MCP rule): adapter replies with its highest supported protocol, CLI decides to proceed or refuse.
    - **`capabilities` object** in the initialize result so per-concept support flags evolve without bumping `v1`→`v2`.
    - **`declared_outputs` list** in the initialize result (Bazel `declare_file` pattern): adapter announces its full planned output set upfront so the CLI can detect undeclared writes mid-stream and refuse.
    - **`cancel` notification** with JSON-RPC id (analog of LSP `$/cancelRequest`) and **`shutdown`** request (analog of Terraform `Stop`) before SIGTERM.
    - **Progress tokens** on long-running emits (LSP `WorkDoneProgress` begin/report/end), so a crashed adapter is reported as "exited after N/M planned writes" rather than silent hang.
    - **Per-op timeouts** with sensible defaults (5s handshake, 30s per op — matching k8s mutating-webhook conventions), configurable per adapter via `adapter.yaml`.
    - **Structured error envelope** using JSON-RPC 2.0 error codes plus LSP-specific extensions (`-32800 RequestCancelled`, `-32801 ContentModified`, `-32803 RequestFailed`) so callers can branch on machine-readable `code`.
    - **Magic-cookie handshake** (copying `hashicorp/go-plugin`): CLI sets an env var per spawn and the adapter's `Hello`/`initialize` must echo it, so running `./aienvs-adapter-foo` directly at a shell refuses.
    - **Reserved `_meta` object** on every frame (MCP convention) for forward-compatible metadata additions.
    - **Stdout reserved for framed bytes only** (LSP rule); stderr is free-form diagnostic channel that the CLI captures and surfaces in failure reports.
   Rejected alternatives: `hashicorp/go-plugin` gRPC (heavier than needed, Go-biased); length-delimited raw JSON with no headers (no content-type, no charset tagging); NDJSON (bans embedded newlines); Go stdlib `plugin` (cross-platform-broken); WASM/wazero (Go→WASI tooling immature in 2026).
   Discovery unchanged: `aienvs-adapter-<name>` binary on `PATH` or explicit `adapters:` entry in manifest; bundled primaries compiled in but also installable as external binaries so third parties see a complete working example. **Declarative-only output** remains the wire contract (CLI performs all writes inside the parent `os.Root`), but is documented as *not a security boundary* against hostile adapters — a malicious subprocess can always fork and write directly; supply-chain trust (TOFU + SHA pin at install time) is the real defense.
9. **TOFU — committed trust sidecar as primary CI pattern [deepened]** (replaces the per-invocation `--accept-new-source=<sha>` model). Two stores with two purposes:
    - **Per-project pin (authoritative for CI):** `trusted_sha:` field in `.aienv.yaml`, committed to the repo alongside `commit:`. Sync in non-interactive/CI mode requires `trusted_sha` to match the resolved SHA exactly; any drift is a hard failure with no prompt. This is the `npm ci` / `go.sum` pattern — trust is established once on a human's machine, enforced mechanically in CI. `aienvs trust pin` is the subcommand that writes `trusted_sha:` into the manifest; `aienvs trust verify` is the read-only CI gate.
    - **Per-user history (authoritative for interactive cross-project recall):** `trust.jsonl` append-only log at `xdg.DataHome()/aienvs/trust.jsonl`, mode 0600. Schema: `{"ts","op","url","sha","prev_sha","source","actor","hostname"}` where `op ∈ {trust, promote, revoke, allow-new-shas-on, allow-new-shas-off}`. Current state is the fold over the log (`aienvs trust status`), matching SSH `known_hosts`/`go.sum` precedent. `aienvs trust compact` rotates oversized logs. **No HMAC/cryptographic integrity in v1** — document honestly that the store is only as trustworthy as the user account, and defend the supply-chain surface at `trusted_sha` instead.
    Three mismatch classes with **distinct error classes and exit codes** (from [SSH/go.sum/gh-attestation UX comparison](https://blog.gitguardian.com/renovate-dependabot-the-new-malware-delivery-system/)):
    - `RevokedTrustAnchor` (exit 3) — SSH-style red banner, **no prompt regardless of TTY**, names the offending file + remediation (`aienvs trust reset <url>`).
    - `TrustDecisionRequired` (exit 4) — first-URL or SHA-update in a non-TTY context: exits immediately with a flag hint (`rerun with --accept-new-source=<sha> or commit trusted_sha`). Never hangs on a prompt.
    - `FirstUseDenied` (exit 5) — user declined the interactive prompt.
    Interactive SHA-update **no longer prompts inside the sync flow**; instead sync emits a one-line reminder and a new **`aienvs trust pending` / `trust diff` / `trust promote`** batch-review command handles promotions out of band (Dependabot/Renovate pattern — trust decisions are audit-grade review, not hot-path friction). `trust-all-new-shas` gains an optional **cooldown** (`minimumReleaseAge`, Renovate pattern) instead of being a raw bypass.
10. **`--accept-new-source` is SHA-scoped and audited [deepened].** Flag shape unchanged: `--accept-new-source=<sha>` / `--accept-new-source=<url>:<sha>`. Every successful use echoes `Trusting new source: URL=<url> SHA=<sha>` to **stderr** before proceeding (gh audit-trail principle — disable the prompt, not the announcement). `--accept-new-source=any` now requires a **peer gate**: either `AIENVS_ALLOW_UNSAFE_ANY=1` in env *or* `--i-understand-this-is-dangerous` on the command line. This follows the [gh#12033 lesson](https://github.com/cli/cli/issues/12033) that single-flag safety overrides produce recurring bug reports. `--yes` / non-interactive flags are no-ops without an explicit resource argument.
11. **`--adopt-prefix` is backed by a backup tarball.** Before adoption writes to the ledger, aienvs writes `.aienv/state/backups/<target>-<timestamp>.tar.gz` containing everything in the reserved prefix. Adoption then proceeds. Interactive flow requires **typed target name** (`claude`, not `y`) per gh/Stripe CLI convention. `--adopt-prefix-no-backup` exists for users who understand. All-or-nothing per prefix; selective preservation requires manual file-move first. Symmetric destructive command `aienvs trust revoke <url>` also requires typed-name confirmation.
12. **Mid-life drift uses the same guard.** If between syncs a user creates a file inside a reserved prefix not tracked by the ledger, `sync` refuses identically to the first-sync guard. Invariant: the ledger-or-adopt rule holds for the entire lifetime of the workspace, not just on first sync.
13. **Ledger missing/corrupted/schema-drift.** Missing or JSON-corrupted ledger triggers the first-sync-guard path (everything in reserved prefix treated as user content needing adoption). Older-schema ledger requires `--migrate-state` or interactive confirm; silent auto-migration is forbidden. Ledger records **content hash** per emitted path so hand-edit drift is detectable by `validate`.
14. **`validate` semantics.** Shares `sync`'s network posture: online if floating + network; cache-only if pinned + cached; fails offline unless `--offline`. Flags `--no-fetch` / `--fetch` override. Exit codes: `0` no drift, `1` drift detected (clean), `2+` operational error. `--output=json` mirrors sync summary schema plus a `diff` node. Capability report assembled in-memory for reporting but **not written to disk** (preserves read-only posture). `--diff` adds unified-diff body for text files.
15. **Symlink-walk terminus & cycle safety.** Walk-up terminates at **filesystem root** by default (picks up `~/.aienv.yaml`). `--workspace-stop-at=<path>` and `AIENVS_WORKSPACE_STOP_AT` env var override. Bounded-hop cycle safety: refuse after 64 parent traversals (documented) — full inode-based cycle detection remains deferred per origin R8.
16. **`init` offline behavior.** `init` requires network to resolve ref → SHA. `--defer-resolve` writes the manifest without `commit:` and makes the first `sync` do the resolve (which is itself TOFU-gated). `--floating` + `--defer-resolve` behave identically for the resolve step.
17. **`init` inside an enclosing Git worktree.** Detected automatically; `init` proposes concrete `.gitignore` entries (`.aienv/state/`, `.aienv/generations/`, `.aienv-staging/`, and any tool's `aienvs/` subtree the user doesn't want committed) and refuses unless accepted or `--no-gitignore-update` is passed.
18. **`init` rejects credentials in URL.** `https://user:token@host/…` surfaces a specific error with a documented remediation (git credential helpers / SSH). Cache-key canonicalization strips userinfo regardless.
19. **Ancestor-workspace nesting policy.** `init` detects an existing `.aienv.yaml` up the tree; requires `--allow-nested` or interactive confirm to proceed. Message names both paths so shadowing is intentional.
20. **Atomic-sync pipeline [deepened] — staging-under-target + syscall-truth error taxonomy.** Stage to `<workspace>/<target-parent>/.aienv-staging/<iso-timestamp>-<short-sha>/<target>/` (same parent directory as the live reserved prefix — e.g., `.claude/.aienv-staging/.../claude/` stages next to `.claude/aienvs/`). This makes cross-FS-by-construction impossible and lets a single parent `os.Root` (decision #6) span both step-1 and step-2 renames. Two-rename swap: `<prefix>` → `<prefix>.old`; `<staging>/<target>` → `<prefix>`; best-effort `RemoveAll <prefix>.old`. **Cross-FS detection uses rename-syscall result as truth, not a statfs pre-flight** (sources: [Linux bind mounts / btrfs subvols / overlayfs all return `EXDEV` despite matching `f_fsid`](https://serverfault.com/questions/327447), [macOS firmlinks cause `EXDEV` even within one APFS container](https://github.com/anthropics/claude-code/issues/40351)). `statfs`/`FILE_ID_INFO` is used only to *enrich* error messages, never to gate the operation. **Error taxonomy** (surfaced as named Go error types): `ErrLocked` (Windows `SHARING_VIOLATION`/`ACCESS_DENIED` — retry up to 5× at 100ms/250ms/500ms/1s/2s with jitter; `--diagnose` opt-in calls Restart Manager enumeration to name the blocking process but **never** auto-`RmShutdown`), `ErrCrossVolume` (`EXDEV`/`ERROR_NOT_SAME_DEVICE` — abort, name both volumes, reference staging-under-target remediation), `ErrStale` (leftover `.old` or `.aienv-staging/*` from a prior crash — run recovery state machine), `ErrPermission` (non-retryable ACL). **Recovery state machine** persists a `<staging>/.state` sentinel with states `intend(ts)` → `step1_done(ts)` → `step2_done(ts)`; startup reconciles by rolling forward on `step1_done` (complete step 2) and rolling back on `intend` (discard staging). Retain the last 3 failed generations under `.aienv-staging/` for forensics; `aienvs sync --clean-scratch` force-clears. **Windows `os.Root` lifecycle:** any read-only per-reserved-prefix root must be closed before step 1 and reopened after step 2 — the parent-root (decision #6) is the only root that stays open across the swap window.
21. **Required-unmet + capability report.** Capability report is assembled during staging and made available regardless of whether swap succeeds. On required_unmet failure, aienvs still writes the in-memory report to `.aienv/state/capability-report.json` so users can see what's missing even after atomic rollback. Best-effort does not weaken this — required-unmet is non-negotiable.
22. **Multi-workspace isolation invariant** (promoted to a hard invariant): *a single invocation reads and writes only within the resolved workspace root plus the user-level cache and trust records — never another workspace's manifest, ledger, reserved prefixes, or state directories.*
23. **Emitted-file markers.** Every emitted file where the format permits a comment gets `# Managed by aienvs — do not edit. Source: <url>@<short-sha>. Regenerate: aienvs sync` at the top. Formats without comment syntax (strict JSON) get a sidecar `.aienvs-managed` marker in the same directory. Every reserved prefix gets a `README.md` on first emission explaining ownership and the exit path (`aienvs unmanage <target>`).
24. **`aienvs unmanage <target>` exists from v1.** Uses the ledger to cleanly remove emitted files and the ledger. Refuses on drift unless `--force`. "Exit is easy" is a design constraint.
25. **Per-tool ownership model [deepened] — reserved subdirectories where viable, tool-owned files via per-entry ledger elsewhere.** The "every concept lives under `.<tool>/aienvs/`" symmetry in the origin requirements is *partially fiction*: three of the six IR concept kinds land in files the tool itself owns (Claude `.mcp.json` at repo root, Cursor `.cursor/mcp.json`, Codex `.codex/config.toml`, plus repo-root `AGENTS.md` for Cursor/Codex). The plan therefore supports **two ownership modes** simultaneously:
    - **Reserved-subdirectory mode** (preferred): adapter owns `.<tool>/<subsystem>/aienvs/` as a namespace. Verified viable for Claude/Cursor rules (`.claude/rules/aienvs/<id>.md`, `.cursor/rules/aienvs/<id>.mdc`) and Claude commands (`.claude/commands/aienvs/<id>.md`, which get free `/aienvs:<id>` slash-command namespacing). Orphan deletion is ledger-driven inside this namespace. **Skills use name-prefix isolation, not subdirectory nesting** (e.g., `.claude/skills/aienvs-<id>/SKILL.md`) because Cursor and Claude's skill discovery requires `<skill-folder-name> == skill-name`.
    - **Per-entry-in-tool-owned-file mode**: concepts that land in `.mcp.json`, `.cursor/mcp.json`, `.codex/config.toml`, or repo-root `AGENTS.md` are tracked via **per-entry ledger records** that name the containing file and the JSON/TOML path (e.g., `$.mcpServers.aienvs_<id>`) or AGENTS.md section marker (`<!-- aienvs:begin id=… -->` / `<!-- aienvs:end id=… -->`). Orphan removal edits the file in place, preserving non-aienvs entries. Emit uses a read-merge-write pattern under a per-file lock; the ledger's content hash covers the aienvs-owned slice, not the whole file.
    The adapter framework (unit 8) exposes a `write_tool_owned` op distinct from `write_file` so this mode is first-class rather than a workaround. The per-tool concept→destination mapping is authoritative in each adapter's `docs/adapters/<tool>.md` and encoded in its `capabilities.yaml`. See Units 9/10/11 for the specific mappings; several origin assumptions (Claude reads `AGENTS.md`, Codex reads `.codex/skills/`, all tools support an aienvs/ subdir symmetrically) are now explicitly reversed based on official-docs research.

## Open Questions

### Resolved During Planning

- TOFU trusted-range semantics → decision #9 above.
- `--adopt-prefix` safety → decision #11.
- Ledger drift handling → decisions #12, #13.
- `validate` network + output contract → decision #14.
- Symlink-walk stop + cycle safety → decision #15.
- `init` offline, Git-worktree enclosure, credential rejection, nesting → decisions #16–#19.
- Atomic-sync mechanics → decision #20.
- Required-unmet + capability report persistence → decision #21.
- Multi-workspace isolation → decision #22 (promoted to invariant).

### Deferred to Implementation

- Exact Bubble Tea screen-tree shape for the init wizard (will evolve during unit 17 as prompts surface; the wizard *specification* is a planning artifact but the internal model tree is an implementation detail).
- Final YAML key names for the manifest schema (drafted in unit 2; may be tuned during adapter integration in units 9–11).
- Capability-report JSON field names (shape locked in plan; exact string keys finalized in unit 15).
- Exact CI matrix sharding (linux/macOS/Windows × amd64/arm64 is the intent; whether Windows/arm64 lands in v1 CI depends on runner availability at implementation time).
- Whether go-git read operations warrant a build-tag-gated "shell-only" mode for platforms where go-git has bugs (finalized in unit 5 based on real-world test results).

## Output Structure

```
.
├── cmd/
│   └── aienvs/
│       └── main.go
├── internal/
│   ├── manifest/            # .aienv.yaml schema, loader, comment-preserving writer
│   ├── workspace/           # discovery, walk, terminus, containment
│   ├── fsroot/              # os.Root wrapper, safe-write primitives
│   ├── cache/               # deterministic cache keys, URL canonicalization
│   ├── git/                 # hybrid: shell-out network + go-git read
│   ├── trust/               # TOFU store (known_hosts-style)
│   ├── ir/                  # v1 IR concept set + decoder
│   ├── capmatrix/           # capability matrix types
│   ├── adapter/             # adapter framework, subprocess protocol, in-process shim
│   │   ├── contract/        # protocol v1 JSON schemas + handshake
│   │   ├── bundled/
│   │   │   ├── claude/
│   │   │   ├── cursor/
│   │   │   └── codex/
│   │   └── conformance/     # shared test kit
│   ├── ledger/              # per-target ledger, schema versioning
│   ├── locks/               # gofrs/flock wrappers
│   ├── sync/                # staging, atomic swap, rollback, orphan+adopt
│   ├── report/              # per-target summary, capability report, JSON schema
│   ├── cli/                 # cobra tree, fang wiring, non-interactive fail-fast
│   ├── tui/                 # bubble tea wizards, huh forms, accessibility
│   ├── validate/            # dry-run surface
│   ├── hooks/               # posix-sh wrapper install/uninstall
│   └── watch/               # debounced watcher
├── pkg/
│   └── adapterkit/          # public SDK for out-of-tree adapter authors
├── docs/
│   ├── brainstorms/
│   ├── plans/
│   ├── solutions/
│   ├── adapter-authoring.md
│   ├── capability-matrix.md # generated from each adapter's capabilities
│   ├── threat-model.md
│   └── spec/
│       ├── ir-v1.md
│       └── adapter-protocol-v1.md
├── testdata/
├── .github/workflows/
├── .gitignore
├── .golangci.yml
├── AGENTS.md
├── CLAUDE.md
├── README.md
├── go.mod
└── go.sum
```

This is the intended shape for v1. The per-unit file lists are authoritative for what each unit creates; the implementer may adjust the layout if implementation reveals a better one.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

### Pipeline

```mermaid
flowchart LR
    M[.aienv.yaml] --> D[Discover workspace]
    D --> R[Resolve canonical source]
    R -->|network + TOFU| C[(Cache: deterministic SHA-256 keyed)]
    C --> I[Compile to IR v1]
    I --> A1[Adapter: claude]
    I --> A2[Adapter: cursor]
    I --> A3[Adapter: codex]
    I --> A4[Adapter: out-of-tree ...]
    A1 --> S1[Stage in .aienv/generations/.../claude/]
    A2 --> S2[Stage in .aienv/generations/.../cursor/]
    A3 --> S3[Stage in .aienv/generations/.../codex/]
    A4 --> SN[Stage ...]
    S1 & S2 & S3 & SN --> G{All OK?}
    G -->|yes, atomic default| SWAP[Two-rename swap per target]
    G -->|no, atomic| RB[Discard generation, keep prior]
    G -->|best-effort| PARTIAL[Swap succeeded; skip failed]
    SWAP --> L[Write ledgers + capability report]
    PARTIAL --> L
    RB --> CR[Write capability report only]
    L --> SUM[Per-target summary]
    CR --> SUM
```

### Generation-directory swap (per target)

```
Before sync:
  ~/ws/.claude/aienvs/                  <-- live reserved prefix
  ~/ws/.aienv/state/claude.json         <-- live ledger (hashed paths)
  ~/ws/.aienv/generations/2026-.../claude/ <-- staged scratch (same FS as prefix)

After staging succeeds:
  rename ~/ws/.claude/aienvs        -> ~/ws/.claude/aienvs.old
  rename ~/ws/.aienv/generations/.../claude -> ~/ws/.claude/aienvs
  RemoveAll ~/ws/.claude/aienvs.old (best-effort)
```

Recovery: on startup if `.old` is found, check the ledger — if the new ledger matches the new prefix contents, finish cleanup; otherwise, swap back to `.old` and report recovery to the user.

### Adapter contract (v1)

Directional sketch only:

```
CLI                                 Adapter binary (aienvs-adapter-<name>)
|--- spawn with stdin/stdout pipe ->|
|                                   |
|--- JSON: {"op":"hello",           |
|           "protocol":"aienvs/v1"} |
|<-- JSON: {"protocol":"aienvs/v1", |
|           "adapter_name":"claude",|
|           "adapter_version":"1.x",|
|           "capabilities":{...}}   |
|                                   |
|--- JSON: {"op":"emit",            |
|           "ir":{...concepts...},  |
|           "workspace_root":"...", |
|           "reserved_prefix":"..."}|
|                                   |
|<-- JSON stream of ops:            |
|     {"op":"write_file",           |
|      "path":"...","content":...}  |
|     {"op":"mkdir",...}            |
|     {"op":"warning","concept":...,|
|      "status":"degraded","note":..|
|     {"op":"done"}                 |
|                                   |
|--- JSON: {"op":"goodbye"}         |
```

CLI core performs the actual filesystem writes inside the adapter's `os.Root`. Adapters never write directly — they *describe* what to write. This centralizes safe-write semantics and enforces declarative-only output (R11).

## Implementation Units

### Phase A — Foundation

- [ ] **Unit 1: Repo scaffolding, Go tooling, safe-filesystem layer**

**Goal:** Stand up the Go module with tooling, CI skeleton, and the `os.Root`-backed safe-filesystem primitives that every later unit depends on.

**Requirements:** R8, R10, R15

**Dependencies:** None.

**Files:**
- Create: `go.mod`, `go.sum`, `.gitignore`, `.golangci.yml`, `README.md`, `AGENTS.md`, `CLAUDE.md`
- Create: `cmd/aienvs/main.go` (stub wiring only)
- Create: `internal/fsroot/root.go`, `internal/fsroot/safewrite.go`, `internal/fsroot/containment.go`
- Create: `.github/workflows/ci.yml`
- Test: `internal/fsroot/root_test.go`, `internal/fsroot/safewrite_test.go`, `internal/fsroot/containment_test.go`

**Approach:**
- Pin Go 1.25+ in `go.mod`.
- `internal/fsroot` wraps `os.Root` with convenience functions: `OpenWorkspaceRoot`, `StagedWrite` (open-NOFOLLOW in same root, write+sync+rename), `SameFilesystem` check, `DetectReparsePoint` (refuses `fs.ModeIrregular`).
- CI runs lint + test on linux/macOS/Windows × amd64/arm64 (Windows/arm64 conditional on runner availability — see Deferred to Implementation).
- `AGENTS.md`/`CLAUDE.md` are the in-repo agent guidance for people working on aienvs itself (meta, not output).

**Patterns to follow:** Go `cmd/go/internal/modfetch/codehost` for cache-key ergonomics; stdlib `os.Root` examples in Go 1.24/1.25 release notes.

**Test scenarios:**
- Happy path: write a file through `StagedWrite` inside a temp root; confirm atomic replace.
- Edge case: target path contains `..`; `os.Root.OpenFile` refuses.
- Edge case: symlink inside the root pointing outside; open refuses.
- Error path: write across filesystems; `SameFilesystem` returns false and `StagedWrite` refuses with a named error.
- Windows-specific (build-tag `windows`): reparse point (junction) detected and refused on write.
- Integration: `go test ./...` passes cleanly on all three OSes under default (no network) conditions.

**Verification:** `golangci-lint run` clean; CI green on all three OSes; `go test -race` clean.

- [ ] **Unit 2: Manifest schema, loader, and comment-preserving writer**

**Goal:** Define the `.aienv.yaml` schema, strict loader, and the round-trip writer that preserves user comments when `init` writes the resolved SHA back.

**Requirements:** R2, R6, R17

**Dependencies:** Unit 1.

**Files:**
- Create: `internal/manifest/schema.go` (Go types, struct tags)
- Create: `internal/manifest/load.go` (goccy/go-yaml decoder with `DisallowUnknownField`, `AllowFieldPrefixes("x-")`)
- Create: `internal/manifest/write.go` (comment-preserving writer using `CommentMap`)
- Create: `docs/spec/manifest-v1.md`
- Test: `internal/manifest/load_test.go`, `internal/manifest/write_test.go`
- Test: `testdata/manifest/valid-*.yaml`, `testdata/manifest/invalid-*.yaml`

**Approach:**
- Schema includes: `version`, `canonical: {url, ref, commit, local_path}`, `floating: bool`, `targets: [name]`, `scope: user|project|global`, `cache: {override}`, `adapters: [{name, source, pin, trusted_sha}]`, `trusted_sha: <40-hex>` (at top level, mirrors `commit:` — project-level TOFU pin; see decision #9), `trust: {require_attestation (reserved, unimplemented in v1), allow_new_shas_until: <RFC3339> (optional cooldown window)}`, plus `x-*` forward-compat extension keys.
- `Load` returns an error on any unknown top-level key (typo guard). Pretty error messages via `yaml.FormatError`.
- Cross-field validation: `commit:` set implies `trusted_sha:` required when mode is non-interactive; `trusted_sha:` without `commit:` is a load error (no floating-with-pin hybrid).
- `WriteResolvedSHA(commit, trustedSHA)` reads the original YAML, preserves comments + key order, updates `commit:` and `trusted_sha:` in a single write, and writes atomically via `internal/fsroot`. (Separate `WriteTrustedSHA` exposed for the `aienvs trust pin` subcommand in unit 6.)
- Manifest schema is documented in `docs/spec/manifest-v1.md` as the canonical reference.

**Patterns to follow:** goccy/go-yaml's `CommentMap` examples; HashiCorp HCL2's "preserve user formatting" patterns for the writer shape.

**Test scenarios:**
- Happy path: load a complete valid manifest; fields populate correctly.
- Happy path: `WriteResolvedSHA` on a commented manifest preserves all comments and only updates `commit:`.
- Edge case: `x-foo` extension key is accepted.
- Error path: unknown key (`canonicl:` typo) returns a line-located error.
- Error path: both `url:` and `local_path:` present → explicit "1:1 violation" error.
- Error path: `floating: true` without `floating` at init flag (caught at sync level, not load — documented).
- Edge case: manifest with no `targets:` → defaults to empty (no adapters active) rather than error.
- Happy path: `WriteResolvedSHA` writes both `commit:` and `trusted_sha:` on a commented manifest, preserving comments on both lines.
- Error path: `trusted_sha:` present without `commit:` → load error naming the invariant.
- Error path: `trusted_sha:` does not match `[0-9a-f]{40}` → load error with line number.

**Verification:** all fixtures round-trip cleanly; manifest-schema doc matches code behavior.

- [ ] **Unit 3: Workspace discovery (logical walk, terminus, bounded cycle safety)**

**Goal:** Walk up from cwd to find `.aienv.yaml`, honor `--workspace` override, terminate safely at filesystem root or `--workspace-stop-at`, refuse excessive hops as a v1 cycle safety net.

**Requirements:** R1, R8, R15

**Dependencies:** Units 1, 2.

**Files:**
- Create: `internal/workspace/discover.go`
- Create: `internal/workspace/workspace.go` (`Workspace` struct: manifest path, root dir, logical path, resolved root)
- Test: `internal/workspace/discover_test.go`

**Approach:**
- `Find(cwd, opts)` walks up via logical parents (not `filepath.EvalSymlinks`) until it finds `.aienv.yaml`, hits filesystem root, or reaches `opts.StopAt`.
- Bounded hop limit (64) aborts with `ErrMaxWalkExceeded` — documented.
- `--workspace <path>` short-circuits discovery and uses the explicit path (still validates the manifest and computes the workspace root).
- Open an `fsroot.Root` for every emitted-files operation downstream, rooted at the workspace directory.
- Multi-workspace isolation invariant (decision #22) is enforced by refusing to write through `fsroot` outside the resolved workspace root.

**Patterns to follow:** git's own `setup_git_directory_gently` for discovery ergonomics; chezmoi's source-dir detection.

**Test scenarios:**
- Happy path: `.aienv.yaml` in `~/ws/`, cwd is `~/ws/a/b/c/`; discovery returns `~/ws/`.
- Happy path: `--workspace ~/ws/` is respected even from an unrelated cwd.
- Edge case: ancestor has `.aienv.yaml`, descendant also has one; nearest wins.
- Edge case: no `.aienv.yaml` anywhere up to root → `ErrNotFound` with a clear message.
- Edge case: `--workspace-stop-at=~/` stops at home, skips `/`.
- Error path: bounded-hop limit hit (deep symlink loop) → `ErrMaxWalkExceeded`.
- Edge case: `AIENVS_WORKSPACE_STOP_AT` env var respected when flag absent.
- Integration: discovery respects logical path through a symlinked directory (as user's `cd` sees it, not resolved).

**Verification:** `Workspace.Root` is consistent across logical and resolved views; containment check rejects writes outside `Root`.

- [ ] **Unit 4: Deterministic cache + URL canonicalization helper**

**Goal:** Compute a stable cache directory path for a (canonical URL, optional ref) pair, with explicit URL canonicalization rules that strip credentials and normalize scp-style SSH URLs.

**Requirements:** R7

**Dependencies:** Unit 1.

**Files:**
- Create: `internal/cache/location.go` (XDG-aware cache root via adrg/xdg + workspace-local override)
- Create: `internal/cache/canonicalize.go` (URL canonicalization rules)
- Create: `internal/cache/key.go` (SHA-256 hash of `"git:" + canonical_url`)
- Test: `internal/cache/canonicalize_test.go`, `internal/cache/key_test.go`
- Test: `testdata/urls/*.txt` (input → expected canonical form, golden-style)

**Approach:**
- Canonicalization: lowercase host, strip userinfo, strip default ports, normalize `git@github.com:foo/bar.git` → `ssh://git@github.com/foo/bar.git`, preserve path case (case-sensitive on GitHub), trim trailing `.git` consistently (choose: always-with-`.git` for stability).
- Cache location: `adrg/xdg.CacheHome` + `aienvs/repos/<sha256-hex>`; manifest override takes precedence.
- Cache directory contains a bare clone + a `canonical-url.txt` audit file showing the plain canonical URL.

**Patterns to follow:** Go cmd/go `modfetch/codehost.WorkDir`; Docker's image reference canonicalization.

**Test scenarios:**
- Happy path: `https://github.com/foo/bar` and `https://github.com/foo/bar.git` map to the same cache key after normalization.
- Edge case: `git@github.com:foo/bar.git` and `ssh://git@github.com/foo/bar.git` map to the same key.
- Edge case: `https://GITHUB.com/Foo/Bar` → host lowercased, path case preserved (distinct from `github.com/foo/bar`).
- Error path: URL containing inline credentials → fed through canonicalizer, credentials are stripped in the canonical form (unit 5 rejects them earlier at init).
- Happy path: manifest cache override respected; audit file written; duplicate workspaces pointing at same URL reuse the clone.
- Fuzz: randomized URL inputs produce deterministic output (no panic, no nondeterminism).

**Verification:** golden-style fixtures pass; cache directory layout matches docs.

### Phase B — Git materialization & Trust

- [ ] **Unit 5: Hybrid Git layer + pin resolution + floating/offline handling**

**Goal:** Materialize the canonical source (via shelling to `git`) and expose a read API (via go-git v5.17). Implement `init`'s pin-at-init resolve, `--defer-resolve`, `--floating`, and the pinned-cached-succeeds offline rule.

**Requirements:** R4, R5, R6, R7

**Dependencies:** Units 1, 2, 4.

**Files:**
- Create: `internal/git/shell.go` (shell-out helpers: `LsRemote`, `Clone`, `Fetch`, `Checkout`)
- Create: `internal/git/read.go` (go-git v5 read wrappers: `ReadTree`, `BlobContent`)
- Create: `internal/git/materialize.go` (Materialize(ctx, canonical, offline) → (sha, localPath, error))
- Create: `internal/git/resolve.go` (resolve ref → SHA at init)
- Test: `internal/git/shell_test.go`, `internal/git/materialize_test.go`, `internal/git/resolve_test.go`
- Test: `testdata/git/*.bundle` (git bundle fixtures for offline tests)

**Approach:**
- Detect `git` on `PATH` at startup; fail with a clear "install git" error on CLI commands that need it.
- `LsRemote(url, ref)` → resolved SHA; used at init and for floating-mode re-resolution.
- `Materialize` flow:
  1. Compute cache dir via unit 4.
  2. If `offline && pinnedSHA in cache`, return it (pinned-cached-succeeds).
  3. Otherwise, try `git fetch`/`git clone` into a scratch dir; rename-on-success into the cache (for partial-fetch recovery).
  4. Verify the pinned SHA is reachable from the configured ref on the configured remote (defense against force-pushed refs).
- Reject URLs containing inline credentials with a specific error at init/manifest-load (decision #18).
- `go-git` used only for `ReadTree`/`BlobContent`/checking reachability of a SHA within the cache — no network calls.

**Patterns to follow:** Go cmd/go's shell-out-to-git pattern; Renovate's pin-to-full-SHA guidance.

**Test scenarios:**
- Happy path: clone a bundle fixture; resolve `main` → SHA; pin written.
- Happy path: pinned SHA already cached + offline flag → succeeds without network.
- Happy path: floating manifest + online → re-resolves, surfaces new SHA to caller.
- Error path: network unreachable + no cache → specific `ErrUnresolvablePinOffline` error with remediation text.
- Error path: network unreachable + floating → standard fail (no pinned-cached-succeeds for floating).
- Error path: credential-in-URL rejected at manifest load (tested via unit 2) and at `ls-remote` path as defense-in-depth.
- Error path: partial fetch interrupted → scratch dir cleaned up on next run; cache is untouched.
- Integration: `git` missing from PATH → specific error with install guidance.
- Edge case: `--defer-resolve` at init skips the resolve call entirely and writes manifest without `commit:`.

**Verification:** manifest gets a valid 40-char SHA in `commit:` after `init`; offline-pinned-cached reproduces identically.

- [ ] **Unit 6: Two-tier trust — committed project pin (`trusted_sha`) + per-user history (`trust.jsonl`) + `trust` subcommand**

**Goal:** Implement the two-store trust model: (a) a committed-in-manifest project pin (`trusted_sha:`) that is authoritative for CI, and (b) an SSH-`known_hosts`-style per-user JSONL history that is authoritative for interactive cross-project recall. Ship the `aienvs trust` CLI surface including the out-of-band batch-review commands (`pending`/`diff`/`promote`) for SHA updates.

**Requirements:** R17

**Dependencies:** Units 1, 2, 3, 5.

**Files:**
- Create: `internal/trust/types.go` (error classes `RevokedTrustAnchor`, `TrustDecisionRequired`, `FirstUseDenied`; `Decision` enum)
- Create: `internal/trust/store.go` (append-only JSONL, atomic append+rename, fold-over-log `Status()`, `Compact()`)
- Create: `internal/trust/policy.go` (`Decide(ctx, url, resolvedSHA, manifestTrustedSHA, flags)` → `Decision`; strictly prefers `trusted_sha:` in non-TTY contexts)
- Create: `internal/trust/prompt.go` (three distinct interactive prompts — first-URL, known-URL-new-SHA, revoked-URL banner-only — with accessibility-aware rendering)
- Create: `internal/trust/pending.go` (pending SHA list — derived from runs that emitted "new SHA available" reminders; persisted at `xdg.StateHome()/aienvs/pending.jsonl`)
- Create: `internal/cli/cmd_trust.go` (subcommand surface)
- Create: `docs/spec/trust-store-v1.md` (schema for `trust.jsonl` + `pending.jsonl` + `trusted_sha:` invariants)
- Test: `internal/trust/store_test.go`, `internal/trust/policy_test.go`, `internal/trust/pending_test.go`

**Approach:**
- **Project pin (authoritative in CI):** `.aienv.yaml:trusted_sha` is the lockfile-committed value. Non-interactive sync loads it via unit 2, compares to the resolved SHA, and on mismatch emits `TrustDecisionRequired` (exit 4) — no prompt, never hangs. Matches the `go.sum` / `package-lock.json` / `Pipfile.lock` precedent.
- **User history:** `adrg/xdg.DataHome()/aienvs/trust.jsonl`, mode 0600, append-only. Line schema (frozen in `docs/spec/trust-store-v1.md`):
  ```json
  {"ts":"<RFC3339>","op":"trust|promote|revoke|allow-new-shas-on|allow-new-shas-off","url":"<canonical>","sha":"<40-hex>","prev_sha":"<40-hex|\"\"","source":"cli|wizard|ci","actor":"<os-user>","hostname":"<host>"}
  ```
  Current state is the **fold over the log** — no separate snapshot file. `Compact()` (subcommand `aienvs trust compact`) rewrites the log when it exceeds 1 MB, keeping the most-recent decision per URL plus the full history of `revoke` ops.
- **No HMAC.** Documented honestly in threat model: store is only as trustworthy as the user account. Supply-chain integrity defense lives at `trusted_sha:`.
- **Three distinct error classes** (decision #9) — each wraps a specific `Decision` outcome:
  - `RevokedTrustAnchor` (exit 3): previously `revoke`d URL re-appears; **no prompt under any condition**; stderr banner + remediation `aienvs trust reset <url>`.
  - `TrustDecisionRequired` (exit 4): first-URL or known-URL-new-SHA in a non-TTY context; exits immediately with a flag hint.
  - `FirstUseDenied` (exit 5): interactive prompt declined.
- **Sync flow no longer prompts mid-sync on a known-URL-new-SHA.** It emits a one-line stderr reminder (`aienvs trust pending` has 1 new item for <url>`), appends to `pending.jsonl`, and continues using the existing `trusted_sha`. This replaces the hot-path friction with async batch review (Dependabot/Renovate precedent).
- **`aienvs trust` subcommands:**
  - `status` — fold over log, prints one line per URL: `url  current_sha  last_op  last_op_ts`.
  - `pending [--json]` — lists URL+new-SHA pairs captured during recent syncs.
  - `diff <url>` — shows commit log between `trusted_sha` and latest observed SHA.
  - `promote <url>` / `promote --all` — updates `trust.jsonl` + optionally writes new `trusted_sha:` into the project manifest (requires `--pin-manifest` flag).
  - `pin [<url>] [--sha=<sha>]` — writes/updates `trusted_sha:` in the current workspace's manifest; used by `init` and by operators.
  - `verify` — read-only CI gate: loads manifest, checks `trusted_sha` matches resolved SHA, exits non-zero on any mismatch.
  - `reset <url>` — drops a `RevokedTrustAnchor` (requires typed-url confirmation; writes a new `trust` op to the log).
  - `add <url> <sha>` — manual add (writes `trust` op; typed-sha confirmation).
  - `revoke <url>` — writes `revoke` op; typed-url confirmation required (decision #11's symmetric pattern).
  - `allow-new-shas <url> [--cooldown=<duration>]` — writes `allow-new-shas-on` op with optional cooldown; `--off` reverses.
  - `compact` — rotates oversized `trust.jsonl`.
- **`--accept-new-source=any` peer gate (decision #10):** requires either `AIENVS_ALLOW_UNSAFE_ANY=1` env var *or* `--i-understand-this-is-dangerous` flag. Every use echoes `Trusting new source: URL=<url> SHA=<sha>` to stderr before proceeding.
- **Local-path canonical sources:** ownership check uses *real uid* (not EUID); refuses on sudo unless `--sudo-ok`. Group-writable refusal preserved, `--trust-group-writable <path>` flag recorded in `trust.jsonl`.
- **Concurrency:** atomic append + rename for write; fold-over-log for read tolerates concurrent appends.

**Patterns to follow:** OpenSSH `known_hosts` fold-over-log semantics; Go module `go.sum` lockfile-in-repo precedent; Renovate/Dependabot async-review pattern; gh/Stripe CLI typed-confirmation patterns.

**Test scenarios:**
- Happy path (interactive, fresh): first sync for new URL → first-URL prompt; accept → `trust` op appended; manifest `trusted_sha:` written.
- Happy path (CI, pinned match): non-interactive sync with `trusted_sha:` matching resolved SHA → no prompt, no log append, sync proceeds.
- Happy path (interactive, known-URL-same-SHA): no prompt, no log append, sync proceeds.
- Happy path (interactive, known-URL-new-SHA): **no mid-sync prompt**; sync proceeds using existing `trusted_sha`; one stderr reminder emitted; `pending.jsonl` appended; `aienvs trust pending` lists the entry; `aienvs trust promote <url> --pin-manifest` updates both stores atomically.
- Error path (CI, `trusted_sha` mismatch): `TrustDecisionRequired` (exit 4); stderr prints remediation; manifest untouched.
- Error path (revoked URL reappears): `RevokedTrustAnchor` (exit 3); SSH-style banner; **prompt not shown even on TTY**; remediation names `aienvs trust reset <url>`.
- Error path (`trust reset <url>` without typed confirmation): rejected; `yes` rejected; exact URL accepted.
- Error path (`--accept-new-source=any` without peer gate): rejected with a specific missing-env-var/flag error.
- Happy path (`--accept-new-source=any` with peer gate): accepted; stderr echoes audit line; log appended with `source: cli`.
- Happy path (`trust allow-new-shas <url> --cooldown=7d`): subsequent new-SHA syncs within 7d auto-promote in the user log but don't mutate manifest `trusted_sha:`; beyond 7d the cooldown lapses and the URL falls back to pending-review flow.
- Happy path (`trust compact` on 2 MB log): reduces to per-URL-current + full revoke history; new log passes schema validator.
- Concurrency: two sync invocations appending concurrently — fold-over-log after both returns a consistent decision per URL (no duplicate promotions).
- Non-interactive with `--accept-new-source=<wrong-sha>` but actual different SHA → hard error; no log append.
- Edge case: sudo-invoked with local-path canonical source → refused unless `--sudo-ok`.
- Edge case: group-writable local path → refused unless `--trust-group-writable <path>`.

**Verification:** JSONL schema matches `docs/spec/trust-store-v1.md`; fold-over-log is deterministic; `aienvs trust verify` exit codes stable for CI consumption; banners never rendered when stderr isn't a TTY (no ANSI leakage).

### Phase C — IR + Adapter framework + Primary adapters

- [ ] **Unit 7: IR v1 decoder + capability matrix types**

**Goal:** Decode the canonical repo's tree into the v1 frozen IR concept set, and define the shared capability-matrix types used by the adapter framework.

**Requirements:** R11

**Dependencies:** Units 1, 5.

**Files:**
- Create: `internal/ir/types.go` (Node with `ID`, `Kind`, `Version`, `Provenance`, `Required`, `Targets`)
- Create: `internal/ir/kinds.go` (per-concept types: `AgentsMD`, `Rule`, `Skill`, `Command`, `PluginReference`, `MCPServerEntry`)
- Create: `internal/ir/decode.go` (canonical-repo layout → []Node)
- Create: `internal/capmatrix/types.go` (`CapabilityStatus: supported|partial|unsupported`, per-concept + per-adapter)
- Create: `docs/spec/ir-v1.md`
- Test: `internal/ir/decode_test.go`
- Test: `testdata/canonical/*/` (fixture canonical repos of varying shape)

**Approach:**
- Concept set is closed: `agents-md`, `rule`, `skill`, `command`, `plugin-reference`, `mcp-server-entry` (origin R11).
- Canonical repo layout is declared in `docs/spec/ir-v1.md`. Decoder is deterministic; same inputs → same IR output.
- Each node carries provenance (relative path in canonical repo, blob SHA) so adapters can surface source-located errors.
- `Required` default false; explicit `required: true` in the node's frontmatter or a collection-level `required-for: [claude, cursor]` scoping.
- Unknown files under an IR-owned directory are errors (catches "I put a .md file in skills/ expecting it to be a skill but misspelled the frontmatter"), not silent drops.

**Patterns to follow:** Pandoc's reader/AST separation; tree-sitter grammar-versus-runtime decoupling.

**Test scenarios:**
- Happy path: canonical repo with all 6 concept kinds decodes into expected node list.
- Happy path: `AGENTS.md` at root decodes as a single `agents-md` node with overlays for `CLAUDE.md` / `GEMINI.md` if present.
- Edge case: skill with frontmatter `required: true` surfaces in `Node.Required`.
- Edge case: `targets: [claude, cursor]` scoping restricts which adapters see the node.
- Error path: unrecognized file under `skills/` → decode error with file path.
- Error path: duplicate node ID within a kind → decode error.
- Determinism: decoding the same fixture 10× yields byte-identical IR.

**Verification:** `docs/spec/ir-v1.md` matches decoder behavior; fuzz inputs don't panic.

- [ ] **Unit 8: Adapter framework — LSP-framed JSON-RPC 2.0 over stdio, subprocess runner, in-process shim, conformance harness**

**Goal:** Ship the `aienvs/v1` adapter protocol as LSP-style `Content-Length`-framed JSON-RPC 2.0 over stdin/stdout, with the full op surface (initialize/initialized lifecycle, capabilities, declared_outputs, cancel, shutdown, progress, per-op timeouts, structured errors, magic cookie, `_meta`), a subprocess runner, an in-process shim for bundled adapters, and a conformance test harness that third-party adapters can embed in their own CI. Freeze the wire protocol at the end of this unit.

**Requirements:** R3, R11

**Dependencies:** Units 1, 7.

**Files:**
- Create: `internal/adapter/contract/framing.go` (LSP `Content-Length:`/`Content-Type:` header reader/writer over `io.Reader`/`io.Writer`)
- Create: `internal/adapter/contract/jsonrpc.go` (JSON-RPC 2.0 envelope: request/response/notification; ID correlator; error-code table including LSP extensions `-32800 RequestCancelled`, `-32801 ContentModified`, `-32803 RequestFailed`)
- Create: `internal/adapter/contract/protocol.go` (method types: `initialize`, `initialized`, `emit`, `progress`, `cancel`, `shutdown`; result types; `Capabilities` struct; `DeclaredOutput` struct; reserved `_meta` field on every envelope)
- Create: `internal/adapter/contract/schema/*.json` (per-method JSON Schemas, one file per method for readability)
- Create: `internal/adapter/subprocess.go` (process management, magic-cookie env var `AIENVS_ADAPTER_COOKIE`, stdio pipe lifecycle, stderr capture to in-memory ring buffer, SIGTERM-after-shutdown flow, timeouts, crash classifier)
- Create: `internal/adapter/inproc.go` (in-process shim speaking the same wire protocol over `io.Pipe` for bundled adapters)
- Create: `internal/adapter/manifest.go` (`adapter.yaml`: `name`, `version`, `contract_version: aienvs/v1`, capabilities ref, command, per-method timeout overrides)
- Create: `internal/adapter/discover.go` (PATH lookup `aienvs-adapter-<name>` + explicit `adapters:` in manifest + bundled)
- Create: `internal/adapter/conformance/harness.go`, `conformance/corpus/*.json` (golden-file corpus; record/replay), `conformance/echo/` (reference "echo" adapter in Go), `conformance/echo-py/` (reference Python adapter, minimal)
- Create: `docs/spec/adapter-protocol-v1.md` (authoritative wire spec; examples; error table; versioning policy)
- Create: `pkg/adapterkit/` (public helpers for Go authors: framing reader/writer, JSON-RPC dispatcher, capabilities builder, testing helpers)
- Test: `internal/adapter/contract/framing_test.go`, `jsonrpc_test.go`, `protocol_test.go`; `subprocess_test.go`, `inproc_test.go`, `manifest_test.go`; `conformance/harness_test.go`

**Approach:**
- **Transport.** `Content-Length: <n>\r\nContent-Type: application/aienvs-v1+json; charset=utf-8\r\n\r\n<n-bytes-of-UTF-8-JSON>`. Stdout is reserved for framed bytes only; stderr is the free-form diagnostic channel that the CLI captures into a ring buffer and surfaces in failure reports. Magic-cookie env var `AIENVS_ADAPTER_COOKIE=<uuid>` set per spawn; the adapter must echo it in the `initialize` params, and the CLI refuses to proceed if missing — so running `./aienvs-adapter-foo` directly at a shell is a fast-fail.
- **Lifecycle** (LSP/MCP hybrid):
  1. CLI spawns adapter with cookie env var + stdio pipes.
  2. CLI sends `initialize` request: `{jsonrpc:"2.0", id:1, method:"initialize", params:{client: "aienvs/<version>", protocol_versions:["aienvs/v1"], cookie, workspace_root, reserved_prefix, ir_version:"v1", _meta:{}}}`.
  3. Adapter responds with result: `{server:"<name>/<ver>", protocol_version:"aienvs/v1", capabilities:{concept_kinds:{rule:"supported", skill:"partial", ...}, write_tool_owned: true, progress: true}, declared_outputs:[{path, mode: "owned-subdir"|"tool-owned-entry", jsonpath?, section_id?}], _meta:{}}`.
  4. On version mismatch, **adapter counter-proposes** its highest supported protocol (MCP rule); CLI decides to continue or refuse with `ErrAdapterProtocolMismatch`.
  5. CLI sends `initialized` notification. Adapter must not emit real ops before receiving it.
  6. CLI sends `emit` request per target, carrying IR. Adapter streams progress notifications (`$/progress` with `WorkDoneProgress` begin/report/end payloads keyed by a `progressToken`) and then responds with a final result listing the ops performed.
  7. CLI sends `shutdown` request (Terraform `Stop` analog). Adapter drains, responds, and CLI sends SIGTERM-then-SIGKILL bounded by timeout (default 5s).
- **`declared_outputs` as integrity gate.** Adapter declares its full planned output set in `initialize` result (Bazel `declare_file` pattern). Any emit op referencing a path outside `declared_outputs` is rejected by the CLI with `ErrUndeclaredOutput`. Deletes inside declared paths are fine; writes outside require a protocol-level `declare_additional` op (out of scope for v1 — defer).
- **Op set (emit stream).** All ops speak JSON-RPC notifications inside the `emit` call via a streaming response pattern (CLI reads response-content incrementally):
  - `write_file` (path, content, mode, encoding)
  - `write_tool_owned` (path, kind: `json-pointer`|`toml-path`|`markdown-section`, locator, content) — **new in v1**; supports the per-entry-in-tool-owned-file ownership mode (decision #25)
  - `mkdir` (path, mode)
  - `delete` (path) — for ownership-migration flows
  - `warning` (concept_id, status: `degraded`|`partial`, note)
  - `symlink` (blocked by default; requires per-adapter justification in `adapter.yaml`)
- **Cancellation.** `cancel` notification (analog of LSP `$/cancelRequest`) carrying the JSON-RPC id of the in-flight `emit` request. CLI uses it when a sibling target fails under atomic-default mode so remaining adapters don't finish wasted work.
- **Timeouts.** Defaults (k8s mutating-webhook convention): handshake 5s, per-emit 30s, shutdown 5s. Overridable per method in `adapter.yaml`. Timeout is enforced from CLI side; adapter has no authority to extend it.
- **Errors.** JSON-RPC 2.0 error object plus `data.error_class` field naming the CLI-side class: `adapter-panic`, `adapter-timeout`, `adapter-protocol-mismatch`, `adapter-undeclared-output`, `adapter-exec-denied`, `adapter-capability-lied`. Exit-code taxonomy: adapter's own `exit_code` + `stderr_tail` attached to the error `data` on abnormal termination.
- **Capabilities vs. protocol version.** `capabilities` grow additively without bumping `aienvs/v1` — that's what the object is for. Protocol-version bumps are reserved for breaking changes to the envelope or framing. Forbid mid-session capability changes in v1.
- **Conformance harness.** Exposed as `aienvs adapter conformance-test <binary>`. Components:
  - **Golden-file corpus** of IR fixtures covering each concept kind, plus malformed/adversarial frames.
  - **Record mode** captures a reference adapter's exchange as golden bytes; **replay mode** validates a new adapter's bytes against golden deltas that matter (path, content hash) while ignoring nondeterministic fields (timestamps, progress tokens).
  - **Passthrough logger** tees all frames to a file for post-mortem.
  - **Reference adapters** (`echo` in Go using `adapterkit`, `echo-py` in Python with minimal stdlib) — ship in-tree as both a test fixture and an authoring template for third parties.
  - Checks: protocol compliance, capability-matrix honesty (declared `supported` must actually emit), `declared_outputs` completeness, rejection of forbidden ops, timeout compliance, graceful shutdown.
- **Startup validation.** Refuse to run if two adapters declare nested reserved prefixes (origin R10 no-nesting invariant). Refuse if declared `reserved_prefix` escapes the workspace or is an unknown tool root.

**Patterns to follow:** [LSP 3.17 base protocol](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/); [MCP 2025-11-25 lifecycle](https://modelcontextprotocol.io/specification/latest/basic/lifecycle); [Terraform plugin protocol](https://developer.hashicorp.com/terraform/plugin/terraform-plugin-protocol); [Bazel `declare_file`](https://bazel.build/rules/lib/builtins/ctx#declare_file); [`hashicorp/go-plugin` magic cookie](https://github.com/hashicorp/go-plugin); Helm plugin `plugin.yaml`; `kubectl` PATH discovery.

**Test scenarios:**
- Happy path: in-process `echo` adapter completes `initialize` → `initialized` → `emit` → `shutdown` cleanly; CLI receives ordered ops; conformance passes.
- Happy path: subprocess `echo-py` completes full lifecycle; protocol frames decoded by CLI with no reshaping.
- Happy path: `$/progress` notifications during a long emit are surfaced to the CLI's progress renderer without blocking the `emit` response.
- Happy path: adapter declares `write_tool_owned` capability and emits a `write_tool_owned` op targeting `.mcp.json:$.mcpServers.aienvs_foo`; CLI performs the merge.
- Error path: adapter emits real ops before receiving `initialized` → `ErrProtocolOrderViolation`; adapter killed.
- Error path: missing cookie → `ErrAdapterCookieMissing`; subprocess rejected pre-handshake.
- Error path: protocol-version mismatch, adapter counter-proposes `aienvs/v0` → CLI refuses with `ErrAdapterProtocolMismatch` and remediation.
- Error path: adapter emits `write_file` outside `declared_outputs` → `ErrUndeclaredOutput`; op rejected; session marked failed.
- Error path: adapter exit 137 mid-emit (OOM-kill) → classified as `adapter-panic`, stderr ring buffer attached to report, lock released.
- Error path: adapter ignores `shutdown` → SIGTERM after timeout; SIGKILL after grace; classified as `adapter-timeout`.
- Error path: adapter declares `rule: supported` in `initialize` but returns no `write_file` ops for rule concepts → conformance classifies `adapter-capability-lied`.
- Error path: CLI sends `cancel` mid-emit → adapter stops cleanly within 2s, responds with `-32800 RequestCancelled`.
- Concurrency: two adapters running in parallel (different targets) don't interleave frames on the same pipes; independent lifecycles.
- Integration: conformance harness replay over the frozen corpus is byte-stable across OSes.
- Security: shell-starting the adapter manually (no cookie env var) → adapter refuses on first frame.

**Verification:** `docs/spec/adapter-protocol-v1.md` is authoritative and matches code; both reference adapters pass conformance; the protocol spec + JSON Schemas are frozen at unit close (any later change requires a new unit and a protocol-version bump discussion).

- [ ] **Unit 9: Primary adapter — claude**

**Goal:** Ship the bundled `claude` adapter that maps the v1 IR concept set to Claude Code's **actual** per-subsystem reserved subdirectories (rules, commands) and its **tool-owned** files (MCP config) using the per-entry ledger mode.

**Requirements:** R3, R11, R12

**Dependencies:** Units 7, 8, 12 (per-entry ledger primitives).

**Files:**
- Create: `internal/adapter/bundled/claude/adapter.go`
- Create: `internal/adapter/bundled/claude/capabilities.yaml`
- Create: `internal/adapter/bundled/claude/emit.go` (concept-specific emission logic; uses `write_file` and `write_tool_owned` ops)
- Create: `docs/adapters/claude.md` (authoritative concept→destination table + path verification notes + deprecation warnings)
- Test: `internal/adapter/bundled/claude/emit_test.go`
- Test: `testdata/adapters/claude/*` (IR fixtures → expected file trees + expected merges)

**Approach:**
- **Reserved subdirectories (preferred mode):**
  - `rule` → `.claude/rules/aienvs/<id>.md` with managed-file header. **Do not** use the legacy `CLAUDE.md` import pattern for rules; prefer the modern rules directory which supports subfolder namespacing and `.mdc`-style frontmatter.
  - `command` → `.claude/commands/aienvs/<id>.md`. The subfolder gives free `/aienvs:<id>` slash-command namespacing without touching user commands.
- **Name-prefix isolation (no subdir nesting):**
  - `skill` → `.claude/skills/aienvs-<id>/SKILL.md` + assets. **Skill folder name must equal skill name** (Claude Code skill discovery constraint). Prefix-per-skill keeps aienvs-owned skills visually distinguishable from user skills; orphan deletion operates on folders matching `aienvs-*`.
- **Tool-owned-file mode (per-entry ledger):**
  - `mcp-server-entry` → merged into `.mcp.json` at **workspace repo root** (not `.claude/aienvs/mcp.json`). Path in the ledger: `.mcp.json` + JSON-pointer `#/mcpServers/aienvs_<id>`. Emits via `write_tool_owned` op. Orphan removal edits in place, preserving user-owned entries.
- **Concepts Claude Code does not read at the project level (declare `unsupported`):**
  - `agents-md` (project-level `AGENTS.md`): not currently part of Claude Code's project discovery. Downgrade to `unsupported` in `capabilities.yaml` with a note pointing users at `CLAUDE.md`. If the user wants workspace-level context, the `claude-claude-md` side-adapter (below) handles it.
  - `plugin-reference`: Claude Code does not read a project-level `plugins.json`. Mark `unsupported`; users install plugins via Claude Code's own plugin command.
- **Frontmatter bug ward:** any `rule` IR node with `paths:` frontmatter emits a one-line warning ("Claude Code has known bugs with `paths:` frontmatter as of 2026-04; the rule will be installed but activation is inconsistent — see docs/adapters/claude.md") but still emits.
- **Companion overlay (scope-contained here, not a separate adapter):** when an IR node of kind `agents-md` exists and targets `claude`, write a managed section into the workspace's root `CLAUDE.md` between `<!-- aienvs:begin id=<id> -->` / `<!-- aienvs:end id=<id> -->` markers via `write_tool_owned`. Ledger records the `section_id` locator. User text outside markers is preserved.
- Every emitted markdown/JSON file or tool-owned section gets the managed-file header/marker (decision #23). Reserved-subdirectories get a one-shot `README.md` on first emission.

**Patterns to follow:** Unit 8 protocol; Claude Code's [skills directory structure](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills) (skill-folder-name == skill-name rule); [project-local `.mcp.json`](https://docs.claude.com/en/docs/claude-code/mcp) conventions.

**Test scenarios:**
- Happy path: IR with `rule`+`command`+`skill`+`mcp-server-entry` → expected tree: `.claude/rules/aienvs/<id>.md`, `.claude/commands/aienvs/<id>.md`, `.claude/skills/aienvs-<id>/SKILL.md`, `.mcp.json` with `mcpServers.aienvs_<id>` merged.
- Happy path: second sync with one removed skill → `.claude/skills/aienvs-<removed>/` deleted; other `aienvs-*` skills and all non-aienvs skills untouched.
- Happy path: `mcp-server-entry` removed in next sync → corresponding `mcpServers.aienvs_*` key removed from `.mcp.json`; user's other `mcpServers` entries preserved.
- Happy path: `agents-md` + workspace-root `CLAUDE.md` exists with user content → aienvs section appended/updated between markers; user content outside markers byte-identical before and after.
- Edge case: concept with `targets: [cursor]` → skipped.
- Edge case: concept of kind `plugin-reference` → warning "Claude Code does not load project-level plugin references"; not emitted.
- Edge case: rule with `paths:` frontmatter → warning emitted alongside the file.
- Edge case: concept with `required: true` that capability says `unsupported` (e.g., `required: true, kind: plugin-reference`) → framework surfaces `required_unmet`; sync fails in atomic mode.
- Integrity: `.mcp.json` merge round-trips through a YAML-level parse (strict JSON); invalid existing file halts emission with a named error (no silent overwrite).
- Conformance: passes the shared conformance harness with zero warnings.

**Verification:** `docs/adapters/claude.md` matches the emitted layout; merge operations preserve user-authored JSON structure verbatim; golden-tree diff is empty across reruns.

- [ ] **Unit 10: Primary adapter — cursor**

**Goal:** Ship the bundled `cursor` adapter with Cursor's actual per-subsystem paths, including repo-root `AGENTS.md` section-merged via `write_tool_owned`.

**Requirements:** R3, R11, R12

**Dependencies:** Units 7, 8, 12.

**Files:**
- Create: `internal/adapter/bundled/cursor/adapter.go`, `capabilities.yaml`, `emit.go`
- Create: `docs/adapters/cursor.md`
- Test: `internal/adapter/bundled/cursor/emit_test.go`, `testdata/adapters/cursor/*`

**Approach:**
- **Reserved subdirectories:**
  - `rule` → `.cursor/rules/aienvs/<id>.mdc` (`.mdc` format; frontmatter-aware).
- **Name-prefix isolation:**
  - `skill` → `.cursor/skills/aienvs-<id>/SKILL.md` — Cursor skills share Claude's "folder name == skill name" constraint. (Cross-check: if Cursor does not yet support subdirectory-based skills at all, declare `skill: unsupported` in `capabilities.yaml`; don't silently degrade.)
- **Tool-owned-file mode:**
  - `mcp-server-entry` → `.cursor/mcp.json` (flat file at `.cursor/` root, not a directory) via `write_tool_owned` with JSON-pointer `#/mcpServers/aienvs_<id>`.
  - `agents-md` → **workspace-root `AGENTS.md`**, section-merged between `<!-- aienvs:begin id=<id> -->` / `<!-- aienvs:end id=<id> -->` markers. Cursor reads `AGENTS.md` at project root (verified 2026-04).
- **Concepts Cursor does not support at project level (declare `unsupported`):**
  - `command`: Cursor has no per-project custom command concept analogous to Claude slash commands. Mark unsupported.
  - `plugin-reference`: no project-level plugin registry. Mark unsupported.
- **Legacy warning:** detect a pre-existing `.cursorrules` file at workspace root; emit a one-time warning that `.cursorrules` is legacy and recommend migration; do not touch it.
- Same managed-file headers + managed-section markers + per-reserved-subdir README as Unit 9.

**Patterns to follow:** Unit 9's shape; [Cursor rules](https://docs.cursor.com/context/rules) documentation; Cursor `.cursor/mcp.json` conventions.

**Test scenarios:** same shape as Unit 9, with cursor-specific deltas: `rule` emits `.mdc`; `mcp-server-entry` merges into `.cursor/mcp.json`; `agents-md` section-merges into repo-root `AGENTS.md`; `command`/`plugin-reference` warn-and-skip under `unsupported`; legacy `.cursorrules` detection emits warning without mutation.

**Verification:** conformance passes; docs match emitted tree; `AGENTS.md` round-trip preserves user-authored sections byte-for-byte.

- [ ] **Unit 11: Primary adapter — codex (Codex CLI)**

**Goal:** Ship the bundled `codex` adapter for **Codex CLI** (2026 model), whose file layout diverges sharply from the `.codex/aienvs/`-symmetry the origin document assumed. Map what Codex CLI actually reads; declare everything else `unsupported` honestly.

**Requirements:** R3, R11, R12

**Dependencies:** Units 7, 8, 12.

**Files:**
- Create: `internal/adapter/bundled/codex/adapter.go`, `capabilities.yaml`, `emit.go`
- Create: `docs/adapters/codex.md` (includes a prominent "path reality vs origin assumption" callout)
- Test: `internal/adapter/bundled/codex/emit_test.go`, `testdata/adapters/codex/*`

**Approach:**
- **Workspace-root files (tool-owned-file mode):**
  - `agents-md` → **workspace-root `AGENTS.md`**, section-merged between `<!-- aienvs:begin id=<id> -->` / `<!-- aienvs:end id=<id> -->` markers. Codex CLI reads `AGENTS.md` at project root.
  - `mcp-server-entry` → **`.codex/config.toml`** via `write_tool_owned` with `toml-path` locator `mcp_servers.aienvs_<id>`. **Warning:** Codex CLI has had bugs loading project-local `config.toml` MCP entries as of 2026-04; emit a one-time advisory alongside the write pointing users at the user-level `~/.codex/config.toml` as a reliable fallback.
- **Name-prefix isolation under `.agents/skills/`:**
  - `skill` → **`.agents/skills/aienvs-<id>/SKILL.md`** (path is `.agents/`, *not* `.codex/`). This is a deliberate cross-tool convention, not a Codex-specific path; Codex reads it because Codex participates in the shared agents-skills spec.
- **Concepts Codex does not currently support at project level (declare `unsupported`):**
  - `rule`: no per-tool rule concept in Codex CLI 2026.
  - `command`: Codex's custom-prompts feature was deprecated in 2026; declare `unsupported` with a note pointing users at skills instead.
  - `plugin-reference`: no project-level plugin registry.
- **Prominent deprecation/bug notes in `docs/adapters/codex.md`:**
  - Custom prompts deprecated (2026) → we do not emit.
  - Project-local `config.toml` MCP loading bugs (2026-04) → we emit anyway but warn.
- Ledger tracks: `.codex/config.toml` (per-entry), workspace `AGENTS.md` (per-section), `.agents/skills/aienvs-*/` (per-folder).

**Patterns to follow:** Units 9 + 10; [Codex CLI `AGENTS.md` discovery](https://developers.openai.com/codex/) (workspace-root convention); Codex 2026 release notes covering prompts deprecation.

**Test scenarios:** same shape as units 9–10, scoped to codex mapping.
- Happy path: IR with `agents-md`+`skill`+`mcp-server-entry` emits: workspace `AGENTS.md` section, `.agents/skills/aienvs-<id>/SKILL.md`, `.codex/config.toml` `[mcp_servers.aienvs_<id>]` table.
- Happy path: concurrent ownership of `AGENTS.md` with the `cursor` adapter (both use repo-root `AGENTS.md`): markers are keyed by `<id>` so each adapter's section is independently deletable; a cross-adapter conflict test asserts both can coexist.
- Edge case: `command` in IR → warn-and-skip; validate surfaces the deprecation.
- Edge case: existing user content in `.codex/config.toml` (top-level `[general]` table) → preserved across reads/writes; ledger covers only the aienvs-owned tables.
- Edge case: `required: true` rule → `required_unmet` (Codex does not support rules).

**Verification:** conformance passes; all three primary adapters share consistent managed-file header format and compatible section-marker syntax when multiple adapters touch the shared `AGENTS.md`; `docs/adapters/codex.md` prominently documents the asymmetry (no `.codex/aienvs/` subdirectory in v1) so users are not surprised.

### Phase D — Sync engine

- [ ] **Unit 12: Ledger + per-target lock**

**Goal:** Implement the per-target ledger (`.aienv/state/<target>.json`) with content hashes, schema versioning, and `gofrs/flock`-backed locking with stale-PID detection.

**Requirements:** R9, R10

**Dependencies:** Units 1, 3.

**Files:**
- Create: `internal/ledger/types.go` (Entry: path, sha256, size, emitted_at; Ledger: version, target, entries)
- Create: `internal/ledger/load.go`, `internal/ledger/write.go` (atomic write via `fsroot`)
- Create: `internal/ledger/migrate.go` (schema-version upgrades gated behind `--migrate-state`)
- Create: `internal/locks/flock.go` (bounded `TryLockContext`; PID+host sidecar; stale-PID detection via signal-0 / `OpenProcess`)
- Test: `internal/ledger/roundtrip_test.go`, `internal/ledger/migrate_test.go`, `internal/locks/flock_test.go`

**Approach:**
- Ledger schema is v1; `Ledger.SchemaVersion` field explicit from day one so v2 has a migration point.
- Missing/corrupted ledger → first-sync-guard path (decision #13) — caller treats reserved prefix as user content needing adoption.
- Content hash per entry is SHA-256 of emitted bytes at emission time (not a re-hash on read).
- Lock file: `.aienv/state/<target>.lock` held via `TryLockContext(2min)` default; sidecar `.lock.pid` records `{pid, host, started_at}`.
- Stale-lock break: if sidecar PID doesn't exist (signal-0 or `OpenProcess` returns "no such process") **and** timestamp is older than 2× the timeout, auto-break with a stderr notice; otherwise require `--break-lock`.

**Patterns to follow:** Terraform state lock semantics; SQLite WAL atomic-write pattern.

**Test scenarios:**
- Happy path: write ledger, read back; entries match bit-for-bit.
- Edge case: corrupted JSON → load returns a specific `ErrLedgerCorrupted` and the caller routes to first-sync-guard.
- Edge case: schema version older than current + no `--migrate-state` → refuses with clear migration guidance.
- Edge case: schema migration runs when `--migrate-state` is set.
- Concurrency: two syncs race for the same target lock; one wins, other bounded-times-out.
- Stale-PID: kill process mid-hold (test scenario simulates this); next sync detects dead PID past age threshold and breaks lock with notice.
- Error path: sidecar present, PID still alive → explicit "another sync running" error.

**Verification:** no ledger corruption after forced crashes; lock-break is audit-logged.

- [ ] **Unit 13: Atomic staging + swap + rollback — staging-under-target, single-parent `os.Root`, explicit recovery state machine**

**Goal:** Implement the two-rename swap pipeline with staging co-located under each target's parent directory, a single parent `os.Root` spanning both rename steps, syscall-truth cross-FS detection, a named error taxonomy, bounded Windows retry for `SHARING_VIOLATION`, opt-in Restart-Manager enumeration via `--diagnose`, and an explicit persisted recovery state machine.

**Requirements:** R10, R16

**Dependencies:** Units 1, 3, 12.

**Files:**
- Create: `internal/sync/staging.go` (per-target staging dir: `<workspace>/<target-parent>/.aienv-staging/<iso-ts>-<short-sha>/`)
- Create: `internal/sync/swap.go` (two-rename algorithm inside a single parent `os.Root`)
- Create: `internal/sync/errors.go` (`ErrLocked`, `ErrCrossVolume`, `ErrStale`, `ErrPermission`, `ErrUndeclaredOutput` passthrough)
- Create: `internal/sync/recover.go` (startup state-machine reconciler)
- Create: `internal/sync/state_sentinel.go` (persisted `.state` file with `intend` → `step1_done` → `step2_done` transitions)
- Create: `internal/sync/diagnose_windows.go` (build-tag `windows`: Restart Manager API enumeration — read-only, non-elevated)
- Create: `internal/sync/diagnose_other.go` (build-tag `!windows`: stub)
- Create: `docs/operations/atomic-swap.md` (explains recovery states + troubleshooting + AV exclusion guidance)
- Test: `internal/sync/swap_test.go`, `internal/sync/recover_test.go`, `internal/sync/state_sentinel_test.go`
- Test: `internal/sync/swap_windows_test.go` (build-tag `windows`: active-file-reader scenarios)

**Approach:**
- **Staging location.** For each target `t` with reserved prefix `<workspace>/<target-parent>/aienvs/` (e.g., `.claude/rules/aienvs/`), stage at `<workspace>/<target-parent>/.aienv-staging/<iso-ts>-<short-sha>/<target>/` (a sibling of the live prefix under the same parent directory). **Invariant:** staging directory and live prefix share a parent, so a single parent `os.Root` (opened at `<workspace>/<target-parent>/`) scopes both rename steps. This makes cross-FS impossible by construction (staging is always on the same filesystem as the live prefix) and satisfies Go 1.25's no-cross-`Root`-rename constraint ([proposal #73041](https://github.com/golang/go/issues/73041)).
- **Tool-owned-file targets** (decision #25 mode): these don't swap directories. Unit 13's swap logic applies only to reserved-subdirectory targets. Tool-owned-file writes use a per-file staging strategy: `<file>.aienv-tmp` → `rename` over `<file>` under a per-file lock (unit 12). Recovery for these is a separate sentinel in `.aienv/state/tool-owned-in-progress.json`.
- **Two-rename swap (reserved-subdirectory mode):**
  1. Write sentinel `<staging>/.state` = `{status:"intend", workspace, target, started_at, sha}`.
  2. `Rename(<prefix>, <prefix>.old)` via parent `Root.Rename`. On error, classify and abort (see taxonomy below).
  3. Update sentinel to `step1_done` (atomic write via `fsroot.StagedWrite`).
  4. `Rename(<staging>/<target-leaf>, <prefix>)` via parent `Root.Rename`.
  5. Update sentinel to `step2_done`.
  6. `RemoveAll(<prefix>.old)` best-effort.
  7. Delete sentinel.
- **Cross-FS detection is syscall-truth, not a pre-flight.** `os.Rename` returns `syscall.EXDEV` on Unix or `syscall.Errno(17) ERROR_NOT_SAME_DEVICE` on Windows when the operands cross filesystems. Rely on that return value exclusively (because: Linux bind-mounts, btrfs subvolumes, overlayfs, and macOS firmlinks all report matching `f_fsid`/device-id but still return `EXDEV` at rename time). `statfs`/`FILE_ID_INFO` is used only to *enrich* the `ErrCrossVolume` error message, never to gate the call. Staging-under-target (above) makes `ErrCrossVolume` unreachable in normal operation; if it ever surfaces it means the user has a bind-mount or submounted filesystem inside their workspace.
- **Error taxonomy** (named Go error types, all sentinel values):
  - `ErrLocked`: Windows `ERROR_SHARING_VIOLATION` (32) / `ERROR_ACCESS_DENIED` (5) on rename. Retry up to 5× at 100ms/250ms/500ms/1s/2s with ±20% jitter (bounded total ≈4s). On final failure, attach process list from `--diagnose` if enabled, surface as `swap-blocked-by-reader`.
  - `ErrCrossVolume`: `EXDEV`/`ERROR_NOT_SAME_DEVICE`. Abort immediately, name both paths + filesystems from advisory `statfs`, recommend same-FS workspace layout.
  - `ErrStale`: on startup, leftover `<prefix>.old` or `<staging>/.state` from a prior crash → invoke recovery state machine; not a retry.
  - `ErrPermission`: non-retryable ACL denial; abort.
- **`--diagnose` (opt-in):** on Windows, calls Restart Manager API (`RmStartSession` / `RmRegisterResources` / `RmGetList`) in **read-only** mode to enumerate processes holding file handles in the target path. Never calls `RmShutdown` (would require elevation; too destructive). Names the blocking processes in the error report. No-op on macOS/Linux (Windows-specific capability). Git for Windows itself does not use RM, so this doesn't help if `git` is the blocker — documented.
- **Windows `os.Root` lifecycle during swap.** On Windows, an `os.Root` handle holds its directory open and prevents rename. Any read-only per-reserved-prefix `os.Root` held by upstream code (discovery, validation) **must be closed** before step 2 and re-opened after the sentinel transitions to `step2_done`. The parent `os.Root` (at `<target-parent>`) stays open across the swap because its directory isn't being renamed. Unix has no such constraint but the code path is identical for consistency.
- **Recovery state machine** (startup scan of all `<target-parent>/.aienv-staging/*/` directories; also runs as `aienvs sync --recover`):
  - Sentinel missing, `<prefix>` exists, no `<prefix>.old`: clean state — nothing to do.
  - Sentinel = `intend`, `<prefix>.old` absent: crash before step 1 — delete staging, log recovery.
  - Sentinel = `intend`, `<prefix>.old` present: impossible state (defensive); log, require operator intervention.
  - Sentinel = `step1_done`, `<prefix>` absent, `<prefix>.old` present, `<staging>/<target-leaf>` present: crash between step 1 and step 2 — retry step 2 (`Rename(<staging>/<target-leaf>, <prefix>)`) then advance sentinel.
  - Sentinel = `step1_done`, `<prefix>` present, `<prefix>.old` present: impossible state (defensive); log and require intervention.
  - Sentinel = `step2_done`, `<prefix>.old` present: crash before cleanup — `RemoveAll(<prefix>.old)` and delete sentinel.
  - Multiple staging dirs for same target: roll forward the newest with valid sentinel, purge older.
- **Retention:** keep last 3 completed-or-failed generations under `.aienv-staging/` per target for forensics. `aienvs sync --clean-scratch` force-clears all staging for all targets.
- **Pre-flight at sync start:** refuse if a prior `<prefix>.old` or `<staging>/.state = intend|step1_done` exists and `--recover` wasn't just run — surface `ErrStale` with remediation `aienvs sync --recover`. Prevents a second sync from stomping on a half-completed one.
- **Alternative considered: symlink-flip.** Rejected for Windows (symlink support requires Developer Mode or admin; many editors don't deref consistently) and for the additional indirection it introduces; documented in Alternatives Considered with full rationale.
- **Antivirus:** `docs/operations/atomic-swap.md` includes narrow AV-exclusion guidance (`.aienv-staging/` only — **not** the reserved prefix) for third-party AV users seeing recurring `ErrLocked`. Defender is documented as generally fine.

**Patterns to follow:** [Go 1.25 `os.Root`](https://go.dev/blog/osroot); nix-community/home-manager generation swap; Capistrano `releases/current`; [Restart Manager API read-only enumeration](https://learn.microsoft.com/en-us/windows/win32/rstmgr/restart-manager-portal); sentinel-file recovery (SQLite WAL, Git `index.lock`).

**Test scenarios:**
- Happy path: single-target staging + swap; final prefix matches staged contents byte-for-byte; sentinel cleaned up.
- Happy path: multiple targets stage independently; swaps are isolated per parent-`os.Root`.
- Error path: adapter B fails during staging in atomic mode; no swap happens; staging retained; sentinel at `intend`.
- Error path (Linux): simulate cross-FS via bind mount + same-device `f_fsid` → `Rename` returns `EXDEV` → `ErrCrossVolume` with both paths named.
- Error path (macOS): firmlink crossing under `/System/Volumes/Data` → `EXDEV` surfaced identically.
- Windows `ErrLocked`: open a file in the prefix (notepad-style reader); swap retries 5×; on persistent lock, `ErrLocked` with retry count + wall-clock reported.
- Windows `ErrLocked` with `--diagnose`: blocker process name included in the error message (integration: spawn a child holding a handle, run `aienvs sync --diagnose`, assert process name appears).
- Recovery: kill between step 1 and step 2 → restart → `aienvs sync --recover` advances sentinel and completes swap; final state matches a never-crashed run.
- Recovery: kill between step 2 and cleanup → restart → cleanup removes `.old`; final state identical.
- Defensive: pre-flight `ErrStale` fires when `.state = intend` exists without `--recover`.
- Housekeeping: 4th failed generation triggers oldest cleanup under `.aienv-staging/`.
- Integration: on-disk state after any simulated crash is recoverable — byte-identical to either pre-sync or post-sync state, never torn.
- `os.Root` lifecycle (Windows): holding a per-reserved-prefix `os.Root` across swap → swap fails; test asserts the production code closes and reopens around the swap window.
- Housekeeping: `aienvs sync --clean-scratch` removes all `.aienv-staging/*` for all targets; sentinel reconciliation logs each purge.

**Verification:** forced-crash test harness never leaves torn state reaching the user; sentinel state machine covered by exhaustive state-transition tests; Windows CI runner exercises `ErrLocked` retry, `--diagnose` enumeration, and `os.Root`-close-reopen.

- [ ] **Unit 14: Orphan deletion + `--adopt-prefix` (with backup + typed-name confirm)**

**Goal:** Implement ledger-diff-based orphan deletion and the safe `--adopt-prefix` flow (backup tarball + typed-name confirmation). Enforce the mid-life drift invariant.

**Requirements:** R10

**Dependencies:** Units 12, 13.

**Files:**
- Create: `internal/sync/orphans.go` (`diff(oldLedger, newLedger) → deleteSet`)
- Create: `internal/sync/adopt.go` (backup tarball via `archive/tar` + `compress/gzip`; typed-name confirmation)
- Create: `internal/sync/drift.go` (reserved-prefix scan, detect unmanaged files mid-life)
- Create: `internal/cli/cmd_adopt.go` (subcommand + interactive prompt)
- Test: `internal/sync/orphans_test.go`, `internal/sync/adopt_test.go`, `internal/sync/drift_test.go`

**Approach:**
- Orphan set = `old_ledger paths \ new_ledger paths`. **Never** computed from filesystem scan (avoid drift-induced phantom orphans).
- Orphan deletion happens *after* the new ledger is durable (write new ledger first, then delete, so a crash mid-delete is recoverable).
- `--adopt-prefix`:
  1. Tarball entire reserved prefix to `.aienv/state/backups/<target>-<iso-timestamp>.tar.gz`.
  2. Print backup path prominently.
  3. Require typed target name (`claude`) in interactive mode; `--adopt-prefix=<target>` non-interactive.
  4. Record every existing file in the ledger as-adopted.
  5. First post-adopt sync applies orphan deletion normally (adopted files not re-emitted get deleted; backup preserves them).
- `--adopt-prefix-no-backup` exists but prints a red warning.
- Scope: `--adopt-prefix` with no target argument adopts all enabled targets that currently have unmanaged content; `--adopt-prefix=claude` adopts just one.
- Mid-life drift guard: scan reserved prefix before sync; any file not in ledger → refuse with `ErrMidLifeDrift` naming the rogue file, pointing to `--adopt-prefix`.
- Ledger adoption integrates with atomic mode (decision #20 continuation): adoption writes land in the scratch ledger and only become durable when the generation swaps.

**Patterns to follow:** `gh repo delete` typed-name confirmation; Capistrano `releases/` rollback semantics.

**Test scenarios:**
- Happy path: sync with 2 deletions in orphan set → deleted; summary shows counts.
- Happy path: `--adopt-prefix=claude` creates `.aienv/state/backups/claude-*.tar.gz` and populates ledger.
- Happy path: typed-name confirmation required interactively; `y`/`yes` rejected; target name accepted.
- Error path: mid-life drift → specific error naming the rogue file.
- Error path: ledger indicates file but filesystem doesn't (out-of-band delete) → silent no-op during sync; validate surfaces it.
- Integration: backup tarball contents are exactly the pre-adopt prefix contents.
- Edge case: `--adopt-prefix-no-backup` prints explicit red warning; proceeds.
- Safety: `--expect-deletions=N` rejects when actual count differs.

**Verification:** golden backup tarballs match prefix bytes; orphan deletion never exceeds declared scope.

- [ ] **Unit 15: Sync summary + capability report + `--best-effort` + `--output=json`**

**Goal:** Produce the per-target sync summary (status tokens, timings, resolved output paths), the persisted capability report, and machine-readable `--output=json`. Implement `--best-effort` semantics.

**Requirements:** R16, Success Criteria

**Dependencies:** Units 7, 8, 12, 13, 14.

**Files:**
- Create: `internal/report/summary.go` (per-target `Status`, counts, duration, paths)
- Create: `internal/report/capability.go` (capability report builder)
- Create: `internal/report/json.go` (stable `--output=json` schema)
- Create: `docs/spec/sync-summary-v1.md`
- Create: `docs/spec/capability-report-v1.md`
- Test: `internal/report/summary_test.go`, `internal/report/capability_test.go`, `internal/report/json_test.go`

**Approach:**
- Status tokens: `ok`, `failed`, `skipped`, `unchanged`, `rolled-back` (atomic-mode preemption), `blocked` (lock held). No `partial` at per-target level.
- Top-line summary: `OK N ok`, `PARTIAL n ok, m failed, p skipped` (best-effort), `FAIL` (atomic rollback or 0 successes). Color + ASCII-token parity (decision R14).
- Capability report written to `.aienv/state/capability-report.json` during staging (before swap), so it's available even if the swap rolls back (decision #21). `validate` computes it in-memory only.
- `--output=json` emits structured `{workspace, commit, generated_at, mode: atomic|best-effort, targets: [...], summary: {...}}` with a pinned schema version for CI stability.
- `--best-effort`: swaps land per-target independently (each calling unit 13's swap for itself) without the atomic-all-or-nothing gate.
- Required-unmet: fails both modes; report is still written to disk.

**Patterns to follow:** Terraform per-resource apply summary; kubectl delete summary style.

**Test scenarios:**
- Happy path: all targets `ok`; top-line `OK 3 ok`; exit 0.
- Happy path: best-effort with 1 failure → `PARTIAL 2 ok, 1 failed`; exit non-zero; per-target detail shows paths and error cause.
- Atomic mode with 1 failure → all targets `rolled-back`; prior state intact.
- Required-unmet → sync fails; capability report on disk includes `required_unmet[]`.
- JSON output schema validates against `docs/spec/sync-summary-v1.md`.
- Color: honors `NO_COLOR`; ASCII tokens appear even in colored mode.
- Deterministic: same inputs → byte-identical summary across platforms.

**Verification:** JSON schema published and versioned; agents can parse output mechanically.

### Phase E — CLI, UX, automation

- [ ] **Unit 16: Cobra/Fang command tree + `--non-interactive` fail-fast + accessibility**

**Goal:** Ship the Cobra + Fang command tree: `init`, `sync`, `validate`, `capabilities`, `trust`, `adapter`, `hooks`, `watch`, `unmanage`, `rollback`, `status`. Enforce the fail-fast non-interactive mode and accessibility basics.

**Requirements:** R13, R14, R15

**Dependencies:** Units 5, 6, 8, 13, 14, 15.

**Files:**
- Create: `internal/cli/root.go` (Fang-wrapped Cobra root)
- Create: `internal/cli/cmd_init.go`, `cmd_sync.go`, `cmd_validate.go`, `cmd_capabilities.go`, `cmd_adapter.go`, `cmd_hooks.go`, `cmd_watch.go`, `cmd_unmanage.go`, `cmd_rollback.go`, `cmd_status.go`
- (`cmd_trust.go` from unit 6, `cmd_adopt.go` from unit 14 reused)
- Create: `internal/cli/nonint.go` (fail-fast `--non-interactive` context propagation)
- Create: `internal/cli/access.go` (NO_COLOR / FORCE_COLOR / TTY detection; ASCII-first status helpers; 80-col reflow)
- Test: `internal/cli/nonint_test.go`, `internal/cli/access_test.go`, `internal/cli/cmd_*_test.go`

**Approach:**
- Fang v2 wraps the Cobra root: styled help + errors + man pages + completions for free.
- `--non-interactive` (alias `--yes`) sets a context value that short-circuits any TUI entry. Every prompt has a flag; a missing required flag with `--non-interactive` fails fast with a specific error naming the missing value — never a silent default.
- Persistent flags: `--workspace`, `--output=text|json`, `--log-level`, `--offline`, `--floating`, `--non-interactive`.
- Accessibility: honor `NO_COLOR`; status helpers produce `OK/FAIL/SKIP/…` prefixes alongside color; 80-col reflow via lipgloss width helpers; keyboard-only nav assumed (TUI unit 17 enforces).
- `unmanage <target>` uses the ledger to remove emitted files + ledger entry. Refuses on drift unless `--force`.
- `rollback` surfaces retained generations under `.aienv/generations/` for manual promotion to current.
- `status` prints: workspace path, manifest commit, last sync time + outcome, capability-report summary, open warnings.

**Patterns to follow:** kubectl/docker command trees; Terraform flag surfaces.

**Test scenarios:**
- Happy path: `aienvs --help` prints styled help with all subcommands.
- Happy path: `aienvs sync --non-interactive --workspace <path>` runs silently on valid inputs.
- Error path: `aienvs sync --non-interactive` without flags for required prompts → fails with specific missing-flag error.
- Edge case: `NO_COLOR=1` suppresses color but retains ASCII tokens.
- Edge case: stdout not a TTY → auto `--output=json` if flag unset; log to stderr; data to stdout.
- Edge case: `aienvs unmanage claude` on clean state removes files + ledger; refuses if drift present.
- Edge case: `aienvs rollback <gen-id>` promotes a retained generation (after safety confirmation).
- Shell completions: `aienvs completion bash` emits a valid script.

**Verification:** golden help output matches spec; `--help` on every subcommand is accurate.

- [ ] **Unit 17: Bubble Tea + huh wizards for `init` and target selection**

**Goal:** Implement the interactive wizards with every branching state fully specified (fresh/existing manifest, ancestor detected, remote unreachable, local-path mode, zero targets, pre-existing target outputs). Flag-parity with unit 16.

**Requirements:** R13

**Dependencies:** Units 2, 3, 5, 6, 8, 16.

**Files:**
- Create: `internal/tui/wizard/init.go` (multi-screen flow, screen per state)
- Create: `internal/tui/wizard/targets.go`
- Create: `internal/tui/nav.go` (small stack navigator)
- Create: `internal/tui/theme.go` (lipgloss styles, `NO_COLOR`-aware)
- Create: `docs/wizard/init-states.md` (the authoritative branching-state spec)
- Test: `internal/tui/wizard/init_test.go`, `internal/tui/wizard/targets_test.go`

**Approach:**
- Entry preconditions: TTY on stdin+stdout AND `--non-interactive` unset AND at least one decision requires user input.
- Init wizard screens (each a separate Bubble Tea model; composed via nav stack):
  1. Confirm workspace directory (exists or about to be created).
  2. Existing-manifest handling (adopt / replace / abort).
  3. Enclosing-Git-worktree detection + offer `.gitignore` additions.
  4. Ancestor-workspace detection + `--allow-nested` confirmation (distinct screen).
  5. Canonical source entry (URL / local path; mutually exclusive).
  6. Ref + pin behavior (`ref`, `--floating`, `--defer-resolve`).
  7. Remote-unreachable fallback (retry / switch to local-path / `--defer-resolve` / abort).
  8. TOFU prompt for the resolved `(URL, SHA)` (first-URL flavor).
  9. Target selection from available adapters (huh multiselect).
  10. Pre-existing target-outputs preview + adopt-on-first-sync acknowledgment (deferred to actual sync; wizard only warns).
  11. Summary + write confirmation.
- Target-selection wizard is a standalone flow (used by `aienvs target add`/`remove` future commands; v1 uses the block inside init).
- Every screen's text, validation, keymap, flag-equivalent, and per-exit-code state is enumerated in `docs/wizard/init-states.md` — the authoritative spec.
- Use `huh.WithAccessible(true)` when `TERM=dumb` or `AIENVS_ACCESSIBLE=1`.

**Patterns to follow:** Charm's own CLIs; `bubblon`/`bubbletea-nav` stack navigator; Cobra+Bubble Tea integration examples.

**Test scenarios:**
- Happy path: fresh directory → full flow → manifest written with pinned SHA.
- Branching: existing manifest, user picks "replace" → old manifest archived to `.aienv/state/backups/manifest-<ts>.yaml`.
- Branching: ancestor workspace detected → `--allow-nested` confirmation screen presented; cancellation aborts cleanly.
- Branching: remote unreachable → fallback screen offers retry / defer-resolve / abort.
- Branching: user selects 0 targets → warning screen + confirmation that sync will be a no-op.
- `teatest` snapshot tests for every screen's output under accessible and normal modes.
- `--non-interactive` bypasses all wizards; if any required value missing, fails fast (unit 16).

**Verification:** `docs/wizard/init-states.md` matches implementation; teatest snapshots stable; accessible mode readable by screen readers (manual check).

- [ ] **Unit 18: `validate` dry-run surface**

**Goal:** Ship `aienvs validate` with the specified network posture, output shape, exit codes, and `--diff` mode.

**Requirements:** R14, Success Criteria

**Dependencies:** Units 5, 7, 8, 12, 15.

**Files:**
- Create: `internal/validate/validate.go`
- Create: `internal/cli/cmd_validate.go`
- Create: `docs/spec/validate-output-v1.md`
- Test: `internal/validate/validate_test.go`

**Approach:**
- Reuses the same pipeline as sync up to staging, but doesn't swap. Capability report assembled in memory, not persisted.
- Network posture: follows sync defaults; `--no-fetch` forces cache-only (fails if uncached), `--fetch` forces fetch even on pinned-cached.
- Output structure: per-target `{would-create[], would-update[], would-delete[], warnings[]}`; optional `--diff` emits unified-diff body for text files; JSON mirrors sync summary schema + adds `diff` node.
- Exit codes: `0` no drift, `1` drift detected (clean), `2+` operational error.
- TOFU is honored — a validate that would trigger a first-time trust prompt blocks on `--non-interactive` without `--accept-new-source`.
- Ledger hashes used to detect hand-edit drift: any file whose on-disk sha256 ≠ ledger hash is flagged under a distinct `out-of-band-modified` label.

**Patterns to follow:** Terraform `plan` output; git `diff --stat`.

**Test scenarios:**
- Happy path: clean workspace → exit 0, no output changes listed.
- Drift path: pending additions + deletions → exit 1, per-target enumeration.
- Drift path: hand-edited file inside reserved prefix → listed under `out-of-band-modified` with hash diff.
- `--no-fetch` on uncached pinned manifest → specific `ErrUnresolvablePinOffline`.
- `--diff` emits valid unified diffs for all text files in the change set.
- JSON output validates against `docs/spec/validate-output-v1.md`.
- Non-interactive + first-time TOFU-gated URL → exits non-zero with a TOFU-specific error class.

**Verification:** CI uses `aienvs validate` as a drift guard in its own test repo; exit codes stable.

- [ ] **Unit 19: Hooks install/uninstall + watch mode**

**Goal:** Implement `aienvs hooks install/uninstall` (POSIX-shell wrapper via `#!/bin/sh`, `$(dirname "$0")`-safe, owner-only) and watch mode (debounced, lock-coordinated, failure-marker file).

**Requirements:** R9, R15

**Dependencies:** Units 12, 15, 16.

**Files:**
- Create: `internal/hooks/install.go` (generate wrapper, detect `core.hooksPath`, detect conflicts with lefthook/husky)
- Create: `internal/hooks/uninstall.go`
- Create: `internal/hooks/templates/post-merge.sh`, `post-checkout.sh` (embedded via `//go:embed`)
- Create: `internal/watch/watch.go` (fsnotify-backed; debounce; lock coordination)
- Create: `internal/cli/cmd_hooks.go`, `cmd_watch.go`
- Test: `internal/hooks/install_test.go`, `internal/watch/watch_test.go`

**Approach:**
- Install flow: require interactive confirmation OR `--install-hooks` flag (decision R9). Detect existing hooks — if non-aienvs present, require `--replace` (with backup to `.aienv/state/hooks-backup/`) or `--append` (wrap existing). Detect lefthook/husky via `package.json`/`lefthook.yml` and print specific advisory. Owner-only 0755 on POSIX; equivalent restrictive ACL on Windows.
- Wrapper script: `#!/bin/sh` + `set -eu` + `exec <resolved-at-install-time-absolute-aienvs-path> sync --post-merge --workspace <absolute-workspace-path>`. Absolute paths avoid PATH surprises in GUI clients.
- Uninstall: remove only aienvs-generated wrappers (identified by marker comment). Restore any `--append`ed predecessor.
- GUI-client advisory printed on install naming known-good (GitHub Desktop 3.5.5+, Tower, Fork, Sublime Merge) and known-gotcha (GitKraken path separator).
- Watch mode:
  - `fsnotify`-observed paths: the manifest, local-path canonical source (if configured), cache dir (optional `--watch-cache`). Explicitly ignore `.aienv/state/` and reserved prefixes (prevents self-retrigger).
  - 500ms debounce (default), configurable via `--debounce-ms`.
  - Acquires the same per-target locks as unit 12; on conflict, yields to manual syncs (the manual sync signals via lock; watch cancels pending debounced run and re-queues).
  - Failures written to `.aienv/state/last-watch.failed` with `{at, error_class, detail}`; subsequent interactive `aienvs` commands print a banner if the marker is newer than the last success.
- Hook invocations detect an in-progress sync (lock held) and **exit 0** after writing `.aienv/state/hook-skipped` marker — avoids breaking user's `git pull` UX.

**Patterns to follow:** lefthook/husky install UX; fsnotify + debouncer idiom; Git for Windows hook conventions.

**Test scenarios:**
- Happy path: install wrapper on fresh repo with no existing hooks; verify contents + mode.
- Edge case: existing foreign hook present; `--replace` backs it up.
- Edge case: existing aienvs hook present; reinstall overwrites cleanly.
- Error path: install without `--install-hooks` + non-interactive → refuses.
- Watch happy path: change manifest file; debounced sync triggers once within 1s.
- Watch concurrency: manual sync while watch mid-debounce; watch cancels pending run.
- Hook-during-long-sync: hook wrapper detects lock held; exits 0; marker file written.
- Cross-platform: install scripts produce equivalent behavior on all three OSes (integration tests).

**Verification:** hook script passes shellcheck; watch doesn't self-loop; no leaked fsnotify descriptors under `go test -race`.

### Phase F — Extension SDK & release

- [ ] **Unit 20: Out-of-tree adapter SDK + conformance test kit + reference external adapter**

**Goal:** Ship the public-facing adapter authoring surface: a Go SDK (`pkg/adapterkit`), a conformance test kit runnable against any binary, and an in-tree reference external adapter binary that third parties can use as a template.

**Requirements:** R3, R11

**Dependencies:** Units 7, 8.

**Files:**
- Create: `pkg/adapterkit/doc.go`, `pkg/adapterkit/adapter.go` (convenience helpers for Go authors: protocol framing, capability declaration, test hooks)
- Create: `cmd/aienvs-adapter-example/main.go` (reference external adapter — minimal, faithful to protocol)
- Create: `internal/adapter/conformance/cli.go` (invoked as `aienvs adapter conformance-test <binary>`)
- Create: `docs/adapter-authoring.md`
- Create: `.github/workflows/adapter-release.yml` (reusable workflow for third-party adapter CI)
- Test: `pkg/adapterkit/adapter_test.go`, `cmd/aienvs-adapter-example/integration_test.go`

**Approach:**
- `adapterkit` is intentionally thin: framing, capability types, testing helpers. No opinions about IR interpretation.
- Reference adapter handles `rule` and `agents-md` only, with `unsupported` for the rest — proves the unsupported path works end-to-end.
- Conformance test kit feeds a catalog of IR fixtures; asserts protocol conformance, capability-matrix honesty, reserved-prefix containment, declarative-only output.
- Publishing model (gh-extension-style): GitHub topic `aienvs-adapter`; install via `aienvs adapter install owner/repo` (clones + verifies via TOFU trust store against repo head commit).

**Patterns to follow:** Helm plugin `plugin.yaml`; gh extension discovery; Terraform's conformance test harness.

**Test scenarios:**
- Happy path: reference adapter passes every conformance test.
- Error path: rig a broken adapter (declares `supported` for `rule` but returns `unsupported` at runtime); conformance catches it.
- Install-via-TOFU: `aienvs adapter install` triggers the same `(URL, SHA)` trust flow as canonical repos.
- Edge case: version mismatch between adapter and CLI → clean refusal + upgrade guidance.
- Docs: `docs/adapter-authoring.md` examples build and pass without modification.

**Verification:** third parties can write an adapter in Go using `adapterkit` inside 30 minutes (validated by running the tutorial on a junior developer during v1 bring-up).

- [ ] **Unit 21: Documentation, threat model, cross-platform CI, release packaging**

**Goal:** Ship the docs site material (capability matrix generator, authoring guide, threat model), lock down the cross-platform CI matrix, and package binary releases.

**Requirements:** R15

**Dependencies:** All prior units.

**Files:**
- Create: `docs/threat-model.md`
- Create: `docs/capability-matrix.md` (+ generator script at `internal/capmatrix/gen.go`)
- Create: `docs/operations/rollback.md`, `docs/operations/migration.md`
- Create: `docs/spec/*` (cross-linking all previously created spec docs)
- Create: `.github/workflows/release.yml` (goreleaser config or equivalent)
- Create: `.goreleaser.yaml`
- Update: `README.md` with install instructions, quickstart, link tree
- Test: integration suite runs under `-tags=integration` covering linux/macOS/Windows × amd64/arm64 (arm64-on-Windows conditional on runner availability)

**Approach:**
- Capability matrix generator reads each bundled adapter's `capabilities.yaml` and emits a single-page markdown table (Pandoc-style).
- Threat model enumerates: arbitrary Git URL risk (mitigated by TOFU + pin-at-init + reachability check), adapter subprocess compromise (mitigated by declarative-output restriction + per-target `os.Root` containment), symlink attacks (mitigated by safe-write + logical-discovery), concurrent-sync corruption (mitigated by per-target flock + PID sidecar).
- Release artifacts: darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, windows/amd64 (windows/arm64 deferred to implementation). SHA256 + minisign signatures for releases.
- README has a 5-minute quickstart; installation via `go install`, homebrew (post-v1), release download.

**Patterns to follow:** goreleaser templates; kubernetes.io-style threat-model documents; Pandoc's format-feature matrix.

**Test scenarios:**
- Happy path: `docs/capability-matrix.md` regenerates on-demand and matches committed state (CI gate).
- CI matrix: all three OSes × two architectures green under `-tags=integration`.
- Release workflow: dry-run produces expected artifacts with signatures.
- README quickstart commands, run verbatim, produce a working setup against a minimal canonical repo fixture.

**Verification:** v1 release published; install-to-first-sync time measured under 5 minutes on a fresh machine.

## System-Wide Impact

- **Interaction graph:** all writes flow through one `fsroot.Root` per adapter's reserved prefix. All network flows through a single `internal/git` seam. All prompts flow through unit 16's non-interactive gate.
- **Error propagation:** unit 15 defines the canonical error-class taxonomy; every subsystem returns errors that fit it. JSON summary exposes the class verbatim for CI.
- **State lifecycle risks:** handled by unit 12 (ledger durability) + unit 13 (atomic swap + recovery) + unit 14 (mid-life drift guard). The invariant "user content is either in the ledger or it blocks sync" holds for the workspace's entire lifetime.
- **API surface parity:** the adapter protocol (`aienvs/v1`) is the single cross-language API. Any feature that would require a new RPC op must be added in a minor-compatible way or gated behind a new protocol version.
- **Integration coverage:** the conformance harness (unit 20) is the integration contract for adapters. The `--tags=integration` suite covers the cross-platform filesystem + git matrix.
- **Unchanged invariants:** none. Greenfield.

## Risks & Dependencies

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Charm v2 stack stability (fresh Feb–Mar 2026 releases) | Medium | Medium | Pin exact versions; plan a small refresh budget per quarter; keep Bubble Tea coupling behind `internal/tui`. |
| go-git v6 lands mid-v1; temptation to migrate early | Low | Medium | Stay on v5.17 until v6.0.0 stable; revisit in a post-v1 minor. |
| Adapter protocol v1 proves under-specified once we ship | Medium | High | Full surface frozen in unit 8 (LSP framing, JSON-RPC 2.0, `initialize`/`initialized`, `capabilities`, `declared_outputs`, cancel/shutdown, progress, per-op timeouts, structured errors, magic cookie, `_meta`); conformance harness + three bundled adapters + two reference external adapters (Go + Python) exercise the protocol before v1 tag; additive-only minor changes. |
| TOFU UX friction drives users to `--accept-new-source=any` | Medium | High | Primary CI pattern is committed `trusted_sha:` in manifest (no flags, no drift); `=any` now requires a peer-flag/env-var gate (decision #10); mid-sync prompts on known-URL-new-SHA replaced by async `aienvs trust pending`/`promote` review (decision #9); stderr audit lines on every override. |
| Non-TTY hang on first-URL or SHA-update prompts | Medium | High | `TrustDecisionRequired` (exit 4) exits immediately in non-TTY contexts — never prompts; specific flag-hint remediation in stderr. |
| Ephemeral CI runners losing `trust.jsonl` | Low (with `trusted_sha`) | High (without it) | `trusted_sha:` commit-in-manifest is the primary CI trust anchor; user-level `trust.jsonl` is for interactive cross-project recall only. CI path never reads it. |
| Windows atomic-swap handle contention (`SHARING_VIOLATION`) with active editors | High | Medium | Bounded retry (5×, ~4s total, jittered); opt-in `--diagnose` Restart-Manager enumeration names the blocker; staging-under-target + single parent `os.Root` makes the swap window as narrow as possible; documented workaround. |
| `os.Root` held on reserved prefix blocks its own swap (Windows) | Medium | High | Unit 13 explicitly closes and re-opens per-reserved-prefix roots around the swap; parent-`os.Root` is the only handle held across the swap window. |
| Cross-FS workspace causes silent atomicity loss | Very Low (with staging-under-target) | High | Staging-under-target makes cross-FS unreachable by construction; `EXDEV`/`ERROR_NOT_SAME_DEVICE` surfaced as `ErrCrossVolume` if a bind-mount slips in; never silent fallback. |
| Per-tool path assumptions drift as tools evolve (AGENTS.md, MCP, skills paths) | High | Medium | Per-adapter destination table lives in `docs/adapters/<tool>.md` and `capabilities.yaml`; `capabilities.yaml` is the machine-readable source of truth; path changes are a version-bump event for the adapter, not the CLI. |
| Tool-owned-file merges corrupt user content (MCP, AGENTS.md) | Medium | High | `write_tool_owned` op (unit 8) keyed by JSON-pointer/TOML-path/markdown-section markers; read-merge-write under per-file lock (unit 12); round-trip test fixtures asserting user-authored regions are byte-identical pre/post. |
| Codex CLI project-local MCP loading bugs (known 2026-04) | High (bug) | Low (diagnostic) | Adapter warns on emit; docs recommend user-level `~/.codex/config.toml` fallback; unit 11 emits anyway so users upgrade into working behavior. |
| Users edit inside reserved prefixes despite managed headers | High | Low–Medium | Validate surfaces drift; sync overwrites but emits warning with pre-overwrite hash for recovery; docs explicit. |
| First-sync adoption destroys user content (P0 from review) | Medium | High | Mandatory tarball backup + typed-name confirmation (decision #11, unit 14); symmetric typed-url confirmation on `trust revoke`. |
| Mid-life drift goes undetected | Low | High | Drift guard refuses with named rogue path (decision #12, unit 14). |
| Adapter subprocess panics leak lock | Medium | Medium | Defer lock release + PID+timestamp sidecar + stale-PID auto-break (unit 12); subprocess crash classified via structured error (`adapter-panic`) with stderr ring-buffer attached. |
| Hostile adapter writes directly despite declarative-only contract | Low | High | Documented as *not* a security boundary; supply-chain trust (TOFU + `trusted_sha`) is the real defense; `declared_outputs` gate catches honest bugs; magic cookie defeats direct shell execution. |
| Community builds out-of-tree adapters that break on every minor release | Medium | Medium | Additive-only protocol changes; `capabilities` object is the extension point (no `v1→v2` bump needed); conformance test suite third parties embed in their CI; contract version decoupled from CLI version. |
| Third-party AV causes recurring swap failures | Low–Medium | Medium | `docs/operations/atomic-swap.md` ships narrow AV-exclusion guidance for `.aienv-staging/` only (not the reserved prefix); Defender verified fine. |
| Dependency on `git` on PATH | Low | Medium | Detect at startup; clear "install git" error; include in README prereqs and CI images. |

## Dependencies / Prerequisites

- Go 1.25+ toolchain on CI runners.
- `git` on PATH on user machines (common prereq; v1 documents it).
- Charm v2 libraries published and stable through 2026 Q2 (their release cadence supports this).
- GitHub topic `aienvs-adapter` conventions documented before v1 launch for third-party adapter discovery.
- No external services required at runtime (Git remote is the only dependency; trust + state all local).

## Phased Delivery

**Phase A — Foundation (Units 1–4).** Get the filesystem-safety layer, manifest, workspace discovery, and cache story right. Nothing user-visible ships; prerequisite for everything after.

**Phase B — Git + Trust (Units 5–6).** First user-visible surface: `aienvs init` works end-to-end (resolves SHA, writes manifest, materializes into cache, TOFU on first use). No emission yet.

**Phase C — IR + Adapter framework + Primary adapters (Units 7–11).** `aienvs sync` starts producing real output into reserved prefixes. Claude/Cursor/Codex adapters ship as bundled. Adapter protocol frozen at `aienvs/v1`.

**Phase D — Sync engine (Units 12–15).** Atomic swap, ledger, orphan deletion, capability report. `sync` is now robust against crashes, adoption-safe, and produces machine-readable output.

**Phase E — CLI, UX, automation (Units 16–19).** Full command surface, wizards, validate, hooks, watch. v1-quality UX.

**Phase F — Extension SDK & release (Units 20–21).** SDK + conformance kit + release packaging. v1 launch.

Milestone markers: end of Phase B is the first alpha (can init + materialize); end of Phase C is the first beta (can sync, unsafe); end of Phase D is the first release candidate; end of Phase F is v1.0.0.

## Documentation Plan

- `README.md` — install, quickstart, link tree.
- `docs/spec/manifest-v1.md` — authoritative manifest schema.
- `docs/spec/ir-v1.md` — authoritative IR concept set + canonical repo layout.
- `docs/spec/adapter-protocol-v1.md` — authoritative adapter protocol; third parties read this.
- `docs/spec/sync-summary-v1.md`, `docs/spec/capability-report-v1.md`, `docs/spec/validate-output-v1.md` — stable JSON schemas for CI.
- `docs/spec/trust-store-v1.md` — `trust.jsonl` + `pending.jsonl` schemas + `trusted_sha:` invariants.
- `docs/adapter-authoring.md` — how to write an adapter in any language.
- `docs/adapters/claude.md`, `docs/adapters/cursor.md`, `docs/adapters/codex.md` — per-adapter concept→destination tables + capability tables + tool-specific deprecation notes.
- `docs/capability-matrix.md` — generated cross-adapter capability grid.
- `docs/threat-model.md` — supply-chain surface + mitigations.
- `docs/wizard/init-states.md` — authoritative branching-state spec for the init wizard.
- `docs/operations/rollback.md`, `docs/operations/migration.md` — operator guides.
- `docs/operations/atomic-swap.md` — sync recovery state machine, Windows troubleshooting, narrow AV-exclusion guidance.
- `CHANGELOG.md` — kept from v1.0.0-rc1 onward.

## Operational / Rollout Notes

- v1 ships as a pre-1.0 release (`v0.x`) until the adapter protocol has seen a few out-of-tree adapters in the wild, then tags `v1.0.0` committing to the protocol.
- No telemetry in v1 (privacy posture + keeps the "no network except Git" property).
- Release binaries signed with minisign; checksum + signature published alongside each release.
- Bug-fix releases are additive-only w.r.t. adapter protocol; any breaking protocol change is a major CLI bump and a protocol version bump.
- Users upgrading from an older ledger schema see an explicit one-time prompt + `--migrate-state` gate (decision #13); never silent.

## Alternative Approaches Considered

- **Daemon-first sync.** Rejected in origin doc; re-evaluated here: the atomic-swap + pinned-cached-succeeds design removes the "propagation lag" pain that daemons usually address. Watch mode + hooks cover sub-second needs.
- **In-process Go `plugin` package for adapters.** Rejected: no Windows support, brittle ABI, cross-toolchain-version breakage. Subprocess protocol is cheaper to maintain.
- **`hashicorp/go-plugin` gRPC subprocess.** Rejected for v1: heavier dependency, gRPC overhead, Go-to-Go biased authoring story (Python/Node/Rust adapter authors would need to pull in gRPC codegen). LSP-framed JSON-RPC 2.0 satisfies v1 needs with minimal cross-language ceremony and doesn't foreclose a v2 upgrade to gRPC if bidirectional high-throughput streaming becomes necessary.
- **Raw length-delimited JSON (no headers).** Rejected: no `Content-Type`, no charset tagging, no forward-compat envelope for additional header metadata (auth, trace IDs). LSP-style `Content-Length:`/`Content-Type:` headers cost nothing and enable later evolution.
- **NDJSON.** Rejected: bans embedded newlines in payloads (brittle for adapters emitting multi-line content bodies) and lacks the content-type tagging above.
- **WASM adapters via wazero.** Rejected for v1: Go→WASI toolchain still immature in 2026; adapter authors overwhelmingly come from JS/Python/Go/Rust camps, not WASI-first languages. Revisit in 2027.
- **Symlink-only deploy (Stow-style).** Rejected: Windows symlink story is poor (Developer Mode / admin requirement); some target tools dereference inconsistently; orphan-deletion and drift-detection are harder with symlinks.
- **Symlink-flip atomic deploy (Capistrano `current` symlink).** Specifically considered during unit 13 design and rejected for Windows parity reasons (same poor symlink story), the additional indirection users and downstream tools would need to follow, and because tool-owned-file targets (MCP configs, AGENTS.md) can't be symlinked — they require in-place merges. The two-rename directory swap is uniform across reserved-subdir and per-entry modes.
- **Ledger inside the reserved prefix (`.claude/aienvs/.state.json`).** Rejected: a user nuking the prefix would nuke the ledger and lose orphan-detection signal. Ledger lives outside, at `.aienv/state/<target>.json`.
- **Autocommit pinned SHA back to Git automatically on `init`.** Rejected: `init` only writes the local manifest; the user commits via normal Git. Avoids surprise commits.
- **Per-invocation `--accept-new-source=<sha>` as the primary CI pattern** (pre-deepening assumption). Rejected: ephemeral CI runners lose `~/.local/share/aienvs/trust.jsonl` between runs, forcing every CI pipeline to accept the flag at every invocation — friction that trains users into `--accept-new-source=any`. Committed `trusted_sha:` in the manifest (decision #9) is the `go.sum`/`package-lock.json` lockfile pattern and eliminates the runner-mutability problem entirely.
- **Mid-sync prompt on known-URL-new-SHA.** Rejected after UX review: frequently-updated canonical repos would train users to press ENTER on every sync, defeating the security benefit. Replaced with async `aienvs trust pending` / `diff` / `promote` (Dependabot/Renovate pattern) — sync continues using the existing `trusted_sha` and emits a reminder.
- **Silent prompt-on-SHA-change for TOFU (SSH-style banner on *every* mismatch class).** Rejected: SSH-style hard-block is correct only for a *revoked* URL reappearing. For first-URL and known-URL-new-SHA it's over-aggressive. Three distinct error classes (decision #9) differentiate the UX shapes.
- **HMAC or content-signed `trust.jsonl`.** Rejected: store is per-user, mode 0600; an attacker with write access to it already owns the account. Complexity buys no real defense. `trusted_sha:` at the project level is where supply-chain integrity actually lives.
- **Per-reserved-prefix `os.Root` scoping.** Specifically considered and rejected in unit 13 design: on Windows, an open `Root` handle holds its directory against rename, so the swap would block itself. Parent-of-reserved-prefix `os.Root` scoping (decision #6) is the required alternative.
- **Pre-flight `statfs`/`FILE_ID_INFO` as the cross-FS gate.** Rejected: bind mounts, btrfs subvolumes, overlayfs, and macOS firmlinks all defeat the check (same device, different filesystem behavior at rename time). Rename syscall return value is the only authoritative signal.
- **Single monolithic `sync.lock` across targets.** Rejected: serializes targets that could run in parallel; Terraform-style per-resource locking is the better fit.
- **Symmetric `.{tool}/aienvs/` layout across all adapters** (origin doc assumption). Rejected based on research into each tool's actual 2026 file discovery rules: three of six IR concept kinds land in tool-owned files (MCP configs, `AGENTS.md`) that tools don't discover via an `aienvs/` subdirectory. Unit 25's two-mode ownership model handles the asymmetry honestly (decision #25).

## Success Metrics

- First-user time from `aienvs init` to successful first `sync` under 5 minutes on macOS/Linux/Windows.
- Zero data-loss incidents reported during first 30 days post-v1 (measured by GitHub Issues with `data-loss` label).
- At least one out-of-tree adapter shipped by the community within 60 days of v1.
- v1 binary ships at <20 MB per platform.
- `aienvs sync` on a 50-concept canonical repo completes in under 5s (warm cache) on a modern laptop.

## Sources & References

- **Origin document:** `docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md`
- Related research (internal, this plan):
  - Framework-docs research (hybrid git, Charm v2, `os.Root`, goccy/go-yaml, gofrs/flock, JSON-over-stdio plugin model).
  - Best-practices research (home-manager generations, SSH `known_hosts` TOFU, Terraform plan/apply, LSP capabilities, gh/helm extension model, typed-name destructive confirmations).
  - Flow analysis (TOFU trusted-range, `--adopt-prefix` data safety, ledger drift, validate contract, symlink terminus).
- **Deepening-pass research (2026-04-21 confidence pass):**
  - *Per-tool reserved prefixes* — verified Claude Code, Cursor, and Codex CLI 2026 file discovery rules; established that symmetric `.{tool}/aienvs/` is partially fiction and required the two-mode ownership model (decision #25, units 9/10/11).
  - *Adapter subprocess protocols* — gap analysis against LSP 3.17, MCP 2025-11-25, Terraform plugin protocol, `hashicorp/go-plugin`, and Bazel action API; drove the frozen `aienvs/v1` wire surface (decision #8, unit 8).
  - *Windows atomic swap + cross-FS detection* — verified Go 1.25 `os.Root` constraints on Windows rename, cross-FS detection reality on bind-mounts/btrfs/overlayfs/APFS firmlinks, and Restart Manager API limits; drove unit 13's single-parent-`os.Root` design, syscall-truth cross-FS detection, and opt-in `--diagnose` (decisions #6, #20).
  - *TOFU CI and mismatch UX patterns* — synthesized SSH/go.sum/gh-attestation precedent with Renovate/Dependabot async-review pattern; drove committed `trusted_sha:` as primary CI pattern, three distinct error classes, peer-gated `=any`, and `aienvs trust pending`/`promote` batch review (decisions #9, #10, unit 6).
- **External refs cited in decisions:**
  - Filesystem / Go: [Go 1.25 release notes](https://tip.golang.org/doc/go1.25) and [`os.Root` blog post](https://go.dev/blog/osroot); CVE-2025-0913; [proposal #73041 — cross-Root rename](https://github.com/golang/go/issues/73041).
  - Windows atomic swap: [Restart Manager API docs](https://learn.microsoft.com/en-us/windows/win32/rstmgr/restart-manager-portal); `ERROR_SHARING_VIOLATION` (32) / `ERROR_ACCESS_DENIED` (5) / `ERROR_NOT_SAME_DEVICE` (17); nix-community/home-manager generation swap; Capistrano `releases/current`.
  - Adapter protocol: [LSP 3.17 base protocol](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/); [MCP 2025-11-25 lifecycle](https://modelcontextprotocol.io/specification/latest/basic/lifecycle); [Terraform plugin protocol](https://developer.hashicorp.com/terraform/plugin/terraform-plugin-protocol); [`hashicorp/go-plugin` magic cookie](https://github.com/hashicorp/go-plugin); [Bazel `declare_file`](https://bazel.build/rules/lib/builtins/ctx#declare_file).
  - TOFU / supply-chain: OpenSSH `known_hosts` semantics; Go module `go.sum`; `package-lock.json` / `Pipfile.lock`; [Renovate `config:best-practices`](https://docs.renovatebot.com/upgrade-best-practices/); Dependabot grouped-updates pattern; gh CLI typed-name destructive-confirmation patterns ([gh#12033](https://github.com/cli/cli/issues/12033)).
  - Per-tool layouts: Claude Code [skills directory](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills) and [project-local `.mcp.json`](https://docs.claude.com/en/docs/claude-code/mcp); Cursor [rules](https://docs.cursor.com/context/rules) and `.cursor/mcp.json`; [Codex CLI `AGENTS.md`](https://developers.openai.com/codex/) workspace-root convention.
  - Stack: [Charm `fang` v2](https://github.com/charmbracelet/fang); [go-git v5](https://pkg.go.dev/github.com/go-git/go-git/v5); [adrg/xdg](https://github.com/adrg/xdg); [gofrs/flock](https://github.com/gofrs/flock); [goccy/go-yaml](https://github.com/goccy/go-yaml).

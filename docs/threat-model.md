# agent-sync threat model (v1)

This document describes the trust boundaries, assets, adversaries, and
mitigations for `agent-sync`. It is the security companion to the
implementation plan and the frozen specs under `docs/spec/`. It is written to
be honest about what v1 does *not* defend against.

Scope: the `agent-sync` CLI as built — manifest resolution, git materialization,
adapter compilation, and the atomic swap into each tool's reserved prefix. It
does not cover the security of the AI tools that consume the rendered output
(Claude Code, Cursor, etc.), nor the security of the canonical content itself
beyond integrity-of-delivery.

## 1. System overview

`agent-sync` takes a per-workspace manifest (`.agent-sync.yaml`) that pins a canonical
Git repo by commit SHA, materializes that commit, compiles it through per-tool
adapters into declarative ops, and atomically swaps the result into each tool's
reserved subdirectory (e.g. `.claude/rules/agent-sync/`).

```
.agent-sync.yaml ──► resolve/pin ──► git materialize ──► adapter compile ──► stage ──► atomic swap
   (pin)          (SHA)          (subprocess)        (in-proc/subproc)   (fsroot)   (two-rename)
                     │                                                       │
                  trust store                                            ledger
              (TOFU on URL,SHA)                                    (orphan authority)
```

## 2. Trust boundaries

| # | Boundary | Crosses from → to | Enforcement point |
|---|----------|-------------------|-------------------|
| B1 | Canonical Git source | Remote network → local cache | `internal/git` subprocess + pin verification |
| B2 | Manifest authorship | Repo author → CLI | `internal/manifest` schema validation; pin/`trusted_sha` |
| B3 | First use of a `(URL, SHA)` | User decision → trust store | `internal/trust` TOFU policy |
| B4 | Adapter execution | Adapter code → CLI core | declarative ops only; in-proc panic recovery; subprocess sandbox |
| B5 | Workspace filesystem | CLI writes → user disk | `internal/fsroot` (`os.Root` containment) |
| B6 | Reserved-prefix ownership | Prior sync → current sync | `internal/ledger` + atomic swap state machine |

## 3. Assets

- **A1 — Workspace files outside reserved prefixes.** The user's own code and
  configuration. Must never be written, deleted, or overwritten by a sync.
- **A2 — Reserved-prefix contents.** Tool config that agent-sync owns. Must only
  ever reflect a pinned, trusted commit.
- **A3 — Trust store (`trust.jsonl`, `pending.jsonl`).** The per-user record of
  which `(URL, SHA)` pairs have been accepted.
- **A4 — Cache.** Materialized commits keyed by canonical URL + SHA.
- **A5 — Manifest pin (`commit:` / `trusted_sha:`).** The code-reviewed integrity
  anchor.

## 4. Adversaries and threats

### T1 — Compromised or malicious canonical source (supply chain)
An attacker gains push access to the canonical repo, or MITMs the fetch, and
serves malicious content (e.g. a `rule` or `mcp-server-entry` that exfiltrates
secrets once an agent loads it).

**Mitigations.**
- **The manifest always carries a concrete pin.** `init` resolves refs to a SHA
  and writes it back; auto-advance (below) re-pins **forward** to a new concrete
  SHA rather than floating. A moving branch never silently changes what syncs —
  every synced state is a recorded commit.
- **Auto-advance is fast-forward-only.** By default `sync` advances the pin to the
  newest upstream commit, but only when the new SHA is a descendant of the current
  `trusted_sha`. A rewritten / force-pushed history is **not** a fast-forward and
  is refused (the pin is kept; the scope is reported). This is the primary rail
  now that human review is not in the default advance path.
- **Auto-advance routes through the trust policy engine, not around it.** The
  allow-new-shas posture is evaluated by `internal/trust` `Decide`: an active
  **revoke always wins** (even under auto), and the posture is **fail-closed
  without loaded trust state** — a caller that has not folded the trust store
  cannot auto-advance. Every auto-advance appends to `pending.jsonl` for audit.
- **`trusted_sha:`** remains the authoritative integrity anchor: with auto-advance
  disabled (`--frozen` / `canonical.auto: false`) sync **fails closed** on a
  mismatch in non-interactive/CI, with no prompt — the human-code-reviewed,
  `go.sum`-like behavior, preserved for reproducibility-sensitive workspaces.
- **TOFU on `(URL, SHA)`** (B3): first use of an unseen pair requires an explicit
  interactive decision; CI never auto-accepts.
- Git fetches verify the resolved SHA matches the requested pin before the commit
  is admitted to cache (B1); offline / unreachable sync falls back to the cached
  pin rather than adopting an unverified head.

**Residual risk.** With auto-advance as the default, **human review is no longer
in the default path** for moving the pin forward — fast-forward-only and the
trust policy (revoke + fail-closed) are the remaining rails. agent-sync still does
**not** verify upstream commit signatures (e.g. GPG/SSH-signed tags), so a
malicious but fast-forward commit pushed to the tracked ref by someone with push
access **will** auto-advance and sync unless the anchor is revoked or the
workspace opts out via `--frozen` / `canonical.auto: false`. Commit-signature
verification is now a higher-value candidate for a future version. Workspaces that
require human-reviewed advancement should set `canonical.auto: false` and move the
pin with the explicit, gated `agent-sync update`.

### T2 — Cache-key / URL poisoning
An attacker crafts a canonical URL whose `userinfo` or alternate encoding
collides with a legitimate cache entry, serving attacker content under a trusted
key.

**Mitigations.** The canonical-URL canonicalizer (`internal/cache/canonicalize.go`)
strips `userinfo` and normalizes the URL before it is used as a cache/trust key,
so credentials embedded in a URL cannot fork or poison an entry. Trust records
are stored under the canonical form (see `docs/spec/trust-store-v1.md`).

### T3 — Path traversal / writes outside the reserved prefix
A malicious adapter, manifest, or canonical file attempts to write or delete
outside the reserved prefix (e.g. `../../.ssh/authorized_keys`, an absolute path,
or a symlink that escapes the workspace).

**Mitigations.**
- **All workspace writes go through `internal/fsroot`** (invariant #1): no direct
  `os.WriteFile`/`os.Create`/`os.Rename` against workspace paths. `fsroot` uses Go
  1.25 `os.Root` so traversal and symlink escapes are refused at the syscall
  boundary (`internal/fsroot/containment*.go`, `nofollow_*.go`).
- **Adapters never write files** (invariant #2): they emit declarative ops; the
  CLI core performs every write, so safe-write semantics are enforced in one place.
- Op paths are validated against the declared-outputs / reserved-prefix gates
  before they are applied.

**Residual risk.** The reserved prefix itself is, by design, fully managed: a
trusted-but-wrong commit can place arbitrary content *inside* the prefix. This is
the intended contract; defense is at T1 (pin + trust), not at the filesystem layer.

### T4 — Malicious or crashing adapter
An adapter (bundled or out-of-tree) panics, hangs, emits oversized frames, lies
about its capabilities, or emits undeclared outputs.

**Mitigations.**
- In-process adapters run under panic recovery (`internal/adapter/inproc.go`): a
  panic becomes a classified error, not a CLI crash.
- Subprocess adapters run via `os/exec` with `CommandContext` (cancellable), no
  shell, frame-size limits, and a stderr ring buffer attached to the report;
  abnormal exit is classified (`adapter-panic`) and the per-target lock is
  released (`internal/locks`).
- Protocol gates reject **capability-lied** and **undeclared-output** responses
  (`internal/adapter/conformance`), so an adapter cannot emit ops it did not
  declare.

**Residual risk.** In-process bundled adapters share the CLI address space;
panic recovery contains crashes but not a deliberately malicious in-tree adapter.
Out-of-tree adapters should run as subprocesses (the SDK default).

### T5 — Data loss during swap (interruption / concurrency)
A sync is interrupted (Ctrl-C, crash, power loss) mid-swap, or two syncs run
concurrently against the same target, leaving a half-written or corrupted prefix.

**Mitigations.**
- **Two-rename atomic swap** (invariant #6) with a sentinel state machine
  (`intend → step1_done → step2_done`) and a **startup recovery reconciler**
  (`internal/sync/recover.go`): an interrupted swap is rolled forward or back on
  the next run; the reserved prefix is never left partial.
- Both renames share a single parent `os.Root`, sized at the prefix parent so the
  open handle does not block its own rename on Windows.
- **Per-target flock** plus a **PID+timestamp sidecar** with stale-PID auto-break
  serialize concurrent syncs (`internal/locks`).
- Tool-owned external files are merged surgically and **fail closed** on
  ambiguity (`internal/merge`), never blind-overwritten.

### T6 — Orphan over-deletion
A bug causes agent-sync to delete user files it does not own ("any file under the
prefix we don't currently emit").

**Mitigation.** **The ledger is the sole authority on orphan deletion**
(invariant #7): orphans are `previous_ledger − current_desired_outputs`, never a
filesystem scan. A file agent-sync never recorded writing is never deleted. Drift
of an externally modified managed file surfaces as `ErrMidLifeDrift` rather than a
silent clobber.

### T7 — Credential / prompt hijack via subprocess
A git operation triggers an interactive credential or host-key prompt, hanging
CI or leaking into an unexpected channel.

**Mitigations.** Git subprocesses run with `GIT_TERMINAL_PROMPT=0`, isolated
config (`GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM` neutralized in tests), and no
interactive SSH/credential prompts (AGENTS.md style rules). Non-interactive mode
never prompts and never hangs (invariant #3): it exits with a documented code and
the exact flag needed to proceed.

### T8 — Local tampering with the trust store
An attacker with write access to the user account edits `trust.jsonl` to pre-trust
a malicious `(URL, SHA)`.

**Mitigation / honest limitation.** There is **no cryptographic integrity** on the
trust store in v1. It is only as trustworthy as the user account; files are
`0600` under a `0700` directory. Supply-chain defense deliberately lives at
`trusted_sha:` — the committed, code-reviewed pin — not at the per-user store.
This is documented in `docs/spec/trust-store-v1.md` and is a conscious design
choice, not an oversight.

## 5. Non-goals (v1)

- Verifying upstream commit/tag signatures (see T1 residual risk).
- Cryptographic integrity of the per-user trust store (see T8).
- Sandboxing the *content* of a trusted commit — a trusted pin is trusted.
- Securing the downstream AI tools or their MCP servers once files are rendered.
- A long-lived privileged daemon: `watch` is a foreground, cancellable process
  only.

## 6. Release integrity

Release binaries are built reproducibly via goreleaser (`-trimpath`,
`CGO_ENABLED=0`) and published with a `checksums.txt` (SHA-256). When a signing
key is configured, `checksums.txt.minisig` is a minisign signature over that
file. Verify a download with:

```bash
# 1. confirm the checksum of your artifact appears in checksums.txt, then:
minisign -Vm checksums.txt -P <agent-sync public key>
```

The public key and its publication channel are established at first tagged
release; see `CHANGELOG.md` and the release notes.

## 7. Reporting

Security issues should be reported privately to the maintainers rather than via
public issues. (Establish and link a security contact / `SECURITY.md` at first
public release.)

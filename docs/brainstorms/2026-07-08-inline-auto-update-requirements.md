# Inline Auto-Update — Requirements

- **Date:** 2026-07-08
- **Status:** Ready for planning
- **Scope tier:** Deep — feature
- **Related:** `internal/cli/cmd_sync.go`, `internal/cli/cmd_update.go`, `internal/git/materialize.go`, `internal/manifest/schema.go`, `docs/spec/manifest-v1.md`

## Problem

`agent-sync sync` materializes the canonical source at the **pinned** `commit` and never advances it. Moving to newer upstream content is a separate, human-gated `agent-sync update` (non-interactively requires `--accept-update=<sha>`). There is no way for a workspace to stay current with upstream automatically — every refresh is a deliberate operator action. We want workspaces to track upstream on their own.

## Outcome

`agent-sync sync` advances the canonical pin to the newest upstream commit **by default** and applies it, auto-accepting the trust decision. Workspaces that want today's deterministic, gated behavior opt out explicitly. Advancement is constrained by safety rails so "automatic" does not mean "unbounded."

## Decisions

### Default behavior flips to floating
- `sync` resolves the newest upstream commit (see resolution rule below), advances the pin, and writes the resulting configuration in one pass.
- The `trusted_sha` gate that `update` enforces today is **auto-accepted** on the sync path. `commit` and `trusted_sha` are rewritten to the newly-landed SHA on every advance — they become a record of "what we last landed," not a pre-approval gate.
- This is a **semver-major** change: upgrading agent-sync changes behavior for existing workspaces with no manifest edit. Ships with a loud migration note.

### Opt-out to pinned + gated behavior
- `agent-sync sync --frozen` — runtime override, restores today's pinned+gated behavior for that run.
- `canonical.auto: false` in the manifest — per-workspace opt-out, durable. (Field name to be finalized in planning.)
- With auto-update disabled, `sync` behaves exactly as it does today, including the `update` gate for advancing.

### Resolution rule (assumption — confirmed by default)
- "Newest upstream commit" follows `canonical.ref` (e.g. `main`); when no `ref` is set, it follows the remote's default branch. This is the same resolution `agent-sync update` uses today.

### Safety rails (all in scope)
1. **Fast-forward only.** Refuse to advance when the new commit is not a descendant of the current pin (rewritten / force-pushed history), unless explicitly overridden. Mirrors the existing `update` fast-forward guard.
2. **Offline falls back to pin.** If the ref cannot be resolved online, use the last-known pin + cache and emit a warning instead of failing the sync. Preserves air-gapped / flaky-network operation.
3. **Record what moved.** Every auto-advance stamps the old→new SHA into the managed-file headers (and/or a log line) so unattended advances are auditable after the fact.
4. **Escape hatch.** `--frozen` + `canonical.auto: false` (see opt-out above) return a workspace to pinned+gated behavior.

## Success Criteria

- A workspace with a floating manifest, run twice across an upstream commit, lands the newer content on the second `sync` with no `--accept-update` and no prompt.
- `sync --frozen` and `canonical.auto: false` each reproduce today's pinned output byte-for-byte.
- An upstream force-push does not silently land: auto-advance refuses (ff-only) and reports why.
- Syncing offline against a floating manifest succeeds using the cached pin and warns; it does not error.
- After an auto-advance, the emitted files/log show the old→new SHA transition.

## Consequences Accepted (not solved here)

- **Loss of default reproducibility.** Two machines syncing the same floating manifest at different times may produce different output. `--frozen` / `canonical.auto: false` is the reproducibility path; CI pipelines that need determinism should use it.
- **Posture flip on upgrade.** Existing workspaces begin floating on the agent-sync version bump. Requires a prominent CHANGELOG/migration callout and a major version.

## Scope Boundaries

**Out of scope (other "auto-sync" shapes, deferred):**
- Scheduled PR-bot (CI/cron that runs `update` and opens a PR for review).
- Hook-driven re-sync (post-merge hook re-materializing at the current pin without advancing).

**Explicitly not doing:**
- Signature / provenance verification beyond fast-forward reachability.
- Any background daemon or long-lived watcher process (forbidden by `CLAUDE.md`; auto-update stays on-demand at `sync` time).

## Dependencies / Assumptions

- `internal/git/materialize.go` already has a `Floating` mode that resolves a ref online instead of using a pin — expected to be the primary building block rather than net-new machinery.
- The existing fast-forward-only guard and old→new SHA change summary in `internal/cli/cmd_update.go` are reusable for rails 1 and 3.
- Manifest schema (`internal/manifest/schema.go`, `docs/spec/manifest-v1.md`) gains one opt-out field; exact naming is a planning decision.

## Open Questions for Planning

- Field naming and manifest-v1 spec wording for the opt-out (`auto` vs `frozen` vs `floating`), and whether it lives on `canonical` or top-level.
- Where the audit record lands — managed-file header only, a dedicated log, or both — and its exact format.
- Interaction with hierarchy-aware / multi-scope sync: does each scope's manifest carry its own opt-out, and how do mixed floating/frozen scopes behave in one run?
- Whether `--frozen` and `--best-effort`/`--expect-deletions` and the `--post-merge` hook mode compose cleanly.

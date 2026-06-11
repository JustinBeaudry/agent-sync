---
title: Safely renaming load-bearing on-disk and wire-format identifiers
date: 2026-06-11
category: best-practices
module: agent-sync (repo-wide)
problem_type: best_practice
component: development_workflow
severity: high
applies_when:
  - Renaming a name that appears in on-disk paths, file formats, or wire-protocol identifiers (not just code symbols)
  - "A token to rename is a substring-prefix of another token that must NOT change (e.g. `.aienv` vs `.aienvs`)"
  - Renamed identifiers cross a producer/consumer or published-SDK boundary
  - Executing a large mechanical sed-style rename across many files
tags: [rename, refactor, wire-format, on-disk-format, breaking-change, verification, grep, sed]
---

# Safely renaming load-bearing on-disk and wire-format identifiers

## Context

Renaming the legacy project name (`aienvs`/`aienv` → `agent-sync`) across the whole
codebase looked like a trivial find-replace. It was not. The name was embedded in
**three classes** of identifier with very different risk:

1. Cosmetic (prose, comments, a stale lint config) — safe.
2. Go symbols — safe-ish, but break the build if half-renamed.
3. **Load-bearing on-disk and wire-format identifiers** — the manifest filename, the
   reserved state directory, markdown managed-block markers, the MCP key prefix, IR
   frontmatter keys, env vars, the adapter wire-protocol version (`aienvs/v1`), the
   subprocess handshake cookie env var, emitted skill/rules dirs, and sidecar markers.

Class 3 is where a careless `sed` corrupts data or desyncs a contract. This doc is the
recipe that worked, plus the specific traps that bit us.

## Guidance

### 1. Split the target spelling by *context*, not aesthetics

Filesystem paths and env vars want hyphens (`agent-sync`, `AGENT_SYNC_*`). But the
**MCP key prefix** is a TOML bare table name (`[mcp_servers.agentsync_<id>]`) and a JSON
pointer key — hyphens there force quoting and are fragile. So:

- `agent-sync` (hyphenated) for paths, dirs, env-var family, prose.
- `agentsync` (no hyphen) ONLY for identifiers that must be valid TOML bare keys / clean
  JSON keys (the MCP prefix `agentsync_`, frontmatter keys `__agentsync_*`).

Decide this rule up front and write it into the plan. A reviewer flagging the split as
"inconsistent" is wrong — tell them it's intentional.

### 2. Group commits by identifier, and keep each commit green

Rename **one identifier group end-to-end per commit** — production constant + every
test + every golden/fixture line for that identifier together. A single golden file may
be touched by several commits (each editing different lines); that is fine. What is NOT
fine is splitting a production-constant change from its test/golden update across
commits — that lands a red commit. After each group: `build + vet + test -race + lint`.

### 3. Beware the substring-prefix trap

`.aienv` (the state dir) is a **prefix of** `.aienvs` (the product-name sidecars like
`.aienvs-managed`). A blanket `s/\.aienv/.agent-sync/g` corrupts `.aienvs-managed` →
`.agent-syncs-managed`. Use a negative pattern that matches the shorter token only when
it is NOT followed by the distinguishing character:

```bash
# Rename .aienv (state dir, NOT .aienvs-*) — matches .aienv only when the next char isn't 's'
sed -i '' -E 's/\.aienv([^s])/.agent-sync\1/g' file.go
```

Likewise, camelCase Go identifiers (`aienvsSkill`, `AienvsPath`) must be renamed as
explicit symbol renames *before* any blanket lowercase `aienvs → agent-sync` sweep — a
blanket sweep would produce the invalid identifier `agent-syncSkill`.

### 4. Verify wire-boundary consistency explicitly

For every renamed identifier that crosses a producer/consumer or SDK boundary, grep that
the new value is **byte-identical on both sides**. A rename that updates the producer but
not the consumer compiles and passes unit tests, then fails at runtime/handshake:

```bash
# Contract version must match across internal + public SDK + config + schemas + corpus
grep -rn 'agent-sync/v1' internal/adapter pkg/adapterkit internal/adapter/bundled/*/capabilities.yaml
# And confirm zero stragglers of the old value:
grep -rn 'aienvs/v1' . --include='*.go' --include='*.yaml' --include='*.json'
```

### 5. Don't rewrite history; preserve dated artifacts — but fix their links

Dated docs (`docs/{plans,brainstorms,handoffs,solutions}/`) keep the old name on purpose
— rewriting them falsifies history. But links *to* those files, embedded in live docs
and code comments, must keep pointing at the real (unrenamed) filenames. The blanket sweep
will mangle those link paths; restore them afterward.

## Why This Matters

- **Data loss / contract desync.** On-disk and wire identifiers are how the tool finds
  prior state and how processes agree. A half-rename silently orphans state or breaks a
  handshake. (We preserved the swap/ledger *logic* and only moved directory *names* —
  invariant-asserting `-race` tests confirmed it.)
- **The final-grep allowlist has a blind spot.** Our completion gate was
  `grep -rni aienv` returning only allowlisted historical paths. It passed — but it
  **structurally cannot catch a link that was wrongly renamed TO `agent-sync`** (the
  mangled form contains no `aienv`). A reviewer caught a broken
  `docs/plans/...feat-agent-sync-workspace-cli-plan.md` reference in a `.go` comment that
  our grep was blind to. Lesson: also grep for the *broken target form*
  (`grep -rn 'feat-agent-sync-workspace-cli-plan'`), not just the old name.
- **Cross-platform constants.** Renaming a path constant (e.g. cache `DirName =
  "agent-sync/repos"`) is safe on Windows *only because* comparison sites already
  normalize with `filepath.ToSlash`. Audit comparison sites after renaming any path
  constant (see [[go-windows-cross-platform-pitfalls-2026-04-24]]).

## When to Apply

- Any rename touching on-disk paths, file formats, markers, key prefixes, env vars, or
  protocol/version strings — not just code symbols.
- Whenever a to-be-renamed token is a prefix of a token that must stay.
- Whenever the renamed value is a contract shared across a process or SDK boundary.

## Examples

**The trap that the completion grep missed (real):**

```
# Final gate, looked clean:
$ grep -rni aienv . --exclude-dir=.git | grep -v <allowlist>
# (empty) ✓

# But a .go comment had been mangled to a broken link the grep can't see:
internal/adapter/subprocess.go:186:
  // ... see docs/plans/2026-04-21-001-feat-agent-sync-workspace-cli-plan.md  ← file is actually feat-aienvs-...
```

Fix: a second grep for the broken *new-form* link, run across ALL files (not just the
handful you remembered to check):

```bash
grep -rn 'feat-agent-sync-workspace-cli-plan\|agent-sync-agent-workspace-requirements' . --exclude-dir=.git
```

**Process note (real):** the multi-agent code review degraded mid-run when the org spend
limit was hit; 6 of 7 reviewers returned nothing. The one reviewer that completed
(learnings) plus inline wire-boundary greps + the full CI matrix carried the verification.
See [[spend-limit-degrades-review]] — for a breaking rename, the cross-platform CI matrix
is the load-bearing safety net when subagent review is unavailable.

## Related

- [[go-windows-cross-platform-pitfalls-2026-04-24]] — audit path-constant comparison sites after a rename
- [[spec-impl-drift-at-pr-review-2026-04-25]] — update `docs/spec/` in the same change as the code it describes
- Plan: `docs/plans/2026-06-10-001-refactor-aienvs-to-agent-sync-rename-plan.md` (KTD-1 split rule, KTD-4 per-commit-green)

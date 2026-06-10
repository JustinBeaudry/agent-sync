# Trust store v1

This document is the frozen contract for the two-tier trust system introduced
in Unit 6 of the v1 implementation plan. Downstream code (policy engine, CLI,
sync pipeline, CI `verify` gate) treats this spec as the source of truth.

Scope:

- [`trust.jsonl`](#trustjsonl) — per-user append-only history.
- [`pending.jsonl`](#pendingjsonl) — per-user queue of SHAs observed during
  sync that the user has not yet reviewed.
- [`trusted_sha:`](#trusted_sha-in-agent-syncyaml) — committed project pin in
  `.agent-sync.yaml`.
- [Three error classes](#error-classes) — `RevokedTrustAnchor`,
  `TrustDecisionRequired`, `FirstUseDenied`.
- [Canonical URL form](#canonical-url-form) — the key under which records are
  stored.

## Design rationale

Two stores, two purposes. This matches the precedent set by Go modules
(`go.sum` in-repo + `GOMODCACHE` per-user), npm (`package-lock.json` in-repo
+ `~/.npm` per-user), and OpenSSH (committed `known_hosts` in configuration
management + `~/.ssh/known_hosts` per-user):

- **Project pin (`trusted_sha:`)** is authoritative in non-interactive /
  CI contexts. Sync refuses to proceed on a mismatch, with no prompt, so CI
  fails closed on supply-chain drift.
- **User history (`trust.jsonl`)** is authoritative for interactive
  cross-project recall. A developer who has previously trusted
  `github.com/foo/bar@abc123` on one machine is not asked again on the next
  workspace that references the same canonical source.

There is **no cryptographic integrity** on either store in v1. The threat
model is honest about this: `trust.jsonl` is only as trustworthy as the user
account. Supply-chain defense lives at `trusted_sha:` (the committed pin
that a human code-reviewed into the repo) and at the canonical-URL
canonicalizer (which strips `userinfo` so cache keys cannot be poisoned).

## `trust.jsonl`

Path: `$XDG_DATA_HOME/agent-sync/trust.jsonl` (resolved via `adrg/xdg`; on macOS
this is `~/Library/Application Support/agent-sync/trust.jsonl`, on Linux
`~/.local/share/agent-sync/trust.jsonl`, on Windows
`%LOCALAPPDATA%\agent-sync\trust.jsonl`).

File mode: `0600`. Parent directory mode: `0700`.

Encoding: UTF-8. One JSON object per line, terminated by `\n`. No leading or
trailing whitespace inside a line. Empty lines are ignored on read for
forward compatibility.

### Line schema

```json
{
  "ts":       "<RFC3339 timestamp with second precision and trailing Z>",
  "op":       "trust | promote | revoke | allow-new-shas-on | allow-new-shas-off",
  "url":      "<canonical URL — see Canonical URL form below>",
  "sha":      "<40-lowercase-hex commit SHA, or empty string for allow-new-shas-off>",
  "prev_sha": "<40-lowercase-hex commit SHA, or empty string when not applicable>",
  "source":   "cli | wizard | ci",
  "actor":    "<operating-system username as reported by user.Current()>",
  "hostname": "<short hostname as reported by os.Hostname()>"
}
```

Every field is required. Unknown fields in a record are ignored on read (not
rejected) so older binaries tolerate newer records. Implementations MAY add
fields in a future minor version; they MUST NOT remove or repurpose
existing ones.

### Ops

| Op | Meaning | `sha` | `prev_sha` |
|---|---|---|---|
| `trust` | User has accepted this URL + SHA pair (first-URL or `trust add`). | required | `""` if first-URL, else previous trusted SHA |
| `promote` | User has accepted a new SHA for a URL that already has a trust record (`trust promote` from the batch-review flow). | required | required — the SHA being replaced |
| `revoke` | User has explicitly withdrawn trust from this URL. | `""` | the SHA at time of revoke |
| `allow-new-shas-on` | User has opted into auto-promotion for this URL, optionally with a cooldown window encoded in the next-line record (see below). | `""` | `""` |
| `allow-new-shas-off` | User has reversed `allow-new-shas-on`. | `""` | `""` |

#### `allow-new-shas-on` cooldown encoding

When `--cooldown=<duration>` is supplied, the record carries the cooldown in
a derived field `allow_new_shas_cooldown_seconds` appended to the line:

```json
{"ts":"2026-05-01T12:00:00Z","op":"allow-new-shas-on","url":"…","sha":"","prev_sha":"","source":"cli","actor":"…","hostname":"…","allow_new_shas_cooldown_seconds":604800}
```

Readers that don't know the field ignore it, per the unknown-field rule.
When the field is absent, the cooldown is treated as *indefinite* (until an
explicit `allow-new-shas-off`).

### Fold semantics

Current state per URL is derived by folding the log in file order:

1. Start with an empty map `url → state`.
2. For each record in order, apply the op:
   - `trust` | `promote`: set the URL's state to `{current_sha: sha, last_op: op, last_op_ts: ts, revoked: false, allow_new_shas_on: existing}`.
   - `revoke`: set `revoked: true, current_sha: ""` (preserve the full op
     record in the log for audit; readers that want the pre-revoke SHA walk
     backward to find it).
   - `allow-new-shas-on`: set `allow_new_shas_on: true` with the encoded
     cooldown (or indefinite). Does not clear `revoked`.
   - `allow-new-shas-off`: set `allow_new_shas_on: false`.

Any record with an unrecognized `op` is ignored (forward compat).

This mirrors SSH `known_hosts` semantics (newest record wins for each host
key) and Go `go.sum` (verify against the set, not the order).

### Atomic append

Writers create a record with `encoding/json`, append a `\n`, and invoke a
single `write(2)` for the full byte slice. On POSIX-like filesystems,
writes up to `PIPE_BUF` (typically 4096 bytes, larger than any trust
record) are atomic — two writers cannot produce interleaved bytes. Windows
uses `FILE_APPEND_DATA` with `OVERLAPPED` `Offset=0xFFFFFFFF` to achieve the
equivalent via the OS-level append-only rule. No cross-process lock is
required for single-line appends; a process-level mutex guards in-process
concurrency.

`Compact()` is the one operation that rewrites the file: it writes to a
sibling `trust.jsonl.tmp`, then renames over the original, both steps
inside a `gofrs/flock` advisory lock on `trust.jsonl.lock` to serialize with
other `Compact()` invocations. Appends during compaction race with the
rename harmlessly: if a concurrent append writes to the old inode and the
rename replaces it, the next fold run reads the compacted file (losing the
concurrent append is acceptable — the append operation is retriable and the
caller is interactive by construction at compact time).

### Compaction

`agent-sync trust compact` rewrites the log when it exceeds **1 MiB**:

- Retain the most-recent `trust` / `promote` / `allow-new-shas-*` record
  per URL.
- Retain **every** `revoke` record (revokes are audit-grade and must survive
  compaction).
- Ordering: sort by original `ts` ascending; on tie, by URL ascending.

After compaction, re-folding the file MUST yield the same current state as
folding the pre-compaction file. This is a test invariant.

## `pending.jsonl`

Path: `$XDG_STATE_HOME/agent-sync/pending.jsonl` (Linux
`~/.local/state/agent-sync/pending.jsonl`; macOS falls through to
`~/Library/Application Support/agent-sync/pending.jsonl` since XDG_STATE_HOME is
not standardized on macOS — `adrg/xdg` handles the mapping).

File mode: `0600`. Parent directory mode: `0700`.

### Purpose

`pending.jsonl` is the audit queue for SHA updates observed during
non-interactive sync. When a sync encounters a known URL with a new SHA,
it does **not** prompt — it appends to `pending.jsonl` and emits a one-line
stderr reminder. The user drains the queue out-of-band via
`agent-sync trust pending` → `agent-sync trust promote`.

### Line schema

```json
{
  "ts":      "<RFC3339 timestamp>",
  "url":     "<canonical URL>",
  "new_sha": "<40-lowercase-hex>",
  "old_sha": "<40-lowercase-hex — the currently trusted SHA>"
}
```

### Lifecycle

- **Append** on every sync that observes a known URL with a new resolved
  SHA. Idempotent: if the (url, new_sha) pair already appears as the latest
  entry for that URL, the append is skipped (prevents the queue from
  ballooning on repeated syncs of the same manifest).
- **List** via `agent-sync trust pending` (latest entry per URL, newest first).
- **Clear** on `agent-sync trust promote <url>` and `agent-sync trust promote --all`.
  Cleared entries are simply removed from the file via rewrite.

Pending is a queue, not a log; no history is preserved after promotion.
Audit history lives in `trust.jsonl` (the `promote` op).

## `trusted_sha:` in `.agent-sync.yaml`

Top-level field on the manifest (see `internal/manifest/schema.go`).

```yaml
version: 1
canonical:
  url: https://github.com/example/canonical-rules
  ref: main
  commit: 0123456789abcdef0123456789abcdef01234567
trusted_sha: 0123456789abcdef0123456789abcdef01234567
```

Rules:

1. `trusted_sha` is 40 lowercase hex characters when present.
2. When present, it MUST equal `canonical.commit`. `agent-sync trust verify` is
   the CI gate that enforces this.
3. When absent, sync in interactive mode falls through to the user history.
   Sync in non-interactive mode refuses with `TrustDecisionRequired` (exit
   4) and a documented remediation.
4. `agent-sync trust pin` writes / updates the field using the manifest's
   comment-preserving writer (see `internal/manifest/write.go`).
5. `agent-sync trust promote --pin-manifest` updates both `trust.jsonl` and
   `trusted_sha:` atomically in the sense that a failure in either step
   leaves a clear recovery path: if the JSONL append succeeds but the
   manifest write fails, the user gets an error naming both files; the next
   `promote` retries the manifest write.

## Error classes

These error classes are returned by `internal/trust/policy.go`'s `Decide()`
function. Each maps to a documented exit code surfaced by the CLI.

| Class | Exit | When | Prompt? |
|---|---|---|---|
| `RevokedTrustAnchor` | 3 | A URL previously `revoke`d re-appears in a sync or trust op. | **Never**, even on TTY. |
| `TrustDecisionRequired` | 4 | Non-interactive context needs a trust decision (first-URL with no `--accept-new-source`, or `trusted_sha` mismatch). | **Never** — exits immediately. |
| `FirstUseDenied` | 5 | Interactive first-URL prompt declined by the user. | N/A — returned after the prompt ran. |

All three are typed errors (`errors.Is`-compatible sentinel wrapping) so
callers can branch without string-matching.

Exit codes 1 and 2 retain their standard meanings (generic error, misuse)
and are reserved for the CLI layer — the trust package never returns them.

## Canonical URL form

Trust records are keyed on the canonical URL produced by
`internal/cache.CanonicalizeURL` (Unit 4). Properties:

- `userinfo` is stripped.
- Scheme lowercased.
- Default ports removed.
- Trailing `.git` removed.
- Path lowercased on hosts that are case-insensitive (GitHub, GitLab,
  Bitbucket — per their published policies); preserved elsewhere.
- Fragment stripped; query parameters stripped.

Local-path sources use an absolute, symlink-resolved path as their
canonical form, prefixed with `file://`.

Any record written with a non-canonical URL is a bug; readers tolerate it
via the unknown-field rule but tooling emits a warning on fold.

## Concurrency

- **Appends** to `trust.jsonl` and `pending.jsonl` are lock-free (atomic
  single-`write(2)` under `PIPE_BUF`).
- **Compaction** and **Clear** take a `gofrs/flock` advisory lock on the
  sibling `*.lock` file. TryLock with 5-second timeout; on timeout, emit
  `ErrLocked` and abort — the caller retries or fails.
- **Fold** reads are unlocked. A concurrent append during a fold may be
  observed partially; the fold re-reads on `io.UnexpectedEOF` up to 3 times
  before failing.

## Test invariants

The following invariants are test-suite-enforced:

1. `Fold(append(log, r))` ⊇ `Fold(log)` for every `trust`/`promote` record `r`
   (new records never erase existing URL state except when `revoke`).
2. `Compact(log)` preserves `Fold(log)` exactly (modulo the revoke-history
   retention rule).
3. Appending the same `(url, sha, op)` record twice folds to the same state
   as appending it once (idempotent fold).
4. `Decide(ctx, url, resolvedSHA, trustedSHA, state, flags)` is a pure
   function — no I/O, no time.Now, no random — so the test matrix is
   exhaustively enumerable.
5. No non-interactive code path ever reaches `prompt.go`. The policy
   engine's non-interactive branches return error classes directly; the CLI
   layer converts them to exit codes.

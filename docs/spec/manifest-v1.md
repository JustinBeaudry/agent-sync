---
title: "aienvs manifest schema (v1)"
status: active
date: 2026-04-21
owner: internal/manifest
---

# aienvs manifest schema — v1

The manifest is the single file that binds a workspace directory to its
canonical agent-config source. Exactly one manifest lives per workspace;
running `aienvs sync` resolves the workspace by walking up from cwd (or
honoring `--workspace <path>`) and reading the file at `<root>/.aienv.yaml`.

This document is **authoritative** for the v1 schema. `internal/manifest`
is the only package that may parse or write `.aienv.yaml`.

## File name and location

- Name: **`.aienv.yaml`** (literal, lowercase, singular).
- Location: workspace root (the directory the user chose when running
  `aienvs init`). Discovery semantics are in `internal/workspace`
  (unit 3).

## Schema

```yaml
# --- Required ----------------------------------------------------------

# Manifest schema version. v1 is the only accepted value today.
version: 1

# Canonical agent-config source. Exactly one of `url` or `local_path`
# must be set; setting both is a load error.
canonical:
  url: https://github.com/example/agents-config.git  # exclusive with local_path
  # local_path: /absolute/path/to/a/working/clone     # exclusive with url

  # Optional git ref (branch or tag). `aienvs init` resolves this to a
  # SHA unless `--defer-resolve` is used.
  ref: main

  # 40-char lowercase hex commit SHA. Pinning is the default — `init`
  # writes this back after resolving `ref`. Mutually required with
  # `trusted_sha` when non-empty.
  commit: 1111111111111111111111111111111111111111

# --- Recommended (required by `--non-interactive` / CI when pinned) ---

# Project-level trust anchor. Committed to git. CI fails closed if the
# resolved SHA drifts from this value. Must match `canonical.commit`
# exactly. See plan decision #9.
trusted_sha: 1111111111111111111111111111111111111111

# --- Optional ---------------------------------------------------------

# Explicit opt-in to floating-ref mode. Must be accompanied by the
# `--floating` flag on `aienvs init`/`sync`. When true, `commit` may be
# absent and each sync re-resolves from `ref`.
floating: false

# Target adapters active for this workspace. Inactive adapters emit
# nothing.
targets:
  - claude
  - cursor
  - codex

# Scope hint informing per-target emission paths. One of:
#   user | project | global
# Empty (default) means no explicit hint.
scope: project

# Cache overrides. Empty means "use user XDG cache".
cache:
  override: /custom/workspace-local/cache

# Adapter source pins (reserved for Unit 20; v1 bundled adapters ignore
# this section).
adapters:
  - name: claude
    source: github.com/example/aienvs-adapter-claude
    pin: true
    trusted_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa

# Reserved for future use. `require_attestation` is ignored in v1; the
# cooldown window is defined but only acted on by Unit 6.
trust:
  require_attestation: false
  allow_new_shas_until: 2026-06-01T00:00:00Z

# --- Forward-compat extension keys ------------------------------------

# Keys whose names start with `x-` are explicitly accepted by the
# loader and ignored by core. This provides a stable extension surface
# for community tooling.
x-local-note: any-string-you-like
```

## Validation rules

The loader enforces these at parse time. Any violation returns
`ErrInvalidManifest` wrapping a specific cause.

| Rule | Error text excerpt |
|------|---------------------|
| Unknown top-level key | `unknown field` (goccy formatter names line + field) |
| `canonical.url` AND `canonical.local_path` both set | `canonical source must set exactly one of url or local_path (got both)` |
| Neither `canonical.url` nor `canonical.local_path` set | `canonical source must set exactly one of url or local_path (got neither)` |
| `trusted_sha` set, `canonical.commit` empty | `trusted_sha is set but canonical.commit is empty (no floating-with-pin hybrid)` |
| `canonical.commit` not 40-hex | `canonical.commit must be 40 lowercase hex` |
| `trusted_sha` not 40-hex | `trusted_sha must be 40 lowercase hex` |
| `canonical.commit` ≠ `trusted_sha` | `trusted_sha must mirror canonical.commit` |
| `scope` not in `{"", user, project, global}` | `scope must be one of user|project|global` |
| Non-interactive + `canonical.commit` set but `trusted_sha` empty | `non-interactive mode requires trusted_sha when canonical.commit is set` |
| `version != 1` (and not 0) | `unsupported manifest version N (want 1)` |

Rules that are **deliberately** not enforced at load:

- `floating: true` without the `--floating` CLI flag — caught at sync
  level (a manifest marked floating is always parseable; only sync
  refuses if the flag is missing).
- `canonical.commit` reachability from `canonical.ref` on the remote —
  network operation, handled by unit 5.
- Pre-existing keys for `WriteResolvedSHA` — checked only when that
  function is called (returns `ErrKeyMissing`).

## Field reference

### `version` (int, required)

Manifest schema version. v1 is the only accepted value. The loader
defaults `version: 0` → `1` silently during the greenfield phase; once
aienvs has shipped, this default will be removed and the field becomes
strictly required.

### `canonical` (mapping, required)

Describes where the portable agent-config source lives.

- `canonical.url` (string, xor `local_path`): Git URL.
- `canonical.local_path` (string, xor `url`): absolute filesystem path
  to a clone. Intended for air-gapped environments and aienvs
  development itself.
- `canonical.ref` (string, optional): symbolic git ref; only consumed
  by `aienvs init` to resolve to `commit`.
- `canonical.commit` (string, optional when `floating: true`): 40-char
  lowercase hex SHA. Pinning is the default.

### `trusted_sha` (string, optional)

The per-project trust anchor. Lockfile-style: committed to the repo
alongside `canonical.commit`. CI's `aienvs trust verify` fails closed if
this drifts from the resolved SHA. **Must** mirror `canonical.commit`
exactly when both are set.

### `floating` (bool, default false)

Explicit opt-in to floating-ref mode. Must be paired with a CLI
`--floating` flag at init/sync (enforced at sync, not load).

### `targets` (seq of string, optional)

Names of active adapters. Default (empty) means "no adapters active" —
a valid state for a workspace that is not yet configured. Adapters not
listed here emit nothing.

### `scope` (string, optional)

One of `user | project | global`. Informs the adapters where the
emitted files live relative to the tool's own scope discovery. Empty
means "no explicit hint."

### `cache` (mapping, optional)

- `cache.override` (string, optional): absolute path overriding the
  default user-cache location. Use when you want the clone to live
  inside the workspace (air-gap, portability).

### `adapters` (seq of mapping, optional)

Out-of-tree adapter source pins. Reserved for Unit 20; v1 bundled
adapters ignore this section.

### `trust` (mapping, optional)

- `trust.require_attestation` (bool, reserved): ignored in v1; placeholder
  for supply-chain attestation verification (post-v1).
- `trust.allow_new_shas_until` (RFC3339 timestamp, optional): cooldown
  window within which new resolved SHAs auto-promote without manual
  review. Consumed by Unit 6's trust flow.

## Extension keys (`x-*`)

Any top-level key whose name starts with `x-` is allowed by the
loader's `AllowFieldPrefixes("x-")` configuration. aienvs core ignores
them. Third-party tooling (editor plugins, CI lint rules, workspace
dashboards) can use the prefix to attach additional metadata without
needing a core schema bump.

## Writing the manifest — `WriteResolvedSHA`

`WriteResolvedSHA(orig []byte, commit, trustedSHA string) → []byte`
is the only supported way for aienvs to mutate an existing manifest.

**Contract:**

1. Input is the exact source bytes of the current manifest.
2. Both target keys (`canonical.commit` and `trusted_sha`) **must already
   exist** in `orig`, with any value (including the empty string). If
   either key is missing, the function returns `ErrKeyMissing` — it
   **never** appends silently into a nested mapping (doing so would
   lose user intent around indentation and ordering).
3. The function updates the scalar values via goccy's AST
   `PathString().ReplaceWithReader`; every comment and key outside the
   targeted nodes is preserved byte-for-byte.
4. A trailing newline is normalized into the output.
5. Pass `""` for either argument to skip updating that key.

`WriteTrustedSHA(orig, trustedSHA)` is a thin alias used by
`aienvs trust pin`; it updates only `trusted_sha:`.

`WriteFile(path, content)` writes the manifest through
`internal/fsroot.StagedWrite` — atomic, containment-checked, crash-safe.

## Init template

`aienvs init` emits a template that pre-declares the keys
`WriteResolvedSHA` expects. A minimal example lives at
`testdata/manifest/valid-template-unresolved.yaml`.

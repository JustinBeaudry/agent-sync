---
title: "agent-sync manifest schema (v1)"
status: active
date: 2026-04-21
owner: internal/manifest
---

# agent-sync manifest schema — v1

The manifest is the single file that binds a workspace directory to its
canonical agent-config source. Exactly one manifest lives per workspace;
running `agent-sync sync` resolves the workspace by walking up from cwd (or
honoring `--workspace <path>`) and reading the file at `<root>/.agent-sync.yaml`.

This document is **authoritative** for the v1 schema. `internal/manifest`
is the only package that may parse or write `.agent-sync.yaml`.

## File name and location

- Name: **`.agent-sync.yaml`** (literal, lowercase, singular).
- Location: workspace root (the directory the user chose when running
  `agent-sync init`). Discovery semantics are in `internal/workspace`
  (unit 3).

## Schema

```yaml
# --- Required ----------------------------------------------------------

# Manifest schema version. v1 is the only accepted value today.
version: 1

# Canonical agent-config source. Exactly one of `url`, `local_path`, or
# `local_dir` must be set; setting more than one is a load error.
#
#   url        — a remote git repository (cloned + pinned by `commit`).
#   local_path — a local git repository / clone (opened + pinned by `commit`).
#   local_dir  — an in-repo working-tree directory read straight from the
#                filesystem. Unpinned by nature: no `commit`/`ref`/`trusted_sha`,
#                and exempt from trust (TOFU) and offline-strict.
canonical:
  url: https://github.com/example/agents-config.git  # exclusive with local_path/local_dir
  # local_path: /absolute/path/to/a/working/clone     # exclusive with url/local_dir
  # local_dir: .agents                                 # exclusive with url/local_path

  # Optional git ref (branch or tag). `agent-sync init` resolves this to a
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

# floating: reserved; not yet in the v1 schema. Will be added by Unit 5/6.

# Target adapters active for this workspace. Inactive adapters emit
# nothing.
targets:
  - claude
  - cursor
  - codex

# Scope hint informing per-target emission paths. One of:
#   user | workspace | project | global
# `global` is a legacy alias for project-level emission.
# Empty (default) means no explicit hint.
scope: project

# Marks this workspace manifest as an activation boundary for hierarchy
# discovery. Valid only with `scope: workspace`.
activation_root: false

# Cache overrides. Empty means "use user XDG cache".
cache:
  override: /custom/workspace-local/cache

# Adapter source pins (reserved for Unit 20; v1 bundled adapters ignore
# this section). `pin` and `trusted_sha` on adapters are reserved; not
# yet in the v1 schema. Will be added by Unit 20.
adapters:
  - name: claude
    source: github.com/example/agent-sync-adapter-claude

# trust: reserved; not yet in the v1 schema. Fields `require_attestation`
# and `allow_new_shas_until` will be added by Unit 5/6.
trust:

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
| Not exactly one of `canonical.url` / `local_path` / `local_dir` set | `canonical source must set exactly one of url, local_path, or local_dir (got N)` |
| `canonical.local_dir` set with `commit`/`ref`/`trusted_sha` | `canonical.local_dir is an unpinned working-tree source and cannot set commit, ref, or trusted_sha` |
| `canonical.local_dir` is the workspace root (`.`) | `canonical.local_dir must name a workspace subdirectory, not the workspace root` |
| `canonical.local_dir` is absolute / rooted / contains `..` | `canonical.local_dir: …` (wraps the reserved-prefix path rules) |
| `trusted_sha` set, `canonical.commit` empty | `trusted_sha is set but canonical.commit is empty (no floating-with-pin hybrid)` |
| `canonical.commit` not 40-hex | `canonical.commit must be 40 lowercase hex` |
| `trusted_sha` not 40-hex | `trusted_sha must be 40 lowercase hex` |
| `canonical.commit` ≠ `trusted_sha` | `trusted_sha must mirror canonical.commit` |
| `scope` not in `{"", user, project, workspace, global}` | `scope must be one of user|project|workspace|global` |
| `activation_root: true` without `scope: workspace` | `activation_root can only be used when scope is "workspace"` |
| Non-interactive + `canonical.commit` set but `trusted_sha` empty | `non-interactive mode requires trusted_sha when canonical.commit is set` |
| `version != 1` (and not 0) | `unsupported manifest version N (want 1)` |
| `cache.override` is non-empty and not absolute | `cache.override must be absolute` |
| `cache.override` contains `..` segments | `cache.override must not contain .. segments` |

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
agent-sync has shipped, this default will be removed and the field becomes
strictly required.

### `canonical` (mapping, required)

Describes where the portable agent-config source lives.

- `canonical.url` (string; exactly one of url/local_path/local_dir): Git URL.
- `canonical.local_path` (string; exactly one of url/local_path/local_dir):
  absolute filesystem path to a git clone. Intended for air-gapped
  environments and agent-sync development itself. Pinned by `commit`.
- `canonical.local_dir` (string; exactly one of url/local_path/local_dir):
  a workspace-relative directory (e.g. `.agents`) whose contents are compiled
  straight from the **working tree** — no git, no clone, no commit. Unpinned by
  nature and exempt from trust (TOFU) and offline-strict, since there is
  nothing remote to fetch, pin, or trust. Must be a clean, non-root,
  workspace-relative path. The `agent-sync-` skill-id prefix is reserved for
  emitted output in the shared `.agents/skills` tree and is not available for
  authored skill ids (the reader skips `skills/agent-sync-*`).
- `canonical.ref` (string, optional): symbolic git ref; only consumed
  by `agent-sync init` to resolve to `commit`. Not valid with `local_dir`.
- `canonical.commit` (string, optional when `floating: true`): 40-char
  lowercase hex SHA. Pinning is the default. Not valid with `local_dir`.

### `trusted_sha` (string, optional)

The per-project trust anchor. Lockfile-style: committed to the repo
alongside `canonical.commit`. CI's `agent-sync trust verify` fails closed if
this drifts from the resolved SHA. **Must** mirror `canonical.commit`
exactly when both are set.

### `floating` (reserved — not yet in v1 schema)

Will be added by Unit 5/6. Explicit opt-in to floating-ref mode. Must be
paired with a CLI `--floating` flag at init/sync (enforced at sync, not load).

### `targets` (seq of string, optional)

Names of active adapters. Default (empty) means "no adapters active" —
a valid state for a workspace that is not yet configured. Adapters not
listed here emit nothing.

### `scope` (string, optional)

One of `user | workspace | project | global`. Informs the adapters where the
emitted files live relative to the tool's own scope discovery. Empty
means "no explicit hint." `global` is accepted as a legacy alias for
project-level emission.

### `activation_root` (bool, optional)

Marks this manifest as the activation boundary for hierarchy discovery.
When a sync starts inside an activation root, discovery stops at that
workspace and does not continue to the user-home root. Valid only with
`scope: workspace`.

### `cache` (mapping, optional)

- `cache.override` (string, optional): absolute path overriding the
  default user-cache location. Use when you want the clone to live
  inside the workspace (air-gap, portability).

### `adapters` (seq of mapping, optional)

Out-of-tree adapter source pins. Reserved for Unit 20; v1 bundled
adapters ignore this section.

### `trust` (mapping, optional)

The `trust` mapping is present in the schema as a placeholder but contains
no active fields in v1. Fields `require_attestation` and `allow_new_shas_until`
are reserved and will be added by Unit 5/6.

## Extension keys (`x-*`)

Any top-level key whose name starts with `x-` is allowed by the
loader's `AllowFieldPrefixes("x-")` configuration. agent-sync core ignores
them. Third-party tooling (editor plugins, CI lint rules, workspace
dashboards) can use the prefix to attach additional metadata without
needing a core schema bump.

## Writing the manifest — `WriteResolvedSHA`

`WriteResolvedSHA(orig []byte, commit, trustedSHA string) → []byte`
is the only supported way for agent-sync to mutate an existing manifest.

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
`agent-sync trust pin`; it updates only `trusted_sha:`.

`WriteFile(path, content)` writes the manifest through
`internal/fsroot.StagedWrite` — atomic, containment-checked, crash-safe.

## Init template

`agent-sync init` emits a template that pre-declares the keys
`WriteResolvedSHA` expects. A minimal example lives at
`testdata/manifest/valid-template-unresolved.yaml`.

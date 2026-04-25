# IR v1

The Intermediate Representation (IR) is the format `aienvs` decodes
canonical-repo content into before any adapter sees it. Every adapter
(claude, cursor, codex, …) consumes IR, never raw canonical files; every
target tool's output is derived from IR, never from another tool's output.

This document is the frozen v1 contract. Decoder output, adapter input, and
test fixtures all reference it.

## Contents

- [Concept set](#concept-set) — the closed set of 6 node kinds.
- [Canonical repo layout](#canonical-repo-layout) — where each kind lives on disk.
- [Frontmatter](#frontmatter) — YAML metadata block format and recognized fields.
- [Node](#node) — the IR record the decoder produces.
- [Determinism](#determinism) — invariants the decoder must satisfy.
- [Errors](#errors) — when the decoder rejects vs. accepts with warnings.
- [Capability matrix](#capability-matrix) — how adapters describe what they support.

## Concept set

Closed set, frozen at v1 (origin requirement R11). Adding a kind is a
v2-grade change that requires a separate spec.

| Kind | What it is | Typical filename |
|---|---|---|
| `agents-md` | Repo-root project guidance file. `AGENTS.md` is the canonical name; `CLAUDE.md` / `GEMINI.md` are recognized overlays that produce additional, target-scoped `agents-md` nodes. | `AGENTS.md` |
| `rule` | A short, behavior-shaping guideline. | `rules/<id>.md` |
| `skill` | A multi-file skill bundle (markdown + optional assets). | `skills/<id>/SKILL.md` |
| `command` | A user-invokable command definition (slash-commandable). | `commands/<id>.md` |
| `plugin-reference` | A pointer to an external plugin or extension to install. | `plugins/<id>.toml` |
| `mcp-server-entry` | An MCP server registration. | `mcp/<server>.json` |

## Canonical repo layout

The canonical repo is a normal git repository. The decoder reads from a
specific commit (Unit 5's `git.Repository` keyed by commit SHA). Tree
walks are case-sensitive even on case-insensitive filesystems — the
canonical SHA is authoritative.

```
canonical-repo/
├── AGENTS.md                 # one agents-md node, id "agents", Targets: [] (unscoped)
├── CLAUDE.md                 # optional overlay; separate agents-md node, id "claude", Targets: [claude]
├── GEMINI.md                 # optional overlay; separate agents-md node, id "gemini", Targets: [gemini]
├── rules/
│   └── <id>.md               # one rule node per file
├── skills/
│   └── <id>/
│       ├── SKILL.md          # one skill node per directory
│       └── <asset...>        # arbitrary skill assets, traversed and hashed
├── commands/
│   └── <id>.md               # one command node per file
├── mcp/
│   └── <server>.json         # one mcp-server-entry node per file
└── plugins/
    └── <id>.toml             # one plugin-reference node per file
```

### Node IDs

`<id>` segments must match `[a-z0-9][a-z0-9-_]{0,63}`. Decoder rejects
ids that violate the pattern. ID uniqueness is enforced **per kind**
(two skills can't share an id; a skill and a rule can).

For `agents-md` overlays at the canonical repo root the id is derived
from the filename basename, lowercased, with the extension stripped:
`AGENTS.md` → `agents`, `CLAUDE.md` → `claude`, `GEMINI.md` → `gemini`.
Each file is a distinct `agents-md` node, so the per-kind uniqueness
rule is satisfied automatically. The overlay's `Targets` is set from
the filename (`[claude]` for `CLAUDE.md`, `[gemini]` for `GEMINI.md`),
unioned with any `targets:` declared in frontmatter; `AGENTS.md` itself
ships unscoped (`Targets: []`, meaning all adapters).

### Recognized files vs. unknown files

Inside each IR-owned directory the decoder enumerates files with the
expected extension and treats anything else as an error — see
[Errors](#errors). This catches the typo case the plan calls out
("dropped a `.md` into `skills/` expecting it to register as a skill").

Outside IR-owned directories the decoder walks the tree but emits
nothing. A `README.md` at the canonical repo root is fine and ignored.

## Frontmatter

Every markdown-bearing node (agents-md, rule, skill, command) supports
optional YAML frontmatter at the top of the file:

```markdown
---
required: true
targets: [claude, cursor]
version: 2
---

The body of the rule / skill / command goes here.
```

Delimiter is `---\n` (three hyphens + newline) on a line by itself.
Closing delimiter is the same. The block is parsed with
`goccy/go-yaml`'s `DisallowUnknownField` so typos are surfaced.

### Recognized fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `required` | bool | `false` | Capability matrix MUST mark this node `supported` for every targeted adapter; failure means sync rejects with `required_unmet`. |
| `targets` | `[]string` | `[]` (means: all adapters) | Restrict which adapters see this node. Values must match a registered adapter name; unknown names are decode errors. |
| `version` | int | `1` | Author-controlled monotonic counter. Adapters MAY use it for migration (e.g., "this rule moved from path A to B between v1 and v2"). |

Unknown frontmatter fields are decode errors. `x-` prefixed fields are
reserved for forward-compat experimentation and are tolerated but ignored.

### Frontmatter on non-markdown nodes

`mcp-server-entry` (JSON) carries metadata in the body of the file via
reserved top-level keys `__aienvs_required`, `__aienvs_targets`,
`__aienvs_version` (so the file stays valid JSON). These keys are stripped
from the body the adapter receives.

`plugin-reference` (TOML) is decoded with default metadata (`required:
false`, `targets: []`, `version: 1`) in v1. Reserved-key extraction in
TOML files is deferred to a follow-up (introducing a TOML parser
dependency was not part of the v1 key-decisions list). The body is passed
through unchanged. Plugin authors who need `required: true` semantics in
v1 should mark a sibling rule node `required: true` referencing the plugin
or wait for the v1.x follow-up.

## Node

The decoder produces `[]Node` in a deterministic order (sorted by
`Kind`, then `ID`). Each Node carries:

```go
type Node struct {
    ID         string         // e.g. "no-pr-on-friday"
    Kind       Kind           // e.g. KindRule
    Version    int            // from frontmatter, default 1
    Required   bool           // from frontmatter, default false
    Targets    []string       // from frontmatter, empty == all
    Provenance Provenance     // where in the canonical repo this came from
    Body       []byte         // the post-frontmatter content (or full file for mcp/plugin)
}

type Provenance struct {
    Path     string  // posix-style path within the canonical repo
    BlobSHA  string  // 40-hex git blob SHA (NOT commit SHA — adapter-stable across rebases)
}
```

`Body` is byte-identical to the on-disk content with the frontmatter
block removed (for markdown) or with the reserved `__aienvs_*` keys
stripped (for JSON / TOML).

Skills with assets surface the assets as auxiliary blobs hung off the
skill Node:

```go
type Skill struct {
    Node
    Assets []Asset
}

type Asset struct {
    RelPath  string  // path relative to skills/<id>/
    Provenance
    Content  []byte
}
```

## Determinism

The decoder MUST satisfy these invariants. They are test-suite-enforced.

1. **Same commit SHA → byte-identical IR.** Decoding the same canonical
   repo at the same commit ten times in a row yields ten byte-identical
   `[]Node` slices (encoded via `json.Marshal` for comparison).
2. **Order is content-derived.** Node order is determined by `(Kind, ID)`
   ascending: nodes are grouped by kind in the order kinds are declared
   in `kinds.go`, and within a kind sorted lexicographically by `ID`.
   The test suite asserts this order against a fixture; reordering the
   test fixture's filesystem must not change IR output (the order is
   derived from node identity, not the host fs).
3. **Provenance is git-blob-keyed.** `BlobSHA` is the SHA git itself
   computes for the file content, not a `aienvs`-derived hash. This lets
   downstream tooling (ledger, validate, diff) cross-check against
   `git cat-file` output.
4. **No timestamps anywhere.** The IR carries no wall-clock fields.

## Errors

The decoder distinguishes errors (decode fails, no IR returned) from
warnings (decode succeeds, but the report has notes the CLI surfaces).

### Errors

| Error | When |
|---|---|
| `ErrUnrecognizedFile` | A file inside an IR-owned directory has the wrong extension (e.g. `rules/foo.txt`, `mcp/foo.yaml`). Carries the offending path. |
| `ErrInvalidID` | A node id does not match `[a-z0-9][a-z0-9-_]{0,63}`. |
| `ErrDuplicateID` | Two nodes of the same kind share an id. |
| `ErrUnknownTarget` | Frontmatter `targets:` lists an adapter name not in the registered set. |
| `ErrFrontmatterParse` | Frontmatter block is malformed (no closing `---`, invalid YAML, etc.). |
| `ErrUnknownFrontmatterField` | Frontmatter has a non-`x-` field outside the recognized set. |
| `ErrSkillMissingSKILL` | A directory under `skills/` lacks a `SKILL.md`. |
| `ErrEmptyAgentsMD` | `AGENTS.md` is present but zero-length. |

Each error carries the offending `Provenance` so the CLI can surface a
file:line reference.

### Warnings (not errors)

- `WarnAgentsMDMissing` — no `AGENTS.md` at root. Common in greenfield
  canonical repos; surfaced as a hint, not a failure.
- `WarnSkillAssetUnreadable` — a non-text skill asset cannot be UTF-8
  decoded. The asset is still emitted as base64 to adapters that
  declare `binary_assets: supported`; non-supporting adapters skip it
  with a degraded-capability note.

## Capability matrix

Each adapter declares its per-kind support level via a
`CapabilityStatus`:

```go
type CapabilityStatus string

const (
    CapSupported   CapabilityStatus = "supported"
    CapPartial     CapabilityStatus = "partial"
    CapUnsupported CapabilityStatus = "unsupported"
)
```

The `capmatrix` package owns the type and the merge function. Each
adapter's `capabilities.yaml` (declared in Unit 8) ships a
`map[Kind]CapabilityStatus` that the framework merges into a workspace
wide matrix at sync time. Required nodes (frontmatter `required: true`)
that map to `unsupported` for any targeted adapter cause sync to fail
with `ErrRequiredUnmet`. Required nodes mapped to `partial` succeed but
emit a `warning` op so the user sees the degradation.

Capability strings outside the three documented values are decode
errors at adapter-load time (Unit 8 wires this).

## Forward compatibility

- Adding a recognized frontmatter field is a non-breaking change at v1
  if it ships with a sensible default and existing IR consumers ignore it
  by default. Required for new fields: a sentinel test that an old IR
  fixture (frozen in `testdata/canonical/v1.0/`) still decodes cleanly
  after the field lands.
- Adding a concept kind is a v2-grade change. v1 is closed.
- Renaming or repurposing a frontmatter field is a v2-grade change.

This is the same compatibility posture as adapter protocol v1 (Unit 8):
extension via additive optional fields; structural changes are
version-bumps.

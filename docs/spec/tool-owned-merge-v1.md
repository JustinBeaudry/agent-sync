# Tool-owned-file merge — v1 contract

Authoritative contract for `internal/merge`: how aienvs surgically
inserts, updates, and removes its own managed entries inside
**user-owned** files without corrupting user-authored content. This is
the highest data-loss surface in aienvs; the rules here are
load-bearing. Implementation: `internal/merge/`. Adapters (Units 9–11)
emit `write_tool_owned` ops with these locator kinds; the sync engine
(Unit 13) drives `ApplyToFile`.

## Invariants (all formats)

- **Fail-closed.** On any parse error, ambiguous marker state, or
  invalid input, **no bytes are written** — the file is left
  byte-identical. The two sentinels are `ErrMalformedToolOwnedFile`
  (JSON/TOML) and `ErrMalformedManagedSection` (markdown).
- **Never rewrite a file we cannot parse.** A non-blank file that does
  not parse as its format is refused, never auto-fixed.
- **A blank file is a new file.** "Blank" = empty or only ASCII
  whitespace (space, tab, `\n`, `\r`). Deliberately **not**
  `strings.TrimSpace` (which strips `\f`/`\v`); a file containing such
  control junk is not blank and must parse or be refused.
- **Aienvs entries are keyed by the `aienvs_` prefix** (`aienvs_<id>`
  for JSON/TOML keys, `aienvs:<id>` for markdown locators). The `<id>`
  is extracted by exact-prefix strip (never by splitting on the last
  separator — ids may contain `_`/`-`).
- **Op is explicit.** `MergeEntry.Remove` (a boolean) selects
  delete-vs-upsert; it is **not** inferred from empty `Content` (a
  legitimately-empty markdown section is distinct from a removal).
- **Atomic, locked write.** `ApplyToFile` holds the Unit 12
  per-external-file flock across the read-merge-write and writes via
  `fsroot.StagedWrite` (temp + fsync + rename). It `MkdirAll`s the
  target's parent first (StagedWrite does not create parents).
- **`slice_hash`** is the SHA-256 of the exact rendered aienvs slice as
  written (the JSON value at the pointer / the rendered TOML table span
  / the markdown begin..end block). Empty on remove. Deterministic
  across runs, so the ledger sees no spurious drift.

## JSON (`.mcp.json`, `.cursor/mcp.json`)

- **Engine:** `tidwall/sjson` for the surgical set/delete by path;
  `encoding/json` validity check before any write.
- **Locator:** JSON pointer `/mcpServers/aienvs_<id>`, converted to an
  sjson dot-path (with `.`/`*`/`?`/`\` escaping per segment).
- **Preserved byte-identical:** all user keys and their values, key
  order, indentation style, and trailing-newline convention. Proven by
  the upsert-then-remove-is-identity round-trip and the no-op-upsert
  whole-file-byte-identical tests (incl. a minified input).
- **Rejected (`ErrMalformedToolOwnedFile`):** invalid JSON; a parent
  under the pointer that is not an object (e.g. `mcpServers` is an
  array/scalar); a pre-existing duplicate `aienvs_<id>` key under the
  parent (ledger drift); entry content that is not a valid JSON value.
- **Remove of the last aienvs entry keeps the now-empty parent**
  (`"mcpServers": {}`); the parent is not pruned (pruning a key aienvs
  may not have created requires provenance and is deferred).

## TOML (`.codex/config.toml`)

- **Engine:** a **string-aware line-splice** (NOT a `go-toml/v2`
  re-encode). go-toml/v2's decode→encode drops user `#` comments and
  rewrites quoting/formatting (verified), so the user's bytes are never
  re-rendered. Only the aienvs table's line span is inserted / replaced
  / removed; `go-toml/v2` is used only to **validate** the input parses
  and to validate the rendered aienvs table fragment.
- **Locator:** `mcp_servers.aienvs_<id>` → table `[mcp_servers.aienvs_<id>]`.
- **String-aware span location (load-bearing):** the line scanner
  tracks TOML multiline-string state (`"""`, `'''`), so a header-shaped
  line *inside* a user's multiline string is never mistaken for a table
  header — otherwise a splice could cross a table boundary and eat user
  bytes. A table's span runs from its header line to the line before the
  next top-level header (or EOF).
- **Content** is the raw TOML table **body** (`key = value` lines, no
  header); the engine renders the header + a managed comment + body and
  validates the whole parses before splicing.
- **Preserved byte-identical:** user comments, table order, and all
  user table bytes. Aienvs tables are appended after user content.
- **Rejected (`ErrMalformedToolOwnedFile`):** invalid TOML input; a
  pre-existing duplicate aienvs table; an aienvs body that does not
  parse as TOML.

## Markdown (`AGENTS.md`, `CLAUDE.md`)

- **Engine:** a line-based marker parser. Markers are recognized only at
  **column 0**. Both newline styles are tolerated; the file's dominant
  newline is detected and preserved.
- **Marker grammar (write):** `<!-- aienvs:begin id=<id>[ source=<src>] -->`
  / `<!-- aienvs:end id=<id> -->`. **Read is liberal:** the id-only form
  the adapters emit today (no `source=`) is accepted, as is the
  `source=`-bearing form.
- **The engine owns the markers; callers pass the INNER body.** A
  `body` that itself contains `<!-- aienvs:` is a programmer/contract
  error (prevents a double-wrap when a caller passes pre-wrapped adapter
  content). This is **not** a data-loss sentinel — it is a loud
  contract failure surfaced in tests.
- **Upsert:** replace the content between a well-formed `begin/end` pair
  for the id; user text outside is byte-identical. If absent, append a
  new section at EOF (a new/blank file gets a top-of-file
  "Partially managed by aienvs" header).
- **Recovery — refuse, don't guess (`ErrMalformedManagedSection`, names
  the line):** begin without end; end without begin; nested begin;
  mismatched end id; duplicate id.
- **Indented marker:** treated as user content (not a marker) — EXCEPT
  an indented marker whose id matches the id being managed, which is
  refused (silently appending a second copy would grow stale duplicates
  every sync). A non-matching indented marker yields a `Warning` (not an
  error) and the new section is appended at EOF; the indented line is
  preserved verbatim.
- **User-prose marker collision is fail-safe.** A bare parsed marker is
  not on its own sufficient to authorize replacement; provenance
  corroboration (matching the recorded `slice_hash` / `source=` token)
  is the caller's (Unit 13's) responsibility. The engine's contract is
  "refuse on ambiguity, never eat user prose."

## Returned to the caller (for the ledger)

`ApplyToFile` returns `slice_hash` and an optional `warning`. The caller
(Unit 13) folds `{path, locator_kind, locator_value, slice_hash}` into a
ledger entry (Unit 12) — this unit does not read or write the ledger.

---
title: "feat: Unit 8 PR 1 — adapter wire protocol (framing + JSON-RPC + protocol types + schemas)"
type: feat
status: active
date: 2026-04-25
origin: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# feat: Unit 8 PR 1 — adapter wire protocol

## Overview

PR 1 of a 3-PR carving for Unit 8 of the aienvs workspace CLI plan.
Establishes the on-the-wire alphabet of the `aienvs/v1` adapter protocol:
LSP-style `Content-Length`-framed JSON-RPC 2.0 envelope, the typed method
surface (`initialize` → `initialized` → `emit` → `shutdown`), the op union
exchanged inside `emit`, and per-method JSON Schemas with a parity test.

This PR ships **types and parsing only** — no process management, no
lifecycle orchestration, no spec freeze. PR 2 builds the runtime
(subprocess, in-process shim, manifest, discovery, lifecycle) on top of
these types. PR 3 ships the conformance harness, the `echo` reference
adapter, the public `pkg/adapterkit/`, and the authoritative
`docs/spec/adapter-protocol-v1.md`.

## Problem Frame

Adapters cannot be built without an agreed wire format. The parent plan
froze the *intent* of `aienvs/v1` (Unit 8, lines 599–672) but the bytes
do not exist yet. PR 1's job is to land the parsing/encoding layer in a
form that is fully unit-testable — no subprocess, no fixtures on disk —
so that the runtime layer in PR 2 has a stable target to call into.

The decisions PR 1 freezes are intentionally narrow: framing
shape, JSON-RPC envelope rules, MVP method/result types, and the op
union. Everything that *uses* the wire (lifecycle ordering,
declared-outputs gate, capability arbitration, conformance) is deferred.

(see origin: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
Unit 8 starting at line 599)

## Requirements Trace

- **R1.** LSP-style `Content-Length`/`Content-Type` framing reader/writer
  with a defense-in-depth max frame size — origin Unit 8 "Transport"
  paragraph.
- **R2.** JSON-RPC 2.0 envelope: `Request`, `Response`, `Notification`;
  ID correlator; standard JSON-RPC error code table only (no LSP
  extensions); `data.error_class` field carrying CLI-side classifier
  strings — origin Unit 8 "Errors" paragraph + file list line 611.
- **R3.** MVP method/result types: `initialize`, `initialized`, `emit`,
  `shutdown`. `Capabilities` struct, `DeclaredOutput` struct, reserved
  `_meta` on every envelope — origin Unit 8 "Lifecycle" paragraph + file
  list line 612.
- **R4.** Op union exchanged inside `emit`: `write_file`,
  `write_tool_owned`, `mkdir`, `delete`, `warning`. Default 8 MiB
  per-op payload cap. Base64 encoding for non-UTF-8 byte payloads —
  origin Unit 8 "Op set (emit stream)" paragraph.
- **R5.** Per-method JSON Schemas, one file per method, plus a parity
  test that fails CI when schema field names drift from Go struct JSON
  tags — origin Unit 8 file list line 613.
- **R6.** Tests cover happy-path round trips, malformed-envelope
  rejection, ID correlator concurrency safety, op encode/decode at the
  8 MiB boundary, base64 round-trip — origin Unit 8 "Test scenarios".

## Scope Boundaries

- **No process management.** No `os/exec`, no `io.Pipe` shim, no
  signal handling.
- **No lifecycle orchestration.** No code that *enforces* the
  `initialize → initialized → emit → shutdown` ordering. PR 1 ships
  only the typed messages; PR 2's runtime enforces ordering.
- **No declared-outputs enforcement.** PR 1 ships the
  `DeclaredOutput` *type*; PR 2's runtime rejects undeclared ops.
- **No spec doc.** `docs/spec/adapter-protocol-v1.md` ships in PR 3 once
  the conformance harness validates the spec against running code.
- **No public SDK.** `pkg/adapterkit/` ships in PR 3.
- **No magic-cookie validation.** PR 1 doesn't ship the
  `AIENVS_ADAPTER_COOKIE` check — that lives in the subprocess runner
  in PR 2. The `cookie` field is part of the `initialize` params type
  in this PR, but unchecked.

### Deferred to Separate Tasks

- **PR 2 (Unit 8 runtime):** subprocess runner, in-process shim,
  `adapter.yaml` manifest, PATH discovery, lifecycle orchestrator,
  declared-outputs gate, magic-cookie validation, capability-mismatch
  detection.
- **PR 3 (Unit 8 conformance + SDK):** golden-corpus conformance
  harness, `echo` Go reference adapter, `pkg/adapterkit/`,
  `docs/spec/adapter-protocol-v1.md`, `aienvs adapter conformance-test`
  CLI subcommand.
- **Unit 8b (post-v1, gated on third-party demand):** version
  counter-proposal, `cancel` notification, `$/progress` tokens,
  per-method timeout overrides in `adapter.yaml`, LSP error-code
  extensions (`-32800` / `-32801` / `-32803`), Python reference adapter
  (`echo-py`).

## Context & Research

### Relevant Code and Patterns

- `internal/ir/types.go` — house style for a typed package: package
  doc on `types.go`, sentinel errors as a `var(...)` block with
  `errors.Is` branching, plain `testing` (no testify), table-driven
  tests in `*_test.go` siblings, `t.Parallel()` everywhere, helpers in
  a separate `helpers_test.go` file.
- `internal/manifest/schema.go` — pattern for schema-adjacent code
  living next to types.
- `internal/git/helpers_test.go` — pattern for test-only helpers in a
  `_test.go` sibling (referenced by repo's auto-memory).
- `internal/cache/` and `internal/trust/` — patterns for
  cross-platform tests (Windows path concerns) — relevant because
  framing tests run on the CI matrix.

### Institutional Learnings

- `docs/solutions/2026-04-22-go-cross-platform-ci.md` (referenced from
  commit `763f994`) — Go test code that handles paths must use
  `filepath.ToSlash` and stay off Unix-only syscalls. Framing/JSON-RPC
  is pure byte work and has no path concerns, but the parity test
  must read `internal/adapter/contract/schema/*.json` via
  `filepath.Join`, not hard-coded forward slashes.
- `docs/solutions/2026-04-23-spec-vs-impl-drift.md` (referenced from
  commit `922a832`) — a parent learning: when a doc is the source of
  truth, the parity test is what keeps the doc and code from drifting.
  PR 1 applies this discipline at the schema layer (schema vs Go
  struct tag parity).

### External References

- LSP 3.17 base protocol header block:
  `Content-Length: <n>\r\nContent-Type: <type>; charset=<cs>\r\n\r\n<n bytes>`.
  ReadFrame must accept missing Content-Type for forward-compat with
  stricter LSP toolchains; WriteFrame always emits the aienvs media
  type.
- JSON-RPC 2.0 spec:
  - Standard error codes: `-32700` parse, `-32600` invalid request,
    `-32601` method not found, `-32602` invalid params, `-32603`
    internal, server-error range `-32099..-32000`.
  - `id: null` is reserved for error responses to requests whose ID
    couldn't be determined; the CLI never *sends* null IDs.
  - A response carries exactly one of `result` or `error`, never
    both, never neither.
- MCP 2025-11-25 lifecycle (referenced by parent plan):
  `_meta` on every envelope is the additive extension point. PR 1
  ships the field; PR 2/8b uses it.

## Key Technical Decisions

- **`FrameReader` over a one-shot `ReadFrame`.** Buffered readers
  swallow bytes between calls; a long-lived adapter connection carries
  many frames and must hold one buffer. `ReadFrame(r, max)` stays as a
  one-shot helper for tests and the pre-handshake init step. Already
  reflected in code on the branch.
- **JSON-RPC ID type accepts both int and string.** JSON-RPC 2.0
  permits both. The CLI emits ints (monotonic counter) but must echo
  client-supplied string IDs verbatim if any peer ever sends them. A
  small `ID` type with `IsInt()`, `IsString()`, `IsNull()`,
  `AsInt()`, `AsString()` and custom `MarshalJSON`/`UnmarshalJSON`
  hides the union.
- **ID correlator is mutex-guarded, not channel-based.** Sequence
  generation + pending-request tracking is map mutation under contention
  from goroutines that read frames and dispatch responses. A
  `sync.Mutex` around `next int64` and `pending map[int64]string`
  is simpler than a goroutine-and-channel design and easier to reason
  about. Test with concurrent `Next()` calls under `-race`.
- **Op union encoded by an `op` discriminator field.** `emit`'s
  streaming response delivers a sequence of ops; each op is a separate
  struct, and the wire form is `{"op": "write_file", ...rest}`. The
  contract package exposes:
  - A typed `Op` interface,
  - Per-op concrete structs (`OpWriteFile`, `OpWriteToolOwned`,
    `OpMkdir`, `OpDelete`, `OpWarning`),
  - A `DecodeOp(json.RawMessage) (Op, error)` dispatcher keyed on
    `op`,
  - A `MarshalOp(Op) ([]byte, error)` symmetric encoder.
- **`OpWriteFile.Content` is always `[]byte`; encoding is the wire
  concern.** The Go API exposes raw bytes. Encoding (`utf8` vs
  `base64`) is set on the wire via an `encoding` field. A constructor
  helper `NewOpWriteFile(path, mode, content)` chooses the wire
  encoding automatically based on `utf8.Valid(content)`. Decoder
  reverses: base64-decodes when `encoding == "base64"`, otherwise
  passes bytes through. Hash is over decoded bytes (matches the
  ledger-side decision in the parent plan).
- **8 MiB cap is exposed as `MaxOpPayloadBytes` and validated at
  encode time.** `NewOpWriteFile` returns
  `ErrPayloadTooLarge` for oversize content. Decoder enforces the
  same cap on the decoded length (post-base64) so a hostile peer
  can't slip a huge frame past via base64 expansion. Cap is the
  decoded byte count, not the wire byte count.
- **Reserved `_meta` is `json.RawMessage`, not a typed struct.** Any
  shape lives in `_meta` per the additivity rule. Typed parsing of
  meta fields is the consumer's job. PR 1 ships only the field and an
  `omitempty` tag.
- **Schema-vs-types parity is hand-rolled, no new deps.** A test
  loads `internal/adapter/contract/schema/*.json` into a generic
  `map[string]any`, walks `properties` for each method, and asserts
  the set of `properties` keys equals the set of JSON tags on the
  matching Go struct (both directions). No JSON Schema library,
  no codegen — drift becomes a one-line test diff.
- **No `init()` and no global state.** Per repo conventions in
  CLAUDE.md, the contract package exposes constructors
  (`NewIDCorrelator`, `NewFrameReader`) and pure functions only.
- **Plain `testing` only.** No `testify` import — repo `go.mod` does
  not pull it; `internal/ir` and other packages don't use it. Match
  house style.

## Open Questions

### Resolved During Planning

- **Should `ReadFrame` survive PR 1?** Yes, as a one-shot convenience;
  long-lived loops use `FrameReader`.
- **How is the op union dispatched?** Discriminator field `op` on each
  struct; `DecodeOp` does the type switch.
- **Should the schema test use a JSON Schema library?** No — hand-rolled
  parity test on `properties` keys vs JSON tags. Cheaper, no deps,
  fails on drift.
- **Where does the 8 MiB cap live?** `MaxOpPayloadBytes` constant in
  `internal/adapter/contract`, enforced at encode time and at decode
  time (post-base64). Configurable per-adapter via `adapter.yaml` is a
  PR 2 concern.
- **Does PR 1 enforce magic cookie?** No — it lives in the
  `initialize` params type only. Validation is in PR 2's subprocess
  runner.
- **Does PR 1 enforce lifecycle ordering?** No — message types only.
  Ordering is PR 2's runtime job.

### Deferred to Implementation

- The exact field names in the per-method schemas — settle when
  authoring each schema file. Names match the Go struct JSON tags.
- Whether `OpWriteToolOwned`'s `kind` enum holds string constants in
  Go (`KindJSONPointer`, `KindTOMLPath`, `KindMarkdownSection`) or a
  named type. Probably a named string type with constants for type
  safety; finalize when writing the file.

## Output Structure

```
internal/adapter/
└── contract/
    ├── framing.go              # already in tree (uncommitted)
    ├── framing_test.go         # already in tree (uncommitted)
    ├── jsonrpc.go              # PR 1 unit 2
    ├── jsonrpc_test.go         # PR 1 unit 2
    ├── protocol.go             # PR 1 unit 3
    ├── protocol_test.go        # PR 1 unit 3
    ├── schema_parity_test.go   # PR 1 unit 4
    └── schema/
        ├── initialize.json     # PR 1 unit 4
        ├── initialized.json    # PR 1 unit 4
        ├── emit.json           # PR 1 unit 4
        ├── shutdown.json       # PR 1 unit 4
        ├── op_write_file.json
        ├── op_write_tool_owned.json
        ├── op_mkdir.json
        ├── op_delete.json
        └── op_warning.json
```

## Implementation Units

- [x] **Unit 1: Framing layer (LSP `Content-Length`/`Content-Type`)**

**Goal:** Read and write LSP-style framed messages over `io.Reader`/`io.Writer`. Defense-in-depth max frame size; multi-frame stream support.

**Requirements:** R1, R6.

**Dependencies:** None.

**Status:** Already implemented in tree (uncommitted). `framing.go` and
`framing_test.go` exist on branch `feat/unit-8-pr1-wire-protocol`. Will
be the first commit of PR 1.

**Files:**
- Create: `internal/adapter/contract/framing.go`
- Test: `internal/adapter/contract/framing_test.go`

**Approach:**
- `WriteFrame(w io.Writer, payload []byte) error` emits header block + body.
- `FrameReader` (constructed via `NewFrameReader`) holds a `*bufio.Reader` across calls so multi-frame streams work.
- `ReadFrame(r io.Reader, maxBytes int64) ([]byte, error)` is a one-shot helper that builds a single-use `FrameReader`.
- Sentinel errors: `ErrMissingContentLength`, `ErrMalformedHeader`, `ErrUnsupportedMediaType`, `ErrUnsupportedCharset`, `ErrFrameTooLarge`.
- Constants: `MediaType = "application/aienvs-v1+json"`, `DefaultMaxFrameBytes = 16 MiB`.

**Execution note:** Tests written first; implementation followed. Already RED → GREEN.

**Patterns to follow:**
- `internal/ir/types.go` package doc + sentinel error block.

**Test scenarios:** All currently passing.
- Happy path: WriteFrame round-trips through ReadFrame.
- Happy path: header shape exactly matches LSP form.
- Happy path: case-insensitive header names; whitespace tolerance.
- Happy path: missing Content-Type accepted (LSP backward-compat).
- Happy path: multi-frame stream via `FrameReader` returns ordered payloads then clean EOF.
- Error path: missing Content-Length → `ErrMissingContentLength`.
- Error path: non-numeric / negative Content-Length → `ErrMalformedHeader`.
- Error path: unsupported media type → `ErrUnsupportedMediaType`.
- Error path: charset != utf-8 → `ErrUnsupportedCharset`.
- Error path: declared length > maxBytes → `ErrFrameTooLarge`.
- Error path: header line without colon → `ErrMalformedHeader`.
- Error path: truncated body → `io.ErrUnexpectedEOF`.
- Edge: clean EOF before any header → `io.EOF` unwrapped.
- Edge: WriteFrame surfaces underlying writer errors.

**Verification:**
- `go test -race ./internal/adapter/contract/...` passes for framing.
- `golangci-lint run ./internal/adapter/contract/...` clean.

---

- [ ] **Unit 2: JSON-RPC 2.0 envelope, ID correlator, error codes**

**Goal:** Land the JSON-RPC 2.0 message types (`Request`, `Response`, `Notification`), an `ID` union (int|string|null), the standard error-code constants, the `Error` struct with `data.error_class`, and a thread-safe ID correlator for tracking pending requests.

**Requirements:** R2, R6.

**Dependencies:** Unit 1.

**Files:**
- Create: `internal/adapter/contract/jsonrpc.go`
- Test: `internal/adapter/contract/jsonrpc_test.go`

**Approach:**
- `Request{ID, Method, Params, Meta}`, `Response{ID, Result, Error, Meta}`, `Notification{Method, Params, Meta}` — all carry a JSON-RPC `"jsonrpc": "2.0"` literal on encode and verify it on decode.
- `ID` type with custom MarshalJSON/UnmarshalJSON; constructors `NewIntID`, `NewStringID`; predicates `IsInt`, `IsString`, `IsNull`; accessors `AsInt`, `AsString`.
- `Response.MarshalJSON` returns `ErrResponseHasResultAndError` if both are set, `ErrResponseEmpty` if neither is set. Defends against caller bugs at encode time so the wire never carries a malformed response.
- `IDCorrelator{mu sync.Mutex; next int64; pending map[int64]string}` with `Next() ID`, `MarkPending(ID, method string)`, `Resolve(ID) (method string, ok bool)` (one-shot resolution; second resolve returns false).
- `Error{Code int; Message string; Data ErrorData}` where `ErrorData{ErrorClass ErrorClass; Detail json.RawMessage}` — `error_class` is omitempty so plain JSON-RPC errors don't carry the field.
- `ErrorClass` is a named string type; constants: `ErrorClassAdapterPanic` (`"adapter-panic"`), `…Timeout`, `…ProtocolMismatch`, `…UndeclaredOutput`, `…ExecDenied`, `…CapabilityLied`.
- Standard error codes: `CodeParseError = -32700`, `CodeInvalidRequest = -32600`, `CodeMethodNotFound = -32601`, `CodeInvalidParams = -32602`, `CodeInternalError = -32603`, `CodeServerErrorMin = -32099`, `CodeServerErrorMax = -32000`. (No LSP extensions.)
- `ParseMessage(raw []byte) (Message, error)` classifies an inbound frame as `MessageKindRequest`, `MessageKindNotification`, or `MessageKindResponse` based on field presence; returns `ErrInvalidEnvelope` for missing/wrong `jsonrpc`, both result+error, or no method/result/error.

**Execution note:** Test-first. Tests already written on branch and currently RED. Implementation lands next.

**Patterns to follow:**
- `internal/ir/types.go` for sentinel error block + IsValidID-style predicate helpers.
- `encoding/json` `MarshalJSON` / `UnmarshalJSON` on the `ID` type (no codegen).

**Test scenarios:**
- Happy path: `Request` marshals with canonical field order (`jsonrpc`, `id`, `method`, `params`).
- Happy path: `Request` omits `params` when nil (`omitempty`).
- Happy path: `Notification` marshals without `id`.
- Happy path: `Response` marshals `result` when set; `error` when set.
- Happy path: `Error` marshals with `data.error_class` populated.
- Happy path: `ParseMessage` classifies each of request, notification, response-result, response-error.
- Happy path: `ID` round-trips for both int and string forms.
- Happy path: `ID.UnmarshalJSON("null")` produces `IsNull() == true`.
- Edge: `ID` with int payload marshals as a JSON number, not a quoted string.
- Error path: `Response` with both `result` and `error` set → `ErrResponseHasResultAndError`.
- Error path: `Response` with neither → `ErrResponseEmpty`.
- Error path: `ParseMessage` on missing `jsonrpc` field → `ErrInvalidEnvelope`.
- Error path: `ParseMessage` on `"jsonrpc": "1.0"` → `ErrInvalidEnvelope`.
- Error path: `ParseMessage` on response with both result+error → `ErrInvalidEnvelope`.
- Concurrency: 1000 concurrent `IDCorrelator.Next()` calls produce 1000 unique IDs (no duplicates) under `-race`.
- Lookup: `MarkPending(id, "x")` then `Resolve(id)` returns `("x", true)`; second `Resolve(id)` returns `("", false)` (one-shot).
- Stability: error code constants and `ErrorClass` strings have hard-coded literal expectations to lock the wire contract.

**Verification:**
- `go test -race ./internal/adapter/contract/...` passes for jsonrpc.
- All envelope marshaling produces wire-stable byte strings (verified via literal-string comparison in tests, not just structural equality).

---

- [ ] **Unit 3: Method/result types, capabilities, declared outputs, op union**

**Goal:** Land the typed surface for `initialize`, `initialized`, `emit`, `shutdown`, plus `Capabilities`, `DeclaredOutput`, and the op union exchanged inside `emit`. Reserved `_meta` field on every envelope and every op.

**Requirements:** R3, R4, R6.

**Dependencies:** Unit 2.

**Files:**
- Create: `internal/adapter/contract/protocol.go`
- Test: `internal/adapter/contract/protocol_test.go`

**Approach:**
- Method-name constants: `MethodInitialize`, `MethodInitialized`, `MethodEmit`, `MethodShutdown`.
- `InitializeParams{Client, ProtocolVersions, Cookie, WorkspaceRoot, ReservedPrefix, IRVersion, Meta}` — strings are required-on-encode; `ProtocolVersions []string`.
- `InitializeResult{Server, ProtocolVersion, Capabilities, DeclaredOutputs, Meta}`.
- `Capabilities{ConceptKinds map[string]CapabilityLevel, WriteToolOwned bool, Progress bool, Meta}` where `CapabilityLevel` is a named string type with constants `CapabilitySupported = "supported"`, `…Partial`, `…Unsupported`. Future fields land additively per the parent plan's "capabilities grow without bumping aienvs/v1" rule.
- `DeclaredOutput{Path, Mode, JSONPath, SectionID, Meta}` where `Mode` is a named string type: `OutputModeOwnedSubdir = "owned-subdir"`, `OutputModeToolOwnedEntry = "tool-owned-entry"`. `JSONPath` and `SectionID` are pointer-strings (omitempty when not applicable).
- `EmitParams{Target, IR, Meta}` — `IR` is `json.RawMessage` since the IR types live in `internal/ir` and pulling them here would couple the contract package upward. PR 2 wires the IR through.
- `EmitResult{OpsPerformed []OpRecord, Meta}` — `OpRecord` is a minimal `{Op, Path}` struct so the CLI knows what was applied without re-decoding the streamed ops.
- Op union via interface + discriminator:
  - `Op` interface: `OpKind() OpKind`, `OpPath() string` (every op targets a path).
  - `OpKind` named string type: `OpKindWriteFile = "write_file"`, `…WriteToolOwned`, `…Mkdir`, `…Delete`, `…Warning`.
  - Concrete structs: `OpWriteFile{Path, Mode, Content []byte, Encoding (wire-only), Meta}`, `OpWriteToolOwned{Path, Kind ToolOwnedKind, Locator, Content, Meta}`, `OpMkdir{Path, Mode, Meta}`, `OpDelete{Path, Meta}`, `OpWarning{ConceptID, Status WarningStatus, Note, Meta}`.
  - `ToolOwnedKind` constants: `ToolOwnedKindJSONPointer = "json-pointer"`, `…TOMLPath`, `…MarkdownSection`.
  - `WarningStatus` constants: `WarningStatusDegraded`, `WarningStatusPartial`.
  - `MarshalOp(Op) ([]byte, error)` — encodes with `"op"` discriminator field.
  - `DecodeOp(json.RawMessage) (Op, error)` — type-switches on the `op` field; returns `ErrUnknownOp` for unrecognized kinds.
- `MaxOpPayloadBytes = 8 * 1024 * 1024` constant. `NewOpWriteFile(path, mode, content)` returns `ErrPayloadTooLarge` if `len(content) > MaxOpPayloadBytes`; chooses `EncodingUTF8` if `utf8.Valid(content)` else `EncodingBase64`.
- `Encoding` type — internal-only; `EncodingUTF8`, `EncodingBase64`. The Go API hides this from the caller; only `Marshal/DecodeOp` care.
- Ops not in the v1 set: `symlink` is *blocked by default* per parent plan and is **not** part of the union in PR 1 — gating it lives in PR 2 alongside the manifest. PR 1 simply does not define `OpSymlink`.
- `ShutdownParams{Meta}`, `ShutdownResult{Meta}` — ceremony types so the lifecycle has a typed home.

**Execution note:** Test-first. RED → implementation → GREEN, one struct group at a time so each failing test produces a focused diff.

**Patterns to follow:**
- `internal/ir/kinds.go` for closed-set enum + `All*()` accessor pattern (mirror for `OpKind`, `CapabilityLevel`, `OutputMode`).
- `encoding/json` MarshalJSON/UnmarshalJSON on `OpWriteFile` for the encoding-aware byte handling.

**Test scenarios:**
- Happy path: `InitializeParams` round-trips through Marshal/Unmarshal preserving every field including `_meta`.
- Happy path: `Capabilities.ConceptKinds` round-trips with each `CapabilityLevel`.
- Happy path: `DeclaredOutput` with `OutputModeToolOwnedEntry` round-trips with `JSONPath` populated.
- Happy path: `NewOpWriteFile` with valid UTF-8 content emits `encoding == "utf8"` on the wire.
- Happy path: `NewOpWriteFile` with non-UTF-8 bytes (e.g., a `\x80` byte) emits `encoding == "base64"` and the wire string is base64 of the raw bytes.
- Happy path: `MarshalOp` then `DecodeOp` round-trips for every op kind, recovering byte-equal `Content` for `OpWriteFile`.
- Happy path: `OpWriteToolOwned` with `ToolOwnedKindJSONPointer` carries a JSON-pointer locator string.
- Edge: `_meta` is `omitempty` — a zero-value envelope marshals without the `_meta` key.
- Edge: `OpWriteFile` with exactly `MaxOpPayloadBytes` content succeeds.
- Edge: `OpWriteFile` decode at exactly `MaxOpPayloadBytes` (post-base64) succeeds.
- Error path: `NewOpWriteFile` with `MaxOpPayloadBytes + 1` content → `ErrPayloadTooLarge`.
- Error path: `DecodeOp` on JSON whose decoded base64 exceeds `MaxOpPayloadBytes` → `ErrPayloadTooLarge` (defends against base64-expansion attacks).
- Error path: `DecodeOp` on missing `op` field → `ErrUnknownOp` (or a dedicated `ErrMissingOpKind`).
- Error path: `DecodeOp` on `"op": "symlink"` → `ErrUnknownOp` (proves symlink isn't smuggled in via the wire).
- Error path: `DecodeOp` on malformed JSON → wraps `*json.SyntaxError`.
- Stability: literal expectations on every wire string constant (`OpKind`, `CapabilityLevel`, `OutputMode`, `ToolOwnedKind`, `WarningStatus`) to lock the contract.

**Verification:**
- `go test -race ./internal/adapter/contract/...` passes including the new protocol tests.
- All op kinds in `OpKind`'s `AllOpKinds()` return value match the closed set declared in the parent plan.

---

- [ ] **Unit 4: Per-method JSON Schemas + schema-vs-types parity test**

**Goal:** Author one JSON Schema per method and op, plus a Go test that fails CI when the schemas drift from the Go struct field names.

**Requirements:** R5, R6.

**Dependencies:** Unit 3.

**Files:**
- Create: `internal/adapter/contract/schema/initialize.json`
- Create: `internal/adapter/contract/schema/initialized.json`
- Create: `internal/adapter/contract/schema/emit.json`
- Create: `internal/adapter/contract/schema/shutdown.json`
- Create: `internal/adapter/contract/schema/op_write_file.json`
- Create: `internal/adapter/contract/schema/op_write_tool_owned.json`
- Create: `internal/adapter/contract/schema/op_mkdir.json`
- Create: `internal/adapter/contract/schema/op_delete.json`
- Create: `internal/adapter/contract/schema/op_warning.json`
- Test: `internal/adapter/contract/schema_parity_test.go`
- Modify: `internal/adapter/contract/protocol.go` — add `//go:embed schema/*.json` directive (declared package-private; consumers in PR 3 will expose it via `pkg/adapterkit/`).

**Approach:**
- Schemas are JSON Schema draft 2020-12 (lock with `$schema`).
- Each method file has `$id` set to a stable fragment URL like `https://aienvs.dev/schema/v1/initialize.json` so external consumers (PR 3 conformance harness) can resolve them by id.
- Schemas describe the *params* and *result* shapes (where applicable) and reference shared op schemas via `$ref`.
- Embed via `embed.FS`: `var SchemaFS embed.FS` (package-exported for PR 3's conformance harness; unused outside the parity test in PR 1).
- Parity test lives in package `contract` (white-box) and walks each known method:
  1. Load `<method>.json` from `SchemaFS`.
  2. Walk its `properties` (and `properties` of nested objects pointed to by `$ref`) collecting property names.
  3. Reflect on the corresponding Go struct's fields, collecting JSON tag names.
  4. Assert set equality both directions.
- Op-side parity walks each `OpKind` and checks `op_<kind>.json` against the matching concrete op struct.
- Schemas don't enforce *constraints* (lengths, regexes, value ranges) yet — their job in PR 1 is field-name parity. Tightening to validation-grade schemas is a PR 3 concern when they become consumer-facing.

**Execution note:** Author one schema, run the parity test, watch it pass for that schema and skip-or-fail the others, then move to the next. Faster feedback than authoring all nine and debugging in bulk.

**Patterns to follow:**
- `embed` directive for static assets — same pattern as `internal/manifest/schema.go` if it embeds anything; otherwise the standard `//go:embed schema/*.json` form documented in `embed`.
- `internal/ir`'s closed-set list pattern for "every method has a schema" iteration.

**Test scenarios:**
- Schema-vs-types parity, happy path: every method/op schema's `properties` set equals its Go struct's JSON-tagged field set (both directions).
- Edge: nested object schemas referenced by `$ref` are walked transitively (e.g., `Capabilities`, `DeclaredOutput` inside `InitializeResult`).
- Stability: every method in `[]string{MethodInitialize, MethodInitialized, MethodEmit, MethodShutdown}` has a schema file embedded.
- Stability: every op in `AllOpKinds()` has a corresponding `op_<kind>.json` schema file.
- Error: a deliberately-broken test fixture (e.g., a schema with a renamed property) fails the parity assertion with a clear diff message.
- JSON validity: every schema parses as valid JSON.
- Schema metadata: every schema has `$schema`, `$id`, and `type: object` set.

**Verification:**
- `go test -race ./internal/adapter/contract/...` passes including the parity test.
- Renaming any field in a Go struct without updating its schema fails the parity test (manually confirmed once during implementation).

## System-Wide Impact

- **Interaction graph:** Nothing imports `internal/adapter/contract` yet. PR 2 will be the first consumer (subprocess + inproc + lifecycle). PR 3 exposes `SchemaFS` via `pkg/adapterkit/` for third-party authors.
- **Error propagation:** Sentinel errors flow up to PR 2's runtime; PR 2 maps them to JSON-RPC error codes / `ErrorClass` values for the wire response.
- **State lifecycle risks:** None at this layer — pure types and parsers, no I/O, no goroutines except the IDCorrelator's mutex. Race detector covers correlator concurrency.
- **API surface parity:** None — `internal/` packages have no external consumers. PR 3 carves out the public surface in `pkg/adapterkit/`.
- **Integration coverage:** Mocks alone validate this PR; no integration tests needed at this layer. PR 2 owns the subprocess integration tests; PR 3 owns the conformance corpus.
- **Unchanged invariants:** Existing Units 1–7 are unaffected. The `internal/git`, `internal/cache`, `internal/trust`, `internal/ir`, `internal/capmatrix`, `internal/manifest`, `internal/workspace`, `internal/fsroot`, and `internal/cli` packages are not touched. No changes to `cmd/server` (which doesn't exist for aienvs — CLI binary is the single command).

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Wire-format decisions in this PR get baked into the spec; later changes are breaking. | The parent plan already decided the wire shape (Unit 8 lines 599–672); PR 1 just ships it. Spec freeze itself is in PR 3 — last chance to revise before then. |
| Schema-vs-types drift goes undetected until it bites a third-party adapter. | The parity test is the primary defense. Run it on every commit via `go test ./...` (already enforced in CI). |
| `OpWriteFile.Content` payload sizes near 8 MiB stress JSON encoder/decoder performance. | Single boundary test at exactly 8 MiB documents the cap is exact. PR 2's runtime tests will exercise the same boundary under realistic load. |
| ID correlator races under concurrent goroutines from PR 2's runtime. | `-race` is mandatory in CI; PR 1 ships a 1000-goroutine concurrent-Next test that catches sequence reuse. |
| Hand-rolled parity walking misses transitive `$ref` resolution. | Test includes one schema with a nested `$ref` (e.g., `Capabilities` inside `InitializeResult`) and asserts the nested fields are checked too. |

## Documentation / Operational Notes

- No user-facing documentation changes in PR 1. The authoritative spec doc (`docs/spec/adapter-protocol-v1.md`) ships in PR 3.
- Update `AGENTS.md` only if a new invariant emerges from the work — the current plan reuses the existing patterns, so no AGENTS.md change is expected.
- No CI changes needed — existing `go test -race ./... && golangci-lint run` covers the new package.

## Sources & References

- **Parent plan:** [docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md](2026-04-21-001-feat-aienvs-workspace-cli-plan.md), Unit 8 starting at line 599.
- **Branch:** `feat/unit-8-pr1-wire-protocol` (already created off `main` at commit `5f9c964`).
- **Carving conversation:** PR 1/2/3 split agreed in chat preceding this plan; PR 1 = wire types, PR 2 = runtime, PR 3 = conformance + spec freeze.
- **Existing learnings:**
  - `docs/solutions/2026-04-22-go-cross-platform-ci.md` — Windows path discipline.
  - `docs/solutions/2026-04-23-spec-vs-impl-drift.md` — parity-test discipline at boundary docs.
- **External references:**
  - LSP 3.17 base protocol: <https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/>
  - JSON-RPC 2.0: <https://www.jsonrpc.org/specification>
  - JSON Schema draft 2020-12: <https://json-schema.org/draft/2020-12/json-schema-core>

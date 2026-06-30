---
title: agent-sync Adapter Protocol v1
status: frozen
version: agent-sync/v1
date: 2026-04-26
owner: internal/adapter
---

# agent-sync Adapter Protocol v1

This document is the authoritative wire-format specification for
`agent-sync/v1` adapters. It defines the bytes sent over stdio, the JSON-RPC
message shapes, the lifecycle ordering rules, the magic-cookie
handshake, the capability declaration rules, the declared-output safety
gate, and the closed v1 error taxonomy.

The Go implementation that speaks this spec lives in
`internal/adapter/contract/`, `internal/adapter/runtime.go`, and
`pkg/adapterkit/`, but the protocol is frozen independently of those
packages. Third-party adapter authors should be able to implement the
wire contract from this document alone.

## Contents

- [1. Overview](#1-overview)
- [2. Framing](#2-framing)
- [3. JSON-RPC Envelope](#3-json-rpc-envelope)
- [4. Lifecycle](#4-lifecycle)
- [5. Capabilities](#5-capabilities)
- [6. Declared Outputs](#6-declared-outputs)
- [7. Operation Set](#7-operation-set)
- [8. Errors](#8-errors)
- [9. Magic Cookie](#9-magic-cookie)
- [10. Reserved `_meta`](#10-reserved-_meta)
- [11. Timeouts](#11-timeouts)
- [12. Versioning Policy](#12-versioning-policy)
- [13. Reserved for Unit 8b](#13-reserved-for-unit-8b)
- [14. Canonical Examples](#14-canonical-examples)
- [15. Conformance](#15-conformance)
- [16. CLI Reference](#16-cli-reference)

## 1. Overview

An `agent-sync` adapter is a subprocess that translates `agent-sync` IR into
adapter operations. The runtime owns process management and safety
checks; adapters own only protocol responses and declarative output.

The lifecycle is fixed:

1. Runtime spawns the adapter and sets `AGENT_SYNC_ADAPTER_COOKIE`.
2. Runtime sends `initialize`.
3. Adapter replies with `initialize` result, echoing the cookie and
   declaring capabilities and outputs.
4. Runtime sends `initialized`.
5. Runtime sends one or more `emit` requests.
6. Runtime sends `shutdown`.

The runtime treats any lifecycle-order violation as
`adapter-protocol-order`.

## 2. Framing

The transport is LSP-style `Content-Length` framing over stdio. Each
message is one header block followed by a raw JSON payload.

Required header:

- `Content-Length: <decimal byte count>`

Optional recognized header:

- `Content-Type: application/agent-sync-v1+json; charset=utf-8`

Rules:

- The header block terminates with `\r\n\r\n`.
- `Content-Length` counts payload bytes, not Unicode code points.
- `Content-Type` is optional on read and always emitted on write by the
  reference implementation.
- If `Content-Type` is present, the media type MUST be
  `application/agent-sync-v1+json`.
- If `charset` is present, it MUST be `utf-8` case-insensitively.
- Unknown headers are ignored.
- Header lines longer than 8 KiB are invalid.
- More than 32 header lines are invalid.
- Frames larger than 16 MiB are invalid at the transport layer.

Canonical frame shape:

```text
Content-Length: 83\r
Content-Type: application/agent-sync-v1+json; charset=utf-8\r
\r
{"jsonrpc":"2.0","id":1,"method":"shutdown","params":{}}
```

Transport-level errors:

| Condition | Runtime classification |
|---|---|
| Missing `Content-Length` | malformed frame |
| Non-numeric or negative `Content-Length` | malformed frame |
| Wrong `Content-Type` | unsupported media type |
| Non-UTF-8 charset | unsupported charset |
| Declared frame > 16 MiB | `adapter-protocol-mismatch` |
| Truncated header or body | adapter fault |

## 3. JSON-RPC Envelope

The payload inside each frame is JSON-RPC 2.0 with the literal
`"jsonrpc":"2.0"`.

### Request

Requests carry `id`, `method`, and optional `params`.

```json
{"jsonrpc":"2.0","id":1,"method":"emit","params":{"target":"happy-rule","ir":{"nodes":[]}}}
```

### Notification

Notifications carry `method` and optional `params`, but no `id`.

```json
{"jsonrpc":"2.0","method":"initialized","params":{}}
```

### Response

Responses carry `id` and exactly one of `result` or `error`.

```json
{"jsonrpc":"2.0","id":1,"result":{"ops_performed":[]}}
```

### Error object

Errors use the JSON-RPC 2.0 object plus an `agent-sync` extension in
`error.data`.

| Field | Type | Required | Notes |
|---|---|---|---|
| `code` | int | yes | JSON-RPC error code |
| `message` | string | yes | human-readable summary |
| `data.error_class` | string | no | `agent-sync` runtime classifier |
| `data.detail` | any JSON | no | opaque structured detail |

Shape:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "error": {
    "code": -32602,
    "message": "adapter does not support any offered protocol version",
    "data": {
      "error_class": "adapter-protocol-mismatch"
    }
  }
}
```

Envelope invariants:

- Requests and notifications MUST NOT include `result` or `error`.
- Responses MUST include `id`.
- Responses MUST NOT include `method`.
- A response with both `result` and `error` is invalid.
- A response with neither `result` nor `error` is invalid.
- `id` may be integer, string, or `null`; `agent-sync` emits integers.

## 4. Lifecycle

The v1 lifecycle is fixed and ordered:

1. `initialize` request
2. `initialized` notification
3. zero or more `emit` requests
4. `shutdown` request

Adapters MUST NOT:

- send unsolicited requests to the runtime
- send notifications before `initialized`
- answer `emit` before receiving `initialized`
- omit a response to `shutdown`

Method summary:

| Method | Direction | Kind | Purpose |
|---|---|---|---|
| `initialize` | runtime -> adapter | request | negotiate protocol, capabilities, outputs, cookie echo |
| `initialized` | runtime -> adapter | notification | marks lifecycle transition into ready state |
| `emit` | runtime -> adapter | request | hand one IR payload to the adapter |
| `shutdown` | runtime -> adapter | request | end the session cleanly |

### `initialize` params

| Field | Type | Required | Meaning |
|---|---|---|---|
| `client` | string | yes | runtime identity, currently `agent-sync` |
| `protocol_versions` | `[]string` | yes | ordered offered versions, v1 currently offers `["agent-sync/v1"]` |
| `cookie` | string | yes | magic cookie the adapter MUST echo back |
| `workspace_root` | string | yes | absolute workspace path |
| `reserved_prefix` | string | yes | workspace-relative prefix the adapter owns |
| `ir_version` | string | yes | IR schema version, currently `v1` |
| `scope` | string | no | hierarchy level being emitted: `user`, `project`, or `directory`. Additive (omitempty); absent or unrecognized ⇒ `project`. Lets an adapter choose scope-appropriate output paths. |
| `_meta` | any JSON | no | reserved |

### `initialize` result

| Field | Type | Required | Meaning |
|---|---|---|---|
| `server` | string | yes | adapter identity, e.g. `echo/0.1` |
| `protocol_version` | string | yes | selected protocol version |
| `capabilities` | object | yes | capability declaration |
| `declared_outputs` | array | yes | declared output ownership |
| `cookie` | string | yes for subprocess adapters | MUST equal the incoming cookie exactly |
| `_meta` | any JSON | no | reserved |

Lifecycle requirements:

- The adapter MUST select one of the offered protocol versions.
- The adapter MUST echo the cookie exactly.
- The adapter MUST wait for `initialized` before processing `emit`.
- The adapter SHOULD return empty arrays rather than `null` for
  `declared_outputs` and `ops_performed`.

## 5. Capabilities

Capabilities are the additive extension surface for v1. Envelope and
framing changes require `agent-sync/v2`; capability fields can grow
additively inside the existing result object.

Shape:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `concept_kinds` | map `kind -> status` | yes | per-IR-kind support declaration |
| `write_tool_owned` | bool | no | whether `write_tool_owned` ops are supported |
| `progress` | bool | no | reserved for future progress reporting |
| `_meta` | any JSON | no | reserved |

### `concept_kinds`

Closed v1 status set:

| Value | Meaning |
|---|---|
| `supported` | adapter fully implements the kind |
| `partial` | adapter implements the kind with degradation |
| `unsupported` | adapter does not implement the kind |

Known v1 IR kinds:

| Kind |
|---|
| `agents-md` |
| `rule` |
| `skill` |
| `command` |
| `plugin-reference` |
| `mcp-server-entry` |

Runtime behavior:

- If an adapter declares a kind as `supported` and then emits zero
  non-warning ops for IR containing that kind, the runtime reports
  `adapter-capability-lied`.
- Missing entries are treated as not-declared for conformance purposes.

## 6. Declared Outputs

Adapters MUST declare every workspace path they intend to mutate. The
runtime rejects any emitted op outside `declared_outputs`.

Declared output shape:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `path` | string | yes | workspace-relative path |
| `mode` | string | yes | ownership mode |
| `json_pointer` | string | no | RFC 6901 JSON Pointer for tool-owned JSON entries |
| `section_id` | string | no | section identifier for tool-owned markdown entries |
| `_meta` | any JSON | no | reserved |

Ownership modes:

| Value | Meaning | v1 status |
|---|---|---|
| `owned-subdir` | adapter owns the whole subtree rooted at `path` | active |
| `tool-owned-entry` | adapter owns a structured entry inside a tool-owned file | reserved |

Safety rules:

- `path` is workspace-relative, never absolute.
- Backslashes and Windows drive prefixes are invalid on the wire.
- Any op path outside declared outputs is `adapter-undeclared-output`.
- The runtime is the final authority on containment; adapter self-checks
  are optional.

## 7. Operation Set

The v1 operation vocabulary is closed:

| `op` value | Purpose |
|---|---|
| `write_file` | write a fully-owned file |
| `write_tool_owned` | write a structured entry inside a tool-owned file |
| `mkdir` | create a directory |
| `delete` | delete a file or directory |
| `warning` | surface degraded or partial handling |

In the `emit` result, adapters report only `ops_performed`, a list of
minimal `{op, path}` records. The full op JSON schemas below are the
frozen v1 shapes used by the SDK and schema tests.

### `emit` params

| Field | Type | Required | Meaning |
|---|---|---|---|
| `target` | string | yes | case or adapter target identifier |
| `ir` | object | yes | IR payload |
| `_meta` | any JSON | no | reserved |

### `emit` result

| Field | Type | Required | Meaning |
|---|---|---|---|
| `ops_performed` | array of `{op,path}` | yes | ordered summary of operations performed |
| `_meta` | any JSON | no | reserved |

### `write_file`

| Field | Type | Required | Notes |
|---|---|---|---|
| `op` | `"write_file"` | yes | discriminator |
| `path` | string | yes | workspace-relative path |
| `mode` | int | yes | Unix file mode as integer |
| `encoding` | `utf8` \| `base64` | yes | content encoding |
| `content` | string | yes | UTF-8 text or base64 payload |
| `_meta` | any JSON | no | reserved |

Encoding rules:

- Use `utf8` when bytes are valid UTF-8.
- Use `base64` when bytes are not valid UTF-8.
- Decoded payloads larger than 8 MiB are invalid.

### `write_tool_owned`

| Field | Type | Required | Notes |
|---|---|---|---|
| `op` | `"write_tool_owned"` | yes | discriminator |
| `path` | string | yes | workspace-relative tool-owned file |
| `kind` | `json-pointer` \| `toml-path` \| `markdown-section` | yes | locator scheme |
| `locator` | string | yes | locator inside the tool-owned file |
| `encoding` | `utf8` \| `base64` | yes | content encoding |
| `content` | string | yes | encoded content |
| `_meta` | any JSON | no | reserved |

### `mkdir`

| Field | Type | Required |
|---|---|---|
| `op` | `"mkdir"` | yes |
| `path` | string | yes |
| `mode` | int | yes |
| `_meta` | any JSON | no |

### `delete`

| Field | Type | Required |
|---|---|---|
| `op` | `"delete"` | yes |
| `path` | string | yes |
| `_meta` | any JSON | no |

### `warning`

| Field | Type | Required | Notes |
|---|---|---|---|
| `op` | `"warning"` | yes | discriminator |
| `concept_id` | string | yes | IR concept identifier |
| `status` | `degraded` \| `partial` | yes | warning class |
| `note` | string | yes | human-readable warning |
| `_meta` | any JSON | no | reserved |

## 8. Errors

### JSON-RPC standard codes

| Code | Meaning |
|---|---|
| `-32700` | parse error |
| `-32600` | invalid request |
| `-32601` | method not found |
| `-32602` | invalid params |
| `-32603` | internal error |
| `-32099` .. `-32000` | implementation-defined server errors |

### `data.error_class`

The runtime understands the following frozen classifier strings:

| Value | Meaning |
|---|---|
| `adapter-protocol-order` | lifecycle/order violation such as emit-before-initialized or double-initialize |
| `adapter-panic` | adapter bug, malformed behavior, or unexpected subprocess failure |
| `adapter-timeout` | handshake, emit, or shutdown exceeded its timeout |
| `adapter-protocol-mismatch` | version or framing mismatch |
| `adapter-undeclared-output` | op path escaped declared outputs |
| `adapter-exec-denied` | cookie missing or mismatched |
| `adapter-capability-lied` | adapter declared support but produced no non-warning ops |

### Exit diagnostics

When the adapter exits abnormally, the runtime records:

- `exit_code`: operating-system exit code when available
- `stderr_tail`: bounded stderr tail (up to 64 KiB)

These fields are runtime diagnostics, not additional wire fields.

## 9. Magic Cookie

The runtime sets `AGENT_SYNC_ADAPTER_COOKIE` in the adapter process
environment. The adapter MUST read it and MUST echo the exact value in
its `initialize` result.

Rules:

- The adapter MUST treat a missing cookie as fatal and exit non-zero.
- The cookie value is opaque to the adapter.
- The runtime always delivers exactly 32 lowercase hexadecimal
  characters with no surrounding whitespace.
- Comparison is byte-exact; any trailing newline, space, encoding
  difference, or case change results in `adapter-exec-denied`.
- The runtime never reveals the expected cookie value in mismatch error
  messages.
- A missing or mismatched cookie is classified as
  `adapter-exec-denied`.
- Adapters MUST NOT log, print, or otherwise expose the cookie value to
  logs, telemetry, or stderr; exposure weakens the auth boundary the
  cookie exists to enforce.

## 10. Reserved `_meta`

Every request, notification, response result, error payload, capability
object, declared output, and op schema reserves `_meta` for additive
future data.

v1 rules:

- `_meta` is optional everywhere.
- Unknown `_meta` contents are ignored.
- `_meta: null` is treated the same as absent.

## 11. Timeouts

Default subprocess timeouts:

| Phase | Default |
|---|---|
| handshake (`initialize`) | 5s |
| `emit` | 30s |
| `shutdown` | 5s |

These are runtime defaults, not negotiated wire fields. Per-method
overrides are reserved for future work.

## 12. Versioning Policy

Versioning rules:

- `agent-sync/v1` framing and envelope rules are frozen.
- The runtime performs an exact-string match on `protocol_version`
  against `agent-sync/v1`. Any other value, including `agent-sync/v1.1` or
  `agent-sync/v1.0.0`, is rejected with `adapter-protocol-mismatch`.
- There is no minor-version negotiation in v1; capabilities grow
  additively under `Capabilities{}` instead. Counter-propose
  negotiation is reserved for Unit 8b.
- Additive growth happens under `capabilities`.
- Adding or renaming an envelope field is a breaking change.
- `agent-sync/v2` is reserved for envelope-level or framing-level breaks.

## 13. Reserved for Unit 8b

Not part of v1:

- `$/cancelRequest`
- `$/progress`
- per-method timeouts in adapter manifests
- LSP extension error codes `-32800`, `-32801`, `-32803`
- version counter-proposal negotiation

## 14. Canonical Examples

The following examples are spec-locked. Each directive fence names the
matching corpus fixture under `internal/adapter/conformance/corpus/`.
Canonical examples in this spec are tagged with an
`agent-sync:fixture-name` directive immediately preceding the JSON code
fence. The directive links a spec example to a corresponding corpus
fixture in `internal/adapter/conformance/corpus/`. The spec-locked test
in `internal/adapter/conformance/spec_locked_test.go` parses these
directives and asserts that the example payload exactly matches the
fixture's payload; drift in either direction fails CI.

```agent-sync:fixture-name
spec-example-handshake
```
```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"client":"agent-sync","protocol_versions":["agent-sync/v1"],"cookie":"0123456789abcdef0123456789abcdef","workspace_root":"/tmp/agent-sync-conformance","reserved_prefix":".echo","ir_version":"v1"}}
```

Canonical initialize result:

```json
{"jsonrpc":"2.0","id":1,"result":{"server":"echo/0.1","protocol_version":"agent-sync/v1","capabilities":{"concept_kinds":{"agents-md":"supported","rule":"supported","skill":"supported","command":"supported","plugin-reference":"supported","mcp-server-entry":"supported"},"write_tool_owned":true},"declared_outputs":[{"path":".echo","mode":"owned-subdir"}],"cookie":"0123456789abcdef0123456789abcdef"}}
```

```agent-sync:fixture-name
spec-example-emit
```
```json
{"jsonrpc":"2.0","id":2,"method":"emit","params":{"target":"spec-example-emit","ir":{"nodes":[{"id":"spec-emit","kind":"rule","body":"Adapters emit declarative ops."}]}}}
```

Canonical emit result:

```json
{"jsonrpc":"2.0","id":2,"result":{"ops_performed":[{"op":"mkdir","path":".echo"},{"op":"write_file","path":".echo/spec-emit.md"}]}}
```

```agent-sync:fixture-name
spec-example-error-response
```
```json
{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"adapter does not support any offered protocol version","data":{"error_class":"adapter-protocol-mismatch"}}}
```

## 15. Conformance

The frozen corpus lives under
`internal/adapter/conformance/corpus/`. The public CLI entry point is:

```bash
agent-sync adapter conformance-test ./my-adapter --format=json
```

Default CLI behavior runs the positive corpus (`happy-*` plus
`spec-example-*`). Use `--include-adversarial` to also run `error-*`
fixtures; those require a hostile binary to pass and will fail against
an otherwise-correct adapter.

The JSON output schema is:

```json
{
  "version": "agent-sync/v1",
  "cases": [
    {
      "name": "string",
      "status": "pass|fail|skip",
      "reason": "string",
      "expected_ops": [],
      "actual_ops": [],
      "missing_ops": [],
      "extra_ops": []
    }
  ],
  "summary": {
    "total": 0,
    "passed": 0,
    "failed": 0,
    "skipped": 0
  }
}
```

## 16. CLI Reference

`agent-sync adapter conformance-test` exit codes:

- `0`: all cases passed (skips do not count as failures)
- `1`: one or more cases failed
- `2`: adapter binary could not be spawned (path not found, not
  executable, or is a directory)

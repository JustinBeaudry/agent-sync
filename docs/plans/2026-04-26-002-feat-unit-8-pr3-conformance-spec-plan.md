---
title: "feat: Unit 8 PR 3 — conformance harness, adapterkit, spec freeze"
type: feat
status: active
date: 2026-04-26
---

# Unit 8 PR 3 — Conformance Harness, Adapterkit, Reference Echo, Spec Freeze

## Overview

Final PR of Unit 8. After this lands, the `aienvs/v1` adapter protocol
is **frozen**: the wire format, capability matrix, op set, error codes,
magic-cookie handshake, declared-outputs gate, and timeout policy are
all stable. Future adapter capabilities grow additively under
`Capabilities{}` without bumping the protocol version; envelope-level
changes would mean `aienvs/v2`.

What ships:

- A **golden-file conformance harness** (`internal/adapter/conformance/`)
  that drives any adapter binary through the full four-phase lifecycle,
  captures emitted ops, and diffs against frozen-corpus expectations.
- An **embedded corpus** of IR fixtures + expected wire behavior
  covering happy paths per IR kind plus the adversarial cases the
  parent plan calls out (protocol-order violation, missing cookie,
  version mismatch, undeclared output, capability-lied, abnormal exit).
- A **public `pkg/adapterkit/`** Go package — the supported SDK for
  out-of-tree adapter authors. Server + dispatcher, capability
  builder, testing helpers. Mirrors the wire types (does not import
  internal). First inhabitant of `pkg/`; sets the precedent.
- A **reference echo adapter** at `conformance/echo/` that uses
  `pkg/adapterkit/` and handles every IR kind in the v1 capability
  matrix — proves the SDK is usable end-to-end.
- A **`aienvs adapter conformance-test <binary>`** CLI subcommand
  that runs the corpus against a target binary and reports pass/fail
  per fixture.
- A **frozen spec document** at `docs/spec/adapter-protocol-v1.md`
  detailed enough that a third-party can implement an adapter from
  the spec alone, without reading our Go code.

## Problem Frame

Unit 8 PR 1 (#5) shipped wire-protocol types. PR 2 (#6) shipped the
runtime layer (manifest, discovery, subprocess + inproc transports,
lifecycle orchestrator, errors). What's missing for the
"third-parties can write adapters" promise:

1. **No conformance test surface.** A third-party adapter author has
   no automated way to know whether their binary speaks the protocol
   correctly. Without a harness, conformance is "we'll find out at
   integration time" — exactly the failure mode the spec freeze is
   meant to prevent.
2. **No public SDK.** Authors have to either re-implement framing,
   JSON-RPC envelope handling, and capability negotiation from scratch
   (high friction, easy to get subtly wrong) or read our internal
   package and copy patterns (couples them to our refactoring cadence).
3. **No authoritative spec doc.** The Go types in `internal/adapter/contract/`
   are the de-facto spec, but they're not portable to non-Go authors
   and they encode implementation details (e.g., interface receivers)
   that aren't part of the wire contract.
4. **No reference adapter.** The PR 2 testdata echo binary is
   intentionally minimal — it doesn't exercise the full IR kind matrix
   or the adapterkit API, and it ships as `package main` in `testdata/`
   (excluded from `go build ./...`). A separate canonical reference is
   needed.

## Requirements Trace

- **R3** (parent plan): Adapter framework freezes the wire protocol at
  `aienvs/v1`. PR 3 ships the spec doc that defines what's frozen.
- **R11** (parent plan): Third-party adapter authors can build, test,
  and ship adapters without reading our internal packages. Conformance
  harness + `pkg/adapterkit/` + reference echo + spec doc together
  satisfy this.
- **PR 2 residuals reflected**: subprocess `Spawn` ctx-detachment fix
  (P1 from codex review) is already landed and informs how adapterkit's
  testing helpers verify long-running emits. The protocol-shutdown vs
  signal-kill ack flow (`Subprocess.MarkProtocolShutdownAcked`) is
  already landed; adapterkit's testing helpers expose a way to verify
  it's being called correctly. The streaming `json.Decoder` pattern
  from `runtime.go::irContainsSupportedKind` is the reference for
  adapterkit's IR-iteration helper.

## Scope Boundaries

- **No protocol changes.** PR 3 only documents and tests what PR 1 + 2
  shipped. Any change to the wire format, op set, error codes, or
  capability matrix is out of scope and would block this PR.
- **No mirroring of `internal/adapter/contract/`** into a moved or
  copied public location. Adapterkit defines its own types for the
  public API; conformance and runtime continue to use internal types.
  See "Key Technical Decisions" for rationale.
- **No new IR kinds, no new op kinds.** PR 3 only validates the
  existing closed sets from `internal/ir` and `internal/adapter/contract`.

### Deferred to Unit 8b (separate, post-v1 work)

These are explicitly NOT this PR. The spec doc references them as
reserved capability-extension points:

- `$/cancelRequest` notification (LSP analog)
- `$/progress` tokens for long-running emits
- Per-method timeout overrides in `adapter.yaml`
- LSP-extension error codes (`-32800 RequestCancelled`, `-32801 ContentModified`,
  `-32803 RequestFailed`)
- Version counter-proposal negotiation (currently the runtime refuses
  on protocol-version mismatch; counter-proposal would let the adapter
  offer a fallback version)
- `conformance/echo-py/` Python reference adapter
- `pkg/adapterkit/` extension hooks for any of the above

Adapterkit's API surface should leave room for these without
committing to specific shapes.

## Context & Research

### Relevant Code and Patterns

- **CLI factory pattern**: `internal/cli/cmd_trust.go` — `NewTrustCommand(deps TrustDeps) *cobra.Command` with typed `Deps` struct + lazy `resolveX` helpers. New `cmd_adapter.go` follows the same shape.
- **Embed.FS pattern**: `internal/adapter/contract/schema.go` — `//go:embed schema/*.json` + `var SchemaFS embed.FS`. Conformance corpus uses the same pattern at `internal/adapter/conformance/corpus.go`.
- **Subprocess test fixture pattern**: `internal/adapter/subprocess_test.go::buildTestdataBinary` — content-hashed cache in randomized `os.MkdirTemp`, `TestMain` cleanup. Conformance harness tests reuse this.
- **Scripted-adapter pattern**: `internal/adapter/runtime_test.go::scriptedAdapter` — struct with optional response hooks, `bundled(t)` returns a `BundledAdapter`. `pkg/adapterkit/testing.go` exports an exported version for third parties.
- **Cross-platform binary execution**: `subprocess_unix.go` / `subprocess_windows.go` build-tag split + `runtime.GOOS == "windows"` for `.exe` suffix. The conformance harness will need both.
- **Spec doc format**: `docs/spec/{ir-v1,manifest-v1,trust-store-v1}.md` — markdown with tables for enumerations, code fences for examples. `manifest-v1.md` uses YAML frontmatter; the new spec doc adopts the same.
- **Streaming JSON pattern**: `internal/adapter/runtime.go::irContainsSupportedKind` — `json.Decoder` walking tokens with a `skipJSONValue` helper. Adapterkit's IR-iteration helper mirrors this.

### Institutional Learnings

From `docs/solutions/`:

- **`go-windows-cross-platform-pitfalls-2026-04-24.md`**: Cross-platform binary execution must split signal-semantics tests into `_unix_test.go` files behind `//go:build !windows` (not `t.Skip` — `t.Skip` doesn't help if the file fails to compile under `GOOS=windows`). Path-string golden comparisons must use `filepath.ToSlash` on both sides. Run `GOOS=windows GOARCH=amd64 go vet ./...` before declaring PR 3 ready.
- **`spec-impl-drift-at-pr-review-2026-04-25.md`**: When a wire format is frozen and there's both a spec doc AND an impl, drift is the dominant risk. Mitigation: spec-locked fixture tests that **parse canonical examples out of the spec markdown** and run them through the impl. Conformance corpus IS this mechanism. Don't `t.Skip()` corpus fixtures — either delete them or mark them as known-failing in a structured way. Use `Example_*` functions in `pkg/adapterkit/` so prose docs are locked to compiling code.

### External References

None — Phase 1.2 decision: skip external research. The codebase already
has strong local patterns for everything PR 3 needs (Cobra factories,
embed.FS, build-tag splits, content-hashed test binaries). External
research would not add practical value.

## Key Technical Decisions

### Decision 1: Adapterkit mirrors wire types instead of moving the contract package

**Decision:** `pkg/adapterkit/` defines its own Go types for the public
API surface. It does not import or share types with
`internal/adapter/contract/`.

**Rationale:** Three options were considered:

1. **Move `internal/adapter/contract/` to `pkg/adapterkit/contract/`.**
   Architecturally clean — the contract package was always intended to
   be the public wire spec (per the comment in `contract/schema.go`
   that already pre-states PR 3 will consume `SchemaFS` publicly). But
   the move touches every file in `internal/adapter/` that imports
   contract (~10 files), expands PR 3's diff into rename noise,
   creates merge conflicts with any in-flight branches, and risks
   confusing the review of the actual conformance + spec work.
2. **Move only the JSON Schemas.** Smaller move (~5 files), but
   adapterkit still needs Go types matching the wire format, and
   either re-defines them or imports from internal — same problem.
3. **Mirror types in adapterkit (chosen).** Adapterkit defines a
   minimal set of public types (`InitializeResult`, `EmitResult`,
   `OpRecord`, `Capabilities`, `DeclaredOutput`, `Error`, etc.) with
   identical JSON tags and field names. The wire format is the
   contract; both internal and public types serialize to the same
   bytes. Schema parity tests in adapterkit validate the public types
   against the existing `SchemaFS` (which adapterkit re-imports from
   `internal/adapter/contract` is forbidden — but adapterkit tests
   _can_ import the corpus harness, which can read schema bytes via
   a public accessor we'll add). Drift is detected by parity tests at
   build time.

   Trade-off: hand-maintained type duplication. Mitigation: a single
   parity test (`adapterkit_schema_parity_test.go`) marshals samples
   of every adapterkit type and validates the JSON against the
   existing JSON Schemas — same approach `internal/adapter/contract/schema_parity_test.go`
   already uses internally. Drift surfaces as a test failure, not as
   a runtime mismatch.

The schema files themselves stay at `internal/adapter/contract/schema/`
for now. If we ever need to publish them (e.g., for non-Go adapter
authors to validate against), Unit 8b can move them — that's an
additive change, not a rework.

**Why not just publish contract directly?** The "publish contract"
move is right long-term, but PR 3's job is conformance harness + spec
freeze + reference echo. Mixing in a multi-file rename refactor
expands review surface and risks both items getting noticed less.
Defer the move until adapter authors actually request access to
internal types — which the SDK is designed to make unnecessary.

### Decision 2: Conformance harness lives in `internal/adapter/conformance/`, not in adapterkit

**Decision:** The harness types, corpus, and driver live in
`internal/adapter/conformance/`. `pkg/adapterkit/` exposes a thin
wrapper (`adapterkit.RunConformance(t, binary, opts)`) that internally
calls into the conformance package via a public-from-internal facade.

**Rationale:** The conformance harness needs the runtime's actual
spawn machinery (subprocess management, magic-cookie handshake,
declared-outputs gate, timeout enforcement). All of that lives in
`internal/adapter/`. The harness imports it directly.

If we put the harness in `pkg/adapterkit/`, it would need its own
subprocess/lifecycle implementation OR import internal — both bad.
Keeping it in `internal/adapter/conformance/` lets it use the real
runtime, which is the whole point: the harness validates that an
adapter speaks the protocol the way the runtime expects, not some
abstract second model.

The `pkg/adapterkit/` wrapper exists so adapter authors can call
`adapterkit.RunConformance(t, "./my-adapter", nil)` from their test
suite without learning the internal package layout. The wrapper is
a thin facade: it constructs the runtime args from whatever options
the caller passed and dispatches.

How does `pkg/adapterkit/` call into `internal/adapter/conformance/`?
It can't — internal/ blocks the import. Resolution: the
`aienvs adapter conformance-test <binary>` CLI subcommand IS the
public surface. Third-party authors run it as a binary from their
test suite (e.g., via `go run` or by depending on an `aienvs`
binary). The CLI subcommand returns a structured exit code + JSON
report; adapterkit can wrap it via `os/exec` if a Go-API entry point
is wanted later.

For PR 3, the public Go API for conformance is: **none**. Use the
CLI subcommand. This keeps the surface minimal and avoids designing
an adapterkit conformance API before we know the right shape.

### Decision 3: Corpus format — single JSON file per case, embedded via `embed.FS`

**Decision:** Each corpus case is one JSON file at
`internal/adapter/conformance/corpus/<scenario>.json`. The whole
directory is embedded via `//go:embed corpus/*.json` into a
`var Corpus embed.FS`.

**Rationale:** Mirrors the existing `SchemaFS` pattern in
`contract/schema.go`. JSON is verbose but reviewable in a PR — humans
can read what an adversarial fixture is testing. Each file is
self-contained: it carries the IR input, the manifest's expected
declared_outputs and capabilities, the expected ops emitted (or the
expected error class), and a brief human-readable description.

One file per case rather than one big JSON makes diffs reviewable
and lets corpus growth happen without merge conflicts on a single
file.

### Decision 4: Reference echo at `conformance/echo/`, separate from `internal/adapter/testdata/echo/`

**Decision:** A new `conformance/echo/main.go` (top-level, not under
`internal/`) ships in the repo as a published reference. It uses
`pkg/adapterkit/` and handles every IR kind in the v1 capability
matrix.

**Rationale:**

- `internal/adapter/testdata/echo/main.go` is a **test stub** for the
  subprocess transport's integration tests. It's intentionally
  minimal (no IR-kind dispatch, no full capability matrix). It also
  lives under `testdata/` so it's invisible to `go build ./...`.
- The reference adapter is a **published example**. It's part of the
  repo's normal build (`go build ./conformance/echo/`), it uses the
  public adapterkit API (so its existence proves adapterkit is
  usable), and it demonstrates how adapter authors should structure
  their own adapters.

Naming: `conformance/echo/` (not `cmd/echo/` or `examples/echo/`)
because it's both (a) a reference, and (b) the canonical "adapter
under test" in the conformance harness's own integration tests. Both
roles cluster naturally under `conformance/`.

### Decision 5: Spec doc is the source of truth for the wire format; impl tests against it

**Decision:** `docs/spec/adapter-protocol-v1.md` is the authoritative
v1 wire spec. Every canonical example in the spec is also a corpus
fixture (or directly parseable as one). A spec-locked test parses
the canonical examples out of the spec markdown and runs them through
the runtime — failure means spec/impl drift.

**Rationale:** Per the `spec-impl-drift-at-pr-review-2026-04-25.md`
learning, this is the only way to prevent drift. The spec doc is
prose for humans; the corpus is mechanical for machines. They have
to agree, and agreement is enforced by an executable test.

The spec uses YAML frontmatter (`title`, `status: frozen`, `date`,
`version: aienvs/v1`) to mark itself as frozen. The status field is
the contract: changing it requires a deliberate PR with the version
bumped to `aienvs/v2`.

### Decision 6: CLI subcommand returns structured JSON + exit code

**Decision:** `aienvs adapter conformance-test <binary>` runs the
full corpus against the target binary and outputs a JSON report:
`{cases: [{name, status: pass|fail|skip, reason}], summary: {total, passed, failed, skipped}}`.
Exit code 0 if all cases passed (skips don't count as failures), non-zero
otherwise.

**Rationale:** Adapter authors will run this in their CI. Structured
output enables programmatic parsing; exit code enables shell-level
gating. Both are standard CLI conformance patterns.

A `--verbose` flag dumps per-case detail (frames sent/received,
ops emitted, expected vs actual diffs) for debugging when a fixture
fails.

## Open Questions

### Resolved During Planning

- **Q: Should `pkg/adapterkit/` import `internal/adapter/contract/`?**
  A: It can't (Go forbids it). Adapterkit mirrors types and uses
  schema parity tests to detect drift. See Decision 1.
- **Q: Where does the conformance harness live — public or internal?**
  A: Internal. Public surface is the CLI subcommand. See Decision 2.
- **Q: Is the reference echo distinct from the testdata echo?**
  A: Yes. Different roles, different locations. See Decision 4.
- **Q: How is spec/impl drift prevented?**
  A: Canonical examples in the spec are also corpus fixtures; a
  spec-locked test loads them. See Decision 5.

### Deferred to Implementation

- **Exact corpus case names + count.** The plan enumerates required
  scenarios (happy paths per IR kind, adversarial cases) but the
  implementer chooses naming conventions and may discover additional
  cases worth covering during execution. Initial set: ~12-15 fixtures.
- **Adapterkit Server's exact API shape.** The unit defines the
  responsibilities (handler registration, dispatch, error mapping,
  cookie + capabilities boilerplate); the implementer chooses
  between `Server.Handle(method, fn)` vs typed `Server.OnEmit(fn)`
  vs an interface-based approach during implementation. The
  reference echo is the design forcing function — whatever shape
  makes echo cleanest is the right shape.
- **Spec doc length and section order.** Targets ~800-1200 lines of
  markdown. Section order will likely follow the sequence a third-party
  encounters it: framing → envelope → handshake → emit → shutdown →
  errors → versioning. Final outline is an execution-time decision.
- **Whether to add per-case `Example_*` functions in adapterkit or
  centralize them.** Both work; pick whatever reads best in `go doc`.

## Output Structure

```text
internal/adapter/conformance/
  doc.go                          package doc
  harness.go                      driver: spawn binary, run lifecycle, diff
  corpus.go                       embed.FS + corpus loader
  corpus/
    happy-rule.json
    happy-skill.json
    happy-agents-md.json
    happy-command.json
    happy-plugin-reference.json
    happy-mcp-server-entry.json
    error-protocol-order.json
    error-cookie-mismatch.json
    error-version-mismatch.json
    error-undeclared-output.json
    error-capability-lied.json
    error-abnormal-exit.json
    error-large-frame.json
    spec-example-handshake.json   parsed from spec doc
    spec-example-emit.json        parsed from spec doc
  harness_test.go                 harness unit tests
  corpus_test.go                  corpus loader + integrity test
  spec_locked_test.go             reads docs/spec/adapter-protocol-v1.md, runs canonical examples

pkg/adapterkit/
  doc.go                          package doc with overview + Quickstart
  types.go                        public wire types (mirror of contract)
  server.go                       Server + handler registration + dispatch
  capabilities.go                 capability builder
  testing.go                      exported testing helpers (scripted responder etc.)
  example_test.go                 Example_* functions for godoc
  types_test.go                   adapterkit-internal type tests
  schema_parity_test.go           validates types against contract schemas

conformance/echo/
  main.go                         reference Go adapter using adapterkit
  echo_test.go                    runs adapterkit-built echo through harness

internal/cli/
  cmd_adapter.go                  NewAdapterCommand factory + conformance-test leaf
  cmd_adapter_test.go             CLI subcommand tests

docs/spec/
  adapter-protocol-v1.md          authoritative frozen wire spec
```

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for
> review, not implementation specification. The implementing agent
> should treat it as context, not code to reproduce.*

### Conformance harness data flow

```
┌──────────────────────────────────────────────────────────────────┐
│  aienvs adapter conformance-test <binary>                        │
│  (CLI subcommand — internal/cli/cmd_adapter.go)                  │
└─────────────────────────┬────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────────┐
│  conformance.Run(ctx, binary, corpus, opts)                      │
│  (internal/adapter/conformance/harness.go)                       │
└─────────────────────────┬────────────────────────────────────────┘
                          │
                          ▼
        ┌─────────────────────────────────────────┐
        │  for each case in corpus:               │
        │    1. Spawn binary as subprocess         │
        │    2. Drive Initialize → Initialized →   │
        │       Emit → Shutdown                   │
        │    3. Capture emitted ops + errors       │
        │    4. Diff against case.expected         │
        │    5. Record pass | fail | skip          │
        └─────────────────────────────────────────┘
                          │
                          ▼
              ┌───────────────────────┐
              │  Report{cases:[...]}   │
              │  + structured exit     │
              └───────────────────────┘
```

### Adapterkit Server lifecycle

```
   ┌─────────────────────────────┐
   │  package main (adapter author)  │
   │                                  │
   │  s := adapterkit.NewServer(...)  │
   │  s.OnInitialize(myInit)          │
   │  s.OnEmit(myEmit)                │
   │  s.OnShutdown(myShutdown)        │
   │  s.Run(ctx)  // blocks on stdio  │
   └──────────────┬──────────────────┘
                  │
                  ▼
   ┌─────────────────────────────────┐
   │  Server.Run reads framed JSON   │
   │  from stdin, dispatches to      │
   │  the registered handler, writes │
   │  framed JSON to stdout.         │
   │  Magic-cookie + capabilities    │
   │  boilerplate handled internally │
   │  before user handlers fire.      │
   └─────────────────────────────────┘
```

### Spec-locked test wiring

```
  docs/spec/adapter-protocol-v1.md
    ```json
    // canonical handshake example
    {"jsonrpc":"2.0", ...}
    ```
            │
            ▼  (markdown code-fence parser)
  internal/adapter/conformance/spec_locked_test.go
    extracts blocks tagged ```json with a sibling
    ```aienvs:fixture-name``` directive
            │
            ▼
  routes each example to a corpus fixture
            │
            ▼
  runs through the existing harness
            │
            ▼
  pass/fail per example
```

The directive convention (`aienvs:fixture-name` immediately preceding
a `json` code fence) lets the spec doc tag specific examples for
mechanical extraction. Other code fences are documentation-only and
ignored.

## Implementation Units

- [x] **Unit 1: Conformance package — types, corpus loader, embed.FS**

**Goal:** Establish `internal/adapter/conformance/` with the case
data model, the embedded corpus, and a loader. No driver yet — that's
Unit 2. This unit is foundation: it lets Unit 2 compile.

**Requirements:** R3, R11

**Dependencies:** None (PR 1 + PR 2 already merged)

**Files:**
- Create: `internal/adapter/conformance/doc.go` (package doc)
- Create: `internal/adapter/conformance/corpus.go` (`//go:embed corpus/*.json` + types + loader)
- Create: `internal/adapter/conformance/corpus/.gitkeep` (empty corpus dir; populated in Unit 2)
- Test: `internal/adapter/conformance/corpus_test.go` (loader test, integrity test that every embedded file parses)

**Approach:**
- Package doc explains the harness's role: spec freeze enforcement, conformance for third-party adapters.
- `Case` struct: `Name string`, `Description string`, `IR json.RawMessage`, `Manifest CaseManifest` (declared_outputs + capabilities), `Expect Expected` (one of: `Ops []OpRecord`, `Error string` — sentinel name like `ErrAdapterUndeclaredOutput`, or `Skip string` for cases the binary opts out of).
- `LoadCorpus()` reads every `corpus/*.json` from the embed.FS, parses into `[]Case`, sorts deterministically by name. Returns `(cases, error)`.
- `Expected` is a tagged union via discriminator field (`expect_kind: "ops" | "error" | "skip"`).

**Patterns to follow:**
- Embed: `internal/adapter/contract/schema.go` (`//go:embed schema/*.json`).
- Loader pattern: `internal/manifest/load.go` (typed structured load + validation).

**Test scenarios:**
- Happy path: empty corpus directory loads to empty slice, no error.
- Happy path: corpus with three valid files loads to three cases sorted by name.
- Error path: malformed JSON in a corpus file returns a wrapped error naming the file.
- Error path: case missing required field (e.g., empty `Name`) is rejected at load.
- Edge case: case with both `Ops` and `Error` set in `Expect` is rejected.

**Verification:** `go test -race ./internal/adapter/conformance/...` passes; the package compiles with an empty corpus.

---

- [x] **Unit 2: Initial corpus fixtures — happy paths + adversarial**

**Goal:** Populate `corpus/*.json` with the initial set: one happy-path
fixture per IR kind in `internal/ir.AllKinds()` (six total) plus the
adversarial cases the parent plan calls out.

**Requirements:** R3, R11

**Dependencies:** Unit 1

**Files:**
- Create: `internal/adapter/conformance/corpus/happy-rule.json`
- Create: `internal/adapter/conformance/corpus/happy-skill.json`
- Create: `internal/adapter/conformance/corpus/happy-agents-md.json`
- Create: `internal/adapter/conformance/corpus/happy-command.json`
- Create: `internal/adapter/conformance/corpus/happy-plugin-reference.json`
- Create: `internal/adapter/conformance/corpus/happy-mcp-server-entry.json`
- Create: `internal/adapter/conformance/corpus/error-protocol-order.json` (adapter emits ops before `initialized` notification)
- Create: `internal/adapter/conformance/corpus/error-cookie-mismatch.json` (adapter echoes wrong cookie)
- Create: `internal/adapter/conformance/corpus/error-version-mismatch.json` (adapter speaks `aienvs/v0`)
- Create: `internal/adapter/conformance/corpus/error-undeclared-output.json` (adapter emits op outside declared_outputs)
- Create: `internal/adapter/conformance/corpus/error-capability-lied.json` (adapter declares `rule` supported but emits no ops for IR with rule nodes)
- Create: `internal/adapter/conformance/corpus/error-abnormal-exit.json` (adapter exits 13 mid-emit)
- Create: `internal/adapter/conformance/corpus/error-large-frame.json` (adapter sends a frame larger than `DefaultMaxFrameBytes`)
- Modify: `internal/adapter/conformance/corpus_test.go` (add per-case load assertion: every kind in `ir.AllKinds()` has at least one happy-path fixture)

**Approach:**
- Each fixture is a self-contained JSON document. Schema:
  ```
  {
    "name": "happy-rule",
    "description": "Adapter handles a single rule kind, emits one mkdir + one write_file under .echo/",
    "ir": {"nodes": [{"id": "no-fri", "kind": "rule", ...}]},
    "manifest": {
      "declared_outputs": [{"path": ".echo/rules", "mode": "owned-subdir"}],
      "capabilities": {"concept_kinds": {"rule": "supported"}}
    },
    "expect": {
      "kind": "ops",
      "ops": [
        {"op": "mkdir", "path": ".echo/rules"},
        {"op": "write_file", "path": ".echo/rules/no-fri.md"}
      ]
    }
  }
  ```
- Adversarial cases use `expect.kind = "error"` with the expected sentinel name.
- The `large-frame` fixture is a special case: the IR is normal but the corpus says "the adapter should send a frame > 1 MiB"; the harness verifies the runtime rejects it.
- Add a `corpus_completeness_test.go` (or extend `corpus_test.go`): for every kind in `ir.AllKinds()`, assert at least one corpus case exists with that kind in its IR.

**Patterns to follow:**
- Naming: `<class>-<scenario>.json` (e.g., `happy-rule.json`, `error-undeclared-output.json`). Mirrors `testdata/manifest/valid-*.yaml` / `invalid-*.yaml` convention from `internal/manifest/`.

**Test scenarios:**
- Happy path: every fixture loads cleanly.
- Coverage: completeness test passes — every IR kind has at least one happy-path fixture.
- Edge case: each adversarial fixture's expected error is one of the actual sentinel names exported from `internal/adapter/errors.go` (verified via reflection or string match against a known set).

**Verification:** Corpus directory contains ~13 fixtures; all load; all kinds covered.

---

- [x] **Unit 3: Conformance harness driver**

**Goal:** Implement the actual driver that takes a binary path, spawns
it, runs each corpus case through the lifecycle, and reports results.

**Requirements:** R3, R11

**Dependencies:** Units 1 + 2

**Files:**
- Create: `internal/adapter/conformance/harness.go` (`Run(ctx, binary, opts) (Report, error)`)
- Create: `internal/adapter/conformance/assertions.go` (ops diff helpers, error class match)
- Test: `internal/adapter/conformance/harness_test.go`

**Approach:**
- `RunOptions` struct: `Cases []Case` (defaults to `LoadCorpus()`), `Timeouts adapter.SubprocessTimeouts` (defaults to runtime defaults), `Verbose bool`.
- `Report` struct: `Cases []CaseResult`, `Summary{Total, Passed, Failed, Skipped int}`.
- `CaseResult`: `Name string`, `Status string` (`pass`/`fail`/`skip`), `Reason string` (failure detail or skip reason), `ActualOps []OpRecord` (when relevant for verbose mode).
- For each case:
  1. Build an `adapter.AdapterManifest` from `case.Manifest` (synthetic — `Command: []string{binary}`, `ContractVersion: ContractVersionV1`, `Name: "conformance-target"`).
  2. Construct an `adapter.Adapter{Source: SourcePATH, Manifest: ...}`.
  3. Spawn a session via `adapter.NewSession(ctx, opts)`.
  4. Drive `Initialize` → `Initialized` → `Emit(case.IR)` → `Shutdown`.
  5. If `case.Expect.Kind == "ops"`: compare emitted ops against expected, set status accordingly.
  6. If `case.Expect.Kind == "error"`: check that the lifecycle returned an error matching the expected sentinel; pass if match, fail otherwise.
  7. If `case.Expect.Kind == "skip"`: skip the case (still record in report; doesn't count as failure).
  8. If the binary doesn't speak a kind (e.g., a minimal echo that only handles `rule`), the adapter SHOULD return an empty op list with no `capability-lied` error if it didn't declare the kind as supported. Cases that test unsupported kinds for a minimal adapter use `expect.kind = "skip"` keyed off whether the adapter's `initialize` declared the kind.
- Op diff: order-insensitive set match (each emitted op must appear in expected, no extras), unless the case sets `expect.strict_order: true`.
- `assertions.go` helpers: `MatchOps(expected, actual []OpRecord) (ok bool, missing, extra []OpRecord)`; `MatchError(expected string, err error) bool`.

**Patterns to follow:**
- `internal/adapter/runtime_test.go` for lifecycle driving.
- `internal/adapter/subprocess_test.go::buildTestdataBinary` + `TestMain` for binary build/cache in tests.
- `internal/adapter/runtime.go::pathInDeclaredOutputs` for path normalization (corpus path comparisons must match runtime semantics).

**Test scenarios:**
- Happy path: harness runs against `internal/adapter/testdata/echo/main.go` (built via `buildTestdataBinary`) and reports pass for the cases the testdata echo speaks (likely just `happy-rule`); the rest are skipped.
- Happy path: harness runs against the new `conformance/echo/` (built in Unit 6) and reports pass for all happy-path cases.
- Error path: harness runs against a binary that crashes immediately (`testdata/crashy`) and every case fails with `error-abnormal-exit` matching.
- Edge case: empty corpus produces an empty report with no error.
- Edge case: ctx cancellation mid-corpus aborts cleanly with a wrapped `context.Canceled` error.
- Op-diff: extra op in actual → fail; missing op in actual → fail; reordering when `strict_order` is false → pass.

**Execution note:** Test-first for the assertion helpers (op diff, error match). The harness driver itself is integration-heavy and is best validated end-to-end against real binaries.

**Verification:** Tests pass against testdata/echo (subset) and testdata/crashy (all-fail). End-to-end coverage with conformance/echo follows in Unit 6.

---

- [x] **Unit 4: Adapterkit — public types + Server + dispatcher**

**Goal:** First inhabitant of `pkg/`. Define the public Go types
mirroring the wire format, plus a `Server` type that handles the
protocol boilerplate (framing, JSON-RPC envelope, magic-cookie echo,
capability negotiation) so adapter authors only write business logic.

**Requirements:** R11

**Dependencies:** None (parallel to conformance work; PR 1 + 2 merged)

**Files:**
- Create: `pkg/adapterkit/doc.go` (package doc with Quickstart example)
- Create: `pkg/adapterkit/types.go` (public types: `InitializeParams`, `InitializeResult`, `Capabilities`, `DeclaredOutput`, `EmitParams`, `EmitResult`, `OpRecord`, `Op*` concrete types, `Error`, `ErrorClass`, `OpKind` constants, `OutputMode` constants, `CapabilityLevel` constants, `MethodInitialize`/`MethodInitialized`/`MethodEmit`/`MethodShutdown` constants)
- Create: `pkg/adapterkit/server.go` (`Server` struct, `NewServer(opts) *Server`, `OnInitialize`/`OnEmit`/`OnShutdown` registration, `Run(ctx) error` blocks on stdio)
- Create: `pkg/adapterkit/dispatcher.go` (internal: read frame → parse message → dispatch to handler → marshal response → write frame)
- Test: `pkg/adapterkit/server_test.go`
- Test: `pkg/adapterkit/types_test.go` (round-trip JSON marshalling)
- Test: `pkg/adapterkit/schema_parity_test.go` (validates types against schemas — see below)

**Approach:**
- **Mirror types**: identical JSON tags and field names to `internal/adapter/contract`. Different Go types — no aliases. Drift is detected by Unit 4's parity test.
- **Server API shape** (directional — final shape decided during execution based on what makes the reference echo cleanest):
  ```
  // Pseudo-code, not implementation specification:
  type Server struct { ... }
  type InitializeFunc func(ctx context.Context, params InitializeParams) (InitializeResult, error)
  type EmitFunc func(ctx context.Context, params EmitParams) (EmitResult, error)
  type ShutdownFunc func(ctx context.Context) error
  func NewServer(name, version string) *Server
  func (s *Server) OnInitialize(fn InitializeFunc)
  func (s *Server) OnEmit(fn EmitFunc)
  func (s *Server) OnShutdown(fn ShutdownFunc)
  func (s *Server) Run(ctx context.Context) error  // reads stdin, writes stdout
  ```
- The `Server` handles the magic-cookie echo (reads `AIENVS_ADAPTER_COOKIE`, asserts non-empty, exits 7 if missing — matches PR 2 testdata echo behavior), version validation (refuses if client speaks a different version than `aienvs/v1`), and writes "started" to stderr so the runtime's stderr ring buffer has something to capture (matches existing testdata echo convention).
- Schema parity test: for each public adapterkit type, marshal a sample value to JSON and validate it against the schema embedded at `internal/adapter/contract/schema/<method>.json`. The test imports schemas via a public accessor we'll add to `internal/adapter/contract` (a `LoadSchema(name) ([]byte, error)` function — small public addition that doesn't expand the internal package's surface meaningfully). Failure means adapterkit drifted from contract.
- `pkg/adapterkit/internal_test.go` (or pattern-equivalent) for any package-private helpers.

**Patterns to follow:**
- `internal/adapter/contract/protocol.go` — exact field layouts and JSON tags to mirror.
- `internal/adapter/contract/schema_parity_test.go` — schema validation pattern.
- Package doc with Quickstart example: `internal/ir/types.go` (lines 1-10) for the doc style.

**Test scenarios:**
- Happy path: round-trip — every public type marshals to JSON and unmarshals back to an equal value (via reflect.DeepEqual or cmp.Diff).
- Happy path: `Server.Run` against a synthetic `io.Pipe` pair: send framed `initialize` → receive framed `initialize` response with cookie echo + capabilities → send `initialized` notification → send `emit` → receive emit response with handler-returned ops → send `shutdown` → receive empty shutdown response → server exits cleanly.
- Edge case: `Server.Run` with no `AIENVS_ADAPTER_COOKIE` env var exits with code 7 and an error message to stderr.
- Edge case: client sends `initialize` with `protocol_versions: ["aienvs/v0"]` — server responds with an error (not just an unrelated mismatch — must classify properly).
- Edge case: handler panic is recovered and reported as an `adapter-panic` error to the client.
- Schema parity: every adapterkit type that has a schema validates against it; schemas without an adapterkit equivalent are flagged.

**Execution note:** Test-first for the dispatcher — write a test that sends a sequence of framed messages and asserts the responses, then implement. This is the part where it's easiest to get the protocol order subtly wrong.

**Verification:** `go test -race ./pkg/adapterkit/...` passes; `go doc -all github.com/aienvs/aienvs/pkg/adapterkit` produces readable output with the Quickstart in the package overview.

---

- [x] **Unit 5: Adapterkit — capability builder + testing helpers**

**Goal:** Helper APIs for adapter authors. (a) Capability builder
fluent API for constructing `Capabilities{}` without manually wiring
the `concept_kinds` map; (b) testing helpers exported for adapter
authors' unit tests (scripted responder, init-result synthesizer,
verification that `MarkProtocolShutdownAcked` was called).

**Requirements:** R11

**Dependencies:** Unit 4

**Files:**
- Create: `pkg/adapterkit/capabilities.go` (`CapabilitiesBuilder` fluent API)
- Create: `pkg/adapterkit/testing.go` (exported testing helpers — note: not `_test.go`, must be exported)
- Create: `pkg/adapterkit/example_test.go` (`Example_*` functions for godoc — locks docs to compiling code per spec-impl-drift learning)
- Test: `pkg/adapterkit/capabilities_test.go`
- Test: `pkg/adapterkit/testing_test.go`

**Approach:**
- **CapabilitiesBuilder** (directional shape):
  ```
  // Pseudo-code:
  caps := adapterkit.NewCapabilities().
    Supports("rule").
    Partial("skill", "agent assets not yet handled").
    Unsupported("mcp-server-entry").
    WithWriteToolOwned(true).
    Build()
  ```
- **Testing helpers**:
  - `ScriptedResponder` — like `internal/adapter/runtime_test.go::scriptedAdapter` but exported. Lets adapter authors write unit tests that exercise their handlers against synthetic clients.
  - `SynthesizeInitResult(name, version string, caps Capabilities, outputs []DeclaredOutput) []byte` — returns the wire bytes of a valid `InitializeResult`.
  - `RunInprocServer(t, server) (client *Client, cleanup func())` — wires `Server` to an in-memory `io.Pipe` pair so tests can drive it without a real subprocess.
  - `AssertProtocolShutdownAcked(t, server)` — assertion helper that verifies the server called the equivalent of `MarkProtocolShutdownAcked` (PR 2's flow); helps adapter authors catch the case where their shutdown handler returns nil but didn't actually finalize state.
- **Example_* functions**: one per public entry point (`Example_NewServer`, `Example_CapabilitiesBuilder`, `Example_RunInprocServer`). Per the spec-impl-drift learning, these lock docstring code to compiling code.

**Patterns to follow:**
- Fluent builder: any existing fluent builder in the codebase (search at exec time; if none, this sets the precedent — keep it Go-idiomatic, return the builder for chaining).
- Testing helpers exported: `internal/adapter/runtime_test.go::scriptedAdapter` is the unexported template; export the same shape.
- Example tests: standard library examples (e.g., `bytes.Buffer` Example_*) — naming convention `Example_<entrypoint>`.

**Test scenarios:**
- Happy path: builder produces a `Capabilities` whose `concept_kinds` map matches the chained calls.
- Happy path: `ScriptedResponder` round-trip — caller registers handlers, runs lifecycle through `RunInprocServer`, observes expected response.
- Edge case: builder's `Supports` and `Unsupported` for the same kind: last write wins, or the second call panics (decide during execution; document the choice).
- Edge case: `AssertProtocolShutdownAcked` correctly fails when the server's shutdown handler didn't ack.
- Example tests compile and pass.

**Execution note:** When designing the helper API, write the reference-echo Unit 6 first in your head (or sketch its main.go) to make sure the helpers are actually useful for the canonical case.

**Verification:** All Example_* functions compile and pass; `go doc` output for each public entry point includes the relevant example.

---

- [ ] **Unit 6: Reference echo adapter using adapterkit**

**Goal:** A canonical Go adapter at `conformance/echo/main.go` that
uses `pkg/adapterkit/` and handles every IR kind. Proves adapterkit
is usable end-to-end. Becomes the harness's primary positive
integration target.

**Requirements:** R11

**Dependencies:** Units 4 + 5

**Files:**
- Create: `conformance/echo/main.go`
- Create: `conformance/echo/echo_test.go`
- Create: `conformance/echo/README.md` (one-page "this is the canonical reference adapter; here's how to use it as a template")

**Approach:**
- Single `main.go` that:
  1. Constructs an `adapterkit.Server` named `echo/v0.1`.
  2. Builds capabilities declaring every IR kind from `internal/ir.AllKinds()` as supported (using the capabilities builder).
  3. Declares one output: `{Path: ".echo", Mode: OutputModeOwnedSubdir}`.
  4. Registers an `OnEmit` handler that, for each IR node, emits one `mkdir` + one `write_file` under `.echo/<id>.md` with the node's body as content.
  5. Calls `s.Run(ctx)`.
- The handler does NOT write files — it returns ops, and the runtime applies them. (This is the architectural invariant from AGENTS.md: "Adapters never write files directly.")
- The README.md is a one-page guide for adapter authors: "copy this directory as a starting point, change the manifest, change the OnEmit handler."
- Test file runs the conformance harness against the built echo binary and asserts every happy-path case passes.

**Patterns to follow:**
- The existing `internal/adapter/testdata/echo/main.go` is a similar minimal stub — read it for protocol mechanics, but the new echo uses adapterkit instead of speaking the protocol directly.

**Test scenarios:**
- Happy path: every happy-path corpus case passes when the harness runs against the built echo binary.
- Edge case: the echo binary builds cleanly with `go build ./conformance/echo/` (i.e., it's a real Go program reachable from the module root, not under `testdata/`).
- Edge case: running the binary with no `AIENVS_ADAPTER_COOKIE` env var exits with code 7 (matches the protocol's missing-cookie semantics).
- Integration: every adversarial corpus case classifies correctly (e.g., `error-undeclared-output` only triggers if the echo emits something out of scope — since echo only emits under `.echo/`, this case should be marked `skip` for echo. Decide per-case during corpus-Unit-2 execution.)

**Verification:** `go build ./conformance/echo/` succeeds; `go test -race ./conformance/echo/...` passes; the harness reports all happy-path cases pass against the built echo binary.

---

- [ ] **Unit 7: CLI subcommand — `aienvs adapter conformance-test <binary>`**

**Goal:** Public CLI surface for running the conformance corpus
against a target binary. Adapter authors invoke this from their CI.

**Requirements:** R3, R11

**Dependencies:** Unit 3 (harness driver)

**Files:**
- Create: `internal/cli/cmd_adapter.go` (`NewAdapterCommand(deps AdapterDeps) *cobra.Command` factory + `newAdapterConformanceTestCmd(deps)` leaf)
- Test: `internal/cli/cmd_adapter_test.go`
- Modify: `cmd/aienvs/main.go` is currently a stub — DO NOT wire root-level commands here. Unit 16 of the parent plan owns that wiring. Just make sure `NewAdapterCommand` is independently testable via the same `testEnv` pattern as `cmd_trust_test.go`.

**Approach:**
- Follow the established Cobra factory pattern from `cmd_trust.go`:
  - `AdapterDeps` struct: `Out io.Writer`, `Err io.Writer`, `In io.Reader`, `Now func() time.Time` (for deterministic test output).
  - `NewAdapterCommand(deps)` returns the `adapter` parent.
  - `newAdapterConformanceTestCmd(deps)` returns the `conformance-test` leaf.
- Subcommand flags:
  - Positional arg: `binary` (required).
  - `--verbose, -v`: per-case detail output.
  - `--format=json|text`: output format (default `text` for human use; `json` for CI parsing).
  - `--filter=<regex>`: run only cases matching the pattern.
  - `--timeout=<duration>`: override per-case timeout.
- Output:
  - Text: human-readable per-case `pass`/`fail`/`skip` with reasons + final summary.
  - JSON: `{"cases": [{"name", "status", "reason"}], "summary": {"total", "passed", "failed", "skipped"}, "version": "aienvs/v1"}`.
- Exit code: 0 if all cases passed (skips are non-failures); 1 if any case failed; 2 if the binary itself couldn't be spawned (clearer signal for "your binary is broken" vs "your protocol implementation is broken").

**Patterns to follow:**
- `internal/cli/cmd_trust.go` — Cobra factory + Deps struct + lazy resolvers + `outWriter`/`errWriter` helpers + `cobra.Command` settings (`SilenceUsage: true, SilenceErrors: true`, `RunE` over `Run`).
- `internal/cli/cmd_trust_test.go` `testEnv` pattern — `bytes.Buffer` capturing Out/Err.

**Test scenarios:**
- Happy path: `aienvs adapter conformance-test <built-echo-binary>` exits 0; text output matches expected per-case lines + summary.
- Happy path: `--format=json` produces parseable JSON matching the documented schema.
- Error path: `aienvs adapter conformance-test /does/not/exist` exits 2; error message clearly distinguishes "binary doesn't exist" from "binary exists but failed conformance".
- Error path: `aienvs adapter conformance-test <crashy-binary>` exits 1; text output reports per-case failures.
- Edge case: `--filter='happy-.*'` runs only happy-path cases.
- Edge case: `--timeout=1ms` causes every case to timeout; exits 1 with timeout-classified failures.
- Edge case: `--verbose` includes frame-level detail (sent/received) in failure cases.

**Execution note:** Implement json output FIRST and use it as the test's stable representation; text output is then a thin formatter over the same data structure.

**Verification:** Subcommand runs from a test against the conformance/echo binary built in Unit 6 and reports all passes.

---

- [ ] **Unit 8: Spec freeze — `docs/spec/adapter-protocol-v1.md` + spec-locked tests**

**Goal:** The authoritative wire-format spec doc. Every canonical
example in the spec is also a corpus fixture; a spec-locked test
parses examples out of the markdown and runs them through the
harness. Without the test, spec/impl drift is unconstrained.

**Requirements:** R3 (protocol freeze)

**Dependencies:** Units 1 + 3 (need corpus + harness in place)

**Files:**
- Create: `docs/spec/adapter-protocol-v1.md`
- Create: `internal/adapter/conformance/spec_locked_test.go`

**Approach:**
- Spec doc structure (final ordering finalized during execution):
  1. **YAML frontmatter**: `title: aienvs Adapter Protocol v1`, `status: frozen`, `version: aienvs/v1`, `date: 2026-04-26`. The `status: frozen` line is the contract — changing it requires a deliberate `aienvs/v2` bump.
  2. **Overview** — what the protocol is, who it's for.
  3. **Framing** — LSP-style `Content-Length: <n>\r\n\r\n<body>`. `Content-Type` is reserved; v1 ignores it.
  4. **JSON-RPC envelope** — request/response/notification shapes; ID correlation; error object format including `data.error_class`.
  5. **Lifecycle** — `initialize` → `initialized` → `emit*` → `shutdown`. Magic-cookie handshake. Adapter MUST NOT emit ops before receiving `initialized`.
  6. **Capabilities object** — `concept_kinds` map (kind → `supported` | `partial` | `unsupported`), `write_tool_owned`, future-extension reservations.
  7. **Declared outputs** — `path` + `mode` (`owned-subdir` only in v1; `tool-owned` reserved). The integrity gate: any op outside `declared_outputs` is `adapter-undeclared-output`.
  8. **Op set** — `write_file`, `write_tool_owned`, `mkdir`, `delete`, `warning`. Field-by-field schema for each. Encoding rules (`utf8` vs `base64`; required when bytes are non-UTF-8).
  9. **Errors** — JSON-RPC standard codes; `data.error_class` values: `adapter-panic`, `adapter-timeout`, `adapter-protocol-mismatch`, `adapter-undeclared-output`, `adapter-exec-denied`, `adapter-capability-lied`. Exit-code taxonomy (adapter `exit_code` + `stderr_tail`).
  10. **Magic cookie** — `AIENVS_ADAPTER_COOKIE` env var, 32 hex chars, MUST echo verbatim in `initialize` result. Adapters that don't echo it fail with `adapter-exec-denied`.
  11. **`_meta` field** — reserved on every envelope; v1 ignores; future versions may carry implementation hints additively.
  12. **Timeouts** — handshake 5s, per-emit 30s, shutdown 5s. Defaults only; per-method overrides reserved for future.
  13. **Versioning policy** — Capabilities grow additively under `Capabilities{}`. `aienvs/v1` envelope/framing is frozen. `aienvs/v2` is reserved for envelope-level breaking changes.
  14. **Reserved for Unit 8b** — `$/cancelRequest`, `$/progress`, per-method timeouts, LSP error codes (`-32800`/`-32801`/`-32803`), version counter-proposal.
  15. **Examples** — canonical handshake, canonical emit, error response. Each example tagged with an `aienvs:fixture-name` directive immediately preceding the JSON code fence.
  16. **Conformance** — points at `internal/adapter/conformance/corpus/` and `aienvs adapter conformance-test`.
- Examples in the spec use the directive convention:
  ````
  ```aienvs:fixture-name
  spec-example-handshake
  ```
  ```json
  {"jsonrpc":"2.0","id":1,"method":"initialize", ...}
  ```
  ````
- The spec-locked test (`spec_locked_test.go`):
  - Reads `docs/spec/adapter-protocol-v1.md`.
  - Walks the markdown looking for `aienvs:fixture-name` directives followed by ```json fences.
  - For each match, asserts that `corpus/<fixture-name>.json` exists and that the JSON in the spec matches the corresponding field in the corpus fixture (e.g., the spec's example `initialize` payload equals the corpus fixture's expected `initialize` request bytes).
  - Failure modes: spec example with no matching corpus fixture → fail. Corpus fixture without a spec example for it → does NOT fail (corpus is a superset; only examples that the spec promises must be locked).

**Patterns to follow:**
- `docs/spec/manifest-v1.md` — frontmatter, section structure, code-fence usage.
- `docs/spec/ir-v1.md` — table-for-enumerations style (e.g., the IR kind table).

**Test scenarios:**
- Happy path: every `aienvs:fixture-name` directive in the spec resolves to a corpus fixture; every payload matches.
- Error path: a spec example with no matching corpus fixture fails the test with a clear error message.
- Edge case: a spec without any `aienvs:fixture-name` directives still passes the test (it's not required to have any — they're opt-in markers).
- Edge case: malformed directive (e.g., `aienvs:` with no name) is reported as a spec format error.

**Execution note:** Write the spec doc and the corpus fixtures together — every example added to the spec gets a matching fixture in the same commit. The directive convention is the contract that keeps them in sync.

**Verification:** `go test ./internal/adapter/conformance/... -run TestSpecLocked` passes; rendering the spec doc to HTML produces a navigable single-page reference.

---

## System-Wide Impact

- **Interaction graph:** `pkg/adapterkit/` is the new public surface. The conformance harness uses the existing runtime (`internal/adapter/runtime.go`) to drive sessions — no runtime changes required. The CLI subcommand follows the Cobra factory pattern; Unit 16 of the parent plan eventually wires it onto root, but PR 3 just makes it factory-callable.
- **Error propagation:** Conformance harness wraps lifecycle errors in a `CaseResult` rather than returning raw errors — the harness itself never fails (unless the binary can't be spawned at all); failed corpus cases are reported as data.
- **State lifecycle risks:** Per-case spawn → lifecycle → shutdown means each case is independent. No shared state across cases. The harness's only persistent state is the corpus (read-only embed.FS) and the per-run report.
- **API surface parity:** `pkg/adapterkit/` is the new public Go surface. The CLI subcommand is the new public CLI surface. The spec doc is the new public protocol surface. All three must agree, enforced by:
  - Schema parity test (adapterkit types ↔ JSON schemas)
  - Spec-locked test (spec examples ↔ corpus fixtures)
  - Reference echo (adapterkit + corpus running end-to-end)
- **Integration coverage:** The reference echo running through the full conformance corpus end-to-end IS the integration test. Mocks would defeat the point.
- **Unchanged invariants:**
  - Wire protocol bytes are unchanged (PR 1 + 2 shipped them; PR 3 documents them).
  - Runtime behavior (manifest validation, discovery, declared-outputs gate, capability-lied detection) is unchanged.
  - Existing test suites continue to pass without modification.
  - `internal/adapter/contract/` is unchanged; only a small public accessor (`LoadSchema(name) ([]byte, error)`) is added for adapterkit's parity test.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Adapterkit type drift from `internal/adapter/contract/` | Schema parity test in Unit 4; tests fail at build time on drift |
| Spec doc drift from impl | Spec-locked test in Unit 8; examples in spec must match corpus fixtures |
| Conformance harness false-passes (a binary that's broken in subtle ways still passes the corpus) | Adversarial corpus cases (Unit 2) cover the known failure modes from PR 1 + 2 reviews; corpus grows additively as new failure modes are discovered |
| Reference echo ages out as the canonical example | Echo + adapterkit run through the full corpus in CI; if echo breaks, adapterkit-related changes are immediately visible |
| Cross-platform: harness or CLI subcommand fails on Windows | Build-tag splits per `go-windows-cross-platform-pitfalls-2026-04-24.md`; `GOOS=windows GOARCH=amd64 go vet ./...` before declaring done; CI matrix already runs Windows |
| `pkg/adapterkit/` API ossifies before adapter authors validate it | API is intentionally minimal in PR 3 (`Server`, `OnInitialize`/`OnEmit`/`OnShutdown`, capability builder, testing helpers). Future expansion is additive. The reference echo is the design forcing function — if it's awkward to write, the API is wrong |
| Conformance harness runtime cost (subprocess per case × ~13 cases × CI matrix) | Each case is fast (~100ms — handshake + tiny emit + shutdown). Total <2s per platform. If cost grows, batch via session reuse in a follow-up — not blocking for PR 3 |

## Documentation / Operational Notes

- `docs/spec/adapter-protocol-v1.md` is the authoritative protocol spec going forward. Reviews of any future PR touching `internal/adapter/contract/`, `pkg/adapterkit/`, or `internal/adapter/conformance/` MUST verify the spec doc was updated in the same PR.
- `conformance/echo/README.md` is the entry point for adapter authors. Link it from the repo README under a "Building Adapters" section (handled in Unit 16, not PR 3 — but the README file itself ships in PR 3).
- After PR 3 lands, Unit 8 in the parent plan is complete and can be ticked.
- Capture a learning in `docs/solutions/best-practices/` after PR 3 lands: the spec-locked-fixture pattern (parsing canonical examples out of a frozen markdown spec and running them through impl) is a reusable mitigation for spec/impl drift. PR 3 is its first concrete instance.

## Sources & References

- Parent plan: `docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md` (Unit 8, line 599)
- PR 1 plan: `docs/plans/2026-04-25-001-feat-unit-8-pr1-wire-protocol-plan.md`
- PR 2 plan: `docs/plans/2026-04-26-001-feat-unit-8-pr2-runtime-plan.md`
- Cross-platform learning: `docs/solutions/best-practices/go-windows-cross-platform-pitfalls-2026-04-24.md`
- Spec/impl drift learning: `docs/solutions/workflow-issues/spec-impl-drift-at-pr-review-2026-04-25.md`
- Existing wire types: `internal/adapter/contract/protocol.go`
- Existing runtime: `internal/adapter/runtime.go`
- Existing CLI factory pattern: `internal/cli/cmd_trust.go`
- Existing embed.FS pattern: `internal/adapter/contract/schema.go`
- Existing scripted-adapter pattern: `internal/adapter/runtime_test.go::scriptedAdapter`
- Existing spec docs: `docs/spec/{ir-v1,manifest-v1,trust-store-v1}.md`

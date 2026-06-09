---
title: "test: raise coverage to the 80% floor and enforce it in CI"
status: completed
date: 2026-06-09
type: test
origin: docs/plans/2026-06-08-007-feat-cli-tui-sync-engine-plan.md
---

# test: raise coverage to the 80% floor and enforce it in CI

## Summary

The production-hardening branch `chore/production-hardening` added a CI coverage
gate but set it to a **regression ratchet at 77%** rather than the **80% floor**
CLAUDE.md mandates, because total statement coverage is currently 77.2%. This
plan closes that residual: add real behavioral tests to the three packages
dragging the total down — `internal/validate` (0%), `conformance/echo` (~21%),
and `cmd/agent-sync` (0%) — until total coverage is ≥80%, then raise the CI
floor to 80% so the standard is enforced rather than aspirational.

This is test-only work plus a one-line CI threshold change. No production
behavior changes.

---

## Problem Frame

`CLAUDE.md` requires "80%+ line coverage (floor, not ceiling)" enforced in CI.
The hardening pass could not honestly set that floor without breaking the build:
measured total is **77.2%**. The gate was therefore set to 77% with an inline
comment naming the target and the packages responsible. The honest close is to
earn the 80% by testing the untested code, not by lowering the bar or by gaming
coverage with assertion-free tests.

Three packages carry almost all the deficit:

- **`internal/validate`** — 0%. A pure presentation layer over
  `engine.PlanResult` with zero I/O. Trivially testable, currently has no test
  file at all.
- **`conformance/echo`** — ~21%. The reference adapter's in-process functions
  (`handleEmit`, `nodeContent`, `buildCapabilities`) are only ever reached
  through the subprocess conformance corpus, so direct statement coverage is
  low even though behavior is exercised end-to-end.
- **`cmd/agent-sync`** — 0%. `run([]string) int` is directly callable but never
  invoked from a test; only `main()` (which calls `os.Exit`) wraps it.

---

## Requirements Trace

- **R-COV1** — Total statement coverage (`go tool cover -func` total line) is
  ≥80.0%. → U1, U2, U3
- **R-COV2** — The CI coverage gate enforces an 80.0% floor. → U4
- **R-COV3** — New tests assert real behavior (inputs → outputs), follow Go
  table-driven conventions per CLAUDE.md, and pass under `-race`. → U1, U2, U3
- **R-COV4** — `go vet`, `go test -race ./...`, and `golangci-lint run` remain
  clean. → U1–U4

Success criteria: `go test -coverprofile` total ≥80%; CI `coverage` job floor
set to 80.0 and green; no skipped or assertion-free tests introduced.

---

## Scope Boundaries

In scope:
- New `_test.go` files for `internal/validate`, `conformance/echo` (additive),
  `cmd/agent-sync`.
- Bumping the CI coverage `THRESHOLD` from 77.0 to 80.0 and updating its comment.

### Deferred to Follow-Up Work
- Raising coverage of other mid-tier packages (`internal/cli` 66%, `internal/tui`
  61%, `internal/engine` 71%) — not needed to clear 80% total and out of this
  pass's scope.

### Out of scope (deferred to v1.0.0)
- `rollback` / `unmanage` commands.
- The extension-SDK CLI (Unit 20).

---

## Key Technical Decisions

- **Earn the floor, don't lower the bar.** Add genuine tests rather than relax
  the gate or pad with assertion-free tests. The coverage number must reflect
  real verification (R-COV3).
- **Target the zero/low-coverage packages first.** Lifting `internal/validate`
  (0→~95%) and `conformance/echo` (21→~70%) alone is expected to clear 80%
  total; `cmd/agent-sync` adds margin and removes a 0% package. If total still
  falls short after U1–U3, the cheapest additional lift is `internal/validate`
  branch completeness (already near-total) — but project to clear with margin.
- **Test in-process, not via new subprocesses.** For `conformance/echo`, call
  `handleEmit`/`nodeContent` directly in the existing `package main` test file
  rather than spawning more binary runs — direct calls are what raise statement
  coverage and run fast under `-race`.
- **`cmd/agent-sync` tests target `run`, not `main`.** `main()` calls `os.Exit`
  and is not unit-testable; `run([]string) int` is the seam. This mirrors the
  existing `internal/cli` root-command test style (construct root, set args,
  execute, assert exit int).
- **Set the floor to exactly 80.0.** Once measured total clears it with margin,
  80.0 is the enforced floor; do not over-tighten to the measured value (leave
  headroom so unrelated refactors don't trip the gate).

---

## Implementation Units

### U1. Unit-test `internal/validate` (0% → ~95%)

**Goal:** Cover the drift-report presentation layer end to end.

**Requirements:** R-COV1, R-COV3, R-COV4.

**Dependencies:** none.

**Files:**
- `internal/validate/validate_test.go` (new)

**Approach:** Build synthetic `engine.PlanResult` values (no I/O, no git) and
assert each rendering path. Table-driven where the cases share shape. Inspect
`engine.PlanResult` / its target struct fields directly to construct fixtures
(`WorkspacePath`, `Commit`, `DriftDetected`, `Targets[]` with
`WouldCreate/Update/Delete`, `OutOfBand`, `Warnings`, `Error`).

**Patterns to follow:** existing table-driven tests in `internal/report`
(`internal/report/summary_test.go`, `json_test.go`) — same "render a result
struct, assert bytes/fields" shape.

**Test scenarios:**
- `ExitCode` returns `ExitNoDrift` (0) when `DriftDetected` is false; `ExitDrift`
  (1) when true.
- `RenderText` with no drift writes exactly the "No drift" line.
- `RenderText` with a target carrying create/update/delete/out-of-band/warning
  items emits each labeled line in order.
- `RenderText` with a target that has a non-empty `Error` emits the error line
  and skips the list rendering for that target (continue path).
- `RenderText` with multiple targets renders each target header.
- `MarshalJSON` produces `schema_version: 1`, carries workspace/commit/
  drift_detected, and renders nil slices as `[]` (assert the `nonNil` behavior:
  a target with all-nil lists serializes arrays, not `null`).
- `MarshalJSON` omits the `error` field when empty (`omitempty`) and includes it
  when set.
- Round-trip: `MarshalJSON` output unmarshals to the expected document shape.

**Verification:** `go test -race ./internal/validate/...` passes; package
coverage ≥90%.

### U2. Direct in-process tests for `conformance/echo` (~21% → ~70%)

**Goal:** Cover the reference adapter's emit logic without spawning the binary.

**Requirements:** R-COV1, R-COV3, R-COV4.

**Dependencies:** none.

**Files:**
- `conformance/echo/emit_test.go` (new, `package main`)

**Approach:** Call `handleEmit`, `nodeContent`, and `buildCapabilities` directly.
Construct `adapterkit.EmitParams` with a hand-built `emitDocument` JSON in
`params.IR`. Assert returned `OpRecord`s and error classification. Keep the
existing subprocess corpus test (`echo_test.go`) untouched — this is additive.

**Patterns to follow:** `conformance/echo/echo_test.go` for fixture construction
helpers; `internal/adapter/contract` op-record assertions.

**Test scenarios:**
- `handleEmit` with two valid nodes returns a leading `mkdir` op for the output
  root followed by one `write_file` op per node, with forward-slash paths
  (`.echo/<id>.md`).
- `handleEmit` with zero nodes returns no `mkdir` op (the `len(doc.Nodes) > 0`
  guard) and an empty op list.
- `handleEmit` with an invalid node id (e.g. `"Bad ID"`, leading hyphen, >64
  chars) returns an `*adapterkit.Error` with `Code == CodeInvalidParams`.
- `handleEmit` with malformed IR JSON returns a wrapped decode error.
- `nodeContent` with a JSON string body returns the unquoted text bytes.
- `nodeContent` with a JSON object body returns the raw JSON bytes verbatim.
- `nodeContent` with empty/`null` body returns `(nil, nil)`.
- `nodeContent` with invalid JSON returns the "body must be valid JSON" error.
- `buildCapabilities` reports `write_tool_owned` true and lists every
  `ir.AllKinds()` kind as supported.

**Verification:** `go test -race ./conformance/echo/...` passes; package coverage
≥65%.

### U3. Test `cmd/agent-sync` `run` (0% → coverage on the entry seam)

**Goal:** Cover the binary entry point's argument-to-exit-code wiring.

**Requirements:** R-COV1, R-COV3, R-COV4.

**Dependencies:** none.

**Files:**
- `cmd/agent-sync/main_test.go` (new, `package main`)

**Approach:** Call `run([]string{...})` directly and assert the returned int.
`main()` is not unit-testable (calls `os.Exit`); `run` is the seam. Avoid
asserting on fang's styled output bytes (brittle); assert the exit code and that
execution does not panic.

**Patterns to follow:** `internal/cli/root_test.go` (construct root, set args,
execute, assert) — `run` wraps exactly that path via `fang.Execute` + `MapExit`.

**Test scenarios:**
- `run([]string{"--help"})` returns 0.
- `run([]string{"--version"})` returns 0 (version defaults to `"dev"` in tests).
- `run([]string{"definitely-not-a-command"})` returns a non-zero exit code via
  `MapExit` (unknown subcommand → cobra error → mapped).
- `run([]string{})` (no args) returns a documented code without hanging
  (help/usage path; non-interactive-safe).

**Verification:** `go test -race ./cmd/agent-sync/...` passes; package no longer
0%.

### U4. Raise the CI coverage floor to 80%

**Goal:** Enforce the mandated floor now that total clears it.

**Requirements:** R-COV2, R-COV4.

**Dependencies:** U1, U2, U3 (total must measure ≥80% first).

**Files:**
- `.github/workflows/ci.yml` (modify — `coverage` job `THRESHOLD`)
- `CHANGELOG.md` (modify — note the floor raised to 80%)

**Approach:** Change `THRESHOLD=77.0` to `THRESHOLD=80.0` and update the
explanatory comment (drop the "current is ~77.2%" framing; state 80% is the
enforced floor per CLAUDE.md). Verify locally that the measured total clears
80.0 with margin before committing the bump, so the gate is green on first CI
run.

**Patterns to follow:** the existing `coverage` job added in
`chore/production-hardening`.

**Test scenarios:** `Test expectation: none — CI config + changelog. Verified by
the coverage-gate dry-run in the verification step below.`

**Verification:** Local `go test -coverprofile=coverage.out ./... && go tool
cover -func=coverage.out | tail -1` reports total ≥80.0%; the gate's awk
comparison passes against `THRESHOLD=80.0`.

---

## Risks & Dependencies

- **Risk: U1–U3 don't clear 80% total.** Mitigation: `internal/validate` (0%,
  pure logic) and `conformance/echo` (21%, sizable emit path) are the two
  largest deficits; clearing them is projected to clear 80% with margin. If
  short, extend `internal/validate` branch coverage (already near-total) or add
  `internal/cli` command-path cases. Measure after U3 before U4.
- **Risk: assertion-free padding to hit the number.** Mitigation: every scenario
  above names input → expected output; reviewer (and `ce-code-review`) checks for
  real assertions, not just execution.
- **Dependency:** U4 strictly follows U1–U3 — never raise the floor before the
  measured total clears it, or CI goes red.

---

## Verification Strategy

Run the full gate after U1–U3 and again after U4:

- `go vet ./...`
- `AGENT_SYNC_REQUIRE_GIT=1 go test -race -count=1 ./...`
- `golangci-lint run`
- `go test -count=1 -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1` → total ≥80.0%

All four clean is the definition of done.

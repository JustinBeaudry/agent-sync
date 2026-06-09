---
title: Unit 15 — Sync summary + capability report + --output=json
type: feat
status: active
date: 2026-06-08
origin: docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md
---

# Unit 15 — Sync summary + capability report + `--output=json`

## Overview

The reporting layer: the per-target sync summary with top-line outcome,
the persisted capability report, and the stable machine-readable
`--output=json` document. Requirements R16 + Success Criteria. Pure data
(no wall-clock — caller stamps timestamps; no color — the CLI renders
over the ASCII tokens this package emits).

## Scope

**In scope (this PR):** `internal/report/{summary,capability,json}.go`,
`docs/spec/sync-summary-v1.md`, `docs/spec/capability-report-v1.md`,
tests.

**Deferred to Unit 16 (Cobra tree):** the `--output=json` / `--best-effort`
/ `NO_COLOR` flag wiring and color rendering (`access.go`). This unit
provides the builders, the deterministic ASCII `RenderText`, and the
JSON schema the CLI emits.

## Key Technical Decisions

1. **No per-target `partial`.** Per-target tokens are `ok`, `unchanged`,
   `failed`, `skipped`, `rolled-back`, `blocked`. `PARTIAL` is a top-line
   verdict only (best-effort mix of success + failure).
2. **Outcome logic:** `rolled-back > 0` → `FAIL` (atomic rollback);
   `failed > 0 && success > 0` → `PARTIAL`; `failed/blocked > 0` →
   `FAIL`; else `OK`. Non-OK → exit 1.
3. **Determinism:** targets sorted by name; nil slices serialize as `[]`
   not `null`; caller stamps `generated_at`. Same inputs → byte-identical
   output across platforms.
4. **Capability report persisted before swap** (decision #21) so it
   survives an atomic rollback; `required_unmet` (required kinds not
   `supported`) fails the sync in both modes.
5. **Schema versions pinned** (`schema_version: 1`) on both documents for
   CI-consumer stability.

## Test scenarios (shipped)

- All ok → `OK`, exit 0; best-effort 2 ok/1 failed → `PARTIAL`, exit 1;
  atomic rollback → `FAIL`, exit 1; zero-success → `FAIL`.
- Target sort determinism; `RenderText` carries tokens + top-line.
- `BuildCapability` required_unmet detection + `AnyRequiredUnmet`;
  deterministic marshal; `WriteCapabilityReport` lands at
  `.aienv/state/capability-report.json`.
- `MarshalJSON` schema keys, empty targets → `[]`, deterministic.

## Verification

`go vet`, `go test -race`, `golangci-lint` clean; coverage ≥ 80%. JSON
schemas published + versioned; output is mechanically parseable.

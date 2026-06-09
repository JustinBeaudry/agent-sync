# Handoff — aienvs MVP progress (2026-06-08)

Status snapshot for whoever resumes the `aienvs` MVP build. Written at a
deliberate checkpoint after six units landed and before the final
integration unit.

---

## TL;DR

- **`main` is clean and green.** Six units merged this session (PRs #11–#16).
- The **data + reporting primitives are done**; the two hardest
  data-loss units (12a merge engines, 13 atomic swap) are in.
- **Next and last MVP unit: Unit 16** — the Cobra command tree **plus the
  sync orchestration engine** that wires every prior unit into a usable
  `aienvs sync`. It does not exist yet and is the biggest, highest-risk
  integration in the project.
- This session **paused before Unit 16 on purpose**: the multi-agent
  `ce-code-review` is blocked by a Claude monthly **subagent spend
  limit**, and the orchestration engine (which commands the data-loss
  primitives) warrants real adversarial + reliability review rather than
  inline-only. Resume when the limit resets/raises.

---

## What shipped this session

| Unit | PR | Squash SHA | Package(s) | Summary |
|------|----|------------|-----------|---------|
| 10 — cursor adapter | #11 | `3081360` | `internal/adapter/bundled/cursor` | IR → Cursor ops (rules → `.cursor/rules/aienvs/<id>.mdc`, mcp → `.cursor/mcp.json`, agents-md → `AGENTS.md`) |
| 12 — ledger + locks | #12 | `6f8c0f0` | `internal/ledger`, `internal/locks` | SHA-256 ledger (schema-versioned, atomic write); per-target flock; per-external-file flock registry; machine-id |
| 12a — tool-owned merge | #13 | `e692a97` | `internal/merge` | JSON (`sjson`) / TOML (string-aware line-splice) / markdown (marker parser) surgical merge; fail-closed; fuzz-tested; `ApplyToFile` (flock + atomic write) |
| 13 — atomic swap | #14 | `20011f2` | `internal/sync` | two-rename swap (`intend→step1_done→step2_done`) + sentinel + startup recovery reconciler + error taxonomy |
| 14 — orphans + adopt | #15 | `5fbfad4` | `internal/sync` | `Orphans` (ledger-diff only), `DeleteOrphans`, `ScanDrift` (`ErrMidLifeDrift`), `Backup`/`ConfirmAdopt`/`AdoptEntries` |
| 15 — report | #16 | `9496e6e` | `internal/report` | per-target summary + top-line outcome, capability report, stable `--output=json` (schema v1) |

Specs added: `docs/spec/tool-owned-merge-v1.md`, `docs/spec/sync-summary-v1.md`, `docs/spec/capability-report-v1.md`. Ops doc: `docs/operations/atomic-swap.md`. Per-unit plans: `docs/plans/2026-06-08-00{1..6}-*.md`.

---

## How the primitives fit (the engine Unit 16 must assemble)

Every data unit shipped **primitives only** and explicitly deferred
wiring. There is **no orchestration engine yet**. `aienvs sync` must, per
enabled target:

1. **Discover** workspace + config + IR. (Workspace = `internal/fsroot`; IR decode = Unit 7 `internal/ir`; config source — see open questions.)
2. **Run the bundled adapter** (Units 9–10) via the Unit 8 runtime to collect ops. ⚠️ The only existing "run adapter → ops" path is the **conformance harness** (`internal/adapter/conformance.Run`); there is no general-purpose runner API. Either extract one or add a thin engine-facing runner.
3. **Reserved-subdirectory ops** → stage the new generation into `<parent>/.aienv-staging/<ts>-<sha>/<leaf>/`, then `internal/sync.Swap` (Unit 13).
4. **Tool-owned ops** (`write_tool_owned`) → `internal/merge.ApplyToFile` (Unit 12a), passing the locator kind + content. Reconcile the marker-ownership contract here (adapters pre-wrap markdown; the merge engine owns markers and rejects a body containing `<!-- aienvs:` — pass the **inner body**). Provenance corroboration (ledger `slice_hash`/`source=`) for user-prose marker collisions is the engine's job at this layer.
5. **Orphans** → `internal/sync.Orphans(oldLedger, newLedger)`, then `DeleteOrphans` **after** the new ledger is durable (Unit 14). Honor `--expect-deletions=N` via `CheckExpectedDeletions`.
6. **Drift guard** → `internal/sync.ScanDrift` before mutating a prefix; refuse with `ErrMidLifeDrift` → point at `--adopt-prefix`.
7. **Ledger** → write the new per-target ledger (`internal/ledger.Write`, Unit 12). Fold each merge's `slice_hash` into entries.
8. **Report** → `internal/report.Summarize` + `BuildCapability`/`WriteCapabilityReport` (Unit 15). Capability report is written **before** swap so it survives a rollback.

Atomic vs best-effort (Unit 15 `Mode`): atomic = all swaps land or all roll back; best-effort = per-target independent. Required-unmet capability fails **both** modes.

---

## Unit 16 work plan (subset: `init` + `sync`)

**Existing scaffolding:** `internal/cli` has `NewTrustCommand` (Unit 6) and `NewAdapterCommand` (Unit 8) using a deps-injection pattern. `spf13/cobra` is a dep; **Fang is not** added yet. `cmd/aienvs/main.go` is a stub that only handles `--version`.

**To build:**
- `internal/cli/root.go` — assemble the cobra root (optionally Fang-wrapped), wire `NewTrustCommand` + `NewAdapterCommand` + new `init`/`sync`; `Execute()` called from `main.go` (replace the stub).
- `internal/cli/access.go` — `NO_COLOR`/`FORCE_COLOR`/TTY detection, ASCII-first status helpers, 80-col reflow. Renders color over the ASCII tokens `internal/report` already emits.
- `internal/cli/nonint.go` — `--non-interactive` fail-fast context propagation.
- `internal/cli/cmd_init.go` — scaffold `.aienv/` (workspace marker, enabled-targets config).
- `internal/cli/cmd_sync.go` — flags (`--output=json`, `--best-effort`, `--expect-deletions`, `--recover`, `--clean-scratch`, `--adopt-prefix[=t]`, `--adopt-prefix-no-backup`, `--diagnose`) + the orchestration engine above. Consider a separate `internal/engine` (or extend `internal/sync`) for the orchestration so the cobra command stays thin.
- `internal/cli/cmd_adopt.go` — adopt subcommand + interactive typed-name prompt (consumes Unit 14 `Backup`/`ConfirmAdopt`/`AdoptEntries`).

**CLI bits other units deferred into Unit 16:**
- Unit 13: legacy `.cursorrules` workspace-walk warning; live Windows Restart-Manager `--diagnose` enumeration (currently a stub hook).
- Unit 14: `--adopt-prefix` / `--adopt-prefix-no-backup` UX (red warning, scope-all-vs-one).
- Unit 15: `--output=json` / `--best-effort` / `NO_COLOR` flag wiring + color rendering.

---

## Invariants & gotchas learned (apply in Unit 16)

- **Fail-closed everywhere.** On any error, write nothing / leave on-disk byte-identical. The merge engines and swap already guarantee this; the engine must preserve it (e.g. delete orphans only after the ledger is durable; write the capability report before the swap).
- **No wall-clock in cores.** Cores take caller-stamped timestamps/SHAs (`Meta`, `generatedAt`) for deterministic tests and reproducible runs. The engine is where you stamp `time.Now()` once.
- **Forward-slash workspace-relative paths** through `fsroot`/`os.Root`; `filepath.FromSlash` only at host-path boundaries (e.g. the flock abs path).
- **`os.Root` renames:** both operands must be relative to one root (Go 1.25 forbids cross-`Root` rename); never hold a per-prefix root across a swap on Windows.
- **`StagedWrite` does not create parents** — `MkdirAll` the parent first (the merge + swap layers already do; the engine must for any new path).
- **CI:** `darwin/amd64` sits `pending` at 0s (runner-queue lag) while every other platform passes — safe to squash-merge through. Windows + linux + darwin/arm64 + lint + CodeRabbit are the real signal.
- **Platform-split tests:** errno/OS-specific behavior needs `//go:build unix` / `//go:build windows` test files — a darwin-only local run will not catch a Windows errno bug (it didn't, CI did).

---

## Verification gate (run before declaring any unit done)

```
go vet ./...
go test -race -cover ./...
golangci-lint run
GOOS=windows GOARCH=amd64 go build ./...   # + go vet for build-tagged files
GOOS=linux   GOARCH=arm64 go build ./...
```
80% coverage floor. For data-loss-touching code, add fuzz targets and run them (`-fuzz Fuzz... -fuzztime 10s`). Then push, open PR, watch `gh pr checks <n> --watch`, merge through the darwin/amd64 lag.

---

## Review posture while the spend limit is active

Multi-agent `ce-code-review`/`ce-doc-review` subagents return `monthly spend limit` and produce nothing. Fallback used for Units 12a–15: **inline self-review** of the highest-risk paths + the full automated gate (which caught two TOML fuzz crashers, a swap `.old`-cleanup reliability bug, a conformance subprocess-race flake, and a Windows-only errno test bug). For Unit 16's orchestration, prefer waiting for the multi-agent review to come back rather than landing it inline — that was this checkpoint's decision.

---

## Open questions to resolve at Unit 16 start

1. **IR + config source:** where does `aienvs sync` read the IR and the enabled-targets list? (Unit 5 config / Unit 7 IR decoder — confirm the on-disk format and `init`'s scaffold output.)
2. **General adapter runner:** extract a reusable "run bundled adapter against IR → ops" API from `conformance.Run`, or add one in the engine layer.
3. **Fang vs bare cobra** for the root (master plan says Cobra/Fang; Fang dep not yet added).
4. **Engine package boundary:** `internal/engine` vs extending `internal/sync` — keep the cobra command thin either way.

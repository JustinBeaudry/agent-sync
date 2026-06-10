# Handoff — Codex adapter + shared-subdir data-loss fix (2026-06-10)

Status snapshot for whoever resumes `agent-sync`. Written after a long session
that hardened the product, raised coverage to the mandated floor, shipped the
third bundled adapter, and fixed a P1 data-loss bug surfaced while building it.

---

## TL;DR

- **`main` is clean and green** at `b8c3b34` (PR #23 merged). Everything below
  #24 is merged.
- **One PR open: #24** (`feat/codex-adapter`) — the **Codex CLI adapter** *plus*
  the **shared-subdir data-loss fix** that makes it safe. `MERGEABLE`, all
  substantive checks green; only `test (darwin/amd64)` (the chronically slow
  macos-13 runner, twin `darwin/arm64` already passed) was pending at handoff.
  All review threads resolved.
- **Immediate next step:** confirm #24's last check is green, then **squash-merge
  #24 and delete the branch** (per the repo's squash-on-merge convention). That
  lands both the codex adapter and the shared-subdir fix together.
- **One dormant residual** (ADV-1) is documented in #24's body — it activates
  only when the `pi` adapter (Unit 11.5) lands. Not a blocker for #24.

---

## What shipped this session

| PR | State | What |
|----|-------|------|
| #22 | merged | Production hardening: Apache-2.0 LICENSE/NOTICE, `docs/threat-model.md`, `.goreleaser.yaml` + `release.yml`, CI coverage gate, `AGENT_SYNC_REQUIRE_GIT` fail-loud E2E guard, honest README adapter table. Coverage 77.2% → 80.2%. Fixed a nil-deref in `adapterkit.ExitError.Error()`. |
| #23 | merged | Deferred review follow-ups: extracted `requireGit`/`mustGit` into a shared `internal/gittest` package; moved the goreleaser minisign block into `scripts/sign-release.sh`. |
| #24 | **open** | **Codex CLI adapter (Unit 11)** + **shared-subdir ownership fix** (the safety gate). See below. |

### PR #24 contents (commits, oldest first)

- `e0f1ab5` — bundled `codex` adapter (`internal/adapter/bundled/codex/`):
  `agents-md`→workspace `AGENTS.md` section; `skill`→`.agents/skills/aienvs-<id>/SKILL.md`;
  `mcp-server-entry`→`.codex/config.toml [mcp_servers.aienvs_<id>]` (toml-path, with
  a JSON→TOML-table-body renderer); `rule`/`command`/`plugin-reference`→honest
  `unsupported`. Registered in `internal/cli/setup.go`. Docs:
  `docs/adapters/codex.md`. Mapping validated against current Codex docs.
- `1a1899f`, `20c00c7` — review fixes on the adapter (TOML HTML-escaping off; reject
  null MCP body). Also fixed a **latent agents-md bug in BOTH codex and cursor**:
  `emitAgentsMD` was sending marker-wrapped content, but the engine owns the
  markdown-section markers — both now send inner body only.
- `a7f3bc6` — **shared-subdir ownership fix** (plan
  `docs/plans/2026-06-09-003-fix-shared-subdir-ownership-plan.md`). New
  `OutputModeSharedSubdir`. The engine computes an `effective` owned-prefix set =
  owned-subdir prefixes + the agent-sync **leaf** dirs of each shared-subdir
  (from this run's ops ∪ prior ledger); the shared parent is never swapped, so
  foreign skills survive. `.agents/skills` (codex) and `.claude/skills` (claude)
  use it. Preserves AGENTS invariants #6 (atomic swap) and #7 (ledger authority).
- `ed0a87e` — adversarial-review fixes on the fix itself: **per-leaf sentinel
  collision (P1)** — leaves sharing a generation dir shared one `.state`, so one
  leaf's swap could orphan another's `.old` and wedge syncs with `ErrStale`. Now
  each leaf gets `.state-<leaf>`; `reconcileGen` scans all per-leaf sentinels;
  the intend-discard is scoped to the leaf. Plus `leafUnder` traversal guard,
  `sharedSubdirs` longest-first sort, schema-parity case, and `leafUnder`/
  `effectiveOwnedPrefixes` unit tests.

---

## Current state

- **Three bundled adapters work end-to-end:** Claude, Cursor, Codex. Verified by
  a real `init --target codex --target cursor && sync` (renders AGENTS.md
  section, exit 0) and by engine survival tests.
- **Coverage: 80.2%**, CI floor enforced at 80.0 (CLAUDE.md mandate).
- **Local gate green:** `go vet`, `AGENT_SYNC_REQUIRE_GIT=1 go test -race ./...`,
  `golangci-lint run` (0 issues), `shellcheck scripts/sign-release.sh`.
- Plans created this session (all `status: completed`):
  `docs/plans/2026-06-09-001-test-coverage-floor-80-plan.md`,
  `-002-feat-codex-adapter-plan.md`, `-003-fix-shared-subdir-ownership-plan.md`.

---

## Next steps (in order)

1. **Merge #24.** Confirm `test (darwin/amd64)` is green (`gh pr checks 24`),
   then `gh pr merge 24 --squash --delete-branch`. Then `git switch main &&
   git pull`. This lands the codex adapter + shared-subdir fix together.
2. **(When picking up the pi adapter — Unit 11.5):** resolve **ADV-1** first. Two
   adapters that both declare the *same* shared parent (`.agents/skills`) are not
   serialized by the per-target lock and use a per-target generation stamp, so
   they could collide on the shared `.aienv-staging` tree. Fix: per-target staging
   namespace under the shared parent, or cross-adapter coordination. Pi reuses
   `.agents/skills`, which is exactly when this activates. (Full detail in #24's
   "Residual Review Findings" and the ce-code-review run artifact.)

---

## Open items / deferred (not blockers for #24)

- **ADV-1** (above) — dormant until pi.
- **Coverage** is at the 80% floor with ~0.2% headroom; raising it (e.g. the
  zero-coverage `cmd/agent-sync` paths, `internal/validate`) is optional polish.
- **Still out of scope** (master-plan Phase F / v1.0.0 gate): Unit 11.5 `pi`
  adapter, Gemini + experimental adapters, the extension-SDK CLI (Unit 20:
  `adapter conformance-test`, `adapter install`, reference out-of-tree adapter),
  and `rollback`/`unmanage` commands. Threat model and release packaging
  (goreleaser) are **done** (#22).

---

## Key facts for the next session

- **Workflow:** Compound Engineering (`ce-plan` → `ce-work` → `ce-code-review`
  → `ce-commit-push-pr`), often via `/lfg`. Per-unit plans live in `docs/plans/`.
- **Adversarial review pays off:** the shared-subdir P1 *and* the per-leaf
  sentinel P1 were both caught by `ce-code-review`, not the local gate. Keep
  running it on data-loss-critical engine changes.
- **The swap/ownership model is the load-bearing, data-loss-critical area.**
  `internal/engine/target.go` (effective-prefix derivation, stage+swap loop) +
  `internal/sync/{staging,swap,recover}.go` (two-rename + per-leaf sentinels).
  AGENTS.md invariants #6 and #7 govern it.
- **CI quirk:** `test (darwin/amd64)` (macos-13) is consistently the slowest
  runner and lags well behind its `darwin/arm64` twin — not a failure signal.

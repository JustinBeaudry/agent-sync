# AGENTS.md — guidance for agents working on aienvs itself

This file is meta: it guides coding agents (human or otherwise) who are
**contributing to the `aienvs` codebase**. It is not consumed by the sync
pipeline and is not rendered into any target tool.

## Authoritative references

Before making non-trivial changes, read — in order:

1. [`docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md`](docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md) — what we decided to build and why.
2. [`docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md`](docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md) — how we decided to build it.
3. This file for the invariants the plan assumes.

If code disagrees with the plan, update the plan **first** in the same PR,
or stop and surface the disagreement. Silent drift from the plan is a bug.

## Invariants

These are non-negotiable; violating them breaks v1:

1. **All writes into a workspace go through `internal/fsroot`.** No direct
   `os.WriteFile`, `os.Create`, or `os.Rename` against workspace paths.
   The safe-filesystem layer is the only legitimate way to write a file
   inside a reserved prefix. (Units 1, 13.)
2. **Adapters never write files directly.** They emit declarative ops
   over the v1 protocol; the CLI core performs the actual writes. This
   centralizes safe-write semantics and enforces declarative-only output.
   (Units 8, 9, 10, 11.)
3. **Non-interactive mode never prompts, never hangs.** TTY detection gates
   interactive UX; the CLI exits with a documented code and the exact flag
   needed to proceed. (Units 6, 16.)
4. **Pinning is default; `floating: true` must be explicit.** `init`
   resolves refs to SHAs and writes them back to the manifest.
   (Units 2, 5.)
5. **Offline-strict with pinned-cached exception.** `sync` fails offline
   unless the manifest is pinned by `commit:` and the SHA is present in
   cache. (Units 4, 5.)
6. **Two-rename atomic swap.** `<prefix>` → `<prefix>.old`;
   `<staging>/<target>` → `<prefix>`; best-effort `RemoveAll <prefix>.old`.
   Both steps share a single parent `os.Root`. Recovery state machine lives
   at the sibling ledger. (Unit 13.)
7. **Ledger is the authority on orphan deletion.** Orphan detection is
   `previous_ledger − current_desired_outputs`, never "any file under the
   prefix we don't currently emit". (Unit 12.)

## Style

- Go 1.25+. Use `os.Root` whenever touching user paths.
- Errors: wrap with `%w`, define sentinel errors at package level for any
  condition a caller is expected to branch on. Don't return bare `fmt.Errorf`
  for conditions callers need to handle programmatically.
- Tests: prefer `testing.T.TempDir` and real filesystem for `fsroot`;
  `spf13/afero` `MemMapFs` is only appropriate above `fsroot` (it doesn't
  emulate `os.Root` semantics).
- Subprocess: `os/exec` + explicit `CommandContext`. No background shells,
  no interactive SSH prompts, no credential prompts (use `GIT_TERMINAL_PROMPT=0`).
- Concurrency: all long-running work takes a `context.Context`. Cancelling
  a sync must not leave half-written files in a reserved prefix; the
  recovery state machine guarantees this.
- Logging: structured via stderr only. `log/slog` with JSON handler for
  non-interactive, text handler for TTY.

## Pull-request checklist

- [ ] `golangci-lint run` clean.
- [ ] `go test -race ./...` clean on your host.
- [ ] If you changed public behavior: updated docs/ and CHANGELOG (once
      introduced).
- [ ] If you changed the protocol or adapter contract: bumped the version
      per the compatibility policy in unit 8.
- [ ] If you changed the plan's invariants: updated the plan first.

# CLAUDE.md

Claude Code–specific overlay for contributors working on `agent-sync`.

Primary guidance is in [`AGENTS.md`](AGENTS.md). Read it first. This file
adds Claude-Code-specific expectations only.

## When editing `agent-sync` source from Claude Code

- Respect the invariants in `AGENTS.md`. They are load-bearing.
- Prefer small, reviewable commits scoped to one unit of the plan
  (`docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md`).
- Always run `go test -race ./...` and `golangci-lint run` before
  declaring work done.
- If a decision must diverge from the plan, **update the plan first** in
  the same commit.

## Do not

- Do not add background daemons or long-lived processes.
- Do not introduce dependencies that require CGo unless explicitly
  justified in a plan update.
- Do not reach outside `internal/fsroot` to touch user paths — the
  safe-filesystem layer is the single enforcement point.

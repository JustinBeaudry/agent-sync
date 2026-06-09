# agent-sync

`agent-sync` is a Go CLI that keeps AI-agent configuration for multiple tools
(Claude Code, Cursor, Codex CLI — and more to come) in sync from a single
Git-backed manifest. Think of it as `dotfiles` for agents: one canonical
source of skills, rules, commands, and MCP servers; native files rendered
into each tool's own conventions.

> **Status:** pre-alpha. v1 is in active development. Architecture and
> rationale live in:
>
> - Requirements: [`docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md`](docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md)
> - Implementation plan: [`docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md`](docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md)
>
> Module path `github.com/agent-sync/agent-sync` is provisional until the canonical
> hosting location is chosen.

## What agent-sync does

1. **One manifest per workspace.** A `.aienv.yaml` pins the workspace to a
   canonical Git repo (pinned by commit SHA by default) and declares which
   target tools receive configuration.
2. **Explicit `sync`.** `agent-sync sync` materializes the pinned content,
   compiles it via per-tool adapters, stages the output, and atomically
   swaps it into each tool's reserved subdirectory (e.g.
   `.claude/rules/aienvs/`, `.cursor/rules/aienvs/`, `.codex/skills/aienvs-<id>/`).
3. **Safe by default.** Pinning, offline-strict, TOFU on `(URL, SHA)`
   pairs, reserved-prefix ownership with a ledger, and atomic swap with
   rollback — designed to fail closed.
4. **Capability-honest translation.** A tool-agnostic IR (`agents-md`,
   `rule`, `skill`, `command`, `plugin-reference`, `mcp-server-entry`) is
   translated by each adapter; per-target capability reports make
   lossy translations visible.

## Supported tools

Adapters translate the tool-agnostic IR into each tool's native files. Two
adapters ship bundled today; the rest are planned and tracked against the v1
roadmap. The `Planned (tier)` label records the intended support tier once the
adapter lands.

| Tool | Status |
|------|--------|
| Claude Code | ✅ Bundled |
| Cursor | ✅ Bundled |
| Codex CLI | Planned (primary) |
| Pi (`@mariozechner/pi-coding-agent`) | Planned (primary) |
| Gemini CLI | Planned (supported) |
| Windsurf | Planned (experimental) |
| LM Studio | Planned (experimental) |

## Requirements

- Go 1.25+
- Git (shelled out for network operations)

## Build

```bash
go build ./cmd/agent-sync
```

## Test

```bash
go test -race ./...
golangci-lint run
```

## Project layout

See [`docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md`](docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md)
for the complete repo layout. The short version:

```
cmd/agent-sync/    CLI entry point
internal/          All implementation packages; no external API yet
docs/              Requirements, plans, specs
```

## Contributing

See [`AGENTS.md`](AGENTS.md) and [`CLAUDE.md`](CLAUDE.md) for guidance when
working on `agent-sync` itself (including from inside a coding agent). These
files are **in-repo agent guidance for contributors**, not output of the
sync pipeline.

## License

Licensed under the [Apache License 2.0](LICENSE). See [`NOTICE`](NOTICE) for
attribution requirements.

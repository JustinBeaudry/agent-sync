# agent-sync

[![Release](https://img.shields.io/github/v/release/JustinBeaudry/agent-sync?label=release)](https://github.com/JustinBeaudry/agent-sync/releases/latest)

`agent-sync` is a Go CLI that keeps AI-agent configuration for multiple tools
(Claude Code, Cursor, Codex CLI — and more to come) in sync from a single
Git-backed manifest. Think of it as `dotfiles` for agents: one canonical
source of skills, rules, commands, and MCP servers; native files rendered
into each tool's own conventions.

> **Status:** pre-alpha. v1 is in active development. Architecture and
> rationale live in the [requirements](docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md)
> and [implementation plan](docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md).

## Install

Download the prebuilt binary for your platform, verify its checksum, and put it
on your PATH — one command:

```bash
curl -fsSL https://raw.githubusercontent.com/JustinBeaudry/agent-sync/main/install.sh | sh
```

Installs to `/usr/local/bin` (falling back to `~/.local/bin` if that isn't
writable). Pin a version or change the location with env vars:

```bash
curl -fsSL https://raw.githubusercontent.com/JustinBeaudry/agent-sync/main/install.sh \
  | AGENT_SYNC_VERSION=v0.1.0 AGENT_SYNC_INSTALL_DIR="$HOME/bin" sh
```

Prefer not to pipe to a shell? Download the archive for your OS/arch from the
[Releases page](https://github.com/JustinBeaudry/agent-sync/releases), verify it
against `checksums.txt`, extract the `agent-sync` binary, and move it onto your
PATH. (Windows: grab the `.zip`.) Git must be on your PATH at runtime.

## Quickstart

New to agent-sync? [`docs/quickstart.md`](docs/quickstart.md) walks through
authoring a canonical repo and running your first `init → sync → validate`,
with a copyable example under [`examples/canonical/`](examples/canonical/).

## What agent-sync does

1. **Manifests form a hierarchy.** A `.agent-sync.yaml` binds a directory to a
   canonical source and declares which target tools receive configuration. The
   source is one of: a remote Git repo (`url`) or a local clone (`local_path`),
   both pinned by commit SHA; or an in-repo working-tree directory (`local_dir`,
   e.g. `.agents`) for per-repo skills authored right in the repo — unpinned,
   and exempt from trust and offline-strict since there's nothing remote to fetch.
   `sync` walks up from the current directory (project root = nearest `.git`
   ancestor) and emits every manifest it finds, each to its own scope; the
   user-level manifest at `~` is emitted only with `sync --user`. agent-sync
   never merges across levels — each target tool resolves precedence via its own
   native config hierarchy, and `sync` warns when a scope emits a kind a tool
   won't read natively at that level.
2. **Explicit `sync`.** `agent-sync sync` materializes the pinned content,
   compiles it via per-tool adapters, stages the output, and atomically
   swaps it into each tool's reserved subdirectory (e.g.
   `.claude/rules/agent-sync/`, `.cursor/rules/agent-sync/`, `.codex/skills/agent-sync-<id>/`).
   Emitted skills carry a real `name` / `description` frontmatter block so
   they show up correctly in each tool's skill list, and every managed file
   records its source (`<url>@<short-sha>`) in its header.
3. **Moving the pin: `update`.** `agent-sync update` fetches the canonical
   remote, shows what changed since the current pin, and — on confirmation
   (or `--accept-update=<sha>` when non-interactive) — re-pins
   `commit` + `trusted_sha` and re-syncs in one locked step. It is
   fast-forward-only: a rewritten upstream history is refused unless you
   pass `--accept-rewritten-history=<sha>`. Use `--user` to update the
   `~` manifest.
4. **Safe by default.** Pinning, offline-strict, TOFU on `(URL, SHA)`
   pairs, reserved-prefix ownership with a ledger, and atomic swap with
   rollback — designed to fail closed.
5. **Capability-honest translation.** A tool-agnostic IR (`agents-md`,
   `rule`, `skill`, `command`, `plugin-reference`, `mcp-server-entry`) is
   translated by each adapter; per-target capability reports make
   lossy translations visible.

## Supported tools

Adapters translate the tool-agnostic IR into each tool's native files. Five
adapters ship bundled today; the rest are planned, and the `Planned (tier)`
label records the support tier each will land at.

| Tool | Status |
|------|--------|
| Claude Code | ✅ Bundled |
| Cursor | ✅ Bundled |
| Codex CLI | ✅ Bundled |
| Pi (`@mariozechner/pi-coding-agent`) | ✅ Bundled (agents-md; skill & command planned) |
| Antigravity (2.0 IDE + CLI; replaces Gemini CLI, retired 2026-06-18) | ✅ Bundled (full parity) |
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

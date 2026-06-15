# Quickstart

This guide takes you from nothing to a synced workspace in a few minutes. It
covers authoring a **canonical repo**, wiring a workspace to it, and running
`sync` / `validate`.

A copyable example canonical repo lives in
[`examples/canonical/`](../examples/canonical/). The full, frozen format
contract is [`docs/spec/ir-v1.md`](spec/ir-v1.md) — this page stays at the
"what you need to get going" level.

---

## 1. Author a canonical repo

The canonical repo is a normal Git repository holding your shared agent
configuration. agent-sync reads a closed set of **six concept kinds**, each
living at a fixed path:

```
canonical-repo/
├── AGENTS.md                 # project guidance (agents-md)
├── rules/<id>.md             # one rule per file
├── skills/<id>/SKILL.md      # one skill per directory
├── commands/<id>.md          # one command per file
├── mcp/<server>.json         # one MCP server entry per file
└── plugins/<id>.toml         # one plugin reference per file
```

A node's **id comes from its filename or directory name** (`rules/foo.md` →
id `foo`; `skills/code-review/SKILL.md` → id `code-review`). Ids must match
`[a-z0-9][a-z0-9-_]{0,63}`.

### Frontmatter — the one thing that trips people up

Markdown-bearing files (`AGENTS.md`, rules, skills, commands) accept an
**optional** YAML frontmatter block. Only three fields are recognized:

```markdown
---
required: true          # fail the sync if a target can't support this node
targets: [claude, cursor]  # restrict to these adapters (omit = all targets)
version: 2              # author-controlled counter
---

The body goes here.
```

> **Gotcha:** Do **not** put native tool frontmatter like `name:` or
> `description:` in a canonical `SKILL.md`. The decoder rejects unknown
> frontmatter fields with a hard error. The skill's name comes from its
> directory; each adapter generates the tool-native frontmatter on emit.
> Unknown fields prefixed `x-` are tolerated (and ignored) for forward-compat.

---

## 2. Initialize a workspace

From the directory you want managed, point a workspace at your canonical repo.
Pinning to a commit SHA is the default (and required for a local path):

```bash
# Remote canonical repo (resolves the ref to a SHA for you):
agent-sync init --source https://github.com/your-org/agent-config --ref main \
  --target claude --target cursor --target codex

# Or a local canonical repo, pinned to a specific commit:
agent-sync init --local-path ../agent-config --commit <40-char-sha> \
  --target claude --target cursor
```

This writes a `.agent-sync.yaml` manifest with the pinned `commit` and a
`trusted_sha`. On a terminal, `agent-sync init` with no flags runs an
interactive wizard.

---

## 3. Sync

```bash
agent-sync sync
```

This materializes the pinned content, compiles it through each target's
adapter, and writes native files:

- **Reserved subdirectories** (agent-sync owns the whole directory):
  `.claude/rules/agent-sync/`, `.cursor/rules/agent-sync/`,
  `.claude/skills/agent-sync-<id>/`, `.codex/skills/agent-sync-<id>/`.
- **Tool-owned files** (agent-sync owns only a marked section/entry, your
  content is preserved): `CLAUDE.md`, `AGENTS.md`, `.mcp.json`,
  `.cursor/mcp.json`, `.codex/config.toml`.

In tool-owned files, agent-sync content lives between
`<!-- agent-sync:begin id=... -->` / `<!-- agent-sync:end id=... -->` markers
(markdown) or under `agentsync_<id>` keys (JSON/TOML). Edit anything outside
those — the next sync leaves it untouched.

---

## 4. Validate

`validate` is a read-only dry run — it reports drift without writing. It's the
right thing to run in a git hook or CI to answer "is this workspace in sync?":

```bash
agent-sync validate            # exit 0 = no drift, exit 1 = drift detected
agent-sync validate --output=json
```

Immediately after a clean `sync`, `validate` reports **no drift**. If you (or
a teammate) hand-edit inside an agent-sync-managed section, `validate` flags it
so the next `sync` re-rendering it is never a surprise.

---

## Backing out

A first-class `agent-sync unmanage <target>` command is planned but not yet
shipped. To remove agent-sync-managed content by hand in the meantime:

1. Delete the target's reserved subdirectories
   (e.g. `.claude/rules/agent-sync/`, `.claude/skills/agent-sync-*/`).
2. In tool-owned files, delete the section between the
   `<!-- agent-sync:begin id=... -->` and `<!-- agent-sync:end id=... -->`
   markers (or the `agentsync_<id>` keys in JSON/TOML). Leave the rest.
3. Remove the `.agent-sync/` state directory and `.agent-sync.yaml`.

---

## Try the example

```bash
# From a scratch workspace directory:
agent-sync init --local-path /path/to/agent-sync/examples/canonical \
  --commit <sha-of-that-repo> --target claude --target cursor --target codex
agent-sync sync
agent-sync validate    # → no drift
```

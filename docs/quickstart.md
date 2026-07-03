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
**optional** YAML frontmatter block. Four fields are recognized:

```markdown
---
description: Reviews a diff for correctness and style   # skill summary (see below)
required: true          # fail the sync if a target can't support this node
targets: [claude, cursor]  # restrict to these adapters (omit = all targets)
version: 2              # author-controlled counter
---

The body goes here.
```

> **Describe your skills.** Set `description:` in a canonical
> `skills/<id>/SKILL.md` — it becomes the emitted skill's frontmatter
> description, which is what Claude Code and other Agent-Skills consumers show
> in their skill list. A skill with no `description:` still syncs, but it emits
> a placeholder description and a warning. The skill's `name` still comes from
> its directory id; the adapter generates it. Requires agent-sync ≥ 0.5.
>
> **Gotcha:** other native tool frontmatter (`name:`, `allowed-tools:`, …) is
> still rejected with a hard error — only the four fields above are recognized.
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

# Or per-repo skills authored right in this repo (no git, no pin):
agent-sync init --local-dir .agents --target claude --target codex
```

The `url`/`local_path` forms write a `.agent-sync.yaml` with a pinned `commit`
and `trusted_sha`. The `--local-dir` form points the workspace at an in-repo
directory (here `.agents`) read straight from the working tree: no commit, no
trust prompt, and it works offline — author skills under `.agents/skills/<id>/`,
rules under `.agents/rules/`, and so on, then sync. On a terminal, `agent-sync
init` with no flags runs an interactive wizard.

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

## 5. Update — pull in upstream changes

When your canonical source has new commits, `update` moves the pin forward for
you instead of hand-editing `commit:` and `trusted_sha:`:

```bash
agent-sync update              # nearest workspace
agent-sync update --user       # the ~ manifest
```

It fetches the source, shows the old→new SHAs and the commits in between, then
asks before re-pinning and re-syncing. Non-interactively, name the SHA you're
accepting:

```bash
agent-sync update --non-interactive --accept-update=<new-sha>
```

`update` is **fast-forward-only**: if upstream history was rewritten (the
current pin is no longer an ancestor of the new commit) it refuses, and moving
the pin anyway takes the deliberate `--accept-rewritten-history=<new-sha>`.
Nothing is written until you accept — every refusal leaves the manifest
byte-identical. A `local_dir` source has nothing to pin, and `--offline`
declines rather than guessing.

---

## Composing user rules into projects (Cursor)

Cursor has **no user-global rules file** — its "User Rules" live in Cursor's
settings/cloud, not on disk. So a rule you author at the user scope
(`~/.agent-sync.yaml`) can't take effect globally the way `~/.claude/CLAUDE.md`
does for Claude. Cursor's own recommended pattern is to put the rule in each
project's `.cursor/rules/`. agent-sync automates that with **composition**:
opt a project in and its sync folds your user-scope Cursor rules into the
project's `.cursor/rules/agent-sync/` alongside the project's own rules.

Turn it on in the **project** manifest (`.agent-sync.yaml`):

```yaml
version: 1
canonical:
  local_dir: .agents
targets: [cursor]
compose:
  cursor-rules-from-user: true   # opt-in; default off
```

With this set, `agent-sync sync` in the project also emits your user-scope
Cursor rules into the project. On an id collision, the **project rule wins**
(matching Cursor's Team > Project > User precedence) and the shadowed user rule
is logged so the drop is never silent.

**Three things to know before you commit:**

1. **Composed files are per-developer, per-machine.** Their content is *your*
   `~/.agent-sync` rules. If you commit `.cursor/rules/agent-sync/*.mdc`, you
   push your personal global rules onto teammates and cause churn when another
   machine re-syncs. Treat the composed output as local-only —
   **gitignore it** (e.g. add `/.cursor/rules/agent-sync/` to `.gitignore`)
   unless you deliberately want those rules shared.
2. **Opting out needs one more sync.** Composed rules are reclaimed on the
   *next* sync after you set the flag back to `false` (or drop the user rule) —
   agent-sync removes what its ledger owns. If you opt out and never re-sync,
   the files linger (and committed ones persist regardless).
3. **`--workspace` does not compose.** An explicit
   `agent-sync sync --workspace <dir>` runs a single scope and skips
   composition; composition only fires on the normal hierarchy-discovery path.

Composition is currently **Cursor `rule` only**. Other tools read their
user-global configs directly (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`), so
there is no gap to compose around.

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

`examples/canonical/` ships as plain files inside the agent-sync repo, not as a
standalone repo. A local canonical source is resolved by commit SHA via
`git.Open` (which opens the directory you point at — it does not walk up to a
parent repo), so copy the example into its own git repo first:

```bash
# Seed a standalone canonical repo from the bundled example:
cp -r /path/to/agent-sync/examples/canonical /tmp/agent-config
cd /tmp/agent-config && git init -q && git add -A && git commit -q -m "seed"
SHA=$(git rev-parse HEAD)

# Then, from a scratch workspace directory elsewhere:
agent-sync init --local-path /tmp/agent-config --commit "$SHA" \
  --target claude --target cursor --target codex
agent-sync sync
agent-sync validate    # → no drift
```

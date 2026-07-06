# Agent Harness Hierarchy Design

## Goal

Make agent-sync the canonical manager for agent harnesses across user,
workspace, and project scopes without becoming a generic dotfiles copier or
inventing a parallel configuration language for every supported tool.

Agent-sync should manage both:

- portable agent assets that can be translated across tools; and
- tool-native runtime configuration fragments that stay native but are merged,
  inherited, validated, ledgered, and explained safely.

## Product Model

Agent-sync has three scope roots:

| Scope | Root | Created by | Purpose |
|---|---|---|---|
| `user` | `~` | `agent-sync init --user` | Personal defaults outside activation roots. |
| `workspace` | arbitrary directory | `agent-sync init --workspace <dir>` | A shared or personal workspace harness boundary. |
| `project` | repo/project directory | normal `init` / cwd discovery | Project-specific team harness. |

A workspace can declare itself as an activation root:

```yaml
version: 1
scope: workspace
activation_root: true
canonical:
  local_dir: .agents
```

An activation root is a hard stop. When cwd is inside that tree, the user root
is not considered.

Examples:

```text
cwd: ~/src/small-repo
active hierarchy: user -> project

cwd: ~/ActualReality
active hierarchy: workspace

cwd: ~/ActualReality/apps/api
active hierarchy: workspace -> project
```

Nested activation roots are invalid. `init` and `validate` should reject them;
discovery should fail closed and name the conflicting manifests if a broken
tree already contains more than one.

This preserves the existing no-cross-workspace-sync invariant. A sync invocation
has one write target. Ancestor scopes are read-only inputs to the current
target's output plan; they are not synced or mutated.

## Write Model

The write target is selected by the command:

| Command | Writes under | Reads as inputs |
|---|---|---|
| `agent-sync sync --user` | `~` | user scope only |
| `agent-sync sync --workspace ~/ActualReality` | `~/ActualReality` | workspace scope only if activation root; otherwise user -> workspace |
| `agent-sync sync` in a repo outside activation roots | project root | user -> project |
| `agent-sync sync` in a repo inside an activation root | project root | workspace -> project |

Ancestor read failures should not unexpectedly change the failure model of the
current target. During descendant sync, ancestor reads are cache/local-only by
default. If an ancestor source cannot be read without network fetch, TOFU prompt,
or other interactive work, sync continues with a warning unless the descendant
explicitly marks the inherited item as required.

## Resolution Rule

The effective harness is built broadest to closest, within the active hierarchy.

For portable assets:

```text
identity = kind + id
```

For managed native fragments:

```text
identity = target + output path + merge locator
```

Closest scope wins for the same identity. Different identities aggregate.

Example:

```text
workspace:
  skill/code-review
  fragment/codex/.codex/hooks.json#/hooks/PreToolUse

project:
  skill/code-review
  rule/no-friday-deploys
```

Effective project harness:

```text
skill/code-review                              from project
rule/no-friday-deploys                        from project
fragment/codex/.codex/hooks.json#/hooks/...   from workspace
```

The project skill replaces the workspace skill because it has the same
`kind + id`. The hook remains because no closer fragment owns the same target
path and locator.

## Authoring Strategy

Agent-sync should not model every option in Codex, Claude, Cursor,
Antigravity, or Pi. Tool-specific runtime config changes too quickly for a
parallel agent-sync schema to remain accurate.

Use two layers instead.

### Portable Assets

Keep the existing IR-backed portable concepts:

```text
AGENTS.md
CLAUDE.md
GEMINI.md
skills/<id>/SKILL.md
rules/<id>.md
commands/<id>.md
mcp/<id>.json
plugins/<id>.toml
```

These are content assets. Adapters translate them into each target tool's
native layout and report unsupported mappings honestly.

Portable assets can gain optional agent-sync metadata over time, but the
payload remains the authored asset body. Metadata controls management behavior,
not the target tool's native semantics.

### Managed Native Fragments

Runtime harness configuration stays native. Authors write native TOML, JSON, or
other target config payloads and wrap them in minimal management metadata.

Canonical source layout:

```text
.agents/
  configs/
    codex/
      hooks/pre-tool-policy/
        fragment.yaml
        payload.json
      features/hooks/
        fragment.yaml
        payload.toml
    cursor/
      mcp/context7/
        fragment.yaml
        payload.json
```

`fragment.yaml` describes ownership and merge behavior:

```yaml
id: pre-tool-policy
target: codex
path: .codex/hooks.json
merge: json-pointer
locator: /hooks/PreToolUse
visibility: team
inheritance: descendants
safety: executable
payload: payload.json
```

The payload is target-native. For Codex hooks, that means Codex-shaped
`hooks.json` content. For Codex feature flags, that means TOML using Codex's
own keys. Agent-sync does not reinterpret the payload beyond the allowlisted
merge strategy and validity checks required to avoid corrupting user files.

Native fragments are not arbitrary file copies. Every adapter must explicitly
allowlist:

- target output path;
- merge strategy;
- locator shape;
- whether the surface may be inherited;
- whether the surface may be materialized into descendant outputs.

Whole-file replacement of user-owned tool config is out of scope for this
feature.

## Metadata

Both portable assets and native fragments use the same management vocabulary.

### Visibility

`visibility` answers whether the item is safe to share.

| Value | Meaning |
|---|---|
| `personal` | Personal preference or private workflow. Must not be materialized into project-controlled outputs by default. |
| `team` | Safe to share with the workspace or project team. May flow into descendant outputs when inheritance permits. |
| `machine-local` | Depends on local paths, credentials, or machine state. Never materialized into descendant project outputs. |

### Inheritance

`inheritance` answers whether the item can flow into descendant output plans.

| Value | Meaning |
|---|---|
| `root-only` | Apply only when syncing the scope where the item is authored. |
| `descendants` | May be considered by descendant scopes in the active hierarchy. |

Default inheritance should be conservative:

| Authored scope | Default visibility | Default inheritance |
|---|---|---|
| user | `personal` | `root-only` |
| workspace activation root | `team` | `descendants` |
| workspace without activation root | `team` | `descendants` |
| project | `team` | `root-only` |
| any `machine-local` item | `machine-local` | `root-only` |

### Safety

`safety` answers what kind of risk the item introduces.

| Value | Meaning |
|---|---|
| `passive` | Static instructions or preferences. |
| `tool-access` | Enables tools, MCP servers, plugins, external APIs, or data access. |
| `executable` | Can run local commands or lifecycle hooks. |

`tool-access` and `executable` items need explicit reporting and trust handling.
Non-interactive sync should not silently introduce new executable-triggering
config into a descendant scope without a previously recorded trust decision or
an explicit flag.

## Adapter Capability Reporting

Adapters must report how each harness surface is handled. The key question is
not only "supported or unsupported" but "how does inheritance become visible to
the target tool?"

| Mode | Meaning |
|---|---|
| `native-layered` | The target tool natively reads the item from ancestor/user/workspace config. No descendant materialization needed. |
| `materialized` | Agent-sync must write inherited content into the current target's output because the tool lacks native layering for that surface. |
| `unsupported` | The target tool has no equivalent surface, or the surface is deliberately absent. |
| `manual` | The item requires user action outside agent-sync, such as installing a plugin through a tool UI. |

Reports must show unsupported and manual items even when sync succeeds.
Capability honesty is more important than file presence.

## Explainability

Hierarchy makes debugging harder, so explainability is part of the product, not
a nice-to-have.

`validate --explain` and `status --hierarchy` should answer:

- active root;
- whether user root was included or ignored;
- hierarchy order;
- each effective item and its source scope;
- overridden items and the closer item that won;
- materialized outputs;
- native-layered outputs;
- unsupported/manual items;
- safety warnings;
- ancestor read warnings.

Example output:

```text
active root: /Users/justin/ActualReality
user root: ignored (activation root stopped discovery)
hierarchy: workspace -> project

skill/code-review
  source: project (.agents/skills/code-review/SKILL.md)
  overrides: workspace skill/code-review
  outputs: .agents/skills/agent-sync-code-review/SKILL.md

codex hook pre-tool-policy
  source: workspace (.agents/configs/codex/hooks/pre-tool-policy)
  safety: executable
  mode: native-layered
  output: .codex/hooks.json /hooks/PreToolUse
```

## Codex-Specific Grounding

Current Codex docs make lifecycle hooks and feature flags first-class config
surfaces:

- `[features].hooks` is a stable feature key and currently defaults to true.
- Lifecycle hooks can be configured in `hooks.json` or inline `[hooks]` tables.
- Project-local `.codex/` layers load only for trusted projects.
- Non-managed command hooks must still be reviewed and trusted by Codex.
- Plugins can also bundle lifecycle hooks.

Agent-sync should therefore manage Codex hook declarations as Codex-native
fragments, not as a portable `hook` IR kind in the first implementation.
Agent-sync must not bypass Codex's hook trust review, and it should not
silently install executable scripts as part of native fragment sync.

Codex sources used while shaping this design:

- https://developers.openai.com/codex/config-basic#feature-flags
- https://developers.openai.com/codex/hooks
- https://developers.openai.com/codex/plugins
- https://developers.openai.com/codex/mcp

## Non-Goals

- Do not create a broad agent-sync DSL for every target tool setting.
- Do not copy arbitrary native config files wholesale.
- Do not materialize personal or machine-local items into project outputs by
  default.
- Do not bypass target-tool trust flows for hooks, plugins, or executable
  command surfaces.
- Do not sync or mutate ancestor scopes during descendant sync.
- Do not support nested activation roots in the first implementation.

## Open Design Decisions For Implementation Planning

These decisions should be resolved in the implementation plan, not by changing
the product model above:

1. Whether native fragment metadata is parsed into the existing IR package or a
   new harness package that feeds the engine alongside IR nodes.
2. The exact CLI spelling for explaining hierarchy, likely
   `agent-sync validate --explain` and `agent-sync status --hierarchy`.
3. The first adapter surfaces to implement. Codex `config.toml` features and
   `hooks.json` are the best first slice because the current docs clearly
   define them and the existing TOML/JSON merge primitives are close.
4. How trust records for inherited `tool-access` and `executable` fragments are
   represented in the existing trust store.

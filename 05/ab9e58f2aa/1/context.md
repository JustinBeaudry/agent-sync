# Session Context

## User Prompts

### Prompt 1

sync --user is a dead end for Claude globally — agent-sync's Claude adapter writes ~/CLAUDE.md + ~/.mcp.json at user scope, but Claude reads ~/.claude/CLAUDE.md + ~/.claude.json. Since you own agent-sync, that's a real adapter gap worth a future fix (make user-scope target ~/.claude/). Until then, use it at project scope, where ./CLAUDE.md/./.mcp.json are read.

### Prompt 2

sure

### Prompt 3

yes

### Prompt 4

yes

### Prompt 5

Base directory for this skill: /Users/justinbeaudry/.claude/plugins/cache/compound-engineering-plugin/compound-engineering/3.9.3/skills/ce-doc-review

# Document Review

Review requirements or plan documents through multi-persona analysis. Dispatches specialized reviewer agents in parallel, auto-applies `safe_auto` fixes, and routes remaining findings through a four-option interaction (per-finding walk-through, auto-resolve with best judgment, Append-to-Open-Questions, Report-only) for user d...


# Session Context

## User Prompts

### Prompt 1

Base directory for this skill: /Users/justinbeaudry/.claude/plugins/cache/compound-engineering-plugin/compound-engineering/3.9.3/skills/lfg

CRITICAL: You MUST execute every step below IN ORDER. Do NOT skip any required step. Do NOT jump ahead to coding or implementation. The plan phase (step 1) MUST be completed and verified BEFORE any work begins. Violating this order produces bad output.

When invoking any skill referenced below, resolve its name against the available-skills list the host ...

### Prompt 2

Base directory for this skill: /Users/justinbeaudry/.claude/plugins/cache/compound-engineering-plugin/compound-engineering/3.9.3/skills/ce-plan

# Create Technical Plan

**Note: The current year is 2026.** Use this when dating plans and searching for recent documentation.

`ce-brainstorm` defines **WHAT** to build. `ce-plan` defines **HOW** to build it. `ce-work` executes the plan. A prior brainstorm is useful context but never required — `ce-plan` works from any input: a requirements doc, a ...

### Prompt 3

[Request interrupted by user for tool use]

### Prompt 4

continue

### Prompt 5

Base directory for this skill: /Users/justinbeaudry/.claude/plugins/cache/compound-engineering-plugin/compound-engineering/3.9.3/skills/ce-doc-review

# Document Review

Review requirements or plan documents through multi-persona analysis. Dispatches specialized reviewer agents in parallel, auto-applies `safe_auto` fixes, and routes remaining findings through a four-option interaction (per-finding walk-through, auto-resolve with best judgment, Append-to-Open-Questions, Report-only) for user d...

### Prompt 6

Base directory for this skill: /Users/justinbeaudry/.claude/plugins/cache/compound-engineering-plugin/compound-engineering/3.9.3/skills/ce-work

# Work Execution Command

Execute work efficiently while maintaining quality and finishing features.

## Introduction

This command takes a work document (plan or specification) or a bare prompt describing the work, and executes it systematically. The focus is on **shipping complete features** by understanding requirements quickly, following existing...

### Prompt 7

Base directory for this skill: /Users/justinbeaudry/.claude/plugins/cache/compound-engineering-plugin/compound-engineering/3.9.3/skills/ce-code-review

# Code Review

Reviews code changes using dynamically selected reviewer personas. Spawns parallel sub-agents that return structured JSON, then merges and deduplicates findings into a single report.

## When to Use

- Before creating a PR
- After completing a task during iterative implementation
- When feedback is needed on any code changes
- C...

### Prompt 8

Base directory for this skill: /Users/justinbeaudry/.claude/plugins/cache/compound-engineering-plugin/compound-engineering/3.9.3/skills/ce-test-browser

# Browser Test Skill

Run end-to-end browser tests on pages affected by a PR or branch changes using the `agent-browser` CLI.

## Use `agent-browser` Only For Browser Automation

This workflow uses the `agent-browser` CLI exclusively. Do not use any alternative browser automation system, browser MCP integration, or built-in browser-control to...

### Prompt 9

Base directory for this skill: /Users/justinbeaudry/.claude/plugins/cache/compound-engineering-plugin/compound-engineering/3.9.3/skills/ce-commit-push-pr

# Git Commit, Push, and PR

**Asking the user:** When this skill says "ask the user", use the platform's blocking question tool: `AskUserQuestion` in Claude Code (call `ToolSearch` with `select:AskUserQuestion` first if its schema isn't loaded), `request_user_input` in Codex, `ask_user` in Gemini, `ask_user` in Pi (requires the `pi-ask-user...


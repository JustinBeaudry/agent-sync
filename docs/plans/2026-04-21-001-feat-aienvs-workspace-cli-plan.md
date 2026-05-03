---
title: "feat: aienvs agent-workspace CLI v1"
type: feat
status: active
date: 2026-04-21
deepened: 2026-04-21
origin: docs/brainstorms/2026-04-21-aienvs-agent-workspace-requirements.md
---

# feat: aienvs agent-workspace CLI v1

## Overview

Build the v1 of `aienvs` — a Go CLI that keeps AI-agent configuration for multiple tools (v1 primary: Claude, Cursor, Codex) in sync from a single Git-backed manifest (`.aienv.yaml`). This plan turns the approved requirements doc into concrete, dependency-ordered implementation units for a greenfield repository.

The architectural essence is a four-stage pipeline — **materialize → compile → stage → swap** — with a **two-mode ownership model** (reserved-subdirectory ownership where the target tool supports it + per-entry ledgered merges into tool-owned files where it doesn't), ledger-driven orphan deletion in both modes, a **two-tier trust model** (committed `trusted_sha:` in `.aienv.yaml` for CI plus per-user `trust.jsonl` TOFU history for interactive work), and a v1-stable adapter extension contract — **LSP-framed JSON-RPC 2.0 over stdio** with initialize/initialized lifecycle, capabilities, declared outputs, cancel/shutdown, progress tokens, per-op timeouts, structured errors, and a magic-cookie handshake — so the four primary adapters (claude, cursor, codex, pi) and any out-of-tree third-party adapter (Go, Python, or otherwise) speak the same protocol from day one.
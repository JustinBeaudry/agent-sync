// Package merge provides the tool-owned-file merge engines that the
// write_tool_owned op depends on: surgical insert/update/remove of
// aienvs-managed entries inside user-owned files (.mcp.json,
// .cursor/mcp.json, .codex/config.toml, AGENTS.md, CLAUDE.md) without
// corrupting user-authored content.
//
// This is the highest data-loss surface in aienvs, so the design is
// fail-closed: on any parse error or ambiguous marker state, NO bytes
// are written and the file is left byte-identical. The three format
// engines (JSON via tidwall/sjson, TOML via a string-aware line-splice,
// markdown via a marker parser) are pure functions; ApplyToFile is the
// only filesystem-touching entry point and holds the Unit 12 per-file
// flock across the read-merge-write.
//
// Authoritative contract: docs/spec/tool-owned-merge-v1.md.
package merge

import "errors"

// Sentinel errors. Callers branch with errors.Is. Both are fail-closed:
// when either is returned, no write was attempted.
var (
	// ErrMalformedToolOwnedFile is returned when a JSON/TOML target does
	// not parse, has a non-object parent under the pointer, or already
	// contains a duplicate aienvs entry/table (ledger drift). aienvs
	// never auto-fixes a file it cannot parse.
	ErrMalformedToolOwnedFile = errors.New("merge: malformed tool-owned file")

	// ErrMalformedManagedSection is returned for a markdown marker-state
	// failure (begin without end, end without begin, nested begin,
	// duplicate id, or an uncorroborated/indented section colliding with
	// a managed id). The message names the offending line.
	ErrMalformedManagedSection = errors.New("merge: malformed aienvs managed section")
)

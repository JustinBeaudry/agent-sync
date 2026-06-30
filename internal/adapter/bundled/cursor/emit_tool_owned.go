package cursor

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	mcpJSONPath        = ".cursor/mcp.json"
	mcpJSONPointerBase = "/mcpServers/agentsync_"
	mcpSidecarPath     = ".cursor/.agent-sync-managed"
	agentsMDPath       = "AGENTS.md"
	sectionIDPrefix    = "agent-sync:"

	// scopeUser is the wire value (hierarchy.Level.String()) for the user-home
	// scope. Any other value (incl. "" / "project" / "directory") is project
	// scope.
	scopeUser = "user"
)

// pathSet holds the scope-resolved destinations for Cursor's MCP output and
// its strict-JSON sidecar. At user scope the MCP target is the same relative
// path (`.cursor/mcp.json` resolves to `~/.cursor/mcp.json`, Cursor's own
// global config), but the sidecar is suppressed because that file is Cursor's,
// not agent-sync's — mirroring Claude's `~/.claude.json` handling.
//
// Cursor's other two concepts (rule, agents-md) have NO file-addressable
// user-global home (User Rules are app-settings/cloud; there is no global
// AGENTS.md), so they are not represented here — they are skipped at user
// scope in dispatchNode and flagged by internal/coverage. See plan
// docs/plans/2026-06-30-001.
type pathSet struct {
	mcpJSON string
	sidecar string // "" ⇒ do not declare/emit the sidecar (user scope)
}

// resolvePathSet maps the initialize scope to Cursor's scope-dependent paths.
// declaredOutputs (capabilities.go) and the emitted op paths (here) MUST both
// resolve from this one function so they never drift — a mismatch is rejected
// by the runtime's path-safety gate.
func resolvePathSet(scope string) pathSet {
	if scope == scopeUser {
		return pathSet{mcpJSON: mcpJSONPath, sidecar: ""}
	}
	return pathSet{mcpJSON: mcpJSONPath, sidecar: mcpSidecarPath}
}

// markerOpenBytes is the literal HTML-comment opener every agent-sync
// section marker uses. Body content for an agents-md node is rejected
// when it contains this byte sequence: a hostile body could otherwise
// inject a fake end-marker followed by a fake begin-marker, splitting
// the managed section in AGENTS.md and confusing the merge step.
var markerOpenBytes = []byte("<!-- agent-sync:")

// emitMCPServerEntry emits one write_tool_owned op into
// .cursor/mcp.json at /mcpServers/agentsync_<id>, plus a sidecar
// .cursor/.agent-sync-managed marker (deduplicated per emit) that
// advertises ownership next to the strict-JSON file.
//
// The body is required to parse as a JSON object. A scalar, array,
// or boolean body would corrupt the merged .cursor/mcp.json: Cursor
// expects /mcpServers/<key> to map to an object with command/args
// fields, and storing a non-object value silently breaks every MCP
// load for the workspace.
//
// Validation is two-stage:
//  1. json.Valid — proves the bytes parse, catching string-encoded
//     bodies like "{" that look object-shaped at the first byte but
//     are actually unterminated JSON.
//  2. isJSONObject — proves the top-level structure is `{...}` and
//     not a number/array/bool/null that json.Valid alone would
//     accept.
func emitMCPServerEntry(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if !json.Valid(body) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("cursor: mcp-server-entry %q body is not valid JSON; refusing to corrupt %s", node.ID, state.paths.mcpJSON),
		}
	}
	if !isJSONObject(body) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("cursor: mcp-server-entry %q body must be a JSON object (got non-object)", node.ID),
		}
	}

	emitted.add(adapterkit.OpWriteToolOwned{
		Path:    state.paths.mcpJSON,
		Kind:    adapterkit.ToolOwnedKindJSONPointer,
		Locator: mcpJSONPointerBase + node.ID,
		Content: body,
	})

	// The sidecar advertises agent-sync ownership next to an agent-sync-owned
	// strict-JSON file. At user scope the MCP target is ~/.cursor/mcp.json —
	// Cursor's own global config — so paths.sidecar is empty and we suppress it.
	if state.paths.sidecar != "" && !state.sidecarEmitted {
		state.sidecarEmitted = true
		sidecar, err := adapterkit.NewOpWriteFile(state.paths.sidecar, 0o644, jsonSidecarMarker())
		if err != nil {
			return wrapOpErr(node, err)
		}
		emitted.add(sidecar)
	}
	return nil
}

// emitAgentsMD writes the agents-md node into AGENTS.md (workspace
// root) as a managed section between <!-- agent-sync:begin id=<id> -->
// and <!-- agent-sync:end id=<id> --> markers. AGENTS.md is tool-owned
// (read by Cursor and other agents at the project root); user content
// outside the managed section is preserved by the merge step
// (Unit 12a).
//
// This adapter writes exactly one marked section and does not assume
// it owns the whole file — codex and pi (Units 11, 11.5) write their
// own sections to the same AGENTS.md, and the per-external-file lock
// plus multi-section merge (Units 12, 12a) serialize and combine them.
//
// The body is rejected when it contains the agent-sync marker opener.
// Without this guard a body containing `<!-- agent-sync:end id=other -->`
// could break out of its own section or forge another section
// entirely, leaving the merged AGENTS.md with conflicting markers the
// merge step has no way to resolve safely.
//
// No managed-file header is added inside the section: the begin/end
// markers serve as the equivalent ownership advertisement, and a
// header inside a user-owned markdown file would be visually noisy.
func emitAgentsMD(emitted *emittedOps, node irNode) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if bytes.Contains(body, markerOpenBytes) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("cursor: agents-md %q body contains agent-sync marker syntax (%q); refusing to corrupt AGENTS.md", node.ID, string(markerOpenBytes)),
		}
	}
	// The engine owns the begin/end markers (it renders the managed block from
	// the locator during the markdown-section merge). The adapter passes the
	// INNER body only — sending marker-wrapped content is rejected by the merge
	// ("the engine owns markers; pass inner body only").
	emitted.add(adapterkit.OpWriteToolOwned{
		Path:    agentsMDPath,
		Kind:    adapterkit.ToolOwnedKindMarkdownSection,
		Locator: sectionIDPrefix + node.ID,
		Content: body,
	})
	return nil
}

// isJSONObject reports whether body is a JSON object (`{...}`),
// not a scalar, array, or null. Assumes the caller already verified
// the bytes are valid JSON via decodeBodyOrPassthrough; the check
// here is purely structural — first non-whitespace byte must be `{`.
func isJSONObject(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

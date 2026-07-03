package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	sectionIDPrefix    = "agent-sync:"
	mcpJSONPointerBase = "/mcpServers/agentsync_"

	// Project-scope destinations.
	geminiMDPath  = "GEMINI.md"
	mcpConfigPath = ".agents/mcp_config.json"

	// User-scope destinations (scope root is $HOME). Antigravity's shared config
	// base is ~/.gemini/: the tool-specific overlay is ~/.gemini/GEMINI.md and the
	// central MCP config is ~/.gemini/config/mcp_config.json (Antigravity 2.0
	// IDE+CLI). These are genuine remaps — Antigravity does NOT read ~/GEMINI.md
	// or ~/.agents/mcp_config.json at the user level.
	userGeminiMDPath  = ".gemini/GEMINI.md"
	userMCPConfigPath = ".gemini/config/mcp_config.json"

	// scopeUser is the wire value (hierarchy.Level.String()) for the user-home
	// scope. Any other value (incl. "" / "project" / "directory") is project
	// scope.
	scopeUser = "user"
)

// pathSet holds the scope-resolved destinations for the antigravity adapter's
// scope-dependent outputs: the GEMINI.md overlay, the mcp_config.json merge
// target, and the shared skills tree. Rules and workflows (.agent/…) are not
// scope-varying — Antigravity documents no user-global directory for them, so
// they stay project-relative and coverage flags them inert at user scope.
type pathSet struct {
	geminiMD     string
	mcpConfig    string
	skillsParent string
}

// resolvePathSet maps the initialize scope to the adapter's scope-dependent
// paths. declaredOutputs (capabilities.go) and the emitted op paths (here and in
// emit_reserved.go) MUST both resolve from this one function so they never drift
// — a mismatch is rejected by the runtime's path-safety gate.
func resolvePathSet(scope string) pathSet {
	if scope == scopeUser {
		return pathSet{
			geminiMD:     userGeminiMDPath,
			mcpConfig:    userMCPConfigPath,
			skillsParent: userSkillsParent,
		}
	}
	return pathSet{
		geminiMD:     geminiMDPath,
		mcpConfig:    mcpConfigPath,
		skillsParent: skillsParent,
	}
}

// markerOpenBytes is the literal HTML-comment opener every agent-sync section
// marker uses. An agents-md body containing this sequence is rejected so a
// hostile body can't forge a section split inside GEMINI.md.
var markerOpenBytes = []byte("<!-- agent-sync:")

// emitAgentsMD writes the agents-md node into GEMINI.md (workspace root at
// project scope; .gemini/GEMINI.md at user scope) as a managed section between
// <!-- agent-sync:begin id=<id> --> and <!-- agent-sync:end id=<id> --> markers.
// GEMINI.md is tool-owned by Antigravity; user content outside the managed
// section is preserved by the merge step.
//
// The body is rejected when it contains the agent-sync marker opener: a body
// carrying `<!-- agent-sync:end id=other -->` could forge another section,
// leaving the merged GEMINI.md with conflicting markers the merge cannot resolve
// safely.
func emitAgentsMD(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if bytes.Contains(body, markerOpenBytes) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("antigravity: agents-md %q body contains agent-sync marker syntax (%q); refusing to corrupt %s", node.ID, string(markerOpenBytes), state.paths.geminiMD),
		}
	}
	// The engine owns the begin/end markers (it renders the managed block from
	// the locator during the markdown-section merge). The adapter passes the
	// INNER body only — sending marker-wrapped content is rejected by the merge.
	emitted.add(adapterkit.OpWriteToolOwned{
		Path:    state.paths.geminiMD,
		Kind:    adapterkit.ToolOwnedKindMarkdownSection,
		Locator: sectionIDPrefix + node.ID,
		Content: body,
	})
	return nil
}

// emitMCPServerEntry emits one write_tool_owned op into mcp_config.json at
// /mcpServers/agentsync_<id>. Unlike the claude adapter, no .agent-sync-managed
// sidecar is written: Antigravity's mcp_config.json is a shared tool-owned merge
// target (like claude's ~/.claude.json at user scope, where the sidecar is
// likewise suppressed), not an agent-sync-owned strict-JSON file the marker may
// claim.
//
// The body is required to parse as a JSON object. A scalar, array, or boolean
// body would corrupt the merged mcp_config.json: Antigravity expects
// /mcpServers/<key> to map to an object with command/args fields, and storing a
// non-object value silently breaks every MCP load for the workspace.
//
// Validation is two-stage: json.Valid proves the bytes parse (catching
// string-encoded bodies like "{" that look object-shaped at the first byte);
// isJSONObject proves the top-level structure is {...} and not a
// number/array/bool/null that json.Valid alone would accept.
func emitMCPServerEntry(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if !json.Valid(body) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("antigravity: mcp-server-entry %q body is not valid JSON; refusing to corrupt %s", node.ID, state.paths.mcpConfig),
		}
	}
	if !isJSONObject(body) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("antigravity: mcp-server-entry %q body must be a JSON object (got non-object)", node.ID),
		}
	}

	emitted.add(adapterkit.OpWriteToolOwned{
		Path:    state.paths.mcpConfig,
		Kind:    adapterkit.ToolOwnedKindJSONPointer,
		Locator: mcpJSONPointerBase + node.ID,
		Content: body,
	})
	return nil
}

// isJSONObject reports whether body is a JSON object ({...}), not a scalar,
// array, or null. Assumes the caller already verified the bytes are valid JSON;
// the check here is purely structural — first non-whitespace byte must be `{`.
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

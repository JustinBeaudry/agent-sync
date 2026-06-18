package claude

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	mcpJSONPath        = ".mcp.json"
	mcpJSONPointerBase = "/mcpServers/agentsync_"
	claudeMDPath       = "CLAUDE.md"
	claudeMDSidecar    = ".agent-sync-managed"
	sectionIDPrefix    = "agent-sync:"

	// scopeUser is the wire value (hierarchy.Level.String()) for the
	// user-home scope. Any other value (incl. "" / "project" / "directory")
	// is treated as project scope.
	scopeUser = "user"
)

// pathSet holds the scope-resolved destinations for the Claude adapter's two
// tool-owned-entry outputs and the strict-JSON sidecar. At project/directory
// scope they are workspace-root files (the historical behavior). At user
// scope they target Claude Code's real user-config paths — `~/.claude/CLAUDE.md`
// and `~/.claude.json` (relative to the home scope root) — and the sidecar is
// suppressed, because `~/.claude.json` is Claude's own shared file, not an
// agent-sync-owned strict-JSON file the marker may claim.
type pathSet struct {
	claudeMD string
	mcpJSON  string
	sidecar  string // "" ⇒ do not declare/emit the sidecar (user scope)
}

// resolvePathSet maps the initialize scope to the adapter's tool-owned paths.
// Declared outputs (capabilities.go) and emitted op paths (here) MUST resolve
// from this one function so they never drift — a mismatch is rejected by the
// runtime's path-safety gate.
func resolvePathSet(scope string) pathSet {
	if scope == scopeUser {
		return pathSet{claudeMD: ".claude/CLAUDE.md", mcpJSON: ".claude.json", sidecar: ""}
	}
	return pathSet{claudeMD: claudeMDPath, mcpJSON: mcpJSONPath, sidecar: claudeMDSidecar}
}

// markerOpenBytes is the literal HTML-comment opener every agent-sync
// section marker uses. Body content for an agents-md node is rejected
// when it contains this byte sequence: a hostile body could otherwise
// inject a fake end-marker followed by a fake begin-marker, splitting
// the managed section in CLAUDE.md and confusing the merge step.
var markerOpenBytes = []byte("<!-- agent-sync:")

// emitMCPServerEntry emits one write_tool_owned op into .mcp.json
// at /mcpServers/agentsync_<id>, plus a sidecar .agent-sync-managed marker
// (deduplicated per emit) that advertises ownership next to the
// strict-JSON file.
//
// The body is required to parse as a JSON object. A scalar, array,
// or boolean body would corrupt the merged .mcp.json: Claude Code
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
			Message: fmt.Sprintf("claude: mcp-server-entry %q body is not valid JSON; refusing to corrupt .mcp.json", node.ID),
		}
	}
	if !isJSONObject(body) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: mcp-server-entry %q body must be a JSON object (got non-object)", node.ID),
		}
	}

	emitted.add(adapterkit.OpWriteToolOwned{
		Path:    state.paths.mcpJSON,
		Kind:    adapterkit.ToolOwnedKindJSONPointer,
		Locator: mcpJSONPointerBase + node.ID,
		Content: body,
	})

	// The sidecar advertises agent-sync ownership next to an agent-sync-owned
	// strict-JSON file. At user scope the MCP target is `~/.claude.json` —
	// Claude's own shared config — so paths.sidecar is empty and we suppress it.
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

// emitAgentsMD writes the agents-md companion overlay into
// CLAUDE.md (workspace root) as a managed section between
// <!-- agent-sync:begin id=<id> --> and <!-- agent-sync:end id=<id> -->
// markers. CLAUDE.md is tool-owned by Claude Code; user content
// outside the managed section is preserved by the merge step
// (Unit 12a).
//
// The engine owns the begin/end markers — it renders the managed block
// from the locator during the markdown-section merge. The adapter passes
// the INNER body only; sending marker-wrapped content is rejected by the
// merge ("the engine owns markers; pass inner body only"). This mirrors
// the cursor and codex adapters.
//
// The body is still rejected when it contains the agent-sync marker
// opener. Without this guard a body containing
// `<!-- agent-sync:end id=other -->` could forge another section,
// leaving the merged CLAUDE.md with conflicting markers the merge step
// has no way to resolve safely.
//
// No managed-file header is added inside the section: the begin/end
// markers serve as the equivalent ownership advertisement, and a
// header inside a user-owned markdown file would be visually noisy.
func emitAgentsMD(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if bytes.Contains(body, markerOpenBytes) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: agents-md %q body contains agent-sync marker syntax (%q); refusing to corrupt CLAUDE.md", node.ID, string(markerOpenBytes)),
		}
	}

	emitted.add(adapterkit.OpWriteToolOwned{
		Path:    state.paths.claudeMD,
		Kind:    adapterkit.ToolOwnedKindMarkdownSection,
		Locator: sectionIDPrefix + node.ID,
		Content: body,
	})
	return nil
}

// emitPluginReferenceWarning surfaces the unsupported declaration
// for a plugin-reference node. Claude Code does not load
// project-level plugin registries, so the adapter declines and
// surfaces a degradation warning the sync engine will report.
//
// No write_file is emitted. The runtime's capability-lied check
// (internal/adapter/runtime.go) only fires for kinds the adapter
// declared as `supported`, so emitting a warning-only result here
// is safe — plugin-reference is declared `unsupported` in
// capabilities.yaml.
func emitPluginReferenceWarning(emitted *emittedOps, node irNode) error {
	emitted.add(adapterkit.OpWarning{
		ConceptID: node.ID,
		Status:    adapterkit.WarningStatusDegraded,
		Note:      "Claude Code does not load project-level plugin-reference registries; install plugins via Claude Code's own plugin command",
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

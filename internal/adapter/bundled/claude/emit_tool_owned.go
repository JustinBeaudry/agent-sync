package claude

import (
	"encoding/json"
	"fmt"

	"github.com/aienvs/aienvs/pkg/adapterkit"
)

const (
	mcpJSONPath        = ".mcp.json"
	mcpJSONPointerBase = "/mcpServers/aienvs_"
	claudeMDPath       = "CLAUDE.md"
	claudeMDSidecar    = ".aienvs-managed"
	sectionIDPrefix    = "aienvs:"
)

// emitMCPServerEntry emits one write_tool_owned op into .mcp.json
// at /mcpServers/aienvs_<id>, plus a sidecar .aienvs-managed marker
// that advertises ownership next to the strict-JSON file (it has no
// comment syntax to inline a managed-file header).
//
// The body is required to parse as JSON; a malformed body is an
// adapter-level error because emitting it would corrupt the merged
// .mcp.json file at sync time. The runtime cannot distinguish
// "intentional non-JSON" from "decoder bug", so we fail closed.
func emitMCPServerEntry(emitted *emittedOps, node irNode) error {
	body, err := nodeBodyBytes(node)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if !json.Valid(body) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: mcp-server-entry %q body is not valid JSON; refusing to corrupt .mcp.json", node.ID),
		}
	}

	op := adapterkit.OpWriteToolOwned{
		Path:    mcpJSONPath,
		Kind:    adapterkit.ToolOwnedKindJSONPointer,
		Locator: mcpJSONPointerBase + node.ID,
		Content: body,
	}
	if _, err := json.Marshal(op); err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(op)

	sidecar, err := adapterkit.NewOpWriteFile(claudeMDSidecar, 0o644, jsonSidecarMarker())
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(sidecar)
	return nil
}

// emitAgentsMD writes the agents-md companion overlay into
// CLAUDE.md (workspace root) as a managed section between
// <!-- aienvs:begin id=<id> --> and <!-- aienvs:end id=<id> -->
// markers. CLAUDE.md is tool-owned by Claude Code; user content
// outside the managed section is preserved by the merge step
// (Unit 12a).
//
// No managed-file header is added inside the section: the begin/end
// markers serve as the equivalent ownership advertisement, and a
// header inside a user-owned markdown file would be visually noisy.
func emitAgentsMD(emitted *emittedOps, node irNode) error {
	body, err := nodeBodyBytes(node)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	wrapped := wrapManagedSection(node.ID, body)

	op := adapterkit.OpWriteToolOwned{
		Path:    claudeMDPath,
		Kind:    adapterkit.ToolOwnedKindMarkdownSection,
		Locator: sectionIDPrefix + node.ID,
		Content: wrapped,
	}
	if _, err := json.Marshal(op); err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(op)
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

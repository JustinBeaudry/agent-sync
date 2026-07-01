package pi

import (
	"fmt"

	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// unsupportedNotes maps each unsupported IR kind to its user-facing degradation
// note. Pi reads none of these at the project level; the adapter declines
// honestly rather than emitting dead files into directories Pi never reads.
//
// mcp-server-entry and rule are deliberate Pi product exclusions; command is
// planned but not yet emitted (see capabilities.yaml); plugin-reference has no
// project-level file shape in Pi.
var unsupportedNotes = map[ir.Kind]string{
	ir.KindMCPServerEntry:  "Pi does not load MCP servers by design — see https://mariozechner.at/posts/2025-11-02-what-if-you-dont-need-mcp/. The MCP entry was not installed. To expose this capability to Pi, build or install a Pi extension that wraps it.",
	ir.KindRule:            "Pi has no per-tool rule concept distinct from AGENTS.md; the rule was not installed. Consolidate rule content into an agents-md node or a skill.",
	ir.KindCommand:         "The agent-sync pi adapter does not emit prompt-template commands yet (planned); the command was not installed. Pi itself supports prompt templates at .pi/prompts/.",
	ir.KindPluginReference: "Pi packages install via `pi install` (npm/git), not a project-level plugin-reference registry; the reference was not installed.",
}

// emitUnsupportedWarning surfaces the unsupported declaration for a node whose
// kind pi does not emit. No write_file is emitted.
//
// The runtime's capability-lied check only fires for kinds the adapter declared
// as `supported`, so emitting a warning-only result here is safe — all four
// kinds routed here are declared `unsupported` in capabilities.yaml. See
// internal/adapter/runtime.go and the capability-lie-gate memory.
func emitUnsupportedWarning(emitted *emittedOps, node irNode) error {
	note, ok := unsupportedNotes[ir.Kind(node.Kind)]
	if !ok {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInternalError,
			Message: fmt.Sprintf("pi: no unsupported-note for kind %q (node %q)", node.Kind, node.ID),
		}
	}
	emitted.add(adapterkit.OpWarning{
		ConceptID: node.ID,
		Status:    adapterkit.WarningStatusDegraded,
		Note:      note,
	})
	return nil
}

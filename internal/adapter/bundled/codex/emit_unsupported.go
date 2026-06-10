package codex

import (
	"fmt"

	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// unsupportedNotes maps each unsupported IR kind to its user-facing degradation
// note. Codex reads none of these at the project level; the adapter declines
// honestly rather than emitting dead files into directories Codex never reads.
var unsupportedNotes = map[ir.Kind]string{
	ir.KindRule:            "Codex has no per-tool rule concept distinct from AGENTS.md; the rule was not installed. Consolidate rule content into an agents-md node or a skill.",
	ir.KindCommand:         "Codex custom prompts are deprecated and live only in the user home (~/.codex/prompts/), so they are not repo-shareable; the command was not installed. Use a skill instead.",
	ir.KindPluginReference: "Codex does not load project-level plugin references; the reference was not installed.",
}

// emitUnsupportedWarning surfaces the unsupported declaration for a rule,
// command, or plugin-reference node. No write_file is emitted.
//
// The runtime's capability-lied check only fires for kinds the adapter
// declared as `supported`, so emitting a warning-only result here is safe —
// all three kinds are declared `unsupported` in capabilities.yaml.
func emitUnsupportedWarning(emitted *emittedOps, node irNode) error {
	note, ok := unsupportedNotes[ir.Kind(node.Kind)]
	if !ok {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInternalError,
			Message: fmt.Sprintf("codex: no unsupported-note for kind %q (node %q)", node.Kind, node.ID),
		}
	}
	emitted.add(adapterkit.OpWarning{
		ConceptID: node.ID,
		Status:    adapterkit.WarningStatusDegraded,
		Note:      note,
	})
	return nil
}

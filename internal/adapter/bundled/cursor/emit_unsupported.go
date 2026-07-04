package cursor

import (
	"fmt"

	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// unsupportedNotes maps each unsupported IR kind to its user-facing
// degradation note. Cursor reads none of these at the project level;
// the adapter declines honestly rather than emitting dead files into
// directories Cursor never reads.
var unsupportedNotes = map[ir.Kind]string{
	ir.KindPluginReference: "Cursor does not load project-level plugin references; the reference was not installed",
}

// emitUnsupportedWarning surfaces the unsupported declaration for a
// plugin-reference node. No write_file is emitted.
//
// The runtime's capability-lied check (internal/adapter/runtime.go)
// only fires for kinds the adapter declared as `supported`, so
// emitting a warning-only result here is safe — all three kinds are
// declared `unsupported` in capabilities.yaml.
func emitUnsupportedWarning(emitted *emittedOps, node irNode) error {
	note, ok := unsupportedNotes[ir.Kind(node.Kind)]
	if !ok {
		// Defensive: dispatchNode only routes the three unsupported
		// kinds here, so an unmapped kind is a programming error
		// (a new unsupported kind added to the switch but not the
		// note table). Fail loudly rather than emit an empty note.
		return &adapterkit.Error{
			Code:    adapterkit.CodeInternalError,
			Message: fmt.Sprintf("cursor: no unsupported-note for kind %q (node %q)", node.Kind, node.ID),
		}
	}
	emitted.add(adapterkit.OpWarning{
		ConceptID: node.ID,
		Status:    adapterkit.WarningStatusDegraded,
		Note:      note,
	})
	return nil
}

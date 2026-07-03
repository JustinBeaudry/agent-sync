package antigravity

import (
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// emitPluginReferenceWarning surfaces the unsupported declaration for a
// plugin-reference node. Antigravity does not load a project-level plugin
// registry file, so the adapter declines and surfaces a degradation warning the
// sync engine will report.
//
// No write_file is emitted. The runtime's capability-lied check only fires for
// kinds the adapter declared as `supported`, so emitting a warning-only result
// here is safe — plugin-reference is declared `unsupported` in capabilities.yaml.
func emitPluginReferenceWarning(emitted *emittedOps, node irNode) error {
	emitted.add(adapterkit.OpWarning{
		ConceptID: node.ID,
		Status:    adapterkit.WarningStatusDegraded,
		Note:      "Antigravity does not load project-level plugin-reference registries; install plugins through Antigravity's own plugin mechanism",
	})
	return nil
}

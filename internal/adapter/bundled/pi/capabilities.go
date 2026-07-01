package pi

import (
	_ "embed"
	"fmt"

	"github.com/goccy/go-yaml"

	"github.com/agent-sync/agent-sync/internal/capmatrix"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

//go:embed capabilities.yaml
var capabilitiesYAML []byte

// declarationFile is the parsed shape of capabilities.yaml.
type declarationFile struct {
	Name            string                            `yaml:"name"`
	Version         string                            `yaml:"version"`
	ContractVersion string                            `yaml:"contract_version"`
	ReservedPrefix  string                            `yaml:"reserved_prefix"`
	ConceptKinds    map[string]conceptKindDeclaration `yaml:"concept_kinds"`
}

type conceptKindDeclaration struct {
	Status capmatrix.CapabilityStatus `yaml:"status"`
	Note   string                     `yaml:"note,omitempty"`
}

// conceptKinds is the in-code mirror of capabilities.yaml. The unit test
// asserts the two are kept in sync.
//
// Pi declares two kinds supported (agents-md, skill) and four unsupported. MCP
// and rule are deliberate Pi product exclusions; command is planned (Pi's
// prompt dir is flat + shared, needing file-leaf swap support). Skills land in
// the shared .agents/skills tree, co-owned with codex — made safe by the
// engine's union-aware drift/orphan checks (ADV-1). See capabilities.yaml notes
// and docs/adapters/pi.md.
var conceptKinds = map[ir.Kind]capmatrix.CapabilityStatus{
	ir.KindAgentsMD:        capmatrix.Supported,
	ir.KindSkill:           capmatrix.Supported,
	ir.KindCommand:         capmatrix.Unsupported,
	ir.KindMCPServerEntry:  capmatrix.Unsupported,
	ir.KindRule:            capmatrix.Unsupported,
	ir.KindPluginReference: capmatrix.Unsupported,
}

// capabilitiesForWire returns the adapterkit.Capabilities the adapter echoes in
// its initialize response. WriteToolOwned is true because pi emits a
// write_tool_owned op for the AGENTS.md managed section.
//
// Unlike cursor, pi's capability set does not vary by scope: every supported
// kind (agents-md, skill) has a user-global home, so nothing is demoted at user
// scope. Only the agents-md output path is scope-aware (see declaredOutputs).
func capabilitiesForWire() adapterkit.Capabilities {
	builder := adapterkit.NewCapabilities().WithWriteToolOwned(true)
	for kind, status := range conceptKinds {
		switch status {
		case capmatrix.Supported:
			builder.Supports(string(kind))
		case capmatrix.Partial:
			builder.Partial(string(kind))
		case capmatrix.Unsupported:
			builder.Unsupported(string(kind))
		}
	}
	return builder.Build()
}

// declaredOutputs returns the list the adapter advertises in its initialize
// response. The runtime path-safety gate (pathInDeclaredOutputs) accepts an
// emitted op only when its path falls inside one of these declared outputs.
//
// Pi owns no single reserved subdirectory: skills go under the shared
// .agents/skills/ tree, and prose into the tool-owned AGENTS.md. The agents-md
// path is scope-aware — at user scope Pi reads ~/.pi/agent/AGENTS.md, so the
// adapter targets .pi/agent/AGENTS.md there. Both declared and emitted paths
// resolve from resolvePathSet so they never drift. See plan
// docs/plans/2026-06-30-003.
func declaredOutputs(scope string) []adapterkit.DeclaredOutput {
	agentsMDSection := "agent-sync"
	paths := resolvePathSet(scope)
	return []adapterkit.DeclaredOutput{
		// .agents/skills is the shared cross-tool skills tree (pi, codex, and
		// the user all place skills here). shared-subdir → the engine manages
		// only the agent-sync-<id> leaf dirs, never the parent, so foreign
		// skills and sibling-adapter skills survive a sync (ADV-1 co-ownership).
		{Path: skillsParent, Mode: adapterkit.OutputModeSharedSubdir},
		{Path: paths.agentsMD, Mode: adapterkit.OutputModeToolOwnedEntry, SectionID: &agentsMDSection},
	}
}

// parseCapabilitiesYAML decodes the embedded YAML. Used by the parity test and
// by callers that want the human-readable note bodies.
func parseCapabilitiesYAML(src []byte) (declarationFile, error) {
	var d declarationFile
	if err := yaml.UnmarshalWithOptions(
		src,
		&d,
		yaml.DisallowUnknownField(),
		yaml.AllowFieldPrefixes("x-"),
	); err != nil {
		return declarationFile{}, fmt.Errorf("pi: parse capabilities.yaml: %s", yaml.FormatError(err, false, true))
	}
	return d, nil
}

// loadDeclaration parses the embedded capabilities.yaml and returns the result.
func loadDeclaration() (declarationFile, error) {
	return parseCapabilitiesYAML(capabilitiesYAML)
}

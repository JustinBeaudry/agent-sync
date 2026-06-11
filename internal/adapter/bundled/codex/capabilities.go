package codex

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
// Codex declares three kinds supported (agents-md, skill, mcp-server-entry)
// and three unsupported (rule, command, plugin-reference). See
// capabilities.yaml notes and docs/adapters/codex.md for the rationale.
var conceptKinds = map[ir.Kind]capmatrix.CapabilityStatus{
	ir.KindAgentsMD:        capmatrix.Supported,
	ir.KindSkill:           capmatrix.Supported,
	ir.KindMCPServerEntry:  capmatrix.Supported,
	ir.KindRule:            capmatrix.Unsupported,
	ir.KindCommand:         capmatrix.Unsupported,
	ir.KindPluginReference: capmatrix.Unsupported,
}

// capabilitiesForWire returns the adapterkit.Capabilities the adapter echoes
// in its initialize response. WriteToolOwned is true because codex emits
// write_tool_owned ops for AGENTS.md and .codex/config.toml.
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
// Codex owns no single reserved subdirectory: skills go under the shared
// .agents/skills/ tree, MCP entries into the tool-owned .codex/config.toml,
// and prose into the tool-owned workspace-root AGENTS.md.
func declaredOutputs() []adapterkit.DeclaredOutput {
	agentsMDSection := "agent-sync"
	return []adapterkit.DeclaredOutput{
		// .agents/skills is the shared cross-tool skills tree (codex, pi, and
		// the user all place skills here). shared-subdir → the engine manages
		// only the agent-sync-<id> leaf dirs, never the parent, so foreign skills
		// survive a sync.
		{Path: ".agents/skills", Mode: adapterkit.OutputModeSharedSubdir},
		{Path: ".codex/config.toml", Mode: adapterkit.OutputModeToolOwnedEntry},
		{Path: "AGENTS.md", Mode: adapterkit.OutputModeToolOwnedEntry, SectionID: &agentsMDSection},
	}
}

// parseCapabilitiesYAML decodes the embedded YAML. Used by the parity test
// and by callers that want the human-readable note bodies.
func parseCapabilitiesYAML(src []byte) (declarationFile, error) {
	var d declarationFile
	if err := yaml.UnmarshalWithOptions(
		src,
		&d,
		yaml.DisallowUnknownField(),
		yaml.AllowFieldPrefixes("x-"),
	); err != nil {
		return declarationFile{}, fmt.Errorf("codex: parse capabilities.yaml: %s", yaml.FormatError(err, false, true))
	}
	return d, nil
}

// loadDeclaration parses the embedded capabilities.yaml and returns the result.
func loadDeclaration() (declarationFile, error) {
	return parseCapabilitiesYAML(capabilitiesYAML)
}

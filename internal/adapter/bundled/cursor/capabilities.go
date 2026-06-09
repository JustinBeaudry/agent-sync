package cursor

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

// declarationFile is the parsed shape of capabilities.yaml. It is
// intentionally narrower than adapter.AdapterManifest so the file
// stays a per-adapter declaration and not an arbitrary manifest
// override.
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

// conceptKinds is the in-code mirror of capabilities.yaml. The unit
// test asserts the two are kept in sync; if you add a kind here,
// add it there (and vice versa).
//
// Cursor declares three kinds unsupported: skill (no folder-per-skill
// concept), command (no project-level command concept), and
// plugin-reference (no project plugin registry). See capabilities.yaml
// notes and docs/adapters/cursor.md for the rationale.
var conceptKinds = map[ir.Kind]capmatrix.CapabilityStatus{
	ir.KindAgentsMD:        capmatrix.Supported,
	ir.KindRule:            capmatrix.Supported,
	ir.KindMCPServerEntry:  capmatrix.Supported,
	ir.KindSkill:           capmatrix.Unsupported,
	ir.KindCommand:         capmatrix.Unsupported,
	ir.KindPluginReference: capmatrix.Unsupported,
}

// capabilitiesForWire returns the adapterkit.Capabilities the adapter
// echoes in its initialize response. WriteToolOwned is true because
// the cursor adapter emits write_tool_owned ops for .cursor/mcp.json
// and AGENTS.md.
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

// declaredOutputs returns the list the adapter advertises in its
// initialize response. The runtime path-safety gate
// (internal/adapter/runtime.go pathInDeclaredOutputs) accepts an
// emitted op only when its path falls inside one of these declared
// outputs.
func declaredOutputs() []adapterkit.DeclaredOutput {
	mcpPointer := "/mcpServers"
	agentsMDSection := "aienvs"
	return []adapterkit.DeclaredOutput{
		{Path: ".cursor/rules/aienvs", Mode: adapterkit.OutputModeOwnedSubdir},
		{Path: ".cursor/mcp.json", Mode: adapterkit.OutputModeToolOwnedEntry, JSONPointer: &mcpPointer},
		{Path: "AGENTS.md", Mode: adapterkit.OutputModeToolOwnedEntry, SectionID: &agentsMDSection},
		// The strict-JSON sidecar marker (.aienvs-managed) is written
		// next to .cursor/mcp.json under .cursor/. Declared by exact
		// path (owned-subdir mode authorizes the path; the write_file
		// op writes it) rather than over-broadly authorizing .cursor/.
		{Path: ".cursor/.aienvs-managed", Mode: adapterkit.OutputModeOwnedSubdir},
	}
}

// parseCapabilitiesYAML decodes the embedded YAML. Used by the parity
// test and by callers that want the human-readable note bodies.
func parseCapabilitiesYAML(src []byte) (declarationFile, error) {
	var d declarationFile
	if err := yaml.UnmarshalWithOptions(
		src,
		&d,
		yaml.DisallowUnknownField(),
		yaml.AllowFieldPrefixes("x-"),
	); err != nil {
		return declarationFile{}, fmt.Errorf("cursor: parse capabilities.yaml: %s", yaml.FormatError(err, false, true))
	}
	return d, nil
}

// loadDeclaration parses the embedded capabilities.yaml and returns
// the result.
func loadDeclaration() (declarationFile, error) {
	return parseCapabilitiesYAML(capabilitiesYAML)
}

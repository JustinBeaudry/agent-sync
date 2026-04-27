package claude

import (
	_ "embed"
	"fmt"
	"sort"

	"github.com/goccy/go-yaml"

	"github.com/aienvs/aienvs/internal/capmatrix"
	"github.com/aienvs/aienvs/internal/ir"
	"github.com/aienvs/aienvs/pkg/adapterkit"
)

//go:embed capabilities.yaml
var capabilitiesYAML []byte

// declarationFile is the parsed shape of capabilities.yaml. It is
// intentionally narrower than adapter.AdapterManifest so the file
// stays a per-adapter declaration and not an arbitrary manifest
// override.
type declarationFile struct {
	Name            string                              `yaml:"name"`
	Version         string                              `yaml:"version"`
	ContractVersion string                              `yaml:"contract_version"`
	ReservedPrefix  string                              `yaml:"reserved_prefix"`
	ConceptKinds    map[string]conceptKindDeclaration   `yaml:"concept_kinds"`
}

type conceptKindDeclaration struct {
	Status capmatrix.CapabilityStatus `yaml:"status"`
	Note   string                     `yaml:"note,omitempty"`
}

// conceptKinds is the in-code mirror of capabilities.yaml. The unit
// test asserts the two are kept in sync; if you add a kind here,
// add it there (and vice versa).
//
// The status values use capmatrix's vocabulary; the wire conversion
// happens in capabilitiesForWire.
var conceptKinds = map[ir.Kind]capmatrix.CapabilityStatus{
	ir.KindAgentsMD:        capmatrix.Supported,
	ir.KindRule:            capmatrix.Supported,
	ir.KindSkill:           capmatrix.Supported,
	ir.KindCommand:         capmatrix.Supported,
	ir.KindMCPServerEntry:  capmatrix.Supported,
	ir.KindPluginReference: capmatrix.Unsupported,
}

// capabilitiesForWire returns the adapterkit.Capabilities the adapter
// echoes in its initialize response. WriteToolOwned is true because
// the claude adapter emits write_tool_owned ops for .mcp.json and
// CLAUDE.md.
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
	claudeMDSection := "aienvs"
	return []adapterkit.DeclaredOutput{
		{Path: ".claude/rules/aienvs", Mode: adapterkit.OutputModeOwnedSubdir},
		{Path: ".claude/commands/aienvs", Mode: adapterkit.OutputModeOwnedSubdir},
		{Path: ".claude/skills", Mode: adapterkit.OutputModeOwnedSubdir},
		{Path: ".mcp.json", Mode: adapterkit.OutputModeToolOwnedEntry, JSONPointer: &mcpPointer},
		{Path: "CLAUDE.md", Mode: adapterkit.OutputModeToolOwnedEntry, SectionID: &claudeMDSection},
		// The strict-JSON sidecar marker (.aienvs-managed) is written
		// next to .mcp.json at workspace root. Declared as
		// owned-subdir on "." would over-broadly authorize the whole
		// workspace, so we declare the sidecar file by exact path.
		{Path: ".aienvs-managed", Mode: adapterkit.OutputModeOwnedSubdir},
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
		return declarationFile{}, fmt.Errorf("claude: parse capabilities.yaml: %s", yaml.FormatError(err, false, true))
	}
	return d, nil
}

// loadDeclaration parses the embedded capabilities.yaml and returns
// the result. Cached at package level if it ever becomes a hot path;
// for now the call sites are test-only and one-off.
func loadDeclaration() (declarationFile, error) {
	return parseCapabilitiesYAML(capabilitiesYAML)
}

// sortedConceptKinds returns the IR kinds known to the adapter in
// stable order. Used in tests + diagnostics so iteration order is
// deterministic.
func sortedConceptKinds() []ir.Kind {
	kinds := make([]ir.Kind, 0, len(conceptKinds))
	for k := range conceptKinds {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return string(kinds[i]) < string(kinds[j]) })
	return kinds
}

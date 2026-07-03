package antigravity

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

// conceptKinds is the in-code mirror of capabilities.yaml. The unit test asserts
// the two are kept in sync; if you add a kind here, add it there (and vice
// versa).
//
// Antigravity is a full-parity adapter: every kind except plugin-reference is
// supported. Skills land in the shared .agents/skills tree co-owned with
// codex/pi (made safe by the engine's union-aware drift/orphan checks, ADV-1);
// rules and workflows land in the Antigravity-exclusive .agent/ tree.
var conceptKinds = map[ir.Kind]capmatrix.CapabilityStatus{
	ir.KindAgentsMD:        capmatrix.Supported,
	ir.KindRule:            capmatrix.Supported,
	ir.KindSkill:           capmatrix.Supported,
	ir.KindCommand:         capmatrix.Supported,
	ir.KindMCPServerEntry:  capmatrix.Supported,
	ir.KindPluginReference: capmatrix.Unsupported,
}

// capabilitiesForWire returns the adapterkit.Capabilities the adapter echoes in
// its initialize response. WriteToolOwned is true because the antigravity
// adapter emits write_tool_owned ops for GEMINI.md and mcp_config.json.
//
// The capability set does not vary by scope: every supported kind is emitted at
// every scope. Only paths (resolvePathSet) and native-read coverage
// (internal/coverage) are scope-aware — see docs/adapters/antigravity.md.
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
// response. The runtime path-safety gate (internal/adapter/runtime.go
// pathInDeclaredOutputs) accepts an emitted op only when its path falls inside
// one of these declared outputs.
//
// The rules and workflows subdirs (.agent/…) are agent-sync-exclusive
// owned-subdirs and are not scope-varying (Antigravity documents no user-global
// directory for them — coverage flags them inert at user scope). The skills
// tree, mcp config, and GEMINI.md overlay are scope-resolved from resolvePathSet
// so declared and emitted paths never drift.
func declaredOutputs(scope string) []adapterkit.DeclaredOutput {
	mcpPointer := "/mcpServers"
	geminiMDSection := "agent-sync"
	paths := resolvePathSet(scope)
	return []adapterkit.DeclaredOutput{
		// Antigravity-exclusive .agent/ tree (singular) — swapped wholesale.
		{Path: rulesSubdir, Mode: adapterkit.OutputModeOwnedSubdir},
		{Path: commandsSubdir, Mode: adapterkit.OutputModeOwnedSubdir},
		// Shared cross-tool skills tree (plural .agents). shared-subdir → the
		// engine manages only the agent-sync-<id> leaf dirs, never the parent, so
		// foreign and sibling-adapter skills survive a sync (ADV-1 co-ownership).
		{Path: paths.skillsParent, Mode: adapterkit.OutputModeSharedSubdir},
		{Path: paths.mcpConfig, Mode: adapterkit.OutputModeToolOwnedEntry, JSONPointer: &mcpPointer},
		{Path: paths.geminiMD, Mode: adapterkit.OutputModeToolOwnedEntry, SectionID: &geminiMDSection},
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
		return declarationFile{}, fmt.Errorf("antigravity: parse capabilities.yaml: %s", yaml.FormatError(err, false, true))
	}
	return d, nil
}

// loadDeclaration parses the embedded capabilities.yaml and returns the result.
func loadDeclaration() (declarationFile, error) {
	return parseCapabilitiesYAML(capabilitiesYAML)
}

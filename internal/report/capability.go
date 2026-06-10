package report

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/agent-sync/agent-sync/internal/adapter/contract"
	"github.com/agent-sync/agent-sync/internal/fsroot"
)

// CapabilityReportSchemaVersion is the pinned schema version of the
// persisted capability report.
const CapabilityReportSchemaVersion = 1

// capabilityReportRel is where the report is persisted during staging
// (before swap), so it survives an atomic rollback (decision #21).
const capabilityReportRel = ".agent-sync/state/capability-report.json"

// CapabilityInput is one target's declared capabilities plus the concept
// kinds the IR actually requires of it.
type CapabilityInput struct {
	Target        string
	Caps          contract.Capabilities
	RequiredKinds []string
}

// TargetCapability is one target's resolved capability row.
type TargetCapability struct {
	Target         string            `json:"target"`
	ConceptKinds   map[string]string `json:"concept_kinds"`
	WriteToolOwned bool              `json:"write_tool_owned"`
	Progress       bool              `json:"progress"`
	RequiredUnmet  []string          `json:"required_unmet"`
}

// CapabilityReport is the persisted capability document.
type CapabilityReport struct {
	SchemaVersion int                `json:"schema_version"`
	GeneratedAt   string             `json:"generated_at"`
	Targets       []TargetCapability `json:"targets"`
}

// BuildCapability resolves each target's capability row and computes
// required_unmet: the required concept kinds the target reports as
// unsupported or does not report at all. Targets and required_unmet are
// sorted for deterministic output. The caller stamps generatedAt.
func BuildCapability(generatedAt string, inputs []CapabilityInput) CapabilityReport {
	targets := make([]TargetCapability, 0, len(inputs))
	for _, in := range inputs {
		kinds := make(map[string]string, len(in.Caps.ConceptKinds))
		for k, lvl := range in.Caps.ConceptKinds {
			kinds[k] = string(lvl)
		}
		unmet := []string{} // stable JSON: [] not null
		for _, req := range in.RequiredKinds {
			if in.Caps.ConceptKinds[req] != contract.CapabilitySupported {
				unmet = append(unmet, req)
			}
		}
		sort.Strings(unmet)
		targets = append(targets, TargetCapability{
			Target:         in.Target,
			ConceptKinds:   kinds,
			WriteToolOwned: in.Caps.WriteToolOwned,
			Progress:       in.Caps.Progress,
			RequiredUnmet:  unmet,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Target < targets[j].Target })
	return CapabilityReport{
		SchemaVersion: CapabilityReportSchemaVersion,
		GeneratedAt:   generatedAt,
		Targets:       targets,
	}
}

// AnyRequiredUnmet reports whether any target has an unmet required
// capability (a required-unmet condition fails the sync in both modes).
func (r CapabilityReport) AnyRequiredUnmet() bool {
	for _, t := range r.Targets {
		if len(t.RequiredUnmet) > 0 {
			return true
		}
	}
	return false
}

// MarshalCapability renders the report as stable indented JSON.
func MarshalCapability(r CapabilityReport) ([]byte, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("report: marshal capability: %w", err)
	}
	return append(data, '\n'), nil
}

// WriteCapabilityReport persists the report to
// .agent-sync/state/capability-report.json through the workspace root,
// creating the state dir if needed.
func WriteCapabilityReport(root *fsroot.Root, r CapabilityReport) error {
	data, err := MarshalCapability(r)
	if err != nil {
		return err
	}
	if err := root.Inner().MkdirAll(".agent-sync/state", 0o755); err != nil {
		return fmt.Errorf("report: ensure state dir: %w", err)
	}
	if err := root.StagedWrite(capabilityReportRel, data, 0o600); err != nil {
		return fmt.Errorf("report: write capability report: %w", err)
	}
	return nil
}

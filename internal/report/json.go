package report

import (
	"encoding/json"
	"fmt"
)

// SummarySchemaVersion is the pinned schema version of the --output=json
// document. CI consumers gate on this for stability.
const SummarySchemaVersion = 1

// jsonDocument is the stable --output=json shape. It wraps Summary with
// a schema version. Field order in Go does not affect json.Marshal key
// order (which follows struct field order), so this is deterministic.
type jsonDocument struct {
	SchemaVersion int            `json:"schema_version"`
	Workspace     string         `json:"workspace"`
	Commit        string         `json:"commit"`
	GeneratedAt   string         `json:"generated_at"`
	Mode          Mode           `json:"mode"`
	Targets       []TargetReport `json:"targets"`
	Summary       Outcome        `json:"summary"`
}

// MarshalJSON renders the sync summary as the stable, versioned
// --output=json document (indented, trailing newline). Deterministic for
// the same inputs: Summarize already sorts targets.
func MarshalJSON(s Summary) ([]byte, error) {
	doc := jsonDocument{
		SchemaVersion: SummarySchemaVersion,
		Workspace:     s.Workspace,
		Commit:        s.Commit,
		GeneratedAt:   s.GeneratedAt,
		Mode:          s.Mode,
		Targets:       s.Targets,
		Summary:       s.Outcome,
	}
	if doc.Targets == nil {
		doc.Targets = []TargetReport{}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("report: marshal json: %w", err)
	}
	return append(data, '\n'), nil
}

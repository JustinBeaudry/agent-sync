// Package validate renders the engine's dry-run plan as a drift report
// with stable exit codes, suitable as a CI drift guard. It is a thin
// presentation layer over engine.Plan: the engine computes the change
// set; this package shapes the output and maps drift to an exit code.
package validate

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/aienvs/aienvs/internal/engine"
)

// Exit codes for `aienvs validate`. Operational errors (2+) are produced
// by the command layer, not here.
const (
	ExitNoDrift = 0
	ExitDrift   = 1
)

// SchemaVersion is the validate JSON contract version.
const SchemaVersion = 1

// targetJSON is one target's change set in the JSON contract.
type targetJSON struct {
	Target      string   `json:"target"`
	WouldCreate []string `json:"would_create"`
	WouldUpdate []string `json:"would_update"`
	WouldDelete []string `json:"would_delete"`
	OutOfBand   []string `json:"out_of_band_modified"`
	Warnings    []string `json:"warnings"`
	Error       string   `json:"error,omitempty"`
}

type documentJSON struct {
	SchemaVersion int          `json:"schema_version"`
	Workspace     string       `json:"workspace"`
	Commit        string       `json:"commit"`
	DriftDetected bool         `json:"drift_detected"`
	Targets       []targetJSON `json:"targets"`
}

// ExitCode maps a plan to its drift exit code.
func ExitCode(plan engine.PlanResult) int {
	if plan.DriftDetected {
		return ExitDrift
	}
	return ExitNoDrift
}

// RenderText writes a human-readable drift report.
func RenderText(w io.Writer, plan engine.PlanResult) error {
	if !plan.DriftDetected {
		_, err := fmt.Fprintln(w, "No drift: all targets are up to date.")
		return err
	}
	for _, t := range plan.Targets {
		if _, err := fmt.Fprintf(w, "target %s:\n", t.Target); err != nil {
			return err
		}
		if t.Error != "" {
			if _, err := fmt.Fprintf(w, "  error: %s\n", t.Error); err != nil {
				return err
			}
			continue
		}
		writeList(w, "create", t.WouldCreate)
		writeList(w, "update", t.WouldUpdate)
		writeList(w, "delete", t.WouldDelete)
		writeList(w, "out-of-band-modified", t.OutOfBand)
		writeList(w, "warning", t.Warnings)
	}
	return nil
}

func writeList(w io.Writer, label string, items []string) {
	for _, it := range items {
		_, _ = fmt.Fprintf(w, "  %s: %s\n", label, it)
	}
}

// MarshalJSON renders the plan as the stable validate JSON contract.
func MarshalJSON(plan engine.PlanResult) ([]byte, error) {
	doc := documentJSON{
		SchemaVersion: SchemaVersion,
		Workspace:     plan.WorkspacePath,
		Commit:        plan.Commit,
		DriftDetected: plan.DriftDetected,
		Targets:       make([]targetJSON, 0, len(plan.Targets)),
	}
	for _, t := range plan.Targets {
		doc.Targets = append(doc.Targets, targetJSON{
			Target:      t.Target,
			WouldCreate: nonNil(t.WouldCreate),
			WouldUpdate: nonNil(t.WouldUpdate),
			WouldDelete: nonNil(t.WouldDelete),
			OutOfBand:   nonNil(t.OutOfBand),
			Warnings:    nonNil(t.Warnings),
			Error:       t.Error,
		})
	}
	return json.MarshalIndent(doc, "", "  ")
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

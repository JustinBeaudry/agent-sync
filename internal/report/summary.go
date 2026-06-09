// Package report builds the per-target sync summary, the persisted
// capability report, and the stable machine-readable --output=json
// document. It holds no wall-clock and does no color rendering: the
// caller stamps timestamps (deterministic output) and the CLI layer
// (Unit 16) applies color over the ASCII status tokens this package
// emits. Authoritative schemas: docs/spec/sync-summary-v1.md and
// docs/spec/capability-report-v1.md.
package report

import (
	"fmt"
	"sort"
	"strings"
)

// TargetStatus is the per-target outcome token. There is no per-target
// "partial" — partial is a top-line concept only (best-effort with a mix
// of ok and failed). Tokens are ASCII so colored and uncolored renders
// always carry the same literal token (decision R14 parity).
type TargetStatus string

const (
	StatusOK         TargetStatus = "ok"
	StatusFailed     TargetStatus = "failed"
	StatusSkipped    TargetStatus = "skipped"
	StatusUnchanged  TargetStatus = "unchanged"
	StatusRolledBack TargetStatus = "rolled-back" // atomic-mode preemption
	StatusBlocked    TargetStatus = "blocked"     // lock held
)

// Mode is the sync execution mode.
type Mode string

const (
	ModeAtomic     Mode = "atomic"
	ModeBestEffort Mode = "best-effort"
)

// Counts are the per-target operation tallies.
type Counts struct {
	Written   int `json:"written"`
	Deleted   int `json:"deleted"`
	Unchanged int `json:"unchanged"`
	Warnings  int `json:"warnings"`
}

// TargetReport is one target's result.
type TargetReport struct {
	Target     string       `json:"target"`
	Status     TargetStatus `json:"status"`
	Counts     Counts       `json:"counts"`
	DurationMs int64        `json:"duration_ms"`
	Paths      []string     `json:"paths"`
	Error      string       `json:"error,omitempty"`
}

// Outcome is the computed top-line verdict.
type Outcome struct {
	Line       string `json:"line"`
	OK         int    `json:"ok"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	Unchanged  int    `json:"unchanged"`
	RolledBack int    `json:"rolled_back"`
	Blocked    int    `json:"blocked"`
	ExitCode   int    `json:"exit_code"`
}

// Summary is the full per-sync report.
type Summary struct {
	Workspace   string         `json:"workspace"`
	Commit      string         `json:"commit"`
	GeneratedAt string         `json:"generated_at"`
	Mode        Mode           `json:"mode"`
	Targets     []TargetReport `json:"targets"`
	Outcome     Outcome        `json:"summary"`
}

// Summarize tallies targets and computes the top-line outcome. Targets
// are sorted by name so the output is byte-identical for the same inputs
// regardless of arrival order. The caller stamps generatedAt.
func Summarize(workspace, commit, generatedAt string, mode Mode, targets []TargetReport) Summary {
	sorted := append([]TargetReport(nil), targets...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Target < sorted[j].Target })
	for i := range sorted {
		if sorted[i].Paths == nil {
			sorted[i].Paths = []string{} // stable JSON: [] not null
		}
	}

	var o Outcome
	for _, t := range sorted {
		switch t.Status {
		case StatusOK:
			o.OK++
		case StatusUnchanged:
			o.Unchanged++
		case StatusFailed:
			o.Failed++
		case StatusSkipped:
			o.Skipped++
		case StatusRolledBack:
			o.RolledBack++
		case StatusBlocked:
			o.Blocked++
		}
	}
	succeeded := o.OK + o.Unchanged
	switch {
	case o.RolledBack > 0:
		// Atomic rollback: the whole sync failed and prior state is intact.
		o.Line = fmt.Sprintf("FAIL %s", counts(o))
		o.ExitCode = 1
	case o.Failed > 0 && succeeded > 0:
		o.Line = fmt.Sprintf("PARTIAL %s", counts(o))
		o.ExitCode = 1
	case o.Failed > 0 || o.Blocked > 0:
		o.Line = fmt.Sprintf("FAIL %s", counts(o))
		o.ExitCode = 1
	default:
		o.Line = fmt.Sprintf("OK %s", counts(o))
		o.ExitCode = 0
	}

	return Summary{
		Workspace:   workspace,
		Commit:      commit,
		GeneratedAt: generatedAt,
		Mode:        mode,
		Targets:     sorted,
		Outcome:     o,
	}
}

// counts renders the non-zero tally clauses in a stable order.
func counts(o Outcome) string {
	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(o.OK, "ok")
	add(o.Unchanged, "unchanged")
	add(o.Failed, "failed")
	add(o.Skipped, "skipped")
	add(o.RolledBack, "rolled-back")
	add(o.Blocked, "blocked")
	if len(parts) == 0 {
		return "0 ok"
	}
	return strings.Join(parts, ", ")
}

// RenderText renders a deterministic, ASCII, NO_COLOR-safe summary. The
// CLI layer may wrap target tokens with color, but the tokens and
// structure here are the parity baseline.
func RenderText(s Summary) string {
	var b strings.Builder
	for _, t := range s.Targets {
		fmt.Fprintf(&b, "  %-10s %s", t.Status, t.Target)
		if t.Counts.Written > 0 || t.Counts.Deleted > 0 {
			fmt.Fprintf(&b, " (+%d -%d)", t.Counts.Written, t.Counts.Deleted)
		}
		if t.Error != "" {
			fmt.Fprintf(&b, " — %s", t.Error)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "%s\n", s.Outcome.Line)
	return b.String()
}

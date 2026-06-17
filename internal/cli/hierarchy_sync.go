package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/agent-sync/agent-sync/internal/coverage"
	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/report"
)

// scopeOutcome is one discovered scope's sync result. Exactly one of Summary
// (Err == nil) or Err (a prepare/sync failure for this scope) is meaningful.
type scopeOutcome struct {
	Scope   hierarchy.Scope
	Summary report.Summary
	Err     error
	// Warnings are the coverage gaps for this scope: kinds it emits that the
	// target tools will not read natively at the scope's level. Empty for
	// project/user scopes (always native) and for scopes that fail to prepare.
	Warnings []coverage.Warning
}

// hierarchySyncOptions carries the per-run knobs the orchestrator applies to
// every scope's engine.Request, plus the user-scope toggle.
type hierarchySyncOptions struct {
	IncludeUser bool
	EngineOpts  engine.Options // mode, adopt, target filter, expect-deletions, logger, now
}

// runHierarchySync discovers the emit scopes from cwd and runs engine.Sync
// against each, in order. A scope whose prepare or sync fails is recorded in
// its scopeOutcome.Err and the run continues (continue-and-report). Discovery
// failure aborts the whole run (the scope set is indeterminate).
//
// Only emit scopes are synced: the user scope is emitted only when
// opts.IncludeUser is set (the --user flag), so a plain repo sync never writes
// under the home directory. The orchestration is a CLI-layer loop over the
// unmodified engine.Sync; each scope runs against its own fsroot root and so
// writes its own staging and ledger.
func runHierarchySync(ctx context.Context, rc *runtimeContext, cwd, home string, opts hierarchySyncOptions, now time.Time) ([]scopeOutcome, error) {
	scopes, err := hierarchy.Discover(cwd, hierarchy.Options{Home: home, IncludeUser: opts.IncludeUser})
	if err != nil {
		return nil, fmt.Errorf("discover hierarchy: %w", err)
	}

	var outcomes []scopeOutcome
	for _, sc := range scopes {
		if !sc.Emit {
			continue // read-only (user) scope shown elsewhere, not emitted
		}
		// Run each scope inside a closure so prep.Close runs via defer at the
		// end of the iteration: the same per-iteration close timing as a manual
		// Close, but robust against future early returns or a panic in
		// coverage.Analyze / engine.Sync.
		out := func() scopeOutcome {
			out := scopeOutcome{Scope: sc}
			prep, perr := prepareScope(ctx, rc, sc.Root, sc.ManifestPath, now)
			if perr != nil {
				out.Err = perr
				return out // continue-and-report
			}
			defer prep.Close()
			req := prep.Request
			req.Options = opts.EngineOpts
			// Coverage warnings are computed from the decoded IR (the distinct
			// kinds this scope emits), the manifest's targets, and the scope's
			// level. Computed after a successful prepare (nodes exist);
			// independent of the sync outcome.
			out.Warnings = coverage.Analyze(sc.Level, kindsOf(req.Nodes), req.Targets)
			summary, serr := engine.Sync(ctx, req)
			if serr != nil {
				out.Err = serr
			} else {
				out.Summary = summary
			}
			return out
		}()
		outcomes = append(outcomes, out)
	}
	return outcomes, nil
}

// kindsOf returns the distinct IR kinds present in nodes, preserving
// first-seen order. Used to feed coverage.Analyze the scope's emitted kinds.
func kindsOf(nodes []ir.Node) []ir.Kind {
	seen := make(map[ir.Kind]bool, len(nodes))
	var out []ir.Kind
	for _, n := range nodes {
		if seen[n.Kind] {
			continue
		}
		seen[n.Kind] = true
		out = append(out, n.Kind)
	}
	return out
}

// hierarchyExitCode is non-zero when any scope failed to prepare/sync or any
// scope's own summary reported a non-zero exit (continue-and-report: one bad
// scope fails the run without blocking the others' emit).
//
// It preserves the operational/trust exit codes the single-scope path would
// surface: a scope error carrying its own ExitCode() (e.g. the trust gate's
// exit 3/4/5 from materializeURL) is mapped via MapExit rather than collapsed
// to a flat 1. With multiple failing scopes the highest-severity specific code
// wins, since codes are assigned in ascending severity (1 generic failure < 2
// usage < 3/4/5 trust). An ordinary sync-failure summary (no carried code)
// stays at exit 1.
func hierarchyExitCode(outcomes []scopeOutcome) int {
	code := 0
	for _, o := range outcomes {
		c := scopeExitCode(o)
		if c > code {
			code = c
		}
	}
	return code
}

// scopeExitCode is the exit code a single scope contributes: 0 when clean, the
// scope error's mapped code (MapExit unwraps any exitCoder, e.g. the trust
// gate's specific code), or 1 for a summary that reported a non-zero verdict.
func scopeExitCode(o scopeOutcome) int {
	if o.Err != nil {
		return MapExit(o.Err)
	}
	if o.Summary.Outcome.ExitCode != 0 {
		return o.Summary.Outcome.ExitCode
	}
	return 0
}

// renderHierarchyText writes a per-scope block to w: a header naming the
// level and root, then either the scope's report text (success) or its error
// line (prepare/sync failure). Mirrors the spacing of renderSummary in
// cmd_sync.go (report.RenderText already carries the body's formatting).
func renderHierarchyText(w io.Writer, outcomes []scopeOutcome) error {
	for i, o := range outcomes {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "== %s: %s ==\n", o.Scope.Level, o.Scope.Root); err != nil {
			return err
		}
		if o.Err != nil {
			if _, err := fmt.Fprintf(w, "ERROR: %v\n", o.Err); err != nil {
				return err
			}
		} else if _, err := fmt.Fprint(w, report.RenderText(o.Summary)); err != nil {
			return err
		}
		// Coverage warnings are computed independent of the sync outcome (a
		// scope can fail to sync after a successful prepare), so render them
		// even for errored scopes — matching the JSON output, which always
		// carries coverage_warnings.
		for _, warn := range o.Warnings {
			if _, err := fmt.Fprintf(w, "  warning: %s\n", warn.Detail); err != nil {
				return err
			}
		}
	}
	return nil
}

// hierarchyScopeJSON is one scope's entry in the aggregate JSON document.
// Exactly one of Summary or Error is populated, matching scopeOutcome.
type hierarchyScopeJSON struct {
	Root     string             `json:"root"`
	Level    string             `json:"level"`
	Summary  *report.Summary    `json:"summary,omitempty"`
	Error    string             `json:"error,omitempty"`
	Warnings []coverage.Warning `json:"coverage_warnings,omitempty"`
}

// renderHierarchyJSON writes the aggregate machine-readable document: one
// entry per emit scope plus the run-wide exit code. The embedded summary uses
// the report.Summary JSON tags (the same shape report.MarshalJSON emits).
func renderHierarchyJSON(w io.Writer, outcomes []scopeOutcome) error {
	scopes := make([]hierarchyScopeJSON, 0, len(outcomes))
	for _, o := range outcomes {
		entry := hierarchyScopeJSON{
			Root:     o.Scope.Root,
			Level:    o.Scope.Level.String(),
			Warnings: o.Warnings,
		}
		if o.Err != nil {
			entry.Error = o.Err.Error()
		} else {
			// Embed the per-scope summary raw: the aggregate is marshaled
			// below via json.Marshal(doc), which uses report.Summary's struct
			// tags directly rather than report.MarshalJSON. This is
			// intentional. The aggregate document carries its own top-level
			// schema_version, and each embedded summary's JSON matches the
			// standalone report shape minus that version wrapper (targets is
			// populated by Summarize, not by the marshaler). Per-scope
			// summaries deliberately do not repeat schema_version.
			s := o.Summary
			entry.Summary = &s
		}
		scopes = append(scopes, entry)
	}
	doc := struct {
		SchemaVersion int                  `json:"schema_version"`
		Scopes        []hierarchyScopeJSON `json:"scopes"`
		ExitCode      int                  `json:"exit_code"`
	}{
		SchemaVersion: 1,
		Scopes:        scopes,
		ExitCode:      hierarchyExitCode(outcomes),
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("cli: marshal hierarchy json: %w", err)
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

// resolveHome resolves the user's home directory for discovery. It is a
// package-level var so tests can swap it and keep the suite hermetic (never
// touching the real ~); production uses homeDir.
var resolveHome = homeDir

// homeDir resolves the user's home directory for discovery.
func homeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cli: resolve home: %w", err)
	}
	return h, nil
}

// errUserWithWorkspace is returned when --user is combined with an explicit
// --workspace override; the two are mutually exclusive because --workspace
// pins a single scope and --user requests the user scope of the hierarchy.
var errUserWithWorkspace = errors.New("cli: --user cannot be combined with --workspace (--workspace pins a single scope)")

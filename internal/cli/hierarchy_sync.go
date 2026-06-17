package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/report"
)

// scopeOutcome is one discovered scope's sync result. Exactly one of Summary
// (Err == nil) or Err (a prepare/sync failure for this scope) is meaningful.
type scopeOutcome struct {
	Scope   hierarchy.Scope
	Summary report.Summary
	Err     error
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
		out := scopeOutcome{Scope: sc}
		prep, perr := prepareScope(ctx, rc, sc.Root, sc.ManifestPath, now)
		if perr != nil {
			out.Err = perr
			outcomes = append(outcomes, out)
			continue // continue-and-report
		}
		req := prep.Request
		req.Options = opts.EngineOpts
		summary, serr := engine.Sync(ctx, req)
		prep.Close()
		if serr != nil {
			out.Err = serr
		} else {
			out.Summary = summary
		}
		outcomes = append(outcomes, out)
	}
	return outcomes, nil
}

// hierarchyExitCode is non-zero when any scope failed to prepare/sync or any
// scope's own summary reported a non-zero exit (continue-and-report: one bad
// scope fails the run without blocking the others' emit).
func hierarchyExitCode(outcomes []scopeOutcome) int {
	for _, o := range outcomes {
		if o.Err != nil || o.Summary.Outcome.ExitCode != 0 {
			return 1
		}
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
			continue
		}
		if _, err := fmt.Fprint(w, report.RenderText(o.Summary)); err != nil {
			return err
		}
	}
	return nil
}

// hierarchyScopeJSON is one scope's entry in the aggregate JSON document.
// Exactly one of Summary or Error is populated, matching scopeOutcome.
type hierarchyScopeJSON struct {
	Root    string          `json:"root"`
	Level   string          `json:"level"`
	Summary *report.Summary `json:"summary,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// renderHierarchyJSON writes the aggregate machine-readable document: one
// entry per emit scope plus the run-wide exit code. The embedded summary uses
// the report.Summary JSON tags (the same shape report.MarshalJSON emits).
func renderHierarchyJSON(w io.Writer, outcomes []scopeOutcome) error {
	scopes := make([]hierarchyScopeJSON, 0, len(outcomes))
	for _, o := range outcomes {
		entry := hierarchyScopeJSON{
			Root:  o.Scope.Root,
			Level: o.Scope.Level.String(),
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

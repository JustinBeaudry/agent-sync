package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/agent-sync/agent-sync/internal/coverage"
	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/report"
)

// cursorTarget is the adapter name Cursor-rule composition keys on. Kept as a
// local literal rather than importing the cursor adapter: composition is a
// CLI-orchestration concern and must not couple to adapter internals.
const cursorTarget = "cursor"

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
	Frozen      bool
	PostMerge   bool
	EngineOpts  engine.Options // mode, adopt, target filter, expect-deletions, logger, now
	// OfferUser, when non-nil and IncludeUser is false, is invoked once when
	// discovery finds a user-level manifest that would otherwise be skipped
	// (plan R16). Returning true includes the user scope in this run exactly
	// as --user would; returning false skips it AND suppresses the
	// skipped-user notice (the user answered — no nagging). nil means "never
	// ask" — the non-interactive/scripted path. The callback is the TTY seam:
	// the orchestrator itself never touches stdin.
	OfferUser func(manifestPath string) bool
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
//
// The returned notice is non-empty only when the run produced zero emit scopes:
// it explains why nothing was synced (user manifest needs --user, or no manifest
// exists) so an empty run is never a silent no-op. It is advisory — exit code
// stays 0.
func runHierarchySync(ctx context.Context, rc *runtimeContext, cwd, home string, opts hierarchySyncOptions, now time.Time) ([]scopeOutcome, string, error) {
	scopes, err := hierarchy.Discover(cwd, hierarchy.Options{Home: home, IncludeUser: opts.IncludeUser})
	if err != nil {
		return nil, "", fmt.Errorf("discover hierarchy: %w", err)
	}

	// User-scope offer (plan R16): a user manifest discovered read-only gets
	// one chance to join the run. An accepted offer re-discovers with the
	// user scope emitted — identical to --user. A declined offer is recorded
	// so the skipped-user notice below stays quiet for this run.
	userDeclined := false
	if !opts.IncludeUser && opts.OfferUser != nil {
		if us, ok := skippedUserScope(scopes); ok {
			if opts.OfferUser(us.ManifestPath) {
				scopes, err = hierarchy.Discover(cwd, hierarchy.Options{Home: home, IncludeUser: true})
				if err != nil {
					return nil, "", fmt.Errorf("discover hierarchy: %w", err)
				}
			} else {
				userDeclined = true
			}
		}
	}

	// composeActive records whether Cursor-rule composition fired for any
	// project scope in this run. Under `sync --user` the user scope is emitted
	// (and processed before the project scope), so its coverage warnings are
	// post-filtered after the loop once composeActive is known. See U5/D5.
	composeActive := false

	var outcomes []scopeOutcome
	for _, sc := range scopes {
		// Honor cancellation between scopes: a cancelled sync (Ctrl-C, deadline)
		// must not keep emitting subsequent scopes. engine.Sync also respects ctx,
		// but this stops the loop before the next scope's prepare/compose work.
		if err := ctx.Err(); err != nil {
			return outcomes, "", err
		}
		if !sc.Emit {
			continue // read-only (user) scope shown elsewhere, not emitted
		}
		// Run each scope inside a closure so prep.Close runs via defer at the
		// end of the iteration: the same per-iteration close timing as a manual
		// Close, but robust against future early returns or a panic in
		// coverage.Analyze / engine.Sync.
		out := func() scopeOutcome {
			out := scopeOutcome{Scope: sc}
			prep, perr := prepareScopeForSync(ctx, rc, sc.Root, sc.ManifestPath, sc.Level.String(), now, syncPrepareOptions{Frozen: opts.Frozen, PostMerge: opts.PostMerge})
			if perr != nil {
				out.Err = perr
				return out // continue-and-report
			}
			defer prep.Close()
			req := prep.Request
			req.Options = opts.EngineOpts
			req.Options.RunLockHeld = prep.RunLockHeld
			// Fold the user-scope Cursor rule layer into this project scope's node
			// set (plan U4/D1/D2), via the shared entry point also used by the
			// single-scope path. composeActive gates the U5 coverage-warning
			// suppression below — set only when rules were actually composed.
			if applyCursorComposition(ctx, rc, &req, prep.Manifest, sc.Level.String(), home, now) {
				composeActive = true
			}
			// Coverage warnings are computed from the decoded IR (the distinct
			// kinds this scope emits), the manifest's targets, and the scope's
			// level. Computed after a successful prepare (nodes exist);
			// independent of the sync outcome.
			out.Warnings = coverage.Analyze(sc.Level, kindsOf(req.Nodes), req.Targets)
			summary, serr := engine.Sync(ctx, req)
			if serr != nil {
				if prep.PinMovedTo != "" {
					out.Err = pinMovedSyncError(prep.PinMovedTo, serr)
				} else {
					out.Err = serr
				}
			} else {
				out.Summary = summary
				if prep.PinMovedTo != "" && summary.Outcome.ExitCode != 0 {
					out.Err = pinMovedSyncError(prep.PinMovedTo, fmt.Errorf("sync reported failures: %s", summary.Outcome.Line))
					out.Summary = report.Summary{}
				}
			}
			return out
		}()
		outcomes = append(outcomes, out)
	}

	// U5/D5: once user rules compose into the project's .cursor/rules/, the
	// user-scope "Cursor rule is inert at user scope" coverage warning is
	// misleading — the rule DOES take effect, via the project. Under `sync
	// --user` the user scope is emitted (and carries that warning), so drop just
	// that one warning from its outcome when composition fired this run. The
	// agents-md user warning is not composed and stays. Composition-off runs are
	// untouched. Caller-side filter keeps coverage.Analyze pure.
	if composeActive {
		for i := range outcomes {
			if outcomes[i].Scope.Level == hierarchy.LevelUser {
				outcomes[i].Warnings = dropWarning(outcomes[i].Warnings, cursorTarget, ir.KindRule, hierarchy.LevelUser)
			}
		}
	}
	var notice string
	switch {
	case len(outcomes) == 0:
		notice = emptyRunNotice(scopes, cwd)
	case !userDeclined:
		// Plan R17: a user manifest that exists but was not synced is worth a
		// persistent pointer even when project scopes emitted fine — today's
		// silence is how user-scope config quietly drifts stale.
		if us, ok := skippedUserScope(scopes); ok {
			notice = fmt.Sprintf("user-level manifest at %s was not synced; pass --user to include it", us.ManifestPath)
		}
	}
	return outcomes, notice, nil
}

// skippedUserScope returns the discovered user-level scope that is not being
// emitted this run (a plain sync never writes home), when one exists.
func skippedUserScope(scopes []hierarchy.Scope) (hierarchy.Scope, bool) {
	for _, sc := range scopes {
		if sc.Level == hierarchy.LevelUser && !sc.Emit {
			return sc, true
		}
	}
	return hierarchy.Scope{}, false
}

// emptyRunNotice explains a hierarchy sync that discovered zero emit scopes.
// Without it the run prints an empty report and exits 0 — indistinguishable
// from a successful no-op, which reads as "sync doesn't do anything". Two
// cases: discovery found a user-home manifest but it is read-only without
// --user (the plain-sync-never-writes-home invariant), or no manifest exists
// at all.
func emptyRunNotice(scopes []hierarchy.Scope, cwd string) string {
	for _, sc := range scopes {
		if sc.Level == hierarchy.LevelUser && !sc.Emit {
			return fmt.Sprintf("manifest at %s is the user scope and a plain sync never writes to the home directory; run 'agent-sync sync --user' to sync it", sc.ManifestPath)
		}
	}
	return fmt.Sprintf("no .agent-sync.yaml found from %s up to the project root; run 'agent-sync init' to create one", cwd)
}

// dropWarning returns ws without any warning matching (target, kind, level).
// Used to suppress the user-scope Cursor rule warning when composition makes it
// misleading (U5). The input slice is not mutated.
func dropWarning(ws []coverage.Warning, target string, kind ir.Kind, level hierarchy.Level) []coverage.Warning {
	out := make([]coverage.Warning, 0, len(ws))
	for _, w := range ws {
		if w.Target == target && w.Kind == kind && w.Level == level {
			continue
		}
		out = append(out, w)
	}
	return out
}

// applyCursorComposition folds the user-scope Cursor rule layer into a
// project-scope request when the project manifest opts in. It is the single
// composition entry point shared by the hierarchy sync loop AND the single-scope
// path (validate, watch, sync --workspace), so every command that builds a
// project request sees the same composed desired state. Without this, a composed
// project would report false `WouldDelete` drift under validate and lose its
// composed rules under watch / --workspace sync (the owned-subdir swap wipes
// anything not in the freshly-staged set).
//
// It mutates req in place and returns whether rules were composed. All paths are
// gated on m.Compose.CursorRulesFromUser:
//   - scope != project: composition is project-only (D1) — warn that the opt-in
//     has no effect at this scope, return false.
//   - cursor not a target, or no user manifest at home: silent no-op, false.
//   - user source unreadable: defer cursor (drop it from req.Targets) so a
//     transient failure can't wipe previously-composed rules; warn; false.
//   - rules composed: append to req.Nodes; true.
func applyCursorComposition(ctx context.Context, rc *runtimeContext, req *engine.Request, m *manifest.Manifest, scope, home string, now time.Time) bool {
	if !m.Compose.CursorRulesFromUser {
		return false
	}
	if scope != hierarchy.LevelProject.String() {
		// Directory/user manifest set the opt-in; composition only applies at
		// project scope. Warn so it isn't a silent no-op.
		rc.Logger.Warn("compose: cursor-rules-from-user has no effect at this scope; set it on the project manifest", "scope", scope)
		return false
	}
	if !slices.Contains(req.Targets, cursorTarget) {
		return false
	}
	user, ok := hierarchy.UserScope(home)
	if !ok {
		return false // no user manifest to compose from
	}
	composed, failed := composeUserRules(ctx, rc, user, cursorRuleIDsOf(req.Nodes), now)
	switch {
	case failed:
		// The user source could not be read this run (offline URL, malformed user
		// manifest). Defer the Cursor sync rather than syncing project-only rules:
		// .cursor/rules/agent-sync/ is an owned subdir replaced by a wholesale
		// swap, so a project-only sync would wipe the previously-composed rules.
		// Dropping cursor from this run's targets leaves that subdir untouched; the
		// next sync that can read the user source re-syncs cursor fully. Project
		// cursor rule edits wait for that run — the conservative, data-preserving
		// choice (plan D8, transient-failure guard).
		req.Targets = withoutTarget(req.Targets, cursorTarget)
		rc.Logger.Warn("compose: deferring cursor this run — user source unreadable; existing .cursor/rules left intact",
			"user_manifest", user.ManifestPath)
		return false
	case len(composed) > 0:
		req.Nodes = append(req.Nodes, composed...)
		return true
	}
	return false
}

// composeUserRules materializes the user-scope manifest read-only and returns
// its Cursor `rule` nodes for injection into a project sync (plan D1). The
// project root remains the only write target; the user root is opened solely to
// read IR.
//
// It is best-effort (plan D8): any failure — load, open, materialize, offline
// or uncached remote source, decode — yields nil plus a warning and never
// propagates. A project sync must not fail, block on a trust prompt, or hit the
// network because of the user's global manifest. Remote (URL) user sources are
// materialized OFFLINE (cache-only) so composition never fetches; a user who
// wants a remote source composed pre-populates the cache with `sync --user`.
//
// projectRuleIDs is the set of `rule` ids already present in the project scope.
// A user rule whose id collides is dropped (project wins, matching Cursor's
// Team>Project>User precedence) with a per-id warning: the id namespace is flat,
// so a coincidental clash must be observable rather than a silent data loss.
//
// The returned failed flag distinguishes "could not read the user source"
// (load/open/materialize error → failed=true) from "read fine, produced zero
// composable rules" (failed=false). The caller defers the whole Cursor sync when
// failed, so a transient read failure (offline URL, malformed user manifest)
// leaves the previously-composed rules in the owned .cursor/rules/ subdir intact
// rather than letting the subdir swap wipe them — see the deferral at the call
// site (plan D8, transient-failure guard).
func composeUserRules(ctx context.Context, rc *runtimeContext, user hierarchy.Scope, projectRuleIDs map[string]struct{}, now time.Time) (nodes []ir.Node, failed bool) {
	// Deliberately NOT prepareScope: this reads user IR only. prepareScope also
	// runs DiscoverAdapters (which can launch adapter subprocesses) and builds a
	// full emit Request — neither is wanted for a read-only compose. Keep the
	// minimal LoadFile + OpenWorkspaceRoot + materialize path here (D8); do not
	// "consolidate" it back into prepareScope.
	log := rc.Logger
	m, err := manifest.LoadFile(user.ManifestPath, manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		log.Warn("compose: cannot read user manifest", "path", user.ManifestPath, "err", err)
		return nil, true
	}
	root, err := fsroot.OpenWorkspaceRoot(user.Root)
	if err != nil {
		log.Warn("compose: cannot open user root", "root", user.Root, "err", err)
		return nil, true
	}
	defer func() { _ = root.Close() }()

	// Never fetch a remote user source during a project sync: force offline for
	// URL canonicals so an uncached remote soft-no-ops instead of hitting the
	// network. Local sources are unaffected; an explicit --offline still applies.
	offline := rc.Flags.Offline || m.Canonical.URL != ""
	mat, err := materialize(ctx, m, materializeOptions{Offline: offline, Now: now, Root: root})
	if err != nil {
		if ctx.Err() != nil {
			// Cancellation (Ctrl-C, deadline) — not an unreadable user source. The
			// sync is aborting anyway; don't defer cursor or log a misleading
			// "source unreadable" warning. Return not-failed so no deferral fires.
			return nil, false
		}
		log.Warn("compose: cannot materialize user IR", "path", user.ManifestPath, "err", err)
		return nil, true
	}

	var out []ir.Node
	for _, n := range mat.Nodes {
		if n.Kind != ir.KindRule || !nodeTargetsCursor(n) {
			continue
		}
		if _, clash := projectRuleIDs[n.ID]; clash {
			log.Warn("compose: user rule shadowed by project rule of the same id", "id", n.ID)
			continue
		}
		// Pin delivery to Cursor only (D1). A user rule with empty frontmatter
		// targets means "all adapters"; once injected into the project node set it
		// would otherwise also be written to other rule-supporting targets (e.g.
		// claude's .claude/rules/). Constrain the injected copy so composition
		// stays Cursor-only, matching the flag name and docs. n is a range copy;
		// reassigning its Targets does not mutate the source IR.
		n.Targets = []string{cursorTarget}
		// Per-node provenance override (plan U2, resolved Open Question): a
		// composed node's content comes from the USER scope's source, not the
		// project's, so its managed header must say so. mat.SourceURL is
		// already the audit-safe form (canonicalized URL or local path).
		n.SourceURL = mat.SourceURL
		n.SourceCommit = mat.Commit
		out = append(out, n)
	}
	// Deterministic injection: sort the composed subset by id and append it after
	// the project's own nodes (whose order is left untouched). Keeps op/ledger
	// output and the coverage kind set stable across runs.
	slices.SortFunc(out, func(a, b ir.Node) int { return cmp.Compare(a.ID, b.ID) })
	return out, false
}

// withoutTarget returns a copy of targets with drop removed. Used to defer the
// Cursor sync for one run when composition cannot read the user source, so the
// owned .cursor/rules/ subdir is not swapped (and previously-composed rules are
// not wiped). Returns a fresh slice so the caller's other Request copies keep
// the full target list.
func withoutTarget(targets []string, drop string) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		if t != drop {
			out = append(out, t)
		}
	}
	return out
}

// nodeTargetsCursor reports whether an IR node is delivered to the Cursor
// adapter: an empty Targets list means "all adapters", otherwise it must name
// cursor. Mirrors the cursor adapter's own predicate without importing it.
func nodeTargetsCursor(n ir.Node) bool {
	if len(n.Targets) == 0 {
		return true
	}
	return slices.Contains(n.Targets, cursorTarget)
}

// cursorRuleIDsOf returns the set of ids of the project's `rule` nodes that are
// delivered to Cursor, for collision detection against composed user rules.
// Restricting to cursor-targeted rules matches the real .cursor/rules namespace:
// a project rule targeting only a non-cursor adapter never lands in
// .cursor/rules, so it must not shadow (and suppress) a composable user rule.
func cursorRuleIDsOf(nodes []ir.Node) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, n := range nodes {
		if n.Kind == ir.KindRule && nodeTargetsCursor(n) {
			ids[n.ID] = struct{}{}
		}
	}
	return ids
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
// A non-empty notice renders as the nothing-to-sync line on a zero-emit run,
// or as a trailing note: line after the scope blocks (plan R17).
func renderHierarchyText(w io.Writer, outcomes []scopeOutcome, notice string) error {
	if notice != "" && len(outcomes) == 0 {
		if _, err := fmt.Fprintf(w, "nothing to sync: %s\n", notice); err != nil {
			return err
		}
	}
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
	if notice != "" && len(outcomes) > 0 {
		if _, err := fmt.Fprintf(w, "\nnote: %s\n", notice); err != nil {
			return err
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
// notice is additive (omitempty) and set only on zero-emit runs, so existing
// schema_version 1 consumers are unaffected.
func renderHierarchyJSON(w io.Writer, outcomes []scopeOutcome, notice string) error {
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
		Notice        string               `json:"notice,omitempty"`
		ExitCode      int                  `json:"exit_code"`
	}{
		SchemaVersion: 1,
		Scopes:        scopes,
		Notice:        notice,
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

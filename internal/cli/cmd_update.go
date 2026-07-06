package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/agent-sync/agent-sync/internal/cache"
	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/git"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/locks"
	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/trust"
	"github.com/agent-sync/agent-sync/internal/workspace"
)

// exitUpdatePinMoved is the distinct process exit code for the one state
// `update` can leave that is neither "clean" nor "nothing changed": the
// manifest was re-pinned to the new commit but the subsequent sync failed,
// so the trust anchor advanced while the emitted files still reflect the
// old commit. It is deliberately outside the trust-gate family (3/4/5) and
// the generic 1/2 so scripts can detect exactly this recover-by-re-syncing
// condition. See R8b in the plan.
const exitUpdatePinMoved = 6

// changeSummaryMaxLines caps how many commit subjects the confirmation
// gate prints between the old and new pin.
const changeSummaryMaxLines = 20

// updateRunLockTimeout bounds how long update waits for the workspace run
// lock. Long enough to reliably win an uncontended lock past scheduling
// jitter, short enough that a genuinely-held lock refuses "up front"
// rather than blocking the user.
const updateRunLockTimeout = 2 * time.Second

var (
	// errUpdateNeedsAccept is the non-interactive refusal: a newer commit
	// is available but no acceptance flag was passed. Exit code mirrors the
	// trust-decision-required convention (4) — moving the pin is a trust
	// decision.
	errUpdateNeedsAccept = errors.New("update: a newer upstream commit is available; re-run with --accept-update=<sha> to move the pin (or --accept-rewritten-history=<sha> if history was rewritten)")
)

func newUpdateCommand(deps RootDeps) *cobra.Command {
	var (
		acceptUpdate  string
		acceptRewrite string
		userScope     bool
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Fetch new upstream commits and re-pin the canonical source",
		Long: "Resolve the canonical source's configured ref against the remote, " +
			"show what changed since the current pin, then (on acceptance) re-pin " +
			"commit + trusted_sha and run a normal sync — all under one workspace " +
			"run lock. Fast-forward only: a rewritten upstream history is refused " +
			"unless explicitly overridden.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := mustRuntime(cmd)
			if err != nil {
				return err
			}
			if rc.Flags.Workspace != "" && userScope {
				return errUserWithWorkspace
			}
			now := deps.now()()
			return runUpdate(cmd, rc, updateFlags{
				acceptUpdate:  acceptUpdate,
				acceptRewrite: acceptRewrite,
				userScope:     userScope,
			}, now)
		},
	}

	cmd.Flags().StringVar(&acceptUpdate, "accept-update", "", "non-interactive: accept moving the pin to this resolved SHA")
	cmd.Flags().StringVar(&acceptRewrite, "accept-rewritten-history", "", "override the fast-forward guard for this resolved SHA (history was rewritten upstream)")
	cmd.Flags().BoolVar(&userScope, "user", false, "update the user-level (~) manifest instead of the nearest workspace")
	return cmd
}

type updateFlags struct {
	acceptUpdate  string
	acceptRewrite string
	userScope     bool
}

// runUpdate resolves the scope, then drives the update state machine under
// the workspace run lock. The lock is held across the manifest re-pin AND
// the sync so a concurrent sync can never interleave between them (R8b).
func runUpdate(cmd *cobra.Command, rc *runtimeContext, flags updateFlags, now time.Time) error {
	scopeRoot, manifestPath, err := resolveUpdateScope(rc, flags.userScope)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	m, err := manifest.LoadFile(manifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
	if err != nil {
		return fmt.Errorf("update: load manifest: %w", err)
	}

	// Non-git sources have nothing to fetch. Report cleanly and exit 0.
	switch {
	case m.Canonical.LocalDir != "":
		return writeUpdateLine(cmd, "up to date: local_dir source has no pin to update")
	case m.Canonical.URL == "" && m.Canonical.LocalPath == "":
		return errors.New("update: manifest has no canonical source")
	}

	if rc.Flags.Offline && m.Canonical.URL != "" {
		return &exitError{code: exitUsage, err: errors.New("update: --offline cannot reach the remote to resolve a newer commit")}
	}

	// Acquire the workspace run lock up front. Held across the re-pin and the
	// sync (engine.Sync runs with RunLockHeld=true). Contention is a clean
	// refusal — the manifest is never touched.
	root, err := fsroot.OpenWorkspaceRoot(scopeRoot)
	if err != nil {
		return fmt.Errorf("update: open workspace: %w", err)
	}
	defer func() { _ = root.Close() }()

	runLock, err := locks.NewRunLock(root)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	release, err := runLock.Acquire(cmd.Context(), locks.AcquireOpts{Timeout: updateRunLockTimeout})
	if err != nil {
		if errors.Is(err, locks.ErrRunLocked) {
			return &exitError{code: exitFailure, err: errors.New("update: another agent-sync run holds this workspace; try again when it finishes")}
		}
		return fmt.Errorf("update: acquire run lock: %w", err)
	}
	defer func() { _ = release() }()

	// Resolve the new SHA + a current local mirror to inspect. Any failure
	// here is before the gate: the manifest stays byte-identical.
	res, err := resolveUpdate(cmd.Context(), m)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	oldPin := m.Canonical.Commit
	if res.newSHA == oldPin {
		return writeUpdateLine(cmd, fmt.Sprintf("up to date: %s is already pinned at %s", res.refName, shortDisplaySHA(oldPin)))
	}

	// Fast-forward guard (R10): the old pin must be an ancestor of the new
	// SHA. A non-fast-forward result means history was rewritten upstream;
	// refuse unless the caller passed the distinct override flag.
	ff := true
	if oldPin != "" && res.mirrorPath != "" {
		ff, err = git.IsAncestor(cmd.Context(), res.mirrorPath, oldPin, res.newSHA)
		if err != nil {
			// oldPin unreachable in the mirror is itself a rewrite signal;
			// treat as non-fast-forward rather than a hard error.
			ff = false
		}
	}
	if !ff {
		if flags.acceptRewrite != res.newSHA {
			return &exitError{code: trust.ExitTrustDecisionRequired, err: fmt.Errorf(
				"update: upstream history was rewritten — the current pin %s is not an ancestor of %s (%s). "+
					"Review the change, then re-run with --accept-rewritten-history=%s to move the pin anyway",
				shortDisplaySHA(oldPin), shortDisplaySHA(res.newSHA), res.refName, res.newSHA)}
		}
	}

	// Gate. Non-interactive requires the exact acceptance flag; interactive
	// shows old→new + ref + change summary and asks.
	ok, err := confirmUpdate(cmd, rc, m, oldPin, res, ff, flags)
	if err != nil {
		return err
	}
	if !ok {
		return writeUpdateLine(cmd, "update declined; manifest unchanged")
	}

	// Re-pin: commit + trusted_sha together, atomic, comment-preserving.
	orig, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("update: read manifest: %w", err)
	}
	updated, err := manifest.WriteResolvedSHA(orig, res.newSHA, res.newSHA)
	if err != nil {
		return fmt.Errorf("update: rewrite pin: %w", err)
	}
	if err := manifest.WriteFile(manifestPath, updated); err != nil {
		return fmt.Errorf("update: write manifest: %w", err)
	}

	// Sync the scope under the lock we already hold (RunLockHeld=true).
	if err := syncAfterRepin(cmd, rc, scopeRoot, manifestPath, flags.userScope, now); err != nil {
		return &exitError{code: exitUpdatePinMoved, err: fmt.Errorf(
			"update: pin moved to %s but the sync did not land: %w. Re-run `agent-sync sync` to materialize it",
			shortDisplaySHA(res.newSHA), err)}
	}

	return writeUpdateLine(cmd, fmt.Sprintf("updated: %s → %s (%s), synced", shortDisplaySHA(oldPin), shortDisplaySHA(res.newSHA), res.refName))
}

// updateResolution is what resolveUpdate discovers before the gate.
type updateResolution struct {
	newSHA      string
	refName     string // the ref that was resolved (manifest ref, or "HEAD (remote default)")
	mirrorPath  string // local mirror to inspect for the change summary / ancestry (empty for local_path)
	refFromHEAD bool   // true when the manifest had no ref and HEAD was followed
}

// resolveUpdate resolves the new SHA and a mirror to inspect, without
// touching the manifest. URL sources fetch + resolve online; local_path
// sources resolve the ref in the local clone (no network).
func resolveUpdate(ctx context.Context, m *manifest.Manifest) (updateResolution, error) {
	if m.Canonical.LocalPath != "" {
		ref := m.Canonical.Ref
		refName := ref
		fromHEAD := false
		if ref == "" {
			ref = "HEAD"
			refName = "HEAD (local default)"
			fromHEAD = true
		}
		sha, err := git.ResolveLocalRef(ctx, m.Canonical.LocalPath, ref)
		if err != nil {
			return updateResolution{}, fmt.Errorf("resolve local ref: %w", err)
		}
		return updateResolution{newSHA: sha, refName: refName, mirrorPath: m.Canonical.LocalPath, refFromHEAD: fromHEAD}, nil
	}

	canonical, err := cache.Canonicalize(m.Canonical.URL)
	if err != nil {
		return updateResolution{}, fmt.Errorf("canonicalize: %w", err)
	}
	loc, err := cache.Resolve(canonical, cache.ResolveOptions{Override: m.Cache.Override})
	if err != nil {
		return updateResolution{}, fmt.Errorf("resolve cache: %w", err)
	}
	ref := m.Canonical.Ref
	refName := ref
	fromHEAD := false
	if ref == "" {
		ref = "HEAD"
		refName = "HEAD (remote default)"
		fromHEAD = true
	}
	// Floating materialize fetches (or clones) the mirror and resolves the
	// ref online, returning the new SHA + the mirror path.
	mres, err := git.Materialize(ctx, git.Input{
		CanonicalURL: canonical,
		Cache:        loc,
		Ref:          ref,
		Floating:     true,
	})
	if err != nil {
		return updateResolution{}, fmt.Errorf("resolve remote ref: %w", err)
	}
	return updateResolution{newSHA: mres.ResolvedSHA, refName: refName, mirrorPath: mres.LocalPath, refFromHEAD: fromHEAD}, nil
}

// confirmUpdate renders the gate and returns whether to proceed.
// Non-interactive: the exact acceptance flag must match res.newSHA
// (or, for a rewrite, acceptRewrite already matched at the guard).
func confirmUpdate(cmd *cobra.Command, rc *runtimeContext, m *manifest.Manifest, oldPin string, res updateResolution, ff bool, flags updateFlags) (bool, error) {
	if rc.Access.NonInteractive {
		// A rewrite that got here already passed the distinct override flag.
		if !ff {
			return true, nil
		}
		if flags.acceptUpdate == res.newSHA {
			return true, nil
		}
		return false, &exitError{code: trust.ExitTrustDecisionRequired, err: errUpdateNeedsAccept}
	}

	out := cmd.OutOrStdout()
	var b strings.Builder
	fmt.Fprintf(&b, "Update %s\n", displaySource(m))
	fmt.Fprintf(&b, "  ref: %s\n", res.refName)
	fmt.Fprintf(&b, "  pin: %s → %s\n", shortDisplaySHA(oldPin), shortDisplaySHA(res.newSHA))
	if !ff {
		b.WriteString("  WARNING: history was rewritten — the old pin is not an ancestor of the new commit\n")
	}
	if summary := changeSummaryOrEmpty(cmd.Context(), res, oldPin); summary != "" {
		fmt.Fprintf(&b, "\n%s\n\n", summary)
	}
	if res.refFromHEAD {
		fmt.Fprintf(&b, "note: no `ref` is set in the manifest; following %s. Add `canonical.ref: <branch>` to regain the per-sync reachability defense.\n", res.refName)
	}
	if _, err := io.WriteString(out, b.String()); err != nil {
		return false, err
	}

	p := trust.NewPrompter(stdinOrEmpty(cmd), out)
	return p.ConfirmNewSHA(displaySource(m), res.newSHA, oldPin)
}

// syncAfterRepin runs the normal single-scope sync for the scope, with the
// run lock already held by the caller.
func syncAfterRepin(cmd *cobra.Command, rc *runtimeContext, scopeRoot, manifestPath string, userScope bool, now time.Time) error {
	scope := "project"
	if userScope {
		scope = "user"
	}
	prep, err := prepareScope(cmd.Context(), rc, scopeRoot, manifestPath, scope, now)
	if err != nil {
		return err
	}
	defer prep.Close()

	req := prep.Request
	req.Options = engine.Options{
		Now:         func() time.Time { return now },
		Logger:      rc.Logger,
		RunLockHeld: true, // we already hold the workspace run lock
	}
	if home, herr := resolveHome(); herr == nil {
		applyCursorComposition(cmd.Context(), rc, &req, prep.Manifest, req.Scope, home, now)
	}
	summary, err := engine.Sync(cmd.Context(), req)
	if err != nil {
		return err
	}
	if summary.Outcome.ExitCode != 0 {
		return fmt.Errorf("sync reported failures: %s", summary.Outcome.Line)
	}
	return nil
}

// resolveUpdateScope picks the single scope update operates on: the
// user-home manifest under --user, an explicit --workspace, or the nearest
// discovered workspace.
func resolveUpdateScope(rc *runtimeContext, userScope bool) (root, manifestPath string, err error) {
	if userScope {
		home, herr := resolveHome()
		if herr != nil {
			return "", "", herr
		}
		us, ok := hierarchy.UserScope(home)
		if !ok {
			return "", "", fmt.Errorf("no user-level .agent-sync.yaml found under %s", home)
		}
		return us.Root, us.ManifestPath, nil
	}
	ws, ferr := workspace.Find(rc.Flags.Workspace, workspace.Options{Workspace: rc.Flags.Workspace})
	if ferr != nil {
		return "", "", fmt.Errorf("locate workspace: %w", ferr)
	}
	return ws.Root, ws.ManifestPath, nil
}

func changeSummaryOrEmpty(ctx context.Context, res updateResolution, oldPin string) string {
	if oldPin == "" || res.mirrorPath == "" {
		return ""
	}
	s, err := git.ChangeSummary(ctx, res.mirrorPath, oldPin, res.newSHA, changeSummaryMaxLines)
	if err != nil {
		return ""
	}
	return s
}

func displaySource(m *manifest.Manifest) string {
	switch {
	case m.Canonical.URL != "":
		if c, err := cache.Canonicalize(m.Canonical.URL); err == nil {
			return c
		}
		return m.Canonical.URL
	case m.Canonical.LocalPath != "":
		return m.Canonical.LocalPath
	default:
		return m.Canonical.LocalDir
	}
}

func shortDisplaySHA(sha string) string {
	if sha == "" {
		return "(unpinned)"
	}
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func writeUpdateLine(cmd *cobra.Command, line string) error {
	_, err := fmt.Fprintln(cmd.OutOrStdout(), line)
	return err
}

func stdinOrEmpty(cmd *cobra.Command) io.Reader {
	if in := cmd.InOrStdin(); in != nil {
		return in
	}
	return os.Stdin
}

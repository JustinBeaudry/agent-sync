package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	antigravityadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/antigravity"
	claudeadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/claude"
	codexadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/codex"
	cursoradapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/cursor"
	piadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/pi"
	"github.com/agent-sync/agent-sync/internal/cache"
	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/locks"
	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/trust"
	"github.com/agent-sync/agent-sync/internal/workspace"
)

// bundledAdapters returns the compiled-in adapter set. Centralized so
// every command discovers the same adapters.
func bundledAdapters() []*adapter.BundledAdapter {
	return []*adapter.BundledAdapter{
		claudeadapter.Bundled(),
		cursoradapter.Bundled(),
		codexadapter.Bundled(),
		piadapter.Bundled(),
		antigravityadapter.Bundled(),
	}
}

// prepared bundles the per-invocation engine inputs shared by sync and
// validate: an opened workspace root (the caller must Close it), the
// loaded manifest, and a fully-built engine.Request. Close releases the
// root.
type prepared struct {
	Workspace *workspace.Workspace
	Manifest  *manifest.Manifest
	Root      *fsroot.Root
	Request   engine.Request
	// RunLockHeld tells sync callers the CLI already owns the workspace
	// run lock and engine.Sync must not re-acquire it.
	RunLockHeld bool
	// PinMovedTo is non-empty only when sync auto-advance already re-pinned the
	// manifest before building this prepared request.
	PinMovedTo string
	Close      func()
}

type syncPrepareOptions struct {
	Frozen bool
	// PostMerge disables auto-advance for the git-hook path. A `git pull`
	// post-merge sync must stay fast and must yield gracefully to an
	// in-progress sync (engine.Sync self-locks with a short timeout and
	// reports blocked); acquiring the CLI run lock here to auto-advance would
	// hard-fail on contention and break `git pull`. Post-merge therefore
	// syncs at the current pin.
	PostMerge bool
}

type autoAdvanceScopeResult struct {
	Manifest    *manifest.Manifest
	ResolvedGit *resolvedGitMaterialization
	RunLockHeld bool
	PinMovedTo  string
	release     func() error
}

// prepareEngine performs the shared setup both sync and validate need:
// locate the workspace, load the manifest, open the root, materialize the
// canonical IR, discover adapters, and assemble an engine.Request. Per-run
// engine.Options (mode, adopt, expect-deletions) are layered on by the
// caller via the returned Request.Options.
//
// prepareEngine is the single-scope wrapper: it resolves the nearest
// workspace from cwd (or the explicit --workspace override) and delegates to
// prepareScope. It is used by validate, and by sync when an explicit
// --workspace override is in effect. The hierarchy sync orchestrator skips
// it and calls prepareScope directly, once per discovered scope.
func prepareEngine(ctx context.Context, rc *runtimeContext, now time.Time) (prepared, error) {
	if rc == nil {
		return prepared{}, errors.New("cli: prepareEngine called with nil runtime context")
	}
	flags := rc.Flags
	ws, err := workspace.Find(flags.Workspace, workspace.Options{Workspace: flags.Workspace})
	if err != nil {
		return prepared{}, fmt.Errorf("locate workspace: %w", err)
	}
	// Single-scope path (explicit --workspace / validate / watch): always project scope.
	prep, err := prepareScope(ctx, rc, ws.Root, ws.ManifestPath, "project", now)
	if err != nil {
		return prepared{}, err
	}
	// Apply Cursor-rule composition here too, so validate, watch, and
	// --workspace sync see the same composed desired state as a plain `sync`.
	// Without it, a composed project reports false WouldDelete drift under
	// validate and loses composed rules under watch/--workspace. Best-effort on
	// home resolution: if it fails, composition simply no-ops (as it does when no
	// user manifest exists).
	if home, herr := resolveHome(); herr == nil {
		applyCursorComposition(ctx, rc, &prep.Request, prep.Manifest, "project", home, now)
	}
	return prep, nil
}

// prepareScopeForSync is the sync-only wrapper around prepareScope. It
// preserves prepareScope's existing behavior for validate/watch, but for sync
// it can acquire the CLI run lock, auto-advance the manifest, and then build
// the request against the exact approved SHA.
func prepareScopeForSync(ctx context.Context, rc *runtimeContext, scopeRoot, manifestPath, scope string, now time.Time, opts syncPrepareOptions) (prepared, error) {
	if rc == nil {
		return prepared{}, errors.New("cli: prepareScopeForSync called with nil runtime context")
	}
	m, err := manifest.LoadFile(manifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
	if err != nil {
		return prepared{}, fmt.Errorf("load manifest: %w", err)
	}
	root, err := fsroot.OpenWorkspaceRoot(scopeRoot)
	if err != nil {
		return prepared{}, fmt.Errorf("open workspace root: %w", err)
	}
	closeRoot := func() { _ = root.Close() }

	if !shouldAutoAdvanceSync(m, opts.Frozen || opts.PostMerge, rc.Flags.Offline) {
		mat, err := materialize(ctx, m, materializeOptions{Offline: rc.Flags.Offline, Now: now, Root: root})
		if err != nil {
			closeRoot()
			return prepared{}, err
		}
		prep, err := buildPrepared(ctx, rc, scopeRoot, manifestPath, scope, now, m, root, mat, closeRoot, false, "")
		if err != nil {
			closeRoot()
			return prepared{}, err
		}
		return prep, nil
	}

	auto, err := prepareAutoAdvanceScope(ctx, rc, root, manifestPath, scope, now)
	if err != nil {
		closeRoot()
		return prepared{}, err
	}
	closeWithRelease := closeRoot
	if auto.release != nil {
		closeWithRelease = func() {
			_ = auto.release()
			_ = root.Close()
		}
	}

	mat, err := materialize(ctx, auto.Manifest, materializeOptions{
		Now:         now,
		Root:        root,
		ResolvedGit: auto.ResolvedGit,
	})
	if err != nil {
		closeWithRelease()
		if auto.PinMovedTo != "" {
			return prepared{}, pinMovedSyncError(auto.PinMovedTo, err)
		}
		return prepared{}, err
	}
	prep, err := buildPrepared(ctx, rc, scopeRoot, manifestPath, scope, now, auto.Manifest, root, mat, closeWithRelease, auto.RunLockHeld, auto.PinMovedTo)
	if err != nil {
		closeWithRelease()
		if auto.PinMovedTo != "" {
			return prepared{}, pinMovedSyncError(auto.PinMovedTo, err)
		}
		return prepared{}, err
	}
	return prep, nil
}

// prepareScope builds the per-invocation engine inputs for one already-located
// scope: it loads the manifest at manifestPath, opens an fsroot at scopeRoot,
// materializes the canonical IR, discovers adapters, and assembles an
// engine.Request. The caller must Close the returned root (via prepared.Close).
//
// This is the multi-scope-safe core: the hierarchy orchestrator calls it once
// per discovered scope, each against that scope's own root, so each scope's
// engine.Sync writes its own staging and ledger.
func prepareScope(ctx context.Context, rc *runtimeContext, scopeRoot, manifestPath, scope string, now time.Time) (prepared, error) {
	if rc == nil {
		return prepared{}, errors.New("cli: prepareScope called with nil runtime context")
	}
	flags := rc.Flags
	m, err := manifest.LoadFile(manifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
	if err != nil {
		return prepared{}, fmt.Errorf("load manifest: %w", err)
	}
	root, err := fsroot.OpenWorkspaceRoot(scopeRoot)
	if err != nil {
		return prepared{}, fmt.Errorf("open workspace root: %w", err)
	}

	mat, err := materialize(ctx, m, materializeOptions{Offline: flags.Offline, Now: now, Root: root})
	if err != nil {
		_ = root.Close()
		return prepared{}, err
	}
	prep, err := buildPrepared(ctx, rc, scopeRoot, manifestPath, scope, now, m, root, mat, func() { _ = root.Close() }, false, "")
	if err != nil {
		_ = root.Close()
		return prepared{}, err
	}
	return prep, nil
}

func buildPrepared(
	ctx context.Context,
	rc *runtimeContext,
	scopeRoot, manifestPath, scope string,
	now time.Time,
	m *manifest.Manifest,
	root *fsroot.Root,
	mat materialized,
	closeFn func(),
	runLockHeld bool,
	pinMovedTo string,
) (prepared, error) {
	// Surface IR decode warnings (missing AGENTS.md, unreadable skill
	// assets, etc.) rather than dropping them silently — they are real
	// drift-guard / debugging signal.
	for _, w := range mat.Warnings {
		rc.Logger.Warn("ir decode warning", "code", w.Code, "message", w.Message, "path", w.Provenance.Path)
	}

	reg, err := adapter.DiscoverAdapters(ctx, adapter.DiscoverOptions{
		Workspace: m,
		Bundled:   bundledAdapters(),
	})
	if err != nil {
		return prepared{}, fmt.Errorf("discover adapters: %w", err)
	}

	req := engine.Request{
		Root:          root,
		WorkspacePath: scopeRoot,
		Scope:         scope,
		Registry:      reg,
		Targets:       m.Targets,
		Nodes:         mat.Nodes,
		Skills:        mat.Skills,
		Commit:        mat.Commit,
		SourceURL:     mat.SourceURL,
		Options:       engine.Options{Now: func() time.Time { return now }, Logger: rc.Logger},
	}
	return prepared{
		Workspace:   &workspace.Workspace{Root: scopeRoot, ManifestPath: manifestPath},
		Manifest:    m,
		Root:        root,
		Request:     req,
		RunLockHeld: runLockHeld,
		PinMovedTo:  pinMovedTo,
		Close:       closeFn,
	}, nil
}

func shouldAutoAdvanceSync(m *manifest.Manifest, frozen, offline bool) bool {
	if offline || frozen {
		return false
	}
	if m.Canonical.Auto != nil && !*m.Canonical.Auto {
		return false
	}
	if m.Canonical.Commit == "" {
		return false
	}
	// Auto-advance moves the trust anchor forward; with no anchor there is
	// nothing to advance from. An empty trusted_sha routes to the normal
	// pinned/TOFU path (materializeURL), which avoids materializing the
	// floating-newest SHA without a pin/ff/audit.
	if m.TrustedSHA == "" {
		return false
	}
	return m.Canonical.URL != "" || m.Canonical.LocalPath != ""
}

func prepareAutoAdvanceScope(
	ctx context.Context,
	rc *runtimeContext,
	root *fsroot.Root,
	manifestPath, scope string,
	now time.Time,
) (autoAdvanceScopeResult, error) {
	runLock, err := locks.NewRunLock(root)
	if err != nil {
		return autoAdvanceScopeResult{}, fmt.Errorf("sync: %w", err)
	}
	release, err := runLock.Acquire(ctx, locks.AcquireOpts{Timeout: updateRunLockTimeout})
	if err != nil {
		if errors.Is(err, locks.ErrRunLocked) {
			return autoAdvanceScopeResult{}, &exitError{code: exitFailure, err: errors.New("sync: another agent-sync run holds this workspace; try again when it finishes")}
		}
		return autoAdvanceScopeResult{}, fmt.Errorf("sync: acquire run lock: %w", err)
	}

	// Load the manifest under the run lock. The fast-forward baseline and
	// trust decision MUST be computed from the on-disk trusted_sha as it
	// stands now — a concurrent same-workspace advance that landed while we
	// waited for the lock would otherwise leave us proving fast-forward and
	// auditing against a stale, pre-lock anchor. (prepareScopeForSync did a
	// pre-lock load only to decide eligibility; this is the authoritative read.)
	m, err := manifest.LoadFile(manifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
	if err != nil {
		_ = release()
		return autoAdvanceScopeResult{}, fmt.Errorf("sync: reload manifest under lock: %w", err)
	}

	res, err := resolveAutoAdvance(ctx, m)
	if err != nil {
		_ = release()
		return autoAdvanceScopeResult{}, fmt.Errorf("sync: auto-advance: %w", err)
	}
	out := autoAdvanceScopeResult{
		Manifest: m,
		ResolvedGit: &resolvedGitMaterialization{
			LocalPath: res.mirrorPath,
			Commit:    res.newSHA,
			SourceURL: res.sourceURL,
		},
		RunLockHeld: true,
		release:     release,
	}

	// Route on the same predicate resolveAutoAdvanceTarget uses to pick the
	// source (local_path first). A local_path source is intentionally not
	// trust-gated (matching materializeLocal) but is still ff-gated below;
	// keying the trust branch off "not local_path" keeps routing and source
	// resolution from ever disagreeing about which source this is.
	if m.Canonical.LocalPath == "" {
		state, err := loadTrustState(res.sourceURL)
		if err != nil {
			_ = release()
			return autoAdvanceScopeResult{}, fmt.Errorf("sync: load trust state: %w", err)
		}
		dec, derr := trust.Decide(trust.DecideInput{
			URL:                res.sourceURL,
			ResolvedSHA:        res.newSHA,
			ManifestTrustedSHA: m.TrustedSHA,
			State:              state,
			StateLoaded:        true,
			Posture:            trust.PostureAllowNewSHAs,
			FastForward:        res.fastForward,
			TTY:                false,
			Now:                now,
		})
		if derr != nil {
			_ = release()
			return autoAdvanceScopeResult{}, wrapAutoAdvanceDecisionError(m, res, derr)
		}
		if res.fellBackToPinned {
			rc.Logger.Warn("sync auto-advance fell back to the cached pin",
				"scope", scope,
				"url", res.sourceURL,
				"ref", res.refName,
				"sha", res.newSHA,
			)
		}
		if dec.Kind != trust.KindProceedAutoAdvance {
			return out, nil
		}
		if err := appendPendingTrustDecision(dec.AppendPending); err != nil {
			_ = release()
			return autoAdvanceScopeResult{}, fmt.Errorf("sync: append pending trust review: %w", err)
		}
		if err := repinManifest(manifestPath, res.newSHA); err != nil {
			_ = release()
			return autoAdvanceScopeResult{}, fmt.Errorf("sync: %w", err)
		}
		if err := res.cacheLoc.AppendAutoAdvance(cache.AdvanceAudit{
			TS:     now,
			OldSHA: dec.AppendPending.OldSHA,
			NewSHA: res.newSHA,
			Ref:    res.refName,
			Scope:  scope,
			URL:    res.sourceURL,
		}); err != nil {
			_ = release()
			return autoAdvanceScopeResult{}, pinMovedSyncError(res.newSHA, fmt.Errorf("write auto-advance audit: %w", err))
		}
		rc.Logger.Info("sync auto-advance applied",
			"old_sha", dec.AppendPending.OldSHA,
			"new_sha", res.newSHA,
			"ref", res.refName,
			"scope", scope,
			"url", res.sourceURL,
		)
		reloaded, err := manifest.LoadFile(manifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
		if err != nil {
			_ = release()
			return autoAdvanceScopeResult{}, pinMovedSyncError(res.newSHA, fmt.Errorf("reload manifest: %w", err))
		}
		out.Manifest = reloaded
		out.PinMovedTo = res.newSHA
		return out, nil
	}

	if res.newSHA == m.Canonical.Commit {
		return out, nil
	}
	if !res.fastForward {
		_ = release()
		return autoAdvanceScopeResult{}, wrapAutoAdvanceDecisionError(m, res, trust.ErrTrustDecisionRequired)
	}
	if err := repinManifest(manifestPath, res.newSHA); err != nil {
		_ = release()
		return autoAdvanceScopeResult{}, fmt.Errorf("sync: %w", err)
	}
	rc.Logger.Info("sync auto-advance applied",
		"old_sha", m.TrustedSHA,
		"new_sha", res.newSHA,
		"ref", res.refName,
		"scope", scope,
		"url", res.sourceURL,
	)
	reloaded, err := manifest.LoadFile(manifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
	if err != nil {
		_ = release()
		return autoAdvanceScopeResult{}, pinMovedSyncError(res.newSHA, fmt.Errorf("reload manifest: %w", err))
	}
	out.Manifest = reloaded
	out.PinMovedTo = res.newSHA
	return out, nil
}

func wrapAutoAdvanceDecisionError(m *manifest.Manifest, res advanceResolution, err error) error {
	if errors.Is(err, trust.ErrTrustDecisionRequired) && !res.fastForward {
		return &exitError{code: trust.ExitTrustDecisionRequired, err: fmt.Errorf(
			"sync: auto-advance refused for %s: %s is not a fast-forward descendant of trusted_sha %s on %s; keeping pinned commit %s",
			displaySource(m),
			shortDisplaySHA(res.newSHA),
			shortDisplaySHA(m.TrustedSHA),
			res.refName,
			shortDisplaySHA(m.Canonical.Commit),
		)}
	}
	return &exitError{code: trust.ExitCodeFor(err), err: fmt.Errorf("sync: trust: %w", err)}
}

func loadTrustState(url string) (trust.State, error) {
	store, err := resolveTrustStore(TrustDeps{})
	if err != nil {
		return trust.State{}, err
	}
	state, err := store.Fold()
	if err != nil {
		return trust.State{}, err
	}
	return state[url], nil
}

func appendPendingTrustDecision(entry trust.PendingEntry) error {
	pending, err := resolvePendingStore(TrustDeps{})
	if err != nil {
		return err
	}
	return pending.Append(entry)
}

func pinMovedSyncError(newSHA string, err error) error {
	return &exitError{code: exitUpdatePinMoved, err: fmt.Errorf(
		"sync: pin moved to %s but the sync did not land: %w. Re-run `agent-sync sync` to materialize it",
		shortDisplaySHA(newSHA), err)}
}

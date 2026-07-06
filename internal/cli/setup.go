package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	antigravityadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/antigravity"
	claudeadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/claude"
	codexadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/codex"
	cursoradapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/cursor"
	piadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/pi"
	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/manifest"
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

func requestScope(m *manifest.Manifest, discovered string) string {
	if m != nil && m.Scope != "" {
		return m.Scope
	}
	if discovered != "" {
		return discovered
	}
	return manifest.ScopeProject
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
	Close     func()
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
	if flags.Workspace != "" {
		ws, err := workspace.Find(flags.Workspace, workspace.Options{Workspace: flags.Workspace})
		if err != nil {
			return prepared{}, fmt.Errorf("locate workspace: %w", err)
		}
		// Single-scope path (explicit --workspace / validate / watch):
		// project fallback unless the loaded manifest declares a scope.
		prep, err := prepareScope(ctx, rc, ws.Root, ws.ManifestPath, "project", now)
		if err != nil {
			return prepared{}, err
		}
		// Apply Cursor-rule composition here too, so validate, watch, and
		// --workspace sync see the same composed desired state as a plain `sync`.
		// Without it, a composed project reports false WouldDelete drift under
		// validate and loses composed rules under watch/--workspace. Best-effort
		// on home resolution: if it fails, composition simply no-ops (as it
		// does when no user manifest exists).
		actualScope := requestScope(prep.Manifest, "project")
		if home, herr := resolveHome(); herr == nil {
			applyCursorComposition(ctx, rc, &prep.Request, prep.Manifest, actualScope, home, now)
		}
		return prep, nil
	}

	// Default nearest-scope path now starts from hierarchy discovery so we can
	// resolve inherited nodes and fragments from ancestors.
	cwd, err := os.Getwd()
	if err != nil {
		return prepared{}, fmt.Errorf("prepare engine: resolve cwd: %w", err)
	}
	home, err := resolveHome()
	if err != nil {
		return prepared{}, fmt.Errorf("prepare engine: %w", err)
	}
	scopes, err := hierarchy.Discover(cwd, hierarchy.Options{Home: home})
	if err != nil {
		return prepared{}, fmt.Errorf("discover hierarchy: %w", err)
	}
	scopes = selectWriteScopes(scopes, false)

	preparedLayers := make([]preparedLayer, 0, len(scopes))
	for _, sc := range scopes {
		pl, ok := materializeLayerReadOnly(ctx, rc, sc, now)
		if !ok {
			continue
		}
		preparedLayers = append(preparedLayers, pl)
	}

	var targetScope hierarchy.Scope
	for _, sc := range scopes {
		if sc.Emit {
			targetScope = sc
			break
		}
	}
	if targetScope.ManifestPath == "" && targetScope.Root == "" {
		return prepared{}, fmt.Errorf("prepare engine: no scope to sync from hierarchy discovery")
	}

	// Single write scope still needs a full prepare for adapter discovery and
	// request construction, then gets resolved ancestors merged in here.
	prep, err := prepareScope(ctx, rc, targetScope.Root, targetScope.ManifestPath, targetScope.Level.String(), now)
	if err != nil {
		return prepared{}, err
	}
	applyResolvedLayers(&prep.Request, targetScope, scopes, preparedLayers)

	if home, herr := resolveHome(); herr == nil {
		applyCursorComposition(ctx, rc, &prep.Request, prep.Manifest, prep.Request.Scope, home, now)
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
		_ = root.Close()
		return prepared{}, fmt.Errorf("discover adapters: %w", err)
	}

	req := engine.Request{
		Root:          root,
		WorkspacePath: scopeRoot,
		Scope:         requestScope(m, scope),
		Registry:      reg,
		Targets:       m.Targets,
		Nodes:         mat.Nodes,
		Skills:        mat.Skills,
		Fragments:     mat.Fragments,
		Commit:        mat.Commit,
		SourceURL:     mat.SourceURL,
		Options:       engine.Options{Now: func() time.Time { return now }, Logger: rc.Logger},
	}
	return prepared{
		Workspace: &workspace.Workspace{Root: scopeRoot, ManifestPath: manifestPath},
		Manifest:  m,
		Root:      root,
		Request:   req,
		Close:     func() { _ = root.Close() },
	}, nil
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	claudeadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/claude"
	codexadapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/codex"
	cursoradapter "github.com/agent-sync/agent-sync/internal/adapter/bundled/cursor"
	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/fsroot"
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
	Close     func()
}

// prepareEngine performs the shared setup both sync and validate need:
// locate the workspace, load the manifest, open the root, materialize the
// canonical IR, discover adapters, and assemble an engine.Request. Per-run
// engine.Options (mode, adopt, expect-deletions) are layered on by the
// caller via the returned Request.Options.
func prepareEngine(ctx context.Context, rc *runtimeContext, now time.Time) (prepared, error) {
	if rc == nil {
		return prepared{}, errors.New("cli: prepareEngine called with nil runtime context")
	}
	flags := rc.Flags
	ws, err := workspace.Find(flags.Workspace, workspace.Options{Workspace: flags.Workspace})
	if err != nil {
		return prepared{}, fmt.Errorf("locate workspace: %w", err)
	}
	m, err := manifest.LoadFile(ws.ManifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
	if err != nil {
		return prepared{}, fmt.Errorf("load manifest: %w", err)
	}
	root, err := fsroot.OpenWorkspaceRoot(ws.Root)
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
		WorkspacePath: ws.Root,
		Registry:      reg,
		Targets:       m.Targets,
		Nodes:         mat.Nodes,
		Skills:        mat.Skills,
		Commit:        mat.Commit,
		Options:       engine.Options{Now: func() time.Time { return now }, Logger: rc.Logger},
	}
	return prepared{
		Workspace: ws,
		Manifest:  m,
		Root:      root,
		Request:   req,
		Close:     func() { _ = root.Close() },
	}, nil
}

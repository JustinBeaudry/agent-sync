package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/report"
	"github.com/agent-sync/agent-sync/internal/watch"
	"github.com/agent-sync/agent-sync/internal/workspace"
)

func newWatchCommand(deps RootDeps) *cobra.Command {
	var debounceMs int

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Re-sync automatically when the manifest or source changes",
		Long: "Watch the manifest (and a local canonical source) and re-run sync " +
			"on changes. Runs in the foreground; press Ctrl+C to stop. Not a daemon.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := mustRuntime(cmd)
			if err != nil {
				return err
			}

			ws, err := workspace.Find(rc.Flags.Workspace, workspace.Options{Workspace: rc.Flags.Workspace})
			if err != nil {
				return fmt.Errorf("watch: locate workspace: %w", err)
			}
			m, err := manifest.LoadFile(ws.ManifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
			if err != nil {
				return fmt.Errorf("watch: load manifest: %w", err)
			}
			// watch runs the single-scope sync path, which never composes. Warn
			// once at startup so an opted-in manifest is not a silent no-op here.
			if m.Compose.CursorRulesFromUser {
				rc.Logger.Warn("compose: cursor-rules-from-user is ignored by watch; it applies only to a hierarchy `sync` (not --workspace/watch)")
			}

			paths := []string{ws.ManifestPath}
			switch {
			case m.Canonical.LocalPath != "":
				paths = append(paths, m.Canonical.LocalPath)
			case m.Canonical.LocalDir != "":
				// Watch the in-repo source directory so edits under it
				// re-sync. The emitted skill output lives inside this tree
				// (<local_dir>/skills/agent-sync-*); a sync that writes it
				// triggers one more event, but the follow-up sync is an
				// idempotent no-op (no writes, no swap), so the loop is
				// bounded rather than runaway — see watch.Run's no-op-sync note.
				paths = append(paths, filepath.Join(ws.Root, m.Canonical.LocalDir))
			}

			// Ctrl+C (SIGINT) or SIGTERM (process managers, Docker, K8s)
			// cancels the watch loop cleanly.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			cfg := watch.Config{
				Paths:          paths,
				IgnorePrefixes: []string{filepath.Join(ws.Root, ".agent-sync")},
				Debounce:       time.Duration(debounceMs) * time.Millisecond,
				Logger:         rc.Logger,
				OnChange: func(ctx context.Context) error {
					return runWatchSync(ctx, rc, deps, ws)
				},
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "watching %d path(s); press Ctrl+C to stop\n", len(paths))
			return watch.Run(ctx, cfg)
		},
	}
	cmd.Flags().IntVar(&debounceMs, "debounce-ms", int(watch.DefaultDebounce/time.Millisecond), "debounce window in milliseconds")
	return cmd
}

// runWatchSync runs one sync for the watcher. On failure it writes the
// last-watch.failed marker that `status` surfaces, then returns the error
// for the watch loop to log.
func runWatchSync(ctx context.Context, rc *runtimeContext, deps RootDeps, ws *workspace.Workspace) error {
	now := deps.now()()
	prep, err := prepareEngine(ctx, rc, now)
	if err != nil {
		writeWatchFailed(ws.Root, now, err)
		return err
	}
	defer prep.Close()

	req := prep.Request
	req.Options.Mode = report.ModeBestEffort
	summary, err := engine.Sync(ctx, req)
	if err != nil {
		writeWatchFailed(ws.Root, now, err)
		return err
	}
	if summary.Outcome.ExitCode != 0 {
		ferr := fmt.Errorf("watch sync reported failures (exit %d)", summary.Outcome.ExitCode)
		writeWatchFailed(ws.Root, now, ferr)
		return ferr
	}
	// Success: clear any stale failure marker.
	clearWatchFailed(ws.Root)
	return nil
}

func writeWatchFailed(wsRoot string, now time.Time, cause error) {
	root, err := fsroot.OpenWorkspaceRoot(wsRoot)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	_ = root.Inner().MkdirAll(filepath.Dir(lastWatchFailedRel), 0o755)
	body := fmt.Sprintf("%s\n%s\n", now.UTC().Format(time.RFC3339), cause.Error())
	_ = root.StagedWrite(lastWatchFailedRel, []byte(body), 0o644)
}

func clearWatchFailed(wsRoot string) {
	root, err := fsroot.OpenWorkspaceRoot(wsRoot)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	_ = root.Inner().Remove(lastWatchFailedRel)
}

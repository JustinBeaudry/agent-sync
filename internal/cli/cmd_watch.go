package cli

import (
	"context"
	"fmt"
	"os/signal"
	"path/filepath"
	"time"

	"os"

	"github.com/spf13/cobra"

	"github.com/aienvs/aienvs/internal/engine"
	"github.com/aienvs/aienvs/internal/fsroot"
	"github.com/aienvs/aienvs/internal/manifest"
	"github.com/aienvs/aienvs/internal/report"
	"github.com/aienvs/aienvs/internal/watch"
	"github.com/aienvs/aienvs/internal/workspace"
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
			rc, _ := runtimeFrom(cmd.Context())

			ws, err := workspace.Find(rc.Flags.Workspace, workspace.Options{Workspace: rc.Flags.Workspace})
			if err != nil {
				return fmt.Errorf("watch: locate workspace: %w", err)
			}
			m, err := manifest.LoadFile(ws.ManifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
			if err != nil {
				return fmt.Errorf("watch: load manifest: %w", err)
			}

			paths := []string{ws.ManifestPath}
			if m.Canonical.LocalPath != "" {
				paths = append(paths, m.Canonical.LocalPath)
			}

			// Ctrl+C cancels the watch loop cleanly.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()

			cfg := watch.Config{
				Paths:          paths,
				IgnorePrefixes: []string{filepath.Join(ws.Root, ".aienv")},
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

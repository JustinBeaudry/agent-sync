package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/aienvs/aienvs/internal/adapter"
	claudeadapter "github.com/aienvs/aienvs/internal/adapter/bundled/claude"
	cursoradapter "github.com/aienvs/aienvs/internal/adapter/bundled/cursor"
	"github.com/aienvs/aienvs/internal/engine"
	"github.com/aienvs/aienvs/internal/fsroot"
	"github.com/aienvs/aienvs/internal/manifest"
	"github.com/aienvs/aienvs/internal/report"
	"github.com/aienvs/aienvs/internal/workspace"
)

// hookSkippedMarker records that a git-hook-driven sync yielded to an
// in-progress manual sync (so it never breaks `git pull`).
const hookSkippedMarker = ".aienv/state/hook-skipped"

func newSyncCommand(deps RootDeps) *cobra.Command {
	var (
		bestEffort      bool
		adoptPrefixes   []string
		targetFilter    []string
		expectDeletions int
		postMerge       bool
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync target tools from the canonical manifest",
		Long: "Materialize the canonical source, run each target's adapter, and " +
			"atomically write the resulting configuration into the workspace.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, _ := runtimeFrom(cmd.Context())
			flags := rc.Flags

			ws, err := workspace.Find(flags.Workspace, workspace.Options{Workspace: flags.Workspace})
			if err != nil {
				return fmt.Errorf("sync: locate workspace: %w", err)
			}
			m, err := manifest.LoadFile(ws.ManifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
			if err != nil {
				return fmt.Errorf("sync: load manifest: %w", err)
			}
			root, err := fsroot.OpenWorkspaceRoot(ws.Root)
			if err != nil {
				return fmt.Errorf("sync: open workspace root: %w", err)
			}
			defer func() { _ = root.Close() }()

			now := deps.now()()
			mat, err := materialize(cmd.Context(), m, materializeOptions{Offline: flags.Offline, Now: now})
			if err != nil {
				return err
			}

			reg, err := adapter.DiscoverAdapters(cmd.Context(), adapter.DiscoverOptions{
				Workspace: m,
				Bundled: []*adapter.BundledAdapter{
					claudeadapter.Bundled(),
					cursoradapter.Bundled(),
				},
			})
			if err != nil {
				return fmt.Errorf("sync: discover adapters: %w", err)
			}

			opts := engine.Options{
				AdoptPrefixes: adoptPrefixes,
				TargetsFilter: targetFilter,
				Now:           func() time.Time { return now },
				Logger:        rc.Logger,
			}
			if bestEffort {
				opts.Mode = report.ModeBestEffort
			} else {
				opts.Mode = report.ModeAtomic
			}
			if cmd.Flags().Changed("expect-deletions") {
				opts.ExpectDeletions = &expectDeletions
			}

			summary, err := engine.Sync(cmd.Context(), engine.Request{
				Root:          root,
				WorkspacePath: ws.Root,
				Registry:      reg,
				Targets:       m.Targets,
				Nodes:         mat.Nodes,
				Skills:        mat.Skills,
				Commit:        mat.Commit,
				Options:       opts,
			})
			if err != nil {
				return fmt.Errorf("sync: %w", err)
			}

			// Post-merge hook mode: if a target was blocked by an in-progress
			// sync's lock, write the skip marker and exit 0 so `git pull` is
			// never broken (plan U3 / AGENTS invariant #3 spirit).
			if postMerge && anyBlocked(summary) {
				_ = writeHookSkipped(root, now)
				return nil
			}

			if err := renderSummary(cmd, rc.Access, summary); err != nil {
				return err
			}
			if summary.Outcome.ExitCode != 0 {
				return &exitError{code: summary.Outcome.ExitCode, err: errors.New("sync reported failures")}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&bestEffort, "best-effort", false, "continue past a failing target instead of aborting the run")
	cmd.Flags().StringArrayVar(&adoptPrefixes, "adopt-prefix", nil, "adopt pre-existing unmanaged content under this reserved prefix")
	cmd.Flags().StringArrayVar(&targetFilter, "target", nil, "restrict the sync to these target names (repeatable)")
	cmd.Flags().IntVar(&expectDeletions, "expect-deletions", 0, "abort unless exactly this many files would be deleted")
	cmd.Flags().BoolVar(&postMerge, "post-merge", false, "git-hook mode: yield to an in-progress sync and exit 0")
	return cmd
}

func anyBlocked(s report.Summary) bool {
	for _, t := range s.Targets {
		if t.Status == report.StatusBlocked {
			return true
		}
	}
	return false
}

func writeHookSkipped(root *fsroot.Root, now time.Time) error {
	if err := root.Inner().MkdirAll(filepath.Dir(hookSkippedMarker), 0o755); err != nil {
		return err
	}
	return root.StagedWrite(hookSkippedMarker, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o644)
}

// renderSummary writes the report to stdout as text or JSON per the
// resolved Access; logs and banners go to stderr (the logger), data to
// stdout.
func renderSummary(cmd *cobra.Command, access Access, summary report.Summary) error {
	if access.Output == OutputJSON {
		data, err := report.MarshalJSON(summary)
		if err != nil {
			return fmt.Errorf("sync: marshal json: %w", err)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return err
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), report.RenderText(summary))
	return err
}

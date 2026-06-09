package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/report"
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
			rc, err := mustRuntime(cmd)
			if err != nil {
				return err
			}
			now := deps.now()()

			prep, err := prepareEngine(cmd.Context(), rc, now)
			if err != nil {
				return fmt.Errorf("sync: %w", err)
			}
			defer prep.Close()

			req := prep.Request
			req.Options.AdoptPrefixes = adoptPrefixes
			req.Options.TargetsFilter = targetFilter
			if bestEffort {
				req.Options.Mode = report.ModeBestEffort
			} else {
				req.Options.Mode = report.ModeAtomic
			}
			if cmd.Flags().Changed("expect-deletions") {
				req.Options.ExpectDeletions = &expectDeletions
			}

			root := prep.Root
			summary, err := engine.Sync(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("sync: %w", err)
			}

			// Post-merge hook mode: if a target was blocked by an in-progress
			// sync's lock, write the skip marker and exit 0 so `git pull` is
			// never broken (plan U3 / AGENTS invariant #3 spirit).
			if postMerge && anyBlocked(summary) {
				if werr := writeHookSkipped(root, now); werr != nil {
					// Still exit 0 so `git pull` is never broken, but log it: the
					// marker is the only breadcrumb explaining the skip, so a
					// silent write failure makes diagnosis hard.
					rc.Logger.Warn("sync: failed to write hook-skipped marker", "err", werr)
				}
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

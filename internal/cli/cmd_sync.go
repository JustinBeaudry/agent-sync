package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/report"
)

// hookSkippedMarker records that a git-hook-driven sync yielded to an
// in-progress manual sync (so it never breaks `git pull`).
const hookSkippedMarker = ".agent-sync/state/hook-skipped"

// postMergeRunLockTimeout caps how long a --post-merge (git-hook) sync waits
// for the per-workspace run lock before yielding a blocked result and exiting
// 0. Short so a contended `git pull` is never stalled by the default multi-minute
// wait.
const postMergeRunLockTimeout = 3 * time.Second

func newSyncCommand(deps RootDeps) *cobra.Command {
	var (
		bestEffort      bool
		adoptPrefixes   []string
		targetFilter    []string
		expectDeletions int
		postMerge       bool
		userScope       bool
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
			if postMerge {
				// A git-hook sync must yield fast to a manual sync rather than
				// stall `git pull`: cap the run-lock wait so contention becomes a
				// quick blocked-and-yield (exit 0) instead of the multi-minute
				// default. The engine reports blocked targets; the post-merge
				// handlers below turn that into a clean exit 0.
				opts.RunLockTimeout = postMergeRunLockTimeout
			}

			// An explicit --workspace override pins a single scope and disables
			// hierarchy discovery (today's behavior wins). --user is meaningless
			// then: it selects the user scope of a discovered hierarchy.
			if rc.Flags.Workspace != "" {
				if userScope {
					return errUserWithWorkspace
				}
				return runSingleScopeSync(cmd, rc, opts, postMerge, now)
			}

			// Hierarchy path: discover every emit scope from cwd and sync each.
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("sync: resolve cwd: %w", err)
			}
			home, err := resolveHome()
			if err != nil {
				return fmt.Errorf("sync: %w", err)
			}
			hOpts := hierarchySyncOptions{IncludeUser: userScope, EngineOpts: opts}
			outcomes, notice, err := runHierarchySync(cmd.Context(), rc, cwd, home, hOpts, now)
			if err != nil {
				return fmt.Errorf("sync: %w", err)
			}

			// Post-merge hook mode: if any scope was blocked by an in-progress
			// sync's lock, write that scope's skip marker and exit 0 so
			// `git pull` is never broken (plan U3 / AGENTS invariant #3 spirit).
			if postMerge {
				if handled := handleHierarchyPostMerge(rc, outcomes, now); handled {
					return nil
				}
			}

			if rc.Access.Output == OutputJSON {
				if err := renderHierarchyJSON(cmd.OutOrStdout(), outcomes, notice); err != nil {
					return err
				}
			} else if err := renderHierarchyText(cmd.OutOrStdout(), outcomes, notice); err != nil {
				return err
			}
			if code := hierarchyExitCode(outcomes); code != 0 {
				return &exitError{code: code, err: errors.New("sync reported failures")}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&bestEffort, "best-effort", false, "continue past a failing target instead of aborting the run")
	cmd.Flags().StringArrayVar(&adoptPrefixes, "adopt-prefix", nil, "adopt pre-existing unmanaged content under this reserved prefix")
	cmd.Flags().StringArrayVar(&targetFilter, "target", nil, "restrict the sync to these target names (repeatable)")
	cmd.Flags().IntVar(&expectDeletions, "expect-deletions", 0, "abort unless exactly this many files would be deleted")
	cmd.Flags().BoolVar(&postMerge, "post-merge", false, "git-hook mode: yield to an in-progress sync and exit 0")
	cmd.Flags().BoolVar(&userScope, "user", false, "also sync the user-level (~) manifest")
	return cmd
}

// runSingleScopeSync runs the legacy single-scope sync against the nearest
// (or explicit --workspace) scope. It preserves today's behavior verbatim and
// is taken only when an explicit --workspace override is in effect; the
// default path is the hierarchy orchestrator. validate also relies on this
// single-scope shape via prepareEngine.
func runSingleScopeSync(cmd *cobra.Command, rc *runtimeContext, opts engine.Options, postMerge bool, now time.Time) error {
	prep, err := prepareEngine(cmd.Context(), rc, now)
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	defer prep.Close()

	req := prep.Request
	req.Options = opts

	root := prep.Root
	summary, err := engine.Sync(cmd.Context(), req)
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	// Post-merge hook mode: if a target was blocked by an in-progress sync's
	// lock, write the skip marker and exit 0 so `git pull` is never broken
	// (plan U3 / AGENTS invariant #3 spirit).
	if postMerge && anyBlocked(summary) {
		if werr := writeHookSkipped(root, now); werr != nil {
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
}

// handleHierarchyPostMerge applies the post-merge yield across the hierarchy:
// for each scope whose sync was blocked by an in-progress lock, it writes that
// scope's hook-skipped marker. It returns true when any scope was blocked, in
// which case the caller exits 0 so `git pull` is never broken. A blocked scope
// has no engine error (the lock yield is a clean outcome), so its summary is
// inspected via anyBlocked.
func handleHierarchyPostMerge(rc *runtimeContext, outcomes []scopeOutcome, now time.Time) bool {
	handled := false
	for _, o := range outcomes {
		if o.Err != nil || !anyBlocked(o.Summary) {
			continue
		}
		handled = true
		root, err := fsroot.OpenWorkspaceRoot(o.Scope.Root)
		if err != nil {
			rc.Logger.Warn("sync: failed to open root for hook-skipped marker", "root", o.Scope.Root, "err", err)
			continue
		}
		if werr := writeHookSkipped(root, now); werr != nil {
			rc.Logger.Warn("sync: failed to write hook-skipped marker", "root", o.Scope.Root, "err", werr)
		}
		_ = root.Close()
	}
	return handled
}

// promptYes writes a [Y/n] question to w (stderr — stdout stays data-only)
// and reads one line from in. Enter and anything not starting with n/N is
// yes. A read error (closed stdin, EOF) is a decline: never treat an
// unanswerable prompt as consent to write the home directory.
func promptYes(in io.Reader, w io.Writer, question string) bool {
	_, _ = fmt.Fprint(w, question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.TrimSpace(line)
	return answer == "" || (answer[0] != 'n' && answer[0] != 'N')
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

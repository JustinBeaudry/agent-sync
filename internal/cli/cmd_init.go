package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aienvs/aienvs/internal/cache"
	"github.com/aienvs/aienvs/internal/git"
	"github.com/aienvs/aienvs/internal/manifest"
	"github.com/aienvs/aienvs/internal/tui"
	"github.com/aienvs/aienvs/internal/tui/wizard"
	"github.com/aienvs/aienvs/internal/workspace"
)

func newInitCommand(deps RootDeps) *cobra.Command {
	var (
		source    string
		localPath string
		ref       string
		commit    string
		floating  bool
		targets   []string
		dir       string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a workspace manifest (.aienv.yaml)",
		Long: "Initialize a workspace by writing a .aienv.yaml manifest. Runs an " +
			"interactive wizard on a TTY; with --non-interactive every value " +
			"must be supplied via flags.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, _ := runtimeFrom(cmd.Context())

			cfg := wizard.InitConfig{
				Dir:       dir,
				SourceURL: source,
				LocalPath: localPath,
				Ref:       ref,
				Commit:    commit,
				Floating:  floating,
				Targets:   targets,
			}

			interactive := tui.Interactive(rc.Access.IsTTY, rc.Access.NonInteractive, rc.Access.Accessible)
			if interactive && source == "" && localPath == "" {
				// Drive the wizard to collect the source/ref/targets.
				wcfg, committed, werr := wizard.Run(
					cmd.Context(), deps.in(), deps.err(), rc.Access.NoColor, bundledTargetNames(),
				)
				if werr != nil {
					return fmt.Errorf("init: wizard: %w", werr)
				}
				if !committed {
					return errors.New("init: aborted")
				}
				cfg = wcfg
				cfg.Dir = dir
				cfg.Floating = floating
			} else {
				// Non-interactive (or flags supplied): require a source.
				if err := requireFlag(rc.Access.NonInteractive, source != "" || localPath != "", "--source", "canonical repo URL or local path"); err != nil {
					return err
				}
			}

			// Pin-at-init (invariant #4): resolve the ref to a SHA unless
			// floating. Local paths and already-pinned configs are left as-is;
			// a URL with a ref resolves over the network.
			if err := resolvePin(cmd.Context(), &cfg); err != nil {
				return fmt.Errorf("init: %w", err)
			}

			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("init: %w", err)
			}

			target := manifestPathFor(cfg.Dir)
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("init: %s already exists (refusing to overwrite)", target)
			}
			data, err := cfg.ManifestYAML()
			if err != nil {
				return fmt.Errorf("init: render manifest: %w", err)
			}
			if err := manifest.WriteFile(target, data); err != nil {
				return fmt.Errorf("init: write manifest: %w", err)
			}
			// Confirm the written manifest re-loads cleanly.
			if _, err := manifest.LoadFile(target, manifest.LoadOptions{}); err != nil {
				return fmt.Errorf("init: wrote an invalid manifest: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", target)
			return nil
		},
	}

	cmd.Flags().StringVar(&source, "source", "", "canonical repository URL")
	cmd.Flags().StringVar(&localPath, "local-path", "", "local canonical repository path (mutually exclusive with --source)")
	cmd.Flags().StringVar(&ref, "ref", "", "git ref (branch/tag) to track")
	cmd.Flags().StringVar(&commit, "commit", "", "pin to this commit SHA (required for a local path unless --floating; URLs resolve --ref automatically)")
	cmd.Flags().BoolVar(&floating, "floating", false, "do not pin to a SHA (pinning is the default)")
	cmd.Flags().StringArrayVar(&targets, "target", nil, "target adapter to enable (repeatable)")
	cmd.Flags().StringVar(&dir, "dir", "", "workspace directory (default: current directory)")
	return cmd
}

// resolvePin fills cfg.Commit when pinning is required. For a URL with a
// ref, it resolves the ref to a SHA over the network. Local paths are not
// network-resolved here (sync handles a pinned local path; floating local
// is rejected downstream).
func resolvePin(ctx context.Context, cfg *wizard.InitConfig) error {
	if cfg.Floating || cfg.Commit != "" {
		return nil
	}
	if cfg.SourceURL == "" {
		// Local path with no commit: leave unpinned; Validate / sync will
		// guide the user (floating local is unsupported).
		return nil
	}
	canonical, err := cache.Canonicalize(cfg.SourceURL)
	if err != nil {
		return fmt.Errorf("canonicalize source: %w", err)
	}
	refToResolve := cfg.Ref
	if refToResolve == "" {
		refToResolve = "HEAD"
	}
	sha, err := git.ResolveRef(ctx, canonical, refToResolve)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", refToResolve, err)
	}
	cfg.Commit = sha
	return nil
}

func manifestPathFor(dir string) string {
	if dir == "" {
		return workspace.ManifestName
	}
	return filepath.Join(dir, workspace.ManifestName)
}

// bundledTargetNames returns the selectable target names for the wizard.
func bundledTargetNames() []string {
	names := make([]string, 0, len(bundledAdapters()))
	for _, b := range bundledAdapters() {
		names = append(names, b.Manifest.Name)
	}
	return names
}

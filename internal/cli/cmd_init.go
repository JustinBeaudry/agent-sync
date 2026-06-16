package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/agent-sync/agent-sync/internal/cache"
	"github.com/agent-sync/agent-sync/internal/git"
	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/tui"
	"github.com/agent-sync/agent-sync/internal/tui/wizard"
	"github.com/agent-sync/agent-sync/internal/workspace"
)

func newInitCommand(deps RootDeps) *cobra.Command {
	var (
		source    string
		localPath string
		localDir  string
		ref       string
		commit    string
		floating  bool
		targets   []string
		dir       string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a workspace manifest (.agent-sync.yaml)",
		Long: "Initialize a workspace by writing a .agent-sync.yaml manifest. Runs an " +
			"interactive wizard on a TTY; with --non-interactive every value " +
			"must be supplied via flags.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := mustRuntime(cmd)
			if err != nil {
				return err
			}

			// The destination directory comes from --dir, falling back to the
			// global --workspace flag, so `agent-sync --workspace X init ...` writes
			// to X.
			destDir := dir
			if destDir == "" {
				destDir = rc.Flags.Workspace
			}

			cfg := wizard.InitConfig{
				Dir:       destDir,
				SourceURL: source,
				LocalPath: localPath,
				LocalDir:  localDir,
				Ref:       ref,
				Commit:    commit,
				Floating:  floating,
				Targets:   targets,
			}

			interactive := tui.Interactive(rc.Access.IsTTY, rc.Access.NonInteractive, rc.Access.Accessible)
			if interactive && source == "" && localPath == "" && localDir == "" {
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
				cfg.Dir = destDir
				cfg.Floating = floating
			} else {
				// Non-interactive (or flags supplied): require a source. Name
				// all three flags — init accepts --source, --local-path, or
				// --local-dir.
				if err := requireFlag(rc.Access.NonInteractive, source != "" || localPath != "" || localDir != "", "--source/--local-path/--local-dir", "canonical repo URL, local git path, or in-repo directory"); err != nil {
					return err
				}
			}

			// Pin-at-init (invariant #4): resolve the ref to a SHA unless
			// floating. A URL resolves over the network; a local path resolves
			// against the local repo (no network). Already-pinned configs are
			// left as-is.
			if err := resolvePin(cmd.Context(), &cfg, rc.Flags.Offline); err != nil {
				return fmt.Errorf("init: %w", err)
			}

			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("init: %w", err)
			}

			target := manifestPathFor(cfg.Dir)
			if _, statErr := os.Stat(target); statErr == nil {
				return fmt.Errorf("init: %s already exists (refusing to overwrite)", target)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				// A non-"not found" stat error (permission, bad path) is fatal —
				// proceeding would surface a less clear error later.
				return fmt.Errorf("init: cannot check %s: %w", target, statErr)
			}
			data, err := cfg.ManifestYAML()
			if err != nil {
				return fmt.Errorf("init: render manifest: %w", err)
			}
			if err := manifest.WriteFile(target, data); err != nil {
				return fmt.Errorf("init: write manifest: %w", err)
			}
			// Confirm the written manifest re-loads cleanly; if it doesn't,
			// remove it (best-effort) so a broken .agent-sync.yaml is never left
			// stranding the user.
			if _, err := manifest.LoadFile(target, manifest.LoadOptions{}); err != nil {
				_ = os.Remove(target)
				return fmt.Errorf("init: wrote an invalid manifest (removed): %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", target)
			return nil
		},
	}

	cmd.Flags().StringVar(&source, "source", "", "canonical repository URL")
	cmd.Flags().StringVar(&localPath, "local-path", "", "local canonical git repository path (mutually exclusive with --source)")
	cmd.Flags().StringVar(&localDir, "local-dir", "", "in-repo working-tree source directory, e.g. .agents (unpinned; mutually exclusive with --source/--local-path)")
	cmd.Flags().StringVar(&ref, "ref", "", "git ref (branch/tag) to track")
	cmd.Flags().StringVar(&commit, "commit", "", "pin to this commit SHA (required for a local path unless --floating; URLs resolve --ref automatically)")
	cmd.Flags().BoolVar(&floating, "floating", false, "do not pin to a SHA (pinning is the default)")
	cmd.Flags().StringArrayVar(&targets, "target", nil, "target adapter to enable (repeatable)")
	cmd.Flags().StringVar(&dir, "dir", "", "workspace directory (default: current directory)")
	return cmd
}

// resolvePin fills cfg.Commit when pinning is required:
//   - A local path resolves its ref/HEAD against the local repo (no
//     network), so it works offline and the wizard can pin a local source.
//   - A URL resolves its ref to a SHA over the network; this is refused
//     under --offline, since a remote lookup cannot run offline.
//
// Already-pinned (cfg.Commit set) and floating configs are left untouched.
func resolvePin(ctx context.Context, cfg *wizard.InitConfig, offline bool) error {
	// An in-repo working-tree source has no commit to resolve or pin.
	if cfg.LocalDir != "" {
		return nil
	}
	if cfg.Floating || cfg.Commit != "" {
		return nil
	}

	if cfg.LocalPath != "" {
		abs, err := filepath.Abs(cfg.LocalPath)
		if err != nil {
			return fmt.Errorf("resolve local path %q: %w", cfg.LocalPath, err)
		}
		sha, err := git.ResolveLocalRef(ctx, abs, cfg.Ref)
		if err != nil {
			return fmt.Errorf("resolve local ref: %w", err)
		}
		cfg.Commit = sha
		return nil
	}

	if cfg.SourceURL == "" {
		return nil
	}
	if offline {
		return fmt.Errorf("cannot resolve a remote ref under --offline; pass --commit to pin or --floating to skip pinning")
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

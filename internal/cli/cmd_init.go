package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/agent-sync/agent-sync/internal/cache"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/git"
	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/tui"
	"github.com/agent-sync/agent-sync/internal/tui/wizard"
	"github.com/agent-sync/agent-sync/internal/workspace"
)

func newInitCommand(deps RootDeps) *cobra.Command {
	var (
		source         string
		localPath      string
		localDir       string
		ref            string
		commit         string
		floating       bool
		targets        []string
		dir            string
		user           bool
		activationRoot bool
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

			scope := manifest.ScopeProject
			// --user writes into the resolved home directory and marks the
			// manifest as the user scope.
			destDir := dir
			if user {
				if dir != "" {
					return errors.New("init: --user cannot be combined with --dir")
				}
				if rc.Flags.Workspace != "" {
					return errUserWithWorkspace
				}
				home, hErr := resolveHome()
				if hErr != nil {
					return fmt.Errorf("init: resolve home: %w", hErr)
				}
				destDir = home
				scope = manifest.ScopeUser
			} else {
				// The destination directory comes from --dir, falling back to the
				// global --workspace flag, so `agent-sync --workspace X init ...`
				// writes to X and sets scope=workspace.
				if destDir == "" {
					destDir = rc.Flags.Workspace
					if destDir != "" {
						scope = manifest.ScopeWorkspace
					}
				}
			}
			if activationRoot && scope != manifest.ScopeWorkspace {
				return fmt.Errorf("init: --activation-root requires --workspace (scope must be %q)", manifest.ScopeWorkspace)
			}
			// A bad destination must fail as a directory error before any
			// discovery or targets messaging can mask it (plan R8). An empty
			// destDir means the cwd, which exists.
			if destDir != "" {
				info, statErr := os.Stat(destDir)
				switch {
				case errors.Is(statErr, os.ErrNotExist):
					return fmt.Errorf("init: directory %s does not exist", destDir)
				case statErr != nil:
					return fmt.Errorf("init: cannot check %s: %w", destDir, statErr)
				case !info.IsDir():
					return fmt.Errorf("init: %s is not a directory", destDir)
				}
			}
			// Refuse an existing manifest before the wizard or discovery run,
			// so the user never completes the whole flow to hit the refusal.
			target := manifestPathFor(destDir)
			if _, statErr := os.Stat(target); statErr == nil {
				return fmt.Errorf("init: %s already exists (refusing to overwrite)", target)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				// A non-"not found" stat error (permission, bad path) is fatal —
				// proceeding would surface a less clear error later.
				return fmt.Errorf("init: cannot check %s: %w", target, statErr)
			}

			cfg := wizard.InitConfig{
				Dir:            destDir,
				SourceURL:      source,
				LocalPath:      localPath,
				LocalDir:       localDir,
				Ref:            ref,
				Commit:         commit,
				Floating:       floating,
				Scope:          scope,
				ActivationRoot: activationRoot,
				Targets:        targets,
			}
			// sourceDefaulted / discovered / notEnabled record what init
			// inferred, so the success line announces it (plan R14): a
			// defaulting feature that prints only "wrote ..." hides
			// misdiscovery until sync.
			sourceDefaulted := false
			var discovered, notEnabled []string

			// Footprint probe (plan R4): read-only stats of the destination
			// for each bundled adapter's reserved-prefix dir. Computed up
			// front so the wizard path can preselect from it (plan R11 keeps
			// the wizard itself free of fs I/O).
			probeDir := destDir
			if probeDir == "" {
				probeDir = "."
			}
			found, warns := discoverTargets(probeDir, bundledAdapters())
			for _, w := range warns {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}

			interactive := tui.Interactive(rc.Access.IsTTY, rc.Access.NonInteractive, rc.Access.Accessible)
			if shouldRunInitWizard(interactive, source, localPath, localDir, targets) {
				// Drive the wizard to collect the source/ref/targets.
				wcfg, committed, werr := wizard.Run(
					cmd.Context(), deps.in(), deps.err(), rc.Access.NoColor, bundledTargetNames(), found,
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
				cfg.Scope = scope
				cfg.ActivationRoot = activationRoot
			} else {
				if source == "" && localPath == "" && localDir == "" {
					// No source flag: default to the in-repo .agents working-tree
					// source (plan R1). Pin flags contradict that default — the
					// .agents source is unpinned — so name the conflict instead of
					// letting the generic local-dir validation confuse the user
					// (plan R3).
					if pinFlag := firstPinFlag(ref, commit, floating); pinFlag != "" {
						return fmt.Errorf("init: %s requires --source or --local-path; without a source flag init defaults to the unpinned .agents in-repo source", pinFlag)
					}
					cfg.LocalDir = defaultLocalDir
					sourceDefaulted = true
				}

				// Targets: explicit --target flags win outright (plan R5); with
				// none, snapshot the discovered footprints (plan R4). Zero
				// discovered is not an error — an empty targets list is the
				// spec-valid "not yet configured" state — but it gets a hint so
				// the user knows why nothing will sync (plan R6).
				if len(targets) == 0 {
					cfg.Targets = found
					discovered = found
					if len(found) == 0 {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"hint: no target tool footprints (.claude, .cursor, ...) found in %s; enable targets with --target or edit targets: in %s (PATH adapters are never auto-discovered)\n",
							probeDir, workspace.ManifestName)
					}
				} else {
					for _, name := range found {
						if !slices.Contains(targets, name) {
							notEnabled = append(notEnabled, name)
						}
					}
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
			// A local_dir manifest whose directory is missing would hard-fail
			// the first sync with a missing-source error; create it (empty) so
			// that degrades to the zero-emit hint instead (plan R2). Creation
			// failure is a warning, not an init failure — the manifest is
			// already valid.
			if cfg.LocalDir != "" {
				if err := ensureLocalDir(cfg.Dir, cfg.LocalDir); err != nil {
					rc.Logger.Warn("init: could not create the local source directory; create it before running sync",
						"dir", cfg.LocalDir, "err", err)
				}
			}
			var notes []string
			if sourceDefaulted {
				notes = append(notes, fmt.Sprintf("source: %s [default]", defaultLocalDir))
			}
			if len(discovered) > 0 {
				notes = append(notes, fmt.Sprintf("targets: %s [discovered]", strings.Join(discovered, ", ")))
			}
			if len(notEnabled) > 0 {
				notes = append(notes, fmt.Sprintf("also detected (not enabled): %s", strings.Join(notEnabled, ", ")))
			}
			suffix := ""
			if len(notes) > 0 {
				suffix = fmt.Sprintf(" (%s)", strings.Join(notes, "; "))
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s%s\n", target, suffix)
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
	cmd.Flags().BoolVar(&user, "user", false, "create a user-level manifest at the home directory (~)")
	cmd.Flags().BoolVar(&activationRoot, "activation-root", false, "mark manifest as the workspace activation root (requires workspace scope)")
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

// defaultLocalDir is the canonical-source fallback when init is given no
// source flag: the in-repo working-tree directory named by AGENTS.md
// invariant #4 as the documented unpinned source.
const defaultLocalDir = ".agents"

// shouldRunInitWizard reports whether init drives the interactive wizard: a
// fully-unspecified interactive invocation. Any source flag — or an explicit
// --target, which combined with the .agents source default makes the
// invocation fully specified — skips the wizard (plan R12).
func shouldRunInitWizard(interactive bool, source, localPath, localDir string, targets []string) bool {
	return interactive && source == "" && localPath == "" && localDir == "" && len(targets) == 0
}

// firstPinFlag names the first pin-related flag in use, or "" when none is.
// Used to reject pin flags when no source flag was given (the defaulted
// .agents source is unpinned, so a pin flag signals a misunderstanding).
func firstPinFlag(ref, commit string, floating bool) string {
	switch {
	case ref != "":
		return "--ref"
	case commit != "":
		return "--commit"
	case floating:
		return "--floating"
	}
	return ""
}

// ensureLocalDir creates the manifest's local_dir under the workspace when
// missing. The write goes through fsroot (AGENTS.md invariant #1); the
// probing stat is read-only per existing precedent.
func ensureLocalDir(wsDir, localDir string) error {
	base := wsDir
	if base == "" {
		base = "."
	}
	if info, err := os.Stat(filepath.Join(base, localDir)); err == nil {
		if info.IsDir() {
			return nil
		}
		return fmt.Errorf("%s exists and is not a directory", localDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	root, err := fsroot.OpenWorkspaceRoot(base)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.Inner().MkdirAll(localDir, 0o755)
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

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aienvs/aienvs/internal/hooks"
	"github.com/aienvs/aienvs/internal/workspace"
)

func newHooksCommand(deps RootDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hooks",
		Short:         "Install or remove git hooks that run sync after pulls",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newHooksInstallCommand(deps), newHooksUninstallCommand())
	return cmd
}

func newHooksInstallCommand(_ RootDeps) *cobra.Command {
	var (
		replace      bool
		appendHook   bool
		installHooks bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install post-merge/post-checkout hooks that run `aienvs sync`",
		Long: "Install git hooks (post-merge, post-checkout) that run " +
			"`aienvs sync --post-merge` after pulls and checkouts. Requires " +
			"--install-hooks to proceed (hooks change git behavior, so the " +
			"opt-in is explicit; this command never prompts).",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := mustRuntime(cmd)
			if err != nil {
				return err
			}

			// Explicit opt-in: hooks change git behavior, so --install-hooks
			// is always required. This command never prompts.
			if !installHooks {
				return fmt.Errorf("hooks install: pass --install-hooks to confirm modifying git hooks")
			}

			ws, err := workspace.Find(rc.Flags.Workspace, workspace.Options{Workspace: rc.Flags.Workspace})
			if err != nil {
				return fmt.Errorf("hooks install: locate workspace: %w", err)
			}
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("hooks install: resolve aienvs path: %w", err)
			}
			if abs, aerr := filepath.Abs(self); aerr == nil {
				self = abs
			}

			res, err := hooks.Install(ws.Root, hooks.Options{
				AienvsPath:    self,
				WorkspacePath: ws.Root,
				Replace:       replace,
				Append:        appendHook,
			})
			if err != nil {
				return fmt.Errorf("hooks install: %w", err)
			}
			w := cmd.OutOrStdout()
			for _, a := range res.Advisory {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "advisory:", a)
			}
			for _, b := range res.BackedUp {
				_, _ = fmt.Fprintln(w, "backed up:", b)
			}
			_, _ = fmt.Fprintf(w, "installed hooks: %v\n", res.Installed)
			return nil
		},
	}
	cmd.Flags().BoolVar(&installHooks, "install-hooks", false, "confirm installation of git hooks")
	cmd.Flags().BoolVar(&replace, "replace", false, "back up and overwrite an existing foreign hook")
	cmd.Flags().BoolVar(&appendHook, "append", false, "wrap an existing foreign hook (run it, then aienvs)")
	return cmd
}

func newHooksUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "uninstall",
		Short:         "Remove aienvs-managed git hooks",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := mustRuntime(cmd)
			if err != nil {
				return err
			}
			ws, err := workspace.Find(rc.Flags.Workspace, workspace.Options{Workspace: rc.Flags.Workspace})
			if err != nil {
				return fmt.Errorf("hooks uninstall: locate workspace: %w", err)
			}
			removed, err := hooks.Uninstall(ws.Root)
			if err != nil {
				return fmt.Errorf("hooks uninstall: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed hooks: %v\n", removed)
			return nil
		},
	}
}

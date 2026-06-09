package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// runtimeContext carries the resolved per-invocation Access, logger, and
// persistent flags from the root's PersistentPreRunE down to every
// subcommand via the command context.
type runtimeContext struct {
	Access Access
	Logger *slog.Logger
	Flags  PersistentFlags
	Deps   RootDeps
}

type runtimeKey struct{}

// runtimeFrom retrieves the runtimeContext a subcommand needs. It is
// always present once PersistentPreRunE has run.
func runtimeFrom(ctx context.Context) (*runtimeContext, bool) {
	rc, ok := ctx.Value(runtimeKey{}).(*runtimeContext)
	return rc, ok
}

// mustRuntime returns the runtimeContext or an error. The root's
// PersistentPreRunE always populates it before a subcommand's RunE runs,
// so this only fails if a subcommand shadows the root's PersistentPreRunE
// — a guard against that future footgun rather than a nil-deref panic.
func mustRuntime(cmd *cobra.Command) (*runtimeContext, error) {
	rc, ok := runtimeFrom(cmd.Context())
	if !ok || rc == nil {
		return nil, errors.New("cli: runtime context missing; root initialization did not run")
	}
	return rc, nil
}

// PersistentFlags holds the root-level flag values shared by every
// subcommand. They are bound on the root and read via the resolved
// Access context.
type PersistentFlags struct {
	Workspace      string
	Output         string
	LogLevel       string
	Offline        bool
	NonInteractive bool
}

// RootDeps injects process-level dependencies. Zero values resolve to the
// real process streams and clock, so production passes RootDeps{} and
// tests inject buffers.
type RootDeps struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Now     func() time.Time
	Version string
}

func (d RootDeps) in() io.Reader {
	if d.In != nil {
		return d.In
	}
	return os.Stdin
}

func (d RootDeps) out() io.Writer {
	if d.Out != nil {
		return d.Out
	}
	return os.Stdout
}

func (d RootDeps) err() io.Writer {
	if d.Err != nil {
		return d.Err
	}
	return os.Stderr
}

func (d RootDeps) now() func() time.Time {
	if d.Now != nil {
		return d.Now
	}
	return time.Now
}

// NewRootCommand builds the `aienvs` root command with persistent flags
// and every subcommand mounted. It mirrors the existing factory+Deps
// pattern (NewTrustCommand/NewAdapterCommand). The returned command is
// fully functional via Execute() without Fang; Fang styling is applied at
// the main boundary (KTD-8) once the TUI dependencies land.
func NewRootCommand(deps RootDeps) *cobra.Command {
	flags := &PersistentFlags{}

	root := &cobra.Command{
		Use:   "aienvs",
		Short: "Keep AI-agent configuration in sync from a Git-backed manifest",
		Long: "aienvs syncs AI-agent configuration for multiple tools (Claude Code, " +
			"Cursor, and more) from a single Git-backed canonical manifest.",
		Version:       deps.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Resolve the per-invocation Access + logger once and stash them in
		// the context so every subcommand reads the same resolution (KTD-5,
		// KTD-6). cobra runs the root's PersistentPreRunE before the
		// subcommand's RunE.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			access := resolveAccessFromFlags(cmd, deps, flags)
			rc := &runtimeContext{
				Access: access,
				Logger: newLogger(deps.err(), access, flags.LogLevel),
				Flags:  *flags,
				Deps:   deps,
			}
			cmd.SetContext(context.WithValue(cmd.Context(), runtimeKey{}, rc))
			return nil
		},
	}

	root.SetIn(deps.in())
	root.SetOut(deps.out())
	root.SetErr(deps.err())

	pf := root.PersistentFlags()
	pf.StringVar(&flags.Workspace, "workspace", "", "workspace directory (default: discover from cwd)")
	pf.StringVar(&flags.Output, "output", "", "output format: text or json (default: text on a TTY, json when piped)")
	pf.StringVar(&flags.LogLevel, "log-level", "info", "log level: debug, info, warn, error")
	pf.BoolVar(&flags.Offline, "offline", false, "forbid all network operations")
	pf.BoolVar(&flags.NonInteractive, "non-interactive", false, "never prompt; fail fast on a missing required value")
	pf.Bool("yes", false, "alias for --non-interactive")

	// Mount subcommands.
	root.AddCommand(newInitCommand(deps))
	root.AddCommand(newSyncCommand(deps))
	root.AddCommand(newStatusCommand())
	root.AddCommand(newValidateCommand(deps))
	root.AddCommand(newHooksCommand(deps))
	root.AddCommand(newWatchCommand(deps))
	root.AddCommand(NewTrustCommand(TrustDeps{
		In:  deps.in(),
		Out: deps.out(),
		Err: deps.err(),
		Now: deps.now(),
	}))
	root.AddCommand(NewAdapterCommand(AdapterDeps{
		In:  deps.in(),
		Out: deps.out(),
		Err: deps.err(),
		Now: deps.now(),
	}))

	return root
}

// resolveAccessFromFlags builds the Access context from the resolved
// persistent flags and the command's streams. --yes is an alias for
// --non-interactive.
func resolveAccessFromFlags(cmd *cobra.Command, deps RootDeps, flags *PersistentFlags) Access {
	nonInteractive := flags.NonInteractive
	if yes, err := cmd.Flags().GetBool("yes"); err == nil && yes {
		nonInteractive = true
	}
	return ResolveAccess(deps.in(), deps.out(), nonInteractive, flags.Output)
}

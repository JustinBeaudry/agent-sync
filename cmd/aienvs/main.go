// Command aienvs is the entry point for the aienvs CLI.
//
// aienvs keeps AI-agent configuration for multiple tools in sync from a
// single Git-backed manifest. See the repository README and
// docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md for the
// architecture.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aienvs/aienvs/internal/cli"
)

// version is overwritten at build time via -ldflags.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	root := cli.NewRootCommand(cli.RootDeps{Version: version})
	root.SetArgs(args)

	// Plain cobra execution. Fang styling (KTD-8) is applied here once the
	// charm dependencies land with the TUI units; the command tree is fully
	// functional without it.
	err := root.ExecuteContext(context.Background())
	if err != nil {
		// Root sets SilenceErrors, so surface the error here and map it to a
		// process exit code via the documented exit-code carriers.
		_, _ = fmt.Fprintln(os.Stderr, "aienvs:", err)
		return cli.MapExit(err)
	}
	return 0
}

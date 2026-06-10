// Command agent-sync is the entry point for the agent-sync CLI.
//
// agent-sync keeps AI-agent configuration for multiple tools in sync from a
// single Git-backed manifest. See the repository README and
// docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md for the
// architecture.
package main

import (
	"context"
	"os"

	"github.com/charmbracelet/fang"

	"github.com/agent-sync/agent-sync/internal/cli"
)

// version is overwritten at build time via -ldflags.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	root := cli.NewRootCommand(cli.RootDeps{Version: version})
	root.SetArgs(args)

	// Fang wraps the cobra root for styled help/errors/man/completions
	// (KTD-8). It prints the error itself (the root sets SilenceErrors); we
	// translate it to a process exit code via the documented exit-code
	// carriers (trust, adapter, missing-flag, report verdict).
	err := fang.Execute(context.Background(), root)
	if err != nil {
		return cli.MapExit(err)
	}
	return 0
}

// Command aienvs is the entry point for the aienvs CLI.
//
// aienvs keeps AI-agent configuration for multiple tools in sync from a
// single Git-backed manifest. See the repository README and
// docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md for the
// architecture.
//
// This file is an intentional stub until unit 16 (cobra + fang wiring)
// lands. It exists now so every later unit can be wired through `go run`
// and so the Go module has a buildable main package.
package main

import (
	"fmt"
	"os"
)

// version is overwritten at build time via -ldflags.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v") {
		_, _ = fmt.Fprintln(os.Stdout, version)
		return 0
	}
	_, _ = fmt.Fprintln(os.Stderr, "aienvs: CLI surface not yet implemented (unit 16).")
	_, _ = fmt.Fprintln(os.Stderr, "       See docs/plans/2026-04-21-001-feat-aienvs-workspace-cli-plan.md")
	return 2
}

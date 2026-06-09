package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCommand_HelpListsSubcommands(t *testing.T) {
	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	help := out.String()
	for _, sub := range []string{"trust", "adapter"} {
		if !strings.Contains(help, sub) {
			t.Errorf("help missing subcommand %q:\n%s", sub, help)
		}
	}
}

func TestRootCommand_Version(t *testing.T) {
	var out bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Version: "1.2.3"})
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !strings.Contains(out.String(), "1.2.3") {
		t.Fatalf("version output = %q, want 1.2.3", out.String())
	}
}

func TestRootCommand_PersistentFlagsRegistered(t *testing.T) {
	root := NewRootCommand(RootDeps{})
	for _, name := range []string{"workspace", "output", "log-level", "offline", "floating", "non-interactive", "yes"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("persistent flag --%s not registered", name)
		}
	}
}

// TestRootCommand_PreRunStashesRuntime verifies PersistentPreRunE resolves
// and stashes the runtimeContext that subcommands consume, and that --yes
// aliases --non-interactive.
func TestRootCommand_PreRunStashesRuntime(t *testing.T) {
	var captured *runtimeContext
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, ok := runtimeFrom(cmd.Context())
			if !ok {
				t.Fatal("runtimeContext not found in context")
			}
			captured = rc
			return nil
		},
	}
	root := NewRootCommand(RootDeps{})
	root.AddCommand(probe)
	root.SetArgs([]string{"probe", "--yes", "--log-level", "debug"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if captured == nil {
		t.Fatal("runtimeContext was not captured")
	}
	if !captured.Access.NonInteractive {
		t.Error("--yes should set Access.NonInteractive")
	}
	if captured.Logger == nil {
		t.Error("runtime logger is nil")
	}
	if captured.Flags.LogLevel != "debug" {
		t.Errorf("flags.LogLevel = %q, want debug", captured.Flags.LogLevel)
	}
}

func TestNewLogger_Constructs(t *testing.T) {
	var buf bytes.Buffer
	// Non-interactive → JSON handler; just confirm it logs without panicking.
	lg := newLogger(&buf, Access{IsTTY: false, NonInteractive: true}, "warn")
	lg.Warn("hello", "k", "v")
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("logger output = %q, want it to contain the message", buf.String())
	}
}

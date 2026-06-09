// Package claude implements the bundled agent-sync adapter for Claude Code.
//
// The adapter consumes IR v1 nodes and emits the v1 op vocabulary
// (write_file, write_tool_owned, mkdir, delete, warning) for Claude
// Code's actual on-disk layout:
//
//   - rule    -> .claude/rules/aienvs/<id>.md
//   - command -> .claude/commands/aienvs/<id>.md
//   - skill   -> .claude/skills/aienvs-<id>/SKILL.md plus assets
//   - mcp-server-entry -> .mcp.json (workspace root) via write_tool_owned
//   - agents-md (companion overlay) -> CLAUDE.md (workspace root)
//   - plugin-reference -> warning, unsupported by design
//
// Bundled() returns an *adapter.BundledAdapter ready for the runtime
// to spawn over an in-process pipe; nothing else in this package is
// part of the public surface.
package claude

import (
	"context"
	"fmt"
	"io"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	adapterName    = "claude"
	adapterVersion = "0.1"
	reservedPrefix = ".claude"
)

// Bundled returns the adapter.BundledAdapter for the claude target.
// The runtime registers it via adapter.DiscoverOptions.Bundled.
func Bundled() *adapter.BundledAdapter {
	return &adapter.BundledAdapter{
		Manifest: adapter.AdapterManifest{
			Name:            adapterName,
			Version:         adapterVersion,
			ContractVersion: adapter.ContractVersionV1,
			ReservedPrefix:  reservedPrefix,
			// Bundled adapters are spawned in-process via Run; the
			// Command slice is required by the shared manifest
			// validator (validateAdapterManifest in
			// internal/adapter/discover.go) but never executed for
			// SourceBundled — the runtime takes the Run path. The
			// placeholder name is recorded for diagnostics only.
			Command: []string{"aienvs-adapter-claude-bundled"},
		},
		Run: run,
	}
}

// run is the in-process entry point invoked by InprocTransport. It
// constructs an adapterkit.Server bound to the supplied pipes, wires
// up the lifecycle handlers, and serves until context is cancelled
// or the runtime sends shutdown.
func run(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	server := adapterkit.NewServer(adapterkit.ServerOptions{
		Name:    adapterName,
		Version: adapterVersion,
		Stdin:   stdin,
		Stdout:  stdout,
		// stderr defaults to os.Stderr; bundled in-process adapters
		// share the parent process so this routes diagnostics to the
		// CLI's own stderr, which is the documented place for them.
		// Getenv defaults to os.Getenv; bundled adapters skip the
		// magic-cookie check (the runtime sets cookie="" for bundled,
		// see internal/adapter/runtime.go Initialize), so the env-var
		// read still happens but the runtime ignores any echoed
		// cookie value.
		//
		// We supply a constant 32-hex string for the env-var so the
		// SDK's startup magic-cookie format check passes; the runtime
		// ignores the echoed value for bundled adapters.
		Getenv: bundledGetenv,
	})

	server.OnInitialize(func(_ context.Context, _ adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
		return adapterkit.InitializeResult{
			Capabilities:    capabilitiesForWire(),
			DeclaredOutputs: declaredOutputs(),
		}, nil
	})

	server.OnEmit(handleEmit)

	if err := server.Run(ctx); err != nil {
		return fmt.Errorf("claude bundled adapter: %w", err)
	}
	return nil
}

// bundledCookie is a 32-hex placeholder sent through the adapterkit
// Server's cookie format check. Bundled adapters skip cookie
// validation at the runtime layer (see internal/adapter/runtime.go
// Initialize: cookie validation is gated on Source != SourceBundled),
// so the value is opaque to the runtime — only the SDK's "format
// must be 32 hex" check observes it.
const bundledCookie = "00000000000000000000000000000000"

func bundledGetenv(name string) string {
	if name == adapterkit.CookieEnvVar {
		return bundledCookie
	}
	return ""
}

// handleEmit lives in emit.go.

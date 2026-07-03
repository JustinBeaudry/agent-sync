// Package cursor implements the bundled agent-sync adapter for Cursor.
//
// The adapter consumes IR v1 nodes and emits the v1 op vocabulary
// (write_file, write_tool_owned, mkdir, delete, warning) for Cursor's
// actual on-disk layout:
//
//   - rule    -> .cursor/rules/agent-sync/<id>.mdc
//   - mcp-server-entry -> .cursor/mcp.json via write_tool_owned
//   - agents-md -> AGENTS.md (workspace root) via write_tool_owned
//   - skill            -> warning, unsupported (Cursor has no skills concept)
//   - command          -> warning, unsupported (no project-level commands)
//   - plugin-reference -> warning, unsupported (no project plugin registry)
//
// It is a structural sibling of internal/adapter/bundled/claude: same
// SDK-backed lifecycle, same managed-file-header / section-marker /
// per-subdir-README helpers, same summary-only OpsPerformed wire
// behavior, same zero-file-I/O invariant. Only the destination mapping
// and the capability matrix differ.
//
// Bundled() returns an *adapter.BundledAdapter ready for the runtime
// to spawn over an in-process pipe; nothing else in this package is
// part of the public surface.
package cursor

import (
	"context"
	"fmt"
	"io"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	adapterName    = "cursor"
	adapterVersion = "0.1"
	reservedPrefix = ".cursor"
)

// Bundled returns the adapter.BundledAdapter for the cursor target.
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
			Command: []string{"agent-sync-adapter-cursor-bundled"},
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
		// See claude/bundled.go: bundled adapters skip cookie
		// validation at the runtime layer, but the SDK's startup
		// magic-cookie format check still observes the env-var, so we
		// supply a constant 32-hex string the runtime then ignores.
		Getenv: bundledGetenv,
	})

	// scope is captured at initialize and read at emit. The adapterkit Server
	// processes initialize before emit on a single goroutine, so this needs no
	// synchronization; each run() (one per session) has its own variable.
	var scope, sourceURL, sourceCommit string

	server.OnInitialize(func(_ context.Context, params adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
		scope = params.Scope
		sourceURL = params.SourceURL
		sourceCommit = params.SourceCommit
		return adapterkit.InitializeResult{
			Capabilities:    capabilitiesForWire(scope),
			DeclaredOutputs: declaredOutputs(scope),
		}, nil
	})

	server.OnEmit(func(ctx context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
		return handleEmit(ctx, params, scope, sourceURL, sourceCommit)
	})

	if err := server.Run(ctx); err != nil {
		return fmt.Errorf("cursor bundled adapter: %w", err)
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

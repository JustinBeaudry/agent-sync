// Package pi implements the bundled agent-sync adapter for Pi
// (@mariozechner/pi-coding-agent, repo earendil-works/pi).
//
// The adapter consumes IR v1 nodes and emits the v1 op vocabulary
// (write_file, write_tool_owned, mkdir, delete, warning) for Pi's actual
// on-disk layout (validated 2026-06-30):
//
//   - agents-md        -> AGENTS.md managed section via write_tool_owned
//     (project root; user scope: .pi/agent/AGENTS.md, i.e. ~/.pi/agent/AGENTS.md)
//   - skill            -> .agents/skills/agent-sync-<id>/SKILL.md (+assets)
//   - mcp-server-entry -> warning, unsupported BY DESIGN (Pi rejects MCP)
//   - rule             -> warning, unsupported (no per-tool rule concept)
//   - command          -> warning, unsupported (planned; Pi's prompt dir is flat + shared)
//   - plugin-reference -> warning, unsupported (no project plugin registry)
//
// Like codex, pi owns NO dedicated reserved subdirectory: skills live under the
// shared .agents/skills/ tree (read by Pi and other agents) and prose lives in
// the shared AGENTS.md. The two tool-owned agents-md destination is scope-aware:
// at user scope it targets Pi's real user-config path. See resolvePathSet and
// docs/adapters/pi.md.
//
// It is a structural sibling of internal/adapter/bundled/{claude,cursor,codex}:
// same SDK-backed lifecycle, same managed-file-header / section-marker helpers,
// same summary-only OpsPerformed wire behavior, same zero-file-I/O invariant.
//
// Bundled() returns an *adapter.BundledAdapter ready for the runtime to spawn
// over an in-process pipe; nothing else in this package is public.
package pi

import (
	"context"
	"fmt"
	"io"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	adapterName    = "pi"
	adapterVersion = "0.1"
	// reservedPrefix is pi's collision-detection namespace (discover.go rejects
	// nested adapter prefixes). The adapter's actual owned outputs span
	// .agents/skills/ and AGENTS.md — declared via declaredOutputs(), not
	// constrained to this prefix (codex/cursor likewise declare AGENTS.md
	// outside their prefix). There is intentionally no .pi/agent-sync/ reserved
	// subdirectory in PR1 (commands, which would live under .pi/prompts/, are a
	// planned follow-up).
	reservedPrefix = ".pi"
)

// Bundled returns the adapter.BundledAdapter for the pi target. The runtime
// registers it via adapter.DiscoverOptions.Bundled.
func Bundled() *adapter.BundledAdapter {
	return &adapter.BundledAdapter{
		Manifest: adapter.AdapterManifest{
			Name:            adapterName,
			Version:         adapterVersion,
			ContractVersion: adapter.ContractVersionV1,
			ReservedPrefix:  reservedPrefix,
			// Bundled adapters are spawned in-process via Run; the Command slice
			// is required by the shared manifest validator but never executed
			// for SourceBundled. Placeholder recorded for diagnostics.
			Command: []string{"agent-sync-adapter-pi-bundled"},
		},
		Run: run,
	}
}

// run is the in-process entry point invoked by InprocTransport. It constructs
// an adapterkit.Server bound to the supplied pipes, wires up the lifecycle
// handlers, and serves until context is cancelled or the runtime sends shutdown.
func run(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	server := adapterkit.NewServer(adapterkit.ServerOptions{
		Name:    adapterName,
		Version: adapterVersion,
		Stdin:   stdin,
		Stdout:  stdout,
		// See claude/bundled.go: bundled adapters skip cookie validation at the
		// runtime layer, but the SDK's startup magic-cookie format check still
		// observes the env-var, so we supply a constant 32-hex string the
		// runtime then ignores.
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
			Capabilities:    capabilitiesForWire(),
			DeclaredOutputs: declaredOutputs(scope),
		}, nil
	})

	server.OnEmit(func(ctx context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
		return handleEmit(ctx, params, scope, sourceURL, sourceCommit)
	})

	if err := server.Run(ctx); err != nil {
		return fmt.Errorf("pi bundled adapter: %w", err)
	}
	return nil
}

// bundledCookie is a 32-hex placeholder sent through the adapterkit Server's
// cookie format check. Bundled adapters skip cookie validation at the runtime
// layer (see internal/adapter/runtime.go Initialize), so the value is opaque to
// the runtime — only the SDK's "format must be 32 hex" check observes it.
const bundledCookie = "00000000000000000000000000000000"

func bundledGetenv(name string) string {
	if name == adapterkit.CookieEnvVar {
		return bundledCookie
	}
	return ""
}

// handleEmit lives in emit.go.

// Package codex implements the bundled agent-sync adapter for Codex CLI.
//
// The adapter consumes IR v1 nodes and emits the v1 op vocabulary
// (write_file, write_tool_owned, mkdir, delete, warning) for Codex CLI's
// actual on-disk layout (validated June 2026):
//
//   - agents-md        -> workspace-root AGENTS.md, managed section via write_tool_owned
//   - skill            -> .agents/skills/agent-sync-<id>/SKILL.md (+assets)
//   - mcp-server-entry -> .codex/config.toml [mcp_servers.agentsync_<id>] via write_tool_owned (toml-path)
//   - rule             -> warning, unsupported (no per-tool rule concept)
//   - command          -> warning, unsupported (custom prompts deprecated + user-home-only)
//   - plugin-reference -> warning, unsupported (no project plugin registry)
//
// Unlike claude/cursor, codex owns NO dedicated reserved subdirectory: skills
// live under the shared .agents/skills/ tree (read by Codex and other agents),
// MCP entries live inside the tool-owned .codex/config.toml, and prose lives in
// the shared AGENTS.md. See docs/adapters/codex.md for the path-reality callout.
//
// It is a structural sibling of internal/adapter/bundled/{claude,cursor}: same
// SDK-backed lifecycle, same managed-file-header / section-marker helpers, same
// summary-only OpsPerformed wire behavior, same zero-file-I/O invariant. Only
// the destination mapping and the capability matrix differ.
//
// Bundled() returns an *adapter.BundledAdapter ready for the runtime to spawn
// over an in-process pipe; nothing else in this package is public.
package codex

import (
	"context"
	"fmt"
	"io"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	adapterName    = "codex"
	adapterVersion = "0.1"
	// reservedPrefix is codex's collision-detection namespace (discover.go
	// rejects nested adapter prefixes). The adapter's actual owned outputs
	// span .agents/skills/, .codex/config.toml, and AGENTS.md — declared via
	// declaredOutputs(), not constrained to this prefix (cursor likewise
	// declares AGENTS.md outside .cursor). There is intentionally no
	// .codex/agent-sync/ reserved subdirectory.
	reservedPrefix = ".codex"
)

// Bundled returns the adapter.BundledAdapter for the codex target.
// The runtime registers it via adapter.DiscoverOptions.Bundled.
func Bundled() *adapter.BundledAdapter {
	return &adapter.BundledAdapter{
		Manifest: adapter.AdapterManifest{
			Name:            adapterName,
			Version:         adapterVersion,
			ContractVersion: adapter.ContractVersionV1,
			ReservedPrefix:  reservedPrefix,
			// Bundled adapters are spawned in-process via Run; the Command
			// slice is required by the shared manifest validator but never
			// executed for SourceBundled. Placeholder recorded for diagnostics.
			Command: []string{"agent-sync-adapter-codex-bundled"},
		},
		Run: run,
	}
}

// run is the in-process entry point invoked by InprocTransport.
func run(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	server := adapterkit.NewServer(adapterkit.ServerOptions{
		Name:    adapterName,
		Version: adapterVersion,
		Stdin:   stdin,
		Stdout:  stdout,
		// Bundled adapters skip cookie validation at the runtime layer, but
		// the SDK's startup magic-cookie format check still observes the
		// env-var, so we supply a constant 32-hex string the runtime ignores.
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
		return fmt.Errorf("codex bundled adapter: %w", err)
	}
	return nil
}

// bundledCookie is a 32-hex placeholder sent through the adapterkit Server's
// cookie format check. Bundled adapters skip cookie validation at the runtime
// layer, so the value is opaque to the runtime.
const bundledCookie = "00000000000000000000000000000000"

func bundledGetenv(name string) string {
	if name == adapterkit.CookieEnvVar {
		return bundledCookie
	}
	return ""
}

// handleEmit lives in emit.go.

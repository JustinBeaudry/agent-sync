// Package antigravity implements the bundled agent-sync adapter for Google
// Antigravity 2.0 (the IDE and CLI share one config surface under ~/.gemini/).
// Antigravity replaced the retired Gemini CLI (2026-06-18) and reads the same
// GEMINI.md overlay; the IR decoder already routes a root GEMINI.md overlay to
// the "antigravity" target (internal/ir/decode.go, PR #36).
//
// The adapter consumes IR v1 nodes and emits the v1 op vocabulary (write_file,
// write_tool_owned, mkdir, warning) for Antigravity's actual on-disk layout:
//
//	rule    -> .agent/rules/agent-sync/<id>.md          (SINGULAR .agent)
//	command -> .agent/workflows/agent-sync/<id>.md      (SINGULAR .agent; slash-workflows)
//	skill   -> .agents/skills/agent-sync-<id>/SKILL.md  (PLURAL .agents; +assets)
//	          (user scope: .gemini/skills/agent-sync-<id>/…)
//	mcp-server-entry -> .agents/mcp_config.json         (PLURAL .agents) via write_tool_owned
//	          (user scope: .gemini/config/mcp_config.json)
//	agents-md (companion overlay) -> GEMINI.md (workspace root) via write_tool_owned
//	          (user scope: .gemini/GEMINI.md, i.e. ~/.gemini/GEMINI.md)
//	plugin-reference -> warning, unsupported by design
//
// The .agent (rules, workflows) vs .agents (skills, mcp) directory split is
// Antigravity's own inconsistency — rules/workflows descend from its
// Windsurf/Codeium lineage, skills/mcp from the cross-tool AGENTS.md ecosystem —
// and the adapter reproduces it faithfully rather than normalizing it (a
// normalized path would be inert). See docs/adapters/antigravity.md.
//
// agents-md targets GEMINI.md ONLY, not AGENTS.md: AGENTS.md is owned by the
// codex/pi adapters and a second writer at the same scope would collide. See the
// plan docs/plans/2026-07-03-001-feat-antigravity-adapter-plan.md.
//
// Bundled() returns an *adapter.BundledAdapter ready for the runtime to spawn
// over an in-process pipe; nothing else in this package is part of the public
// surface.
package antigravity

import (
	"context"
	"fmt"
	"io"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	adapterName    = "antigravity"
	adapterVersion = "0.1"
	// reservedPrefix is the Antigravity-exclusive native directory (rules and
	// workflows live under .agent/). Skills and MCP live OUTSIDE the reserved
	// prefix (.agents/…) and are declared separately as shared-subdir /
	// tool-owned-entry — the same pattern pi uses (reserved_prefix .pi, skills to
	// .agents/skills). ReservedPrefix does not need to cover every declared
	// output.
	reservedPrefix = ".agent"
)

// Bundled returns the adapter.BundledAdapter for the antigravity target. The
// runtime registers it via adapter.DiscoverOptions.Bundled.
func Bundled() *adapter.BundledAdapter {
	return &adapter.BundledAdapter{
		Manifest: adapter.AdapterManifest{
			Name:            adapterName,
			Version:         adapterVersion,
			ContractVersion: adapter.ContractVersionV1,
			ReservedPrefix:  reservedPrefix,
			// Bundled adapters are spawned in-process via Run; the Command slice
			// is required by the shared manifest validator but never executed for
			// SourceBundled. The placeholder name is recorded for diagnostics only.
			Command: []string{"agent-sync-adapter-antigravity-bundled"},
		},
		Run: run,
	}
}

// run is the in-process entry point invoked by InprocTransport. It constructs an
// adapterkit.Server bound to the supplied pipes, wires up the lifecycle
// handlers, and serves until context is cancelled or the runtime sends shutdown.
func run(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	server := adapterkit.NewServer(adapterkit.ServerOptions{
		Name:    adapterName,
		Version: adapterVersion,
		Stdin:   stdin,
		Stdout:  stdout,
		// We supply a constant 32-hex string for the env-var so the SDK's startup
		// magic-cookie format check passes; the runtime ignores the echoed value
		// for bundled adapters (cookie validation is gated on Source !=
		// SourceBundled).
		Getenv: bundledGetenv,
	})

	// scope/source are captured at initialize and read at emit. The adapterkit
	// Server processes initialize before emit on a single goroutine, so this
	// needs no synchronization; each run() (one per session) has its own vars.
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
		return fmt.Errorf("antigravity bundled adapter: %w", err)
	}
	return nil
}

// bundledCookie is a 32-hex placeholder sent through the adapterkit Server's
// cookie format check. Bundled adapters skip cookie validation at the runtime
// layer, so the value is opaque to the runtime — only the SDK's "format must be
// 32 hex" check observes it.
const bundledCookie = "00000000000000000000000000000000"

func bundledGetenv(name string) string {
	if name == adapterkit.CookieEnvVar {
		return bundledCookie
	}
	return ""
}

// handleEmit lives in emit.go.

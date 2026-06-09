package engine

import (
	"context"
	"sort"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/adapter/contract"
	"github.com/agent-sync/agent-sync/internal/report"
)

// writeCapabilityReport assembles and persists the per-target capability
// report. It is best-effort observability: the caller logs and continues
// on error, never failing the sync. It runs a lightweight
// initialize→shutdown against each target to read declared capabilities.
func writeCapabilityReport(ctx context.Context, req Request, now time.Time) error {
	targets := resolvedTargets(req.Targets, req.Options.TargetsFilter)
	required := requiredKindsByTarget(req)

	var inputs []report.CapabilityInput
	for _, target := range targets {
		a, ok := req.Registry.Get(target)
		if !ok {
			continue
		}
		caps, ok := readCapabilities(ctx, a, req.WorkspacePath)
		if !ok {
			continue
		}
		inputs = append(inputs, report.CapabilityInput{
			Target:        target,
			Caps:          caps,
			RequiredKinds: required[target],
		})
	}

	rep := report.BuildCapability(now.UTC().Format(time.RFC3339), inputs)
	return report.WriteCapabilityReport(req.Root, rep)
}

// readCapabilities runs initialize then shutdown to capture an adapter's
// declared capabilities. Returns ok=false if the handshake fails.
func readCapabilities(ctx context.Context, a *adapter.Adapter, workspaceRoot string) (caps contract.Capabilities, ok bool) {
	session, err := a.NewSession(ctx, adapter.SessionOptions{WorkspaceRoot: workspaceRoot, IRVersion: "v1"})
	if err != nil {
		return contract.Capabilities{}, false
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = session.Shutdown(shutdownCtx)
	}()
	res, err := session.Initialize(ctx)
	if err != nil {
		return contract.Capabilities{}, false
	}
	return res.Capabilities, true
}

// requiredKindsByTarget returns, per target, the distinct IR concept
// kinds marked Required that apply to that target (empty Targets means
// all targets).
func requiredKindsByTarget(req Request) map[string][]string {
	out := map[string]map[string]bool{}
	ensure := func(t string) map[string]bool {
		if out[t] == nil {
			out[t] = map[string]bool{}
		}
		return out[t]
	}
	targets := resolvedTargets(req.Targets, req.Options.TargetsFilter)
	for _, n := range req.Nodes {
		if !n.Required {
			continue
		}
		if len(n.Targets) == 0 {
			for _, t := range targets {
				ensure(t)[string(n.Kind)] = true
			}
			continue
		}
		for _, t := range n.Targets {
			ensure(t)[string(n.Kind)] = true
		}
	}
	result := map[string][]string{}
	for t, kinds := range out {
		var ks []string
		for k := range kinds {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		result[t] = ks
	}
	return result
}

package cursor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// TestBundledAdapter_FullLifecycle drives the bundled adapter via
// adapterkit.RunInprocServer through the full
// initialize -> initialized -> emit -> shutdown lifecycle. It is the
// closest the package gets to running through the actual conformance
// harness CLI (Unit 16) until that lands; it uses the same
// adapterkit.Client transport the harness uses internally.
func TestBundledAdapter_FullLifecycle(t *testing.T) {
	t.Parallel()

	server := newServerForTest()
	client, cleanup := adapterkit.RunInprocServer(t, server)
	t.Cleanup(cleanup)

	ctx := context.Background()
	initRes, err := client.Initialize(ctx, adapterkit.InitializeParams{
		Client:           "test",
		ProtocolVersions: []string{adapterkit.ContractVersionV1},
		Cookie:           "00000000000000000000000000000000",
		WorkspaceRoot:    t.TempDir(),
		ReservedPrefix:   reservedPrefix,
		IRVersion:        "v1",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if initRes.ProtocolVersion != adapterkit.ContractVersionV1 {
		t.Errorf("ProtocolVersion=%q want %q", initRes.ProtocolVersion, adapterkit.ContractVersionV1)
	}
	if !initRes.Capabilities.WriteToolOwned {
		t.Error("WriteToolOwned not echoed back")
	}
	if got, want := len(initRes.DeclaredOutputs), len(declaredOutputs()); got != want {
		t.Errorf("DeclaredOutputs len=%d want %d", got, want)
	}
	if err := client.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}

	raw := readFixture(t, "mixed-everything.json")
	emit, err := client.Emit(ctx, adapterkit.EmitParams{Target: adapterName, IR: raw})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// Assert the full op sequence end-to-end. handleEmit sorts nodes
	// by kind then id (agents-md < mcp-server-entry < rule < skill),
	// so the emitted order is deterministic.
	want := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindWriteToolOwned, Path: "AGENTS.md"},
		{Op: adapterkit.OpKindWriteToolOwned, Path: ".cursor/mcp.json"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/.agent-sync-managed"},
		{Op: adapterkit.OpKindMkdir, Path: ".cursor/rules/agent-sync"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/rules/agent-sync/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/rules/agent-sync/house-style.mdc"},
		{Op: adapterkit.OpKindWarning, Path: ""},
	}
	if !reflect.DeepEqual(emit.OpsPerformed, want) {
		t.Fatalf("mixed-everything OpsPerformed mismatch:\n got: %+v\nwant: %+v", emit.OpsPerformed, want)
	}

	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// After shutdown the cleanup func will close the pipes; verify
	// that we did acknowledge the protocol shutdown so a subprocess
	// transport's MarkProtocolShutdownAcked path would be exercised
	// equivalently.
	adapterkit.AssertProtocolShutdownAcked(t, server)
}

// TestBundledAdapter_SupportedSubsetEmitsNoWarnings verifies the
// master plan's "passes the shared conformance harness with zero
// warnings" bar, read against the all-supported fixtures: a rule +
// mcp-server-entry + agents-md IR produces no degradation warnings.
func TestBundledAdapter_SupportedSubsetEmitsNoWarnings(t *testing.T) {
	t.Parallel()

	ir := json.RawMessage(`{"nodes":[
		{"id":"house-style","kind":"rule","body":"Use 2-space indents."},
		{"id":"lsp","kind":"mcp-server-entry","body":"{\"command\":\"node\"}"},
		{"id":"team","kind":"agents-md","targets":["cursor"],"body":"## Build"}
	]}`)
	res, err := handleEmit(context.Background(), adapterkit.EmitParams{Target: adapterName, IR: ir})
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}
	for _, op := range res.OpsPerformed {
		if op.Op == adapterkit.OpKindWarning {
			t.Errorf("all-supported IR must emit zero warnings; got %+v", res.OpsPerformed)
		}
	}
}

// TestBundledAdapter_EmitWithForeignTargetStillProcessesIR documents
// v1 behavior: the adapter does not validate the EmitParams.Target
// field. Routing is the runtime's responsibility; the adapter
// processes the IR it receives. A node's own targets:[...] field
// (not EmitParams.Target) is what filters per-adapter.
func TestBundledAdapter_EmitWithForeignTargetStillProcessesIR(t *testing.T) {
	t.Parallel()

	server := newServerForTest()
	client, cleanup := adapterkit.RunInprocServer(t, server)
	t.Cleanup(cleanup)

	ctx := context.Background()
	if _, err := client.Initialize(ctx, adapterkit.InitializeParams{
		Client:           "test",
		ProtocolVersions: []string{adapterkit.ContractVersionV1},
		Cookie:           "00000000000000000000000000000000",
		IRVersion:        "v1",
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := client.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	// rule-only.json has no per-node targets filter (means "all
	// adapters"), so the rule is processed regardless of the foreign
	// EmitParams.Target value. This test pins the v1 contract.
	emit, err := client.Emit(ctx, adapterkit.EmitParams{
		Target: "claude",
		IR:     readFixture(t, "rule-only.json"),
	})
	if err != nil {
		t.Fatalf("Emit with foreign target should succeed; got %v", err)
	}
	if len(emit.OpsPerformed) == 0 {
		t.Error("emit must still process IR even when EmitParams.Target names a different adapter")
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestBundledAdapter_ReInitAfterShutdownIsRejected verifies the
// stateful Server contract: once Shutdown has run, a second
// Initialize must not succeed on the same Server instance.
func TestBundledAdapter_ReInitAfterShutdownIsRejected(t *testing.T) {
	t.Parallel()

	server := newServerForTest()
	client, cleanup := adapterkit.RunInprocServer(t, server)
	t.Cleanup(cleanup)

	ctx := context.Background()
	if _, err := client.Initialize(ctx, adapterkit.InitializeParams{
		Client:           "test",
		ProtocolVersions: []string{adapterkit.ContractVersionV1},
		Cookie:           "00000000000000000000000000000000",
		IRVersion:        "v1",
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := client.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := client.Initialize(ctx, adapterkit.InitializeParams{
		Client:           "test",
		ProtocolVersions: []string{adapterkit.ContractVersionV1},
		Cookie:           "00000000000000000000000000000000",
		IRVersion:        "v1",
	}); err == nil {
		t.Error("re-Initialize after Shutdown must fail; Server is single-use")
	}
}

// TestBundled_RegistersIntoAdapterRegistry verifies the package's
// public surface (Bundled()) integrates with the adapter discovery
// machinery as a SourceBundled adapter.
func TestBundled_RegistersIntoAdapterRegistry(t *testing.T) {
	t.Parallel()

	reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{Bundled()},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	got, ok := reg.Get(adapterName)
	if !ok {
		t.Fatalf("registry does not contain %q; names=%v", adapterName, reg.Names())
	}
	if got.Source != adapter.SourceBundled {
		t.Errorf("Source=%v want %v", got.Source, adapter.SourceBundled)
	}
	if got.Bundled == nil {
		t.Error("Bundled adapter has no Run callback wired")
	}
}

// TestBundled_OpsPathsMatchDeclaredOutputs sanity-checks that every
// path produced by the mixed-everything fixture would survive the
// runtime's declared-outputs gate. We replicate the gate's
// prefix-based check rather than starting up a real session.
func TestBundled_OpsPathsMatchDeclaredOutputs(t *testing.T) {
	t.Parallel()

	res, _ := emitFixture(t, "mixed-everything.json")
	declared := declaredOutputs()

	for _, op := range res.OpsPerformed {
		if op.Op == adapterkit.OpKindWarning {
			continue
		}
		if !pathInDeclaredOutputs(op.Path, declared) {
			t.Errorf("emitted path %q (kind=%s) is not inside any declared output", op.Path, op.Op)
		}
	}
}

func newServerForTest() *adapterkit.Server {
	server := adapterkit.NewServer(adapterkit.ServerOptions{
		Name:    adapterName,
		Version: adapterVersion,
	})
	server.OnInitialize(func(_ context.Context, _ adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
		return adapterkit.InitializeResult{
			Capabilities:    capabilitiesForWire(),
			DeclaredOutputs: declaredOutputs(),
		}, nil
	})
	server.OnEmit(handleEmit)
	return server
}

func readFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ir", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

// pathInDeclaredOutputs mirrors the runtime's gate function. Kept
// local to the test to avoid coupling test code to internal/adapter
// internals; if the gate behavior changes upstream this test will
// fail loudly.
func pathInDeclaredOutputs(opPath string, decls []adapterkit.DeclaredOutput) bool {
	if opPath == "" {
		return false
	}
	for _, d := range decls {
		if d.Path == "" {
			continue
		}
		if opPath == d.Path {
			return true
		}
		// Owned-subdir mode treats Path as a parent; tool-owned-entry
		// mode treats Path as exact-match only.
		if d.Mode == adapterkit.OutputModeOwnedSubdir {
			if hasPathPrefix(opPath, d.Path) {
				return true
			}
		}
	}
	return false
}

func hasPathPrefix(p, prefix string) bool {
	if len(p) <= len(prefix) {
		return false
	}
	if p[:len(prefix)] != prefix {
		return false
	}
	return p[len(prefix)] == '/'
}

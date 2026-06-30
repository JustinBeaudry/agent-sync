package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	defer cleanup()

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
	if got, want := len(initRes.DeclaredOutputs), len(declaredOutputs("project")); got != want {
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

	// Spot-check that the shutdown contract is satisfied AFTER a
	// full emit ran. Op composition is asserted in emit_test.go;
	// this test cares about the wire round-trip.
	if len(emit.OpsPerformed) == 0 {
		t.Error("mixed-everything emit produced zero ops")
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

// TestBundledAdapter_EmitWithForeignTargetStillProcessesIR documents
// v1 behavior: the adapter does not validate the EmitParams.Target
// field. Routing is the runtime's responsibility; the adapter
// processes the IR it receives. A node's own targets:[...] field
// (not EmitParams.Target) is what filters per-adapter. Per-target
// validation in the adapter is a Unit 8b candidate.
func TestBundledAdapter_EmitWithForeignTargetStillProcessesIR(t *testing.T) {
	t.Parallel()

	server := newServerForTest()
	client, cleanup := adapterkit.RunInprocServer(t, server)
	defer cleanup()

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
		Target: "cursor",
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
	defer cleanup()

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
// prefix-based check rather than starting up a real session, since
// the AdapterSession path requires the full subprocess+cookie
// machinery and the rule under test is the path-set, not the
// transport.
func TestBundled_OpsPathsMatchDeclaredOutputs(t *testing.T) {
	t.Parallel()

	res, _ := emitFixture(t, "mixed-everything.json")
	declared := declaredOutputs("project")

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
	// Mirror production wiring (bundled.go run): capture the init scope and
	// resolve outputs/emit paths from it, so this helper can drive both
	// project and user scope by varying InitializeParams.Scope.
	var scope string
	server.OnInitialize(func(_ context.Context, params adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
		scope = params.Scope
		return adapterkit.InitializeResult{
			Capabilities:    capabilitiesForWire(),
			DeclaredOutputs: declaredOutputs(scope),
		}, nil
	})
	server.OnEmit(func(ctx context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
		return handleEmit(ctx, params, scope)
	})
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
		// Owned-subdir and shared-subdir modes treat Path as a parent;
		// tool-owned-entry mode treats Path as exact-match only.
		if d.Mode == adapterkit.OutputModeOwnedSubdir || d.Mode == adapterkit.OutputModeSharedSubdir {
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

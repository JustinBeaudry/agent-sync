package cursor

import (
	"context"
	"sort"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/capmatrix"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

func TestBundled_ManifestShape(t *testing.T) {
	t.Parallel()

	b := Bundled()
	if b == nil {
		t.Fatal("Bundled returned nil")
	}
	if b.Run == nil {
		t.Fatal("Bundled.Run is nil")
	}

	if got, want := b.Manifest.Name, adapterName; got != want {
		t.Errorf("Manifest.Name = %q, want %q", got, want)
	}
	if got, want := b.Manifest.ContractVersion, adapter.ContractVersionV1; got != want {
		t.Errorf("Manifest.ContractVersion = %q, want %q", got, want)
	}
	if got, want := b.Manifest.ReservedPrefix, reservedPrefix; got != want {
		t.Errorf("Manifest.ReservedPrefix = %q, want %q", got, want)
	}
	// Bundled adapters carry a placeholder Command so the shared
	// adapter-manifest validator (which requires non-empty Command)
	// accepts them at discovery time. The Run callback is what the
	// runtime actually invokes; Command is diagnostic only.
	if len(b.Manifest.Command) == 0 {
		t.Error("Manifest.Command must not be empty (validator rejects empty Command even for bundled adapters)")
	}
}

func TestCapabilitiesYAML_MatchesCodeMap(t *testing.T) {
	t.Parallel()

	d, err := loadDeclaration()
	if err != nil {
		t.Fatalf("loadDeclaration: %v", err)
	}

	if got, want := d.Name, adapterName; got != want {
		t.Errorf("declaration name=%q want %q", got, want)
	}
	if got, want := d.ContractVersion, adapter.ContractVersionV1; got != want {
		t.Errorf("declaration contract_version=%q want %q", got, want)
	}
	if got, want := d.ReservedPrefix, reservedPrefix; got != want {
		t.Errorf("declaration reserved_prefix=%q want %q", got, want)
	}

	// Build the YAML-side kind set keyed by ir.Kind.
	yamlKinds := make(map[ir.Kind]capmatrix.CapabilityStatus, len(d.ConceptKinds))
	for k, v := range d.ConceptKinds {
		yamlKinds[ir.Kind(k)] = v.Status
	}

	// Compare key sets.
	codeKindList := make([]string, 0, len(conceptKinds))
	for k := range conceptKinds {
		codeKindList = append(codeKindList, string(k))
	}
	sort.Strings(codeKindList)

	yamlKindList := make([]string, 0, len(yamlKinds))
	for k := range yamlKinds {
		yamlKindList = append(yamlKindList, string(k))
	}
	sort.Strings(yamlKindList)

	if !equalStringSlices(codeKindList, yamlKindList) {
		t.Fatalf("kind sets diverge:\n  yaml: %v\n  code: %v", yamlKindList, codeKindList)
	}

	// Compare per-kind status.
	for kind, codeStatus := range conceptKinds {
		yamlStatus, ok := yamlKinds[kind]
		if !ok {
			t.Errorf("kind %q in code, missing in yaml", kind)
			continue
		}
		if codeStatus != yamlStatus {
			t.Errorf("kind %q status mismatch: yaml=%q, code=%q", kind, yamlStatus, codeStatus)
		}
	}

	// Every IR kind in the v1 closed set must be declared exactly
	// once. The test catches both omissions (a new kind added to
	// internal/ir but not the adapter) and duplicates.
	for _, kind := range ir.AllKinds() {
		if _, ok := conceptKinds[kind]; !ok {
			t.Errorf("ir kind %q not declared by cursor adapter (must be supported|partial|unsupported, never silent)", kind)
		}
	}
	if got, want := len(conceptKinds), len(ir.AllKinds()); got != want {
		t.Errorf("conceptKinds size %d != ir.AllKinds() size %d", got, want)
	}
}

// TestConceptKinds_UnsupportedSet pins the cursor-specific capability
// downgrades: command and plugin-reference are unsupported. skill is supported
// via the shared .agents/skills tree. This is the load-bearing difference from
// the claude adapter and the honest-mapping decision documented in
// docs/adapters/cursor.md.
func TestConceptKinds_UnsupportedSet(t *testing.T) {
	t.Parallel()

	unsupported := []ir.Kind{ir.KindCommand, ir.KindPluginReference}
	for _, kind := range unsupported {
		if got := conceptKinds[kind]; got != capmatrix.Unsupported {
			t.Errorf("kind %q status=%q want %q", kind, got, capmatrix.Unsupported)
		}
	}
	supported := []ir.Kind{ir.KindAgentsMD, ir.KindRule, ir.KindMCPServerEntry, ir.KindSkill}
	for _, kind := range supported {
		if got := conceptKinds[kind]; got != capmatrix.Supported {
			t.Errorf("kind %q status=%q want %q", kind, got, capmatrix.Supported)
		}
	}
}

func TestDeclaredOutputs_Shape(t *testing.T) {
	t.Parallel()

	got := declaredOutputs("project")
	want := map[string]adapterkit.OutputMode{
		".cursor/rules/agent-sync":    adapterkit.OutputModeOwnedSubdir,
		".cursor/mcp.json":            adapterkit.OutputModeToolOwnedEntry,
		"AGENTS.md":                   adapterkit.OutputModeToolOwnedEntry,
		".cursor/.agent-sync-managed": adapterkit.OutputModeOwnedSubdir,
		".agents/skills":              adapterkit.OutputModeSharedSubdir,
	}
	if len(got) != len(want) {
		t.Fatalf("declaredOutputs len=%d want %d (%+v)", len(got), len(want), got)
	}
	for _, d := range got {
		mode, ok := want[d.Path]
		if !ok {
			t.Errorf("unexpected declared output: %q (%v)", d.Path, d.Mode)
			continue
		}
		if d.Mode != mode {
			t.Errorf("declared output %q mode=%v want %v", d.Path, d.Mode, mode)
		}
	}

	// Cross-check tool-owned-entry locators are present.
	var sawMCPPointer, sawAgentsMDSection bool
	for _, d := range got {
		if d.Path == ".cursor/mcp.json" {
			if d.JSONPointer == nil || *d.JSONPointer == "" {
				t.Error(".cursor/mcp.json declared output missing JSONPointer locator")
			} else {
				sawMCPPointer = true
			}
		}
		if d.Path == "AGENTS.md" {
			if d.SectionID == nil || *d.SectionID == "" {
				t.Error("AGENTS.md declared output missing SectionID locator")
			} else {
				sawAgentsMDSection = true
			}
		}
	}
	if !sawMCPPointer {
		t.Error(".cursor/mcp.json locator not asserted")
	}
	if !sawAgentsMDSection {
		t.Error("AGENTS.md locator not asserted")
	}
}

// TestCapabilitiesForWire_UserScopeDemotesRuleAndAgentsMD pins the
// scope-aware capability declaration. At user scope rule and agents-md must be
// UNSUPPORTED (Cursor has no user-global home for them and the adapter emits
// nothing) so the runtime's capability-lied gate treats a rule-only user-scope
// manifest as an honest no-op rather than a sync failure. At project scope they
// remain supported.
func TestCapabilitiesForWire_UserScopeDemotesRuleAndAgentsMD(t *testing.T) {
	t.Parallel()

	user := capabilitiesForWire("user")
	if got := user.ConceptKinds[string(ir.KindRule)]; got != adapterkit.CapabilityUnsupported {
		t.Errorf("user-scope rule capability = %v, want unsupported", got)
	}
	if got := user.ConceptKinds[string(ir.KindAgentsMD)]; got != adapterkit.CapabilityUnsupported {
		t.Errorf("user-scope agents-md capability = %v, want unsupported", got)
	}
	// mcp-server-entry stays supported at user scope (~/.cursor/mcp.json).
	if got := user.ConceptKinds[string(ir.KindMCPServerEntry)]; got != adapterkit.CapabilitySupported {
		t.Errorf("user-scope mcp capability = %v, want supported", got)
	}
	// Project scope is unchanged: rule and agents-md remain supported.
	proj := capabilitiesForWire("project")
	if got := proj.ConceptKinds[string(ir.KindRule)]; got != adapterkit.CapabilitySupported {
		t.Errorf("project-scope rule capability = %v, want supported", got)
	}
	if got := proj.ConceptKinds[string(ir.KindAgentsMD)]; got != adapterkit.CapabilitySupported {
		t.Errorf("project-scope agents-md capability = %v, want supported", got)
	}
}

// TestDeclaredOutputs_UserScope pins the user-scope declared-outputs shape:
// .cursor/mcp.json (the one file-addressable user-global Cursor config) plus the
// shared .agents/skills tree (Cursor reads ~/.agents/skills). The sidecar, rules
// dir, and AGENTS.md are dropped because they have no user-global home — keeping
// declared outputs in lockstep with what emit.go actually emits at user scope.
func TestDeclaredOutputs_UserScope(t *testing.T) {
	t.Parallel()

	got := declaredOutputs("user")
	want := map[string]adapterkit.OutputMode{
		".cursor/mcp.json": adapterkit.OutputModeToolOwnedEntry,
		".agents/skills":   adapterkit.OutputModeSharedSubdir,
	}
	if len(got) != len(want) {
		t.Fatalf("user-scope declaredOutputs = %d, want %d: %+v", len(got), len(want), got)
	}
	for _, d := range got {
		mode, ok := want[d.Path]
		if !ok {
			t.Errorf("unexpected user-scope declared output: %q (%v)", d.Path, d.Mode)
			continue
		}
		if d.Mode != mode {
			t.Errorf("user-scope declared output %q mode=%v want %v", d.Path, d.Mode, mode)
		}
		if d.Path == ".cursor/mcp.json" && (d.JSONPointer == nil || *d.JSONPointer != "/mcpServers") {
			t.Errorf("user-scope mcp declared output missing /mcpServers pointer: %+v", d)
		}
	}
	// rule/agents-md/sidecar have no user-global home; assert they're absent.
	for _, d := range got {
		if d.Path == ".cursor/rules/agent-sync" || d.Path == "AGENTS.md" || d.Path == ".cursor/.agent-sync-managed" {
			t.Errorf("user-scope must not declare project-only path %q", d.Path)
		}
	}
}

func TestCapabilitiesForWire_ExposesAllKinds(t *testing.T) {
	t.Parallel()

	c := capabilitiesForWire("project")
	if !c.WriteToolOwned {
		t.Error("WriteToolOwned must be true (cursor emits write_tool_owned ops)")
	}
	if c.Progress {
		t.Error("Progress must be false in v1 (Unit 8b feature)")
	}
	for kind := range conceptKinds {
		if _, ok := c.ConceptKinds[string(kind)]; !ok {
			t.Errorf("wire capabilities missing kind %q", kind)
		}
	}
	if len(c.ConceptKinds) != len(conceptKinds) {
		t.Errorf("wire capabilities size %d != code map size %d", len(c.ConceptKinds), len(conceptKinds))
	}
}

func TestRun_InitializeRoundTrip(t *testing.T) {
	t.Parallel()

	server := adapterkit.NewServer(adapterkit.ServerOptions{
		Name:    adapterName,
		Version: adapterVersion,
	})
	var scope string
	server.OnInitialize(func(_ context.Context, params adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
		scope = params.Scope
		return adapterkit.InitializeResult{
			Capabilities:    capabilitiesForWire(scope),
			DeclaredOutputs: declaredOutputs(scope),
		}, nil
	})
	server.OnEmit(func(ctx context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
		return handleEmit(ctx, params, scope, "", "")
	})

	client, cleanup := adapterkit.RunInprocServer(t, server)
	t.Cleanup(cleanup)

	ctx := context.Background()
	res, err := client.Initialize(ctx, adapterkit.InitializeParams{
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
	if res.ProtocolVersion != adapterkit.ContractVersionV1 {
		t.Errorf("ProtocolVersion=%q want %q", res.ProtocolVersion, adapterkit.ContractVersionV1)
	}
	if got, want := len(res.DeclaredOutputs), len(declaredOutputs("project")); got != want {
		t.Errorf("DeclaredOutputs len=%d want %d", got, want)
	}
	if !res.Capabilities.WriteToolOwned {
		t.Error("WriteToolOwned not echoed back")
	}
	if err := client.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	emit, err := client.Emit(ctx, adapterkit.EmitParams{Target: adapterName})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(emit.OpsPerformed) != 0 {
		t.Errorf("scaffold Emit OpsPerformed should be empty; got %v", emit.OpsPerformed)
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

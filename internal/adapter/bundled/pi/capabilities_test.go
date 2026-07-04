package pi

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

	yamlKinds := make(map[ir.Kind]capmatrix.CapabilityStatus, len(d.ConceptKinds))
	for k, v := range d.ConceptKinds {
		yamlKinds[ir.Kind(k)] = v.Status
	}

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

	for _, kind := range ir.AllKinds() {
		if _, ok := conceptKinds[kind]; !ok {
			t.Errorf("ir kind %q not declared by pi adapter (must be supported|partial|unsupported, never silent)", kind)
		}
	}
	if got, want := len(conceptKinds), len(ir.AllKinds()); got != want {
		t.Errorf("conceptKinds size %d != ir.AllKinds() size %d", got, want)
	}
}

// TestConceptKinds_SupportSets pins the pi capability mapping: agents-md and
// skill are supported; mcp-server-entry, rule, command, and plugin-reference
// are unsupported (MCP and rule by Pi product design; command planned).
// See docs/adapters/pi.md.
func TestConceptKinds_SupportSets(t *testing.T) {
	t.Parallel()

	supported := []ir.Kind{ir.KindAgentsMD, ir.KindSkill, ir.KindCommand}
	for _, kind := range supported {
		if got := conceptKinds[kind]; got != capmatrix.Supported {
			t.Errorf("kind %q status=%q want %q", kind, got, capmatrix.Supported)
		}
	}
	unsupported := []ir.Kind{ir.KindMCPServerEntry, ir.KindRule, ir.KindPluginReference}
	for _, kind := range unsupported {
		if got := conceptKinds[kind]; got != capmatrix.Unsupported {
			t.Errorf("kind %q status=%q want %q", kind, got, capmatrix.Unsupported)
		}
	}
}

func TestDeclaredOutputs_Shape(t *testing.T) {
	t.Parallel()

	got := declaredOutputs("project")
	want := map[string]adapterkit.OutputMode{
		".agents/skills": adapterkit.OutputModeSharedSubdir,
		"AGENTS.md":      adapterkit.OutputModeToolOwnedEntry,
		".pi/prompts":    adapterkit.OutputModeFileLeaf,
	}
	if len(got) != len(want) {
		t.Fatalf("declaredOutputs len=%d want %d (%+v)", len(got), len(want), got)
	}
	var sawAgentsMDSection bool
	for _, d := range got {
		mode, ok := want[d.Path]
		if !ok {
			t.Errorf("unexpected declared output: %q (%v)", d.Path, d.Mode)
			continue
		}
		if d.Mode != mode {
			t.Errorf("declared output %q mode=%v want %v", d.Path, d.Mode, mode)
		}
		if d.Path == "AGENTS.md" {
			if d.SectionID == nil || *d.SectionID == "" {
				t.Error("AGENTS.md declared output missing SectionID locator")
			} else {
				sawAgentsMDSection = true
			}
		}
	}
	if !sawAgentsMDSection {
		t.Error("AGENTS.md locator not asserted")
	}
}

// TestDeclaredOutputs_UserScope_AgentsMDRemapped pins the user-scope shape:
// agents-md is declared at .pi/agent/AGENTS.md (Pi's user-global instructions
// path, → ~/.pi/agent/AGENTS.md), while the skills tree is unchanged (already
// correct under $HOME at user scope).
func TestDeclaredOutputs_UserScope_AgentsMDRemapped(t *testing.T) {
	t.Parallel()

	got := declaredOutputs("user")
	want := map[string]adapterkit.OutputMode{
		".agents/skills":      adapterkit.OutputModeSharedSubdir,
		".pi/agent/AGENTS.md": adapterkit.OutputModeToolOwnedEntry,
		".pi/prompts":         adapterkit.OutputModeFileLeaf,
	}
	if len(got) != len(want) {
		t.Fatalf("user-scope declaredOutputs len=%d want %d (%+v)", len(got), len(want), got)
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
	}
	for _, d := range got {
		if d.Path == "AGENTS.md" {
			t.Errorf("user-scope must not declare project-root AGENTS.md; got %+v", got)
		}
	}
}

func TestCapabilitiesForWire_ExposesAllKinds(t *testing.T) {
	t.Parallel()

	c := capabilitiesForWire()
	if !c.WriteToolOwned {
		t.Error("WriteToolOwned must be true (pi emits write_tool_owned ops)")
	}
	if c.Progress {
		t.Error("Progress must be false in v1")
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
			Capabilities:    capabilitiesForWire(),
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

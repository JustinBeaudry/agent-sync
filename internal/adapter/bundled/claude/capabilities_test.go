package claude

import (
	"context"
	"sort"
	"testing"

	"github.com/aienvs/aienvs/internal/adapter"
	"github.com/aienvs/aienvs/internal/capmatrix"
	"github.com/aienvs/aienvs/internal/ir"
	"github.com/aienvs/aienvs/pkg/adapterkit"
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
	if len(b.Manifest.Command) != 0 {
		t.Errorf("Manifest.Command should be empty for bundled adapter; got %v", b.Manifest.Command)
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
			t.Errorf("ir kind %q not declared by claude adapter (must be supported|partial|unsupported, never silent)", kind)
		}
	}
	if got, want := len(conceptKinds), len(ir.AllKinds()); got != want {
		t.Errorf("conceptKinds size %d != ir.AllKinds() size %d", got, want)
	}
}

func TestDeclaredOutputs_Shape(t *testing.T) {
	t.Parallel()

	got := declaredOutputs()
	want := map[string]adapterkit.OutputMode{
		".claude/rules/aienvs":    adapterkit.OutputModeOwnedSubdir,
		".claude/commands/aienvs": adapterkit.OutputModeOwnedSubdir,
		".claude/skills":          adapterkit.OutputModeOwnedSubdir,
		".mcp.json":               adapterkit.OutputModeToolOwnedEntry,
		"CLAUDE.md":               adapterkit.OutputModeToolOwnedEntry,
		".aienvs-managed":         adapterkit.OutputModeOwnedSubdir,
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
	var sawMCPPointer, sawClaudeMDSection bool
	for _, d := range got {
		if d.Path == ".mcp.json" {
			if d.JSONPointer == nil || *d.JSONPointer == "" {
				t.Error(".mcp.json declared output missing JSONPointer locator")
			} else {
				sawMCPPointer = true
			}
		}
		if d.Path == "CLAUDE.md" {
			if d.SectionID == nil || *d.SectionID == "" {
				t.Error("CLAUDE.md declared output missing SectionID locator")
			} else {
				sawClaudeMDSection = true
			}
		}
	}
	if !sawMCPPointer {
		t.Error(".mcp.json locator not asserted")
	}
	if !sawClaudeMDSection {
		t.Error("CLAUDE.md locator not asserted")
	}
}

func TestCapabilitiesForWire_ExposesAllKinds(t *testing.T) {
	t.Parallel()

	c := capabilitiesForWire()
	if !c.WriteToolOwned {
		t.Error("WriteToolOwned must be true (claude emits write_tool_owned ops)")
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
	server.OnInitialize(func(_ context.Context, _ adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
		return adapterkit.InitializeResult{
			Capabilities:    capabilitiesForWire(),
			DeclaredOutputs: declaredOutputs(),
		}, nil
	})
	server.OnEmit(handleEmit)

	client, cleanup := adapterkit.RunInprocServer(t, server)
	defer cleanup()

	ctx := context.Background()
	res, err := client.Initialize(ctx, adapterkit.InitializeParams{
		Client:           "test",
		ProtocolVersions: []string{adapterkit.ContractVersionV1},
		Cookie:           "00000000000000000000000000000000",
		WorkspaceRoot:    "/tmp/aienvs-claude-test",
		ReservedPrefix:   reservedPrefix,
		IRVersion:        "v1",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ProtocolVersion != adapterkit.ContractVersionV1 {
		t.Errorf("ProtocolVersion=%q want %q", res.ProtocolVersion, adapterkit.ContractVersionV1)
	}
	if got, want := len(res.DeclaredOutputs), len(declaredOutputs()); got != want {
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

func TestSortedConceptKinds_Stable(t *testing.T) {
	t.Parallel()

	a := sortedConceptKinds()
	b := sortedConceptKinds()
	if len(a) != len(b) {
		t.Fatalf("sortedConceptKinds returned different lengths: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("sortedConceptKinds not stable at index %d: %q vs %q", i, a[i], b[i])
		}
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

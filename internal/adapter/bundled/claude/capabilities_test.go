package claude

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
			t.Errorf("ir kind %q not declared by claude adapter (must be supported|partial|unsupported, never silent)", kind)
		}
	}
	if got, want := len(conceptKinds), len(ir.AllKinds()); got != want {
		t.Errorf("conceptKinds size %d != ir.AllKinds() size %d", got, want)
	}
}

func TestDeclaredOutputs_Shape(t *testing.T) {
	t.Parallel()

	got := declaredOutputs("project")
	want := map[string]adapterkit.OutputMode{
		".claude/rules/agent-sync":    adapterkit.OutputModeOwnedSubdir,
		".claude/commands/agent-sync": adapterkit.OutputModeOwnedSubdir,
		".claude/skills":              adapterkit.OutputModeSharedSubdir,
		".mcp.json":                   adapterkit.OutputModeToolOwnedEntry,
		"CLAUDE.md":                   adapterkit.OutputModeToolOwnedEntry,
		".agent-sync-managed":         adapterkit.OutputModeOwnedSubdir,
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

// TestDeclaredOutputs_UserScope asserts the user-scope declared outputs
// target Claude Code's real user-config paths (.claude/CLAUDE.md, .claude.json),
// keep the unchanged .claude/{rules,commands,skills} outputs, and omit the
// sidecar (OQ-1: ~/.claude.json is Claude's own file). The tool-owned locators
// (JSON pointer, section id) are scope-invariant.
func TestDeclaredOutputs_UserScope(t *testing.T) {
	t.Parallel()

	got := declaredOutputs("user")
	want := map[string]adapterkit.OutputMode{
		".claude/rules/agent-sync":    adapterkit.OutputModeOwnedSubdir,
		".claude/commands/agent-sync": adapterkit.OutputModeOwnedSubdir,
		".claude/skills":              adapterkit.OutputModeSharedSubdir,
		".claude.json":                adapterkit.OutputModeToolOwnedEntry,
		".claude/CLAUDE.md":           adapterkit.OutputModeToolOwnedEntry,
	}
	if len(got) != len(want) {
		t.Fatalf("user-scope declaredOutputs len=%d want %d (%+v)", len(got), len(want), got)
	}
	var sawMCPPointer, sawClaudeMDSection bool
	for _, d := range got {
		mode, ok := want[d.Path]
		if !ok {
			t.Errorf("unexpected user-scope declared output: %q (%v)", d.Path, d.Mode)
			continue
		}
		if d.Mode != mode {
			t.Errorf("user-scope declared output %q mode=%v want %v", d.Path, d.Mode, mode)
		}
		if d.Path == ".claude.json" && (d.JSONPointer == nil || *d.JSONPointer != "/mcpServers") {
			t.Errorf(".claude.json JSONPointer = %v, want /mcpServers (scope-invariant)", d.JSONPointer)
		} else if d.Path == ".claude.json" {
			sawMCPPointer = true
		}
		if d.Path == ".claude/CLAUDE.md" && (d.SectionID == nil || *d.SectionID != "agent-sync") {
			t.Errorf(".claude/CLAUDE.md SectionID = %v, want agent-sync (scope-invariant)", d.SectionID)
		} else if d.Path == ".claude/CLAUDE.md" {
			sawClaudeMDSection = true
		}
		if d.Path == ".agent-sync-managed" {
			t.Error("user scope must not declare the .agent-sync-managed sidecar (OQ-1)")
		}
	}
	if !sawMCPPointer {
		t.Error(".claude.json locator not asserted")
	}
	if !sawClaudeMDSection {
		t.Error(".claude/CLAUDE.md locator not asserted")
	}
}

// TestDeclaredOutputs_UnknownScopeFallsBackToProject asserts an empty or
// unrecognized scope produces the project-scope outputs (back-compat default).
func TestDeclaredOutputs_UnknownScopeFallsBackToProject(t *testing.T) {
	t.Parallel()

	for _, scope := range []string{"", "project", "directory", "bogus"} {
		got := declaredOutputs(scope)
		paths := map[string]bool{}
		for _, d := range got {
			paths[d.Path] = true
		}
		if !paths["CLAUDE.md"] || !paths[".mcp.json"] || !paths[".agent-sync-managed"] {
			t.Errorf("scope %q: expected project-scope outputs (CLAUDE.md, .mcp.json, .agent-sync-managed), got %v", scope, paths)
		}
		if paths[".claude/CLAUDE.md"] || paths[".claude.json"] {
			t.Errorf("scope %q: must not emit user-scope paths", scope)
		}
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
			DeclaredOutputs: declaredOutputs("project"),
		}, nil
	})
	server.OnEmit(func(ctx context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
		return handleEmit(ctx, params, "project", "", "")
	})

	client, cleanup := adapterkit.RunInprocServer(t, server)
	defer cleanup()

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

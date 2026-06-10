package report

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter/contract"
	"github.com/agent-sync/agent-sync/internal/fsroot"
)

func caps(kinds map[string]contract.CapabilityLevel) contract.Capabilities {
	return contract.Capabilities{ConceptKinds: kinds, WriteToolOwned: true}
}

func TestBuildCapability_RequiredUnmet(t *testing.T) {
	t.Parallel()
	r := BuildCapability("T", []CapabilityInput{
		{
			Target:        "claude",
			Caps:          caps(map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported, "skill": contract.CapabilityUnsupported}),
			RequiredKinds: []string{"rule", "skill"},
		},
	})
	if len(r.Targets) != 1 {
		t.Fatalf("targets = %d", len(r.Targets))
	}
	unmet := r.Targets[0].RequiredUnmet
	if len(unmet) != 1 || unmet[0] != "skill" {
		t.Errorf("required_unmet = %v want [skill]", unmet)
	}
	if !r.AnyRequiredUnmet() {
		t.Error("AnyRequiredUnmet should be true")
	}
}

func TestBuildCapability_AllMet(t *testing.T) {
	t.Parallel()
	r := BuildCapability("T", []CapabilityInput{
		{Target: "claude", Caps: caps(map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported}), RequiredKinds: []string{"rule"}},
	})
	if r.AnyRequiredUnmet() {
		t.Error("AnyRequiredUnmet should be false")
	}
	if len(r.Targets[0].RequiredUnmet) != 0 {
		t.Errorf("unmet should be empty, got %v", r.Targets[0].RequiredUnmet)
	}
}

func TestMarshalCapability_Deterministic(t *testing.T) {
	t.Parallel()
	in := []CapabilityInput{
		{Target: "cursor", Caps: caps(map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported})},
		{Target: "claude", Caps: caps(map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported})},
	}
	a, _ := MarshalCapability(BuildCapability("T", in))
	b, _ := MarshalCapability(BuildCapability("T", in))
	if !bytes.Equal(a, b) {
		t.Error("capability marshal not deterministic")
	}
	// claude sorts before cursor.
	if i, j := bytes.Index(a, []byte("claude")), bytes.Index(a, []byte("cursor")); i > j {
		t.Errorf("targets not sorted: %s", a)
	}
}

func TestWriteCapabilityReport(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })

	r := BuildCapability("T", []CapabilityInput{
		{Target: "claude", Caps: caps(map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported})},
	})
	if err := WriteCapabilityReport(root, r); err != nil {
		t.Fatalf("WriteCapabilityReport: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(ws, ".agent-sync", "state", "capability-report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !bytes.Contains(data, []byte(`"schema_version": 1`)) || !bytes.Contains(data, []byte("claude")) {
		t.Errorf("report content unexpected:\n%s", data)
	}
}

package adapterkit

import "testing"

func TestCapabilitiesBuilder_BuildsConceptKinds(t *testing.T) {
	t.Parallel()

	caps := NewCapabilities().
		Supports("rule").
		Partial("skill", "assets pending").
		Unsupported("mcp-server-entry").
		WithWriteToolOwned(true).
		WithProgress(true).
		Build()

	if caps.ConceptKinds["rule"] != CapabilitySupported {
		t.Fatalf("rule=%q", caps.ConceptKinds["rule"])
	}
	if caps.ConceptKinds["skill"] != CapabilityPartial {
		t.Fatalf("skill=%q", caps.ConceptKinds["skill"])
	}
	if caps.ConceptKinds["mcp-server-entry"] != CapabilityUnsupported {
		t.Fatalf("mcp-server-entry=%q", caps.ConceptKinds["mcp-server-entry"])
	}
	if !caps.WriteToolOwned || !caps.Progress {
		t.Fatalf("caps=%+v", caps)
	}
}

func TestCapabilitiesBuilder_LastWriteWins(t *testing.T) {
	t.Parallel()

	caps := NewCapabilities().
		Supports("rule").
		Unsupported("rule").
		Build()

	if caps.ConceptKinds["rule"] != CapabilityUnsupported {
		t.Fatalf("rule=%q", caps.ConceptKinds["rule"])
	}
}

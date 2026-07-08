package manifest_test

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/agent-sync/agent-sync/internal/manifest"
)

func boolPtr(v bool) *bool { return &v }

func TestCanonicalSourceAuto_MarshalOmitsNil(t *testing.T) {
	m := manifest.Manifest{
		Version: 1,
		Canonical: manifest.CanonicalSource{
			URL: "https://example.com/agents.git",
		},
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(out); strings.Contains(got, "auto:") {
		t.Fatalf("nil auto should be omitted, got:\n%s", got)
	}
}

func TestCanonicalSourceAuto_RoundTripTrue(t *testing.T) {
	m := manifest.Manifest{
		Version: 1,
		Canonical: manifest.CanonicalSource{
			URL:  "https://example.com/agents.git",
			Auto: boolPtr(true),
		},
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(out); !strings.Contains(got, "auto: true") {
		t.Fatalf("marshal missing auto: true:\n%s", got)
	}

	loaded, err := manifest.LoadBytes(out, manifest.LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Canonical.Auto == nil || !*loaded.Canonical.Auto {
		t.Fatalf("auto = %#v, want non-nil true", loaded.Canonical.Auto)
	}
}

func TestCanonicalSourceAuto_RoundTripFalse(t *testing.T) {
	// A non-nil false is the opt-out value; omitempty on *bool must NOT drop
	// it, or a manifest rewrite (WriteResolvedSHA/WriteFile) would silently
	// flip the user back to auto-on.
	m := manifest.Manifest{
		Version: 1,
		Canonical: manifest.CanonicalSource{
			URL:  "https://example.com/agents.git",
			Auto: boolPtr(false),
		},
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(out); !strings.Contains(got, "auto: false") {
		t.Fatalf("marshal missing auto: false:\n%s", got)
	}

	loaded, err := manifest.LoadBytes(out, manifest.LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Canonical.Auto == nil || *loaded.Canonical.Auto {
		t.Fatalf("auto = %#v, want non-nil false", loaded.Canonical.Auto)
	}
}

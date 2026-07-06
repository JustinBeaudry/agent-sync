package harness

import (
	"fmt"
	"testing"

	"github.com/agent-sync/agent-sync/internal/git"
)

type fakeSource struct {
	files map[string][]byte
}

func (f fakeSource) ReadTree(string) ([]git.TreeEntry, error) {
	out := make([]git.TreeEntry, 0, len(f.files))
	for p := range f.files {
		out = append(out, git.TreeEntry{
			Path: p,
			Mode: 0o100644,
		})
	}
	return out, nil
}

func (f fakeSource) BlobContent(_ string, path string) ([]byte, error) {
	b, ok := f.files[path]
	if !ok {
		return nil, fmt.Errorf("missing blob at %q", path)
	}
	return b, nil
}

func TestDecodeFragments_CodexFeatureFlag(t *testing.T) {
	src := fakeSource{files: map[string][]byte{
		"configs/codex/features/hooks/fragment.yaml": []byte(`id: hooks
target: codex
path: .codex/config.toml
merge: toml-key
locator: features.hooks
payload: payload.toml
`),
		"configs/codex/features/hooks/payload.toml": []byte(`[features]
hooks = true
`),
	}}

	frags, warnings, err := Decode(src, "", DecodeOptions{Scope: "workspace"})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	if len(frags) != 1 {
		t.Fatalf("fragments = %d, want 1", len(frags))
	}
	got := frags[0]
	if got.Identity() != "codex\x00.codex/config.toml\x00toml-key\x00features.hooks" {
		t.Fatalf("identity = %q", got.Identity())
	}
	if got.Visibility != VisibilityTeam || got.Inheritance != InheritanceDescendants || got.Safety != SafetyPassive {
		t.Fatalf("defaults = %+v", got)
	}
}

func TestDecodeFragments_RejectsPayloadTraversal(t *testing.T) {
	src := fakeSource{files: map[string][]byte{
		"configs/codex/features/hooks/fragment.yaml": []byte(`id: hooks
target: codex
path: .codex/config.toml
merge: toml-key
locator: features.hooks
payload: ../payload.toml
`),
	}}

	_, _, err := Decode(src, "", DecodeOptions{Scope: "workspace"})
	if err == nil {
		t.Fatal("Decode returned nil error for payload traversal")
	}
}

func TestDecodeFragments_RejectsCleanedPayloadTraversal(t *testing.T) {
	src := fakeSource{files: map[string][]byte{
		"configs/codex/features/hooks/fragment.yaml": []byte(`id: hooks
target: codex
path: .codex/config.toml
merge: toml-key
locator: features.hooks
payload: nested/../payload.toml
`),
		"configs/codex/features/hooks/payload.toml": []byte(`[features]
hooks = false
`),
	}}

	_, _, err := Decode(src, "", DecodeOptions{Scope: "workspace"})
	if err == nil {
		t.Fatal("Decode returned nil error for cleaned payload traversal")
	}
}

func TestDecodeFragments_DoesNotCoerceMachineLocalInheritance(t *testing.T) {
	src := fakeSource{files: map[string][]byte{
		"configs/codex/features/hooks/fragment.yaml": []byte(`id: hooks
target: codex
path: .codex/config.toml
merge: toml-key
locator: features.hooks
visibility: machine-local
inheritance: descendants
payload: payload.toml
`),
		"configs/codex/features/hooks/payload.toml": []byte(`[features]
hooks = false
`),
	}}

	frags, _, err := Decode(src, "", DecodeOptions{Scope: "workspace"})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if frags[0].Visibility != VisibilityMachineLocal || frags[0].Inheritance != InheritanceDescendants {
		t.Fatalf("machine-local fields were coerced: %+v", frags[0])
	}
}

func TestDecodeFragments_UserDefaultsArePersonalRootOnly(t *testing.T) {
	src := fakeSource{files: map[string][]byte{
		"configs/codex/features/hooks/fragment.yaml": []byte(`id: hooks
target: codex
path: .codex/config.toml
merge: toml-key
locator: features.hooks
payload: payload.toml
`),
		"configs/codex/features/hooks/payload.toml": []byte(`[features]
hooks = false
`),
	}}

	frags, _, err := Decode(src, "", DecodeOptions{Scope: "user"})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if frags[0].Visibility != VisibilityPersonal || frags[0].Inheritance != InheritanceRootOnly {
		t.Fatalf("user defaults = %+v", frags[0])
	}
}

func TestDecodeFragments_ProjectDefaultsAreTeamRootOnly(t *testing.T) {
	src := fakeSource{files: map[string][]byte{
		"configs/codex/features/hooks/fragment.yaml": []byte(`id: hooks
target: codex
path: .codex/config.toml
merge: toml-key
locator: features.hooks
payload: payload.toml
`),
		"configs/codex/features/hooks/payload.toml": []byte(`[features]
hooks = true
`),
	}}

	frags, _, err := Decode(src, "", DecodeOptions{Scope: "project"})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if frags[0].Visibility != VisibilityTeam || frags[0].Inheritance != InheritanceRootOnly {
		t.Fatalf("project defaults = %+v", frags[0])
	}
}

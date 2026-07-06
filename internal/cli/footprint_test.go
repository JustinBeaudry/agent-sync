package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverTargets_FindsFootprintDirs(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{".claude", ".cursor"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	got, warns := discoverTargets(dir, bundledAdapters())

	want := []string{"claude", "cursor"}
	if len(got) != len(want) {
		t.Fatalf("discoverTargets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("discoverTargets = %v, want %v", got, want)
		}
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
}

func TestDiscoverTargets_EmptyDirFindsNothing(t *testing.T) {
	got, warns := discoverTargets(t.TempDir(), bundledAdapters())
	if len(got) != 0 {
		t.Fatalf("discoverTargets = %v, want empty", got)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
}

func TestDiscoverTargets_FileIsNotAFootprint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".pi"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write .pi file: %v", err)
	}

	got, _ := discoverTargets(dir, bundledAdapters())
	if len(got) != 0 {
		t.Fatalf("a plain file must not be discovered; got %v", got)
	}
}

// The .agents in-repo skills dir is one character away from antigravity's
// .agent reserved prefix; exact-name matching must keep them apart (the
// documented substring-prefix trap).
func TestDiscoverTargets_AgentsDirDoesNotDiscoverAntigravity(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir .agents: %v", err)
	}

	got, _ := discoverTargets(dir, bundledAdapters())
	if len(got) != 0 {
		t.Fatalf(".agents must not discover antigravity (.agent); got %v", got)
	}
}

func TestDiscoverTargets_AllFootprintsSortedByName(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{".pi", ".codex", ".claude", ".cursor", ".agent"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	got, _ := discoverTargets(dir, bundledAdapters())

	want := []string{"antigravity", "claude", "codex", "cursor", "pi"}
	if len(got) != len(want) {
		t.Fatalf("discoverTargets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("discoverTargets = %v, want %v (sorted by name)", got, want)
		}
	}
}

func TestDiscoverTargets_MissingBaseDirFindsNothing(t *testing.T) {
	got, warns := discoverTargets(filepath.Join(t.TempDir(), "does-not-exist"), bundledAdapters())
	if len(got) != 0 {
		t.Fatalf("discoverTargets = %v, want empty", got)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings for missing base dir: %v", warns)
	}
}

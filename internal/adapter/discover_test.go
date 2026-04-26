package adapter_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aienvs/aienvs/internal/adapter"
	"github.com/aienvs/aienvs/internal/manifest"
)

// makeFakeAdapterBinary creates a file named aienvs-adapter-<name> in
// dir and returns its absolute path. The file is empty but executable
// on Unix; Windows uses a .exe suffix that discovery's PATH lookup
// must handle. The file's contents do not matter for discovery tests.
func makeFakeAdapterBinary(t *testing.T, dir, name string) string {
	t.Helper()
	binName := "aienvs-adapter-" + name
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	path := filepath.Join(dir, binName)
	if err := os.WriteFile(path, []byte{}, 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestDiscover_ExplicitWorkspaceManifestWins(t *testing.T) {
	t.Parallel()

	m := &manifest.Manifest{
		Adapters: []manifest.AdapterDecl{
			{Name: "claude", Command: []string{"aienvs-adapter-claude"}, ReservedPrefix: ".claude"},
		},
	}
	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Workspace: m,
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	if got := r.Names(); len(got) != 1 || got[0] != "claude" {
		t.Errorf("registry names: %v", got)
	}
	a, ok := r.Get("claude")
	if !ok {
		t.Fatal("Get(claude): not found")
	}
	if a.Manifest.Name != "claude" || a.Source != adapter.SourceWorkspaceManifest {
		t.Errorf("adapter: %+v", a)
	}
}

func TestDiscover_PATHLookupFindsBinary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	makeFakeAdapterBinary(t, dir, "cursor")

	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		PATH: []string{dir},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	a, ok := r.Get("cursor")
	if !ok {
		t.Fatalf("cursor not found; names=%v", r.Names())
	}
	if a.Source != adapter.SourcePATH {
		t.Errorf("source: %v", a.Source)
	}
}

func TestDiscover_WorkspaceManifestBeatsPATH(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	makeFakeAdapterBinary(t, dir, "claude")

	m := &manifest.Manifest{
		Adapters: []manifest.AdapterDecl{
			{
				Name:    "claude",
				Command: []string{"override-binary"},
			},
		},
	}
	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Workspace: m,
		PATH:      []string{dir},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	a, _ := r.Get("claude")
	if a.Source != adapter.SourceWorkspaceManifest {
		t.Errorf("workspace must win; got source=%v", a.Source)
	}
	if len(a.Manifest.Command) != 1 || a.Manifest.Command[0] != "override-binary" {
		t.Errorf("workspace command override lost: %v", a.Manifest.Command)
	}
}

func TestDiscover_BundledAdapterRegistered(t *testing.T) {
	t.Parallel()

	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Bundled: []*adapter.BundledAdapter{
			{
				Manifest: adapter.AdapterManifest{
					Name:            "echo",
					ContractVersion: adapter.ContractVersionV1,
					Command:         []string{"echo-bundled"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	a, ok := r.Get("echo")
	if !ok {
		t.Fatalf("echo not found; names=%v", r.Names())
	}
	if a.Source != adapter.SourceBundled {
		t.Errorf("source: %v", a.Source)
	}
}

func TestDiscover_NestedReservedPrefixRejected(t *testing.T) {
	t.Parallel()

	m := &manifest.Manifest{
		Adapters: []manifest.AdapterDecl{
			{Name: "outer", Command: []string{"o"}, ReservedPrefix: ".claude"},
			{Name: "inner", Command: []string{"i"}, ReservedPrefix: ".claude/skills"},
		},
	}
	_, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Workspace: m,
	})
	if !errors.Is(err, adapter.ErrAdapterPrefixNested) {
		t.Fatalf("want ErrAdapterPrefixNested, got %v", err)
	}
	if !strings.Contains(err.Error(), "outer") || !strings.Contains(err.Error(), "inner") {
		t.Errorf("error should mention both adapter names: %v", err)
	}
}

func TestDiscover_NonexistentPATHEntrySkipped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	makeFakeAdapterBinary(t, dir, "foo")

	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		PATH: []string{filepath.Join(dir, "nonexistent"), dir},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	if _, ok := r.Get("foo"); !ok {
		t.Fatalf("foo should be found despite earlier nonexistent dir; names=%v", r.Names())
	}
}

func TestDiscover_EmptyAdapterNameOnPATHIgnored(t *testing.T) {
	// A binary literally named "aienvs-adapter-" (with empty suffix)
	// must not register as an adapter named "".
	t.Parallel()

	dir := t.TempDir()
	emptyName := "aienvs-adapter-"
	if runtime.GOOS == "windows" {
		emptyName += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, emptyName), []byte{}, 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	makeFakeAdapterBinary(t, dir, "real")

	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		PATH: []string{dir},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	if _, ok := r.Get(""); ok {
		t.Error("empty-name adapter should not register")
	}
	if _, ok := r.Get("real"); !ok {
		t.Errorf("real adapter should register; names=%v", r.Names())
	}
}

func TestDiscover_RegistryNamesSortedDeterministically(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"zebra", "alpha", "mike"} {
		makeFakeAdapterBinary(t, dir, name)
	}
	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		PATH: []string{dir},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	names := r.Names()
	want := []string{"alpha", "mike", "zebra"}
	if len(names) != len(want) {
		t.Fatalf("names: %v", names)
	}
	for i := range names {
		if names[i] != want[i] {
			t.Errorf("names[%d]: want %q, got %q", i, want[i], names[i])
		}
	}
}

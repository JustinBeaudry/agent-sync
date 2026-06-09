package adapter_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/manifest"
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

func TestDiscover_RootDotReservedPrefixNestsEverything(t *testing.T) {
	// SEC: an adapter declaring "." (workspace root) as its reserved
	// prefix claims ownership of every other adapter's prefix. Without
	// the "short == \".\"" branch in isPathPrefix the nested check fails
	// because no real prefix literally starts with "./".
	t.Parallel()

	m := &manifest.Manifest{
		Adapters: []manifest.AdapterDecl{
			{Name: "rooty", Command: []string{"r"}, ReservedPrefix: "."},
			{Name: "claude", Command: []string{"c"}, ReservedPrefix: ".claude"},
		},
	}
	_, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Workspace: m,
	})
	if !errors.Is(err, adapter.ErrAdapterPrefixNested) {
		t.Fatalf("want ErrAdapterPrefixNested when one adapter declares \".\", got %v", err)
	}
	if !strings.Contains(err.Error(), "rooty") || !strings.Contains(err.Error(), "claude") {
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

func TestDiscover_PATHAdapterManifestPinsAbsolutePath(t *testing.T) {
	// SEC/TOCTOU: once discovery has located a PATH adapter binary, the
	// manifest's Command must reference that exact file by absolute path.
	// Storing only "aienvs-adapter-<name>" would force a second $PATH
	// resolution at spawn time, which can pick up a different binary if
	// PATH (or the directory contents) changed since discovery.
	t.Parallel()

	dir := t.TempDir()
	expectedPath := makeFakeAdapterBinary(t, dir, "pinned")

	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		PATH: []string{dir},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	a, ok := r.Get("pinned")
	if !ok {
		t.Fatalf("pinned not found; names=%v", r.Names())
	}
	if len(a.Manifest.Command) != 1 {
		t.Fatalf("command len: %d (%v)", len(a.Manifest.Command), a.Manifest.Command)
	}
	got := a.Manifest.Command[0]
	if !filepath.IsAbs(got) {
		t.Errorf("command must be absolute, got %q", got)
	}
	if got != expectedPath {
		t.Errorf("command: want %q, got %q", expectedPath, got)
	}
}

func TestDiscover_PATHAdapterAbsolutePathFromRelativeDir(t *testing.T) {
	// Discovery must resolve to an absolute path even when the caller
	// passes a relative directory in DiscoverOptions.PATH; otherwise a
	// later cwd change between discovery and spawn would break exec.
	t.Parallel()

	absDir := t.TempDir()
	expectedPath := makeFakeAdapterBinary(t, absDir, "relfind")

	// Compute a relative path from cwd to absDir for this test only.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	relDir, err := filepath.Rel(cwd, absDir)
	if err != nil {
		t.Skipf("cannot derive relative path: %v", err)
	}

	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		PATH: []string{relDir},
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	a, ok := r.Get("relfind")
	if !ok {
		t.Fatalf("relfind not found; names=%v", r.Names())
	}
	got := a.Manifest.Command[0]
	if !filepath.IsAbs(got) {
		t.Errorf("command must be absolute even when PATH dir is relative, got %q", got)
	}
	// The resolved path must point to the same file as the absolute
	// fixture; compare via filepath.Clean to neutralize any "./" noise.
	if filepath.Clean(got) != filepath.Clean(expectedPath) {
		t.Errorf("command: want %q (clean), got %q (clean)", filepath.Clean(expectedPath), filepath.Clean(got))
	}
}

func TestDiscover_AdjacentNonNestedPrefixesRegister(t *testing.T) {
	// Regression for the prefix-validation algorithm. After lex sorting,
	// reserved_prefix values that are *adjacent* but not in a path-prefix
	// relationship — for example ".claude" and ".claude-config", or
	// "a-b" sitting between "a" and "a/b" — must NOT be flagged as
	// nested. Equally, ".claude" plus a sibling ".cursor" must register
	// without complaint. This guards against an over-eager adjacency
	// shortcut in the validator.
	t.Parallel()

	m := &manifest.Manifest{
		Adapters: []manifest.AdapterDecl{
			{Name: "claude", Command: []string{"c"}, ReservedPrefix: ".claude"},
			{Name: "claude-config", Command: []string{"cc"}, ReservedPrefix: ".claude-config"},
			{Name: "cursor", Command: []string{"cu"}, ReservedPrefix: ".cursor"},
		},
	}
	r, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
		Workspace: m,
	})
	if err != nil {
		t.Fatalf("DiscoverAdapters: %v", err)
	}
	for _, n := range []string{"claude", "claude-config", "cursor"} {
		if _, ok := r.Get(n); !ok {
			t.Errorf("%q should register; names=%v", n, r.Names())
		}
	}
}

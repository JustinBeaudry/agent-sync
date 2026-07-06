package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/manifest"
)

func runInit(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	root.SetArgs(append([]string{"init", "--non-interactive"}, args...))
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestInit_NonInteractiveLocalPathWritesLoadableManifest(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := t.TempDir()

	_, errOut, err := runInit(t,
		"--dir", ws,
		"--local-path", canonical,
		"--commit", sha,
		"--target", "claude",
	)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, errOut)
	}
	mpath := filepath.Join(ws, ".agent-sync.yaml")
	m, lerr := manifest.LoadFile(mpath, manifest.LoadOptions{})
	if lerr != nil {
		t.Fatalf("written manifest does not load: %v", lerr)
	}
	if m.Canonical.LocalPath != canonical {
		t.Fatalf("local_path = %q, want %q", m.Canonical.LocalPath, canonical)
	}
	if m.Canonical.Commit != sha || m.TrustedSHA != sha {
		t.Fatalf("manifest not pinned: commit=%q trusted=%q want %q", m.Canonical.Commit, m.TrustedSHA, sha)
	}
}

func TestInit_NonInteractiveLocalDirWritesUnpinnedManifest(t *testing.T) {
	ws := t.TempDir()
	if _, errOut, err := runInit(t, "--dir", ws, "--local-dir", ".agents", "--target", "claude"); err != nil {
		t.Fatalf("init: %v\n%s", err, errOut)
	}
	m, lerr := manifest.LoadFile(filepath.Join(ws, ".agent-sync.yaml"), manifest.LoadOptions{})
	if lerr != nil {
		t.Fatalf("written manifest does not load: %v", lerr)
	}
	if m.Canonical.LocalDir != ".agents" {
		t.Fatalf("local_dir = %q, want .agents", m.Canonical.LocalDir)
	}
	if m.Canonical.Commit != "" || m.TrustedSHA != "" {
		t.Fatalf("local_dir manifest must be unpinned (commit=%q trusted=%q)", m.Canonical.Commit, m.TrustedSHA)
	}
}

func TestInit_LocalDirRejectsPinAndOtherSources(t *testing.T) {
	const sha40 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, _, err := runInit(t, "--dir", t.TempDir(), "--local-dir", ".agents", "--commit", sha40); err == nil {
		t.Error("expected error: --local-dir with --commit")
	}
	if _, _, err := runInit(t, "--dir", t.TempDir(), "--local-dir", ".agents", "--floating"); err == nil {
		t.Error("expected error: --local-dir with --floating")
	}
	if _, _, err := runInit(t, "--dir", t.TempDir(), "--local-dir", ".agents", "--source", "https://example.com/x.git"); err == nil {
		t.Error("expected error: --local-dir with --source")
	}
}

// TestInit_BareNonInteractiveDefaultsToAgentsLocalDir pins the zero-flag
// happy path (plan R1/R2): no source flag defaults the canonical source to
// local_dir .agents, unpinned, and creates the directory so the first sync
// degrades to the zero-emit hint instead of a missing-source failure.
func TestInit_BareNonInteractiveDefaultsToAgentsLocalDir(t *testing.T) {
	ws := t.TempDir()
	_, errOut, err := runInit(t, "--dir", ws)
	if err != nil {
		t.Fatalf("bare init should succeed with defaults: %v\n%s", err, errOut)
	}
	m, lerr := manifest.LoadFile(filepath.Join(ws, ".agent-sync.yaml"), manifest.LoadOptions{})
	if lerr != nil {
		t.Fatalf("written manifest does not load: %v", lerr)
	}
	if m.Canonical.LocalDir != ".agents" {
		t.Fatalf("local_dir = %q, want .agents (defaulted)", m.Canonical.LocalDir)
	}
	if m.Canonical.Commit != "" || m.TrustedSHA != "" {
		t.Fatalf("defaulted local_dir manifest must be unpinned (commit=%q trusted=%q)", m.Canonical.Commit, m.TrustedSHA)
	}
	info, statErr := os.Stat(filepath.Join(ws, ".agents"))
	if statErr != nil || !info.IsDir() {
		t.Fatalf("init must create the defaulted .agents dir: %v", statErr)
	}
}

// TestInit_PinFlagsWithoutSourceFail pins the plan R3 guard: a pin flag with
// no source flag must get a purpose-built error, not the generic local-dir
// validation failure (the user never asked for a local_dir source).
func TestInit_PinFlagsWithoutSourceFail(t *testing.T) {
	const sha40 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct {
		name string
		args []string
	}{
		{"ref", []string{"--ref", "main"}},
		{"commit", []string{"--commit", sha40}},
		{"floating", []string{"--floating"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runInit(t, append([]string{"--dir", t.TempDir()}, tc.args...)...)
			if err == nil {
				t.Fatalf("--%s without a source flag must fail", tc.name)
			}
			if !strings.Contains(err.Error(), "--source or --local-path") {
				t.Fatalf("error should name the required source flags, got: %v", err)
			}
			if strings.Contains(err.Error(), "exactly one of") {
				t.Fatalf("error must not be the generic source-validation message: %v", err)
			}
		})
	}
}

// TestInit_CreatesMissingExplicitLocalDir: creation applies to explicit
// --local-dir too (plan R2 — one code path, defaulted or explicit).
func TestInit_CreatesMissingExplicitLocalDir(t *testing.T) {
	ws := t.TempDir()
	if _, errOut, err := runInit(t, "--dir", ws, "--local-dir", "skills", "--target", "claude"); err != nil {
		t.Fatalf("init: %v\n%s", err, errOut)
	}
	info, statErr := os.Stat(filepath.Join(ws, "skills"))
	if statErr != nil || !info.IsDir() {
		t.Fatalf("init must create the missing local-dir: %v", statErr)
	}
}

// TestInit_NonexistentDestDirFails pins plan R8: a bad --dir fails with a
// clear directory error before any discovery or targets messaging.
func TestInit_NonexistentDestDirFails(t *testing.T) {
	_, _, err := runInit(t, "--dir", filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("init with a nonexistent --dir must fail")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error should name the missing directory, got: %v", err)
	}
	if strings.Contains(err.Error(), "--target") {
		t.Fatalf("a bad --dir must not surface as a targets problem: %v", err)
	}
}

// TestShouldRunInitWizard pins the wizard gate (plan R12): the wizard runs
// only for a fully-unspecified interactive init — any source flag or
// explicit --target makes the invocation fully specified via defaults.
func TestShouldRunInitWizard(t *testing.T) {
	cases := []struct {
		name        string
		interactive bool
		source      string
		localPath   string
		localDir    string
		targets     []string
		want        bool
	}{
		{"bare interactive", true, "", "", "", nil, true},
		{"non-interactive", false, "", "", "", nil, false},
		{"source given", true, "https://x/y.git", "", "", nil, false},
		{"local-path given", true, "", "../repo", "", nil, false},
		{"local-dir given", true, "", "", ".agents", nil, false},
		{"target given", true, "", "", "", []string{"claude"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRunInitWizard(tc.interactive, tc.source, tc.localPath, tc.localDir, tc.targets)
			if got != tc.want {
				t.Fatalf("shouldRunInitWizard = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestInit_LocalPathPinsHEADWithoutCommit verifies init resolves a local
// repo's HEAD to a SHA (no --commit, no network), so a local-path manifest
// is pinned and immediately syncable.
func TestInit_LocalPathPinsHEADWithoutCommit(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := t.TempDir()

	if _, errOut, err := runInit(t, "--dir", ws, "--local-path", canonical, "--target", "claude"); err != nil {
		t.Fatalf("init: %v\n%s", err, errOut)
	}
	m, lerr := manifest.LoadFile(filepath.Join(ws, ".agent-sync.yaml"), manifest.LoadOptions{})
	if lerr != nil {
		t.Fatalf("manifest load: %v", lerr)
	}
	if m.Canonical.Commit != sha {
		t.Fatalf("expected init to pin HEAD %s, got commit %q", sha, m.Canonical.Commit)
	}
	// And it syncs.
	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("sync after local-path init: %v\n%s", err, errOut)
	}
}

func TestInit_RefusesToOverwrite(t *testing.T) {
	requireGit(t)
	canonical, _ := makeCanonicalRepo(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runInit(t, "--dir", ws, "--local-path", canonical, "--floating")
	if err == nil {
		t.Fatal("init should refuse to overwrite an existing manifest")
	}
}

// TestInit_ThenSync proves the init -> sync loop closes: a manifest
// written by `agent-sync init` is synced successfully by `agent-sync sync`.
func TestInit_ThenSync(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := t.TempDir()

	if _, errOut, err := runInit(t, "--dir", ws, "--local-path", canonical, "--commit", sha, "--target", "claude"); err != nil {
		t.Fatalf("init: %v\n%s", err, errOut)
	}
	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("sync after init: %v\n%s", err, errOut)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".claude", "rules", "agent-sync", "no-fri.md")); statErr != nil {
		t.Fatalf("expected synced rule file: %v", statErr)
	}
}

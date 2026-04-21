package manifest_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aienvs/aienvs/internal/manifest"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	// Tests live in internal/manifest; repo root is two levels up.
	// testdata/manifest/... is checked in from the repo root.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", "testdata", "manifest", name)
}

func loadFixture(t *testing.T, name string, opts manifest.LoadOptions) (*manifest.Manifest, error) {
	t.Helper()
	return manifest.LoadFile(fixture(t, name), opts)
}

func TestLoad_ValidMinimal(t *testing.T) {
	m, err := loadFixture(t, "valid-minimal.yaml", manifest.LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("version = %d, want 1", m.Version)
	}
	if m.Canonical.URL == "" {
		t.Error("canonical.url empty")
	}
	if m.Canonical.Ref != "main" {
		t.Errorf("canonical.ref = %q, want main", m.Canonical.Ref)
	}
	if m.Canonical.LocalPath != "" {
		t.Errorf("canonical.local_path = %q, want empty", m.Canonical.LocalPath)
	}
}

func TestLoad_ValidPinned(t *testing.T) {
	m, err := loadFixture(t, "valid-pinned.yaml", manifest.LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Canonical.Commit == "" {
		t.Error("canonical.commit empty, want 40-hex")
	}
	if m.TrustedSHA != m.Canonical.Commit {
		t.Errorf("trusted_sha %q != commit %q", m.TrustedSHA, m.Canonical.Commit)
	}
	if m.Scope != "project" {
		t.Errorf("scope = %q, want project", m.Scope)
	}
	if want := []string{"claude", "cursor"}; !equalSlices(m.Targets, want) {
		t.Errorf("targets = %v, want %v", m.Targets, want)
	}
}

func TestLoad_ValidLocalPath(t *testing.T) {
	m, err := loadFixture(t, "valid-local-path.yaml", manifest.LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Canonical.URL != "" {
		t.Errorf("canonical.url = %q, want empty", m.Canonical.URL)
	}
	if m.Canonical.LocalPath == "" {
		t.Error("canonical.local_path empty")
	}
}

func TestLoad_ValidExtensionKey(t *testing.T) {
	// x-* keys should not trigger unknown-field errors.
	if _, err := loadFixture(t, "valid-with-extension-key.yaml", manifest.LoadOptions{}); err != nil {
		t.Fatalf("load: %v", err)
	}
}

func TestLoad_ValidNoTargets(t *testing.T) {
	m, err := loadFixture(t, "valid-no-targets.yaml", manifest.LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(m.Targets) != 0 {
		t.Errorf("targets = %v, want empty slice default", m.Targets)
	}
}

func TestLoad_InvalidUnknownKey(t *testing.T) {
	_, err := loadFixture(t, "invalid-unknown-key.yaml", manifest.LoadOptions{})
	if err == nil {
		t.Fatal("expected error for unknown top-level key")
	}
	if !errors.Is(err, manifest.ErrInvalidManifest) {
		t.Errorf("error not classed as ErrInvalidManifest: %v", err)
	}
	// goccy surfaces the line and key in the error text.
	if !strings.Contains(err.Error(), "canonicl") {
		t.Errorf("error does not mention typo key; got: %v", err)
	}
}

func TestLoad_InvalidBothURLAndLocalPath(t *testing.T) {
	_, err := loadFixture(t, "invalid-both-url-and-local-path.yaml", manifest.LoadOptions{})
	if err == nil {
		t.Fatal("expected 1:1 violation")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error does not name the 1:1 invariant; got: %v", err)
	}
}

func TestLoad_InvalidNeither(t *testing.T) {
	_, err := loadFixture(t, "invalid-neither-url-nor-local-path.yaml", manifest.LoadOptions{})
	if err == nil {
		t.Fatal("expected error when canonical has neither url nor local_path")
	}
}

func TestLoad_InvalidTrustedWithoutCommit(t *testing.T) {
	_, err := loadFixture(t, "invalid-trusted-without-commit.yaml", manifest.LoadOptions{})
	if err == nil {
		t.Fatal("expected load error: trusted_sha without commit")
	}
	if !strings.Contains(err.Error(), "floating-with-pin") && !strings.Contains(err.Error(), "canonical.commit is empty") {
		t.Errorf("error does not name the invariant; got: %v", err)
	}
}

func TestLoad_InvalidBadSHA(t *testing.T) {
	_, err := loadFixture(t, "invalid-bad-sha.yaml", manifest.LoadOptions{})
	if err == nil {
		t.Fatal("expected error for non-hex commit")
	}
	// Either the commit or trusted_sha line should be named.
	if !strings.Contains(err.Error(), "40 lowercase hex") {
		t.Errorf("error does not reference format; got: %v", err)
	}
}

func TestLoad_InvalidScope(t *testing.T) {
	_, err := loadFixture(t, "invalid-scope.yaml", manifest.LoadOptions{})
	if err == nil {
		t.Fatal("expected error for bad scope")
	}
	if !strings.Contains(err.Error(), "scope must be one of") {
		t.Errorf("error does not name valid scopes; got: %v", err)
	}
}

func TestLoad_InvalidCommitMismatch(t *testing.T) {
	_, err := loadFixture(t, "invalid-commit-mismatch.yaml", manifest.LoadOptions{})
	if err == nil {
		t.Fatal("expected error when trusted_sha != commit")
	}
	if !strings.Contains(err.Error(), "mirror canonical.commit") {
		t.Errorf("error does not name the mirror invariant; got: %v", err)
	}
}

func TestLoad_NonInteractiveRequiresTrustedSHA(t *testing.T) {
	// valid-minimal has no commit and no trusted_sha — should pass
	// even in non-interactive mode.
	if _, err := loadFixture(t, "valid-minimal.yaml", manifest.LoadOptions{NonInteractive: true}); err != nil {
		t.Fatalf("non-interactive + floating manifest should load: %v", err)
	}

	// Construct a manifest with commit set but no trusted_sha → CI guard fires.
	src := []byte("version: 1\ncanonical:\n  url: https://example.com/x.git\n  commit: 1111111111111111111111111111111111111111\n")
	if _, err := manifest.LoadBytes(src, manifest.LoadOptions{NonInteractive: true}); err == nil {
		t.Fatal("expected non-interactive error when commit is set without trusted_sha")
	}
}

func TestLoadBytes_EmptyDocument(t *testing.T) {
	_, err := manifest.LoadBytes([]byte(""), manifest.LoadOptions{})
	if err == nil {
		t.Fatal("empty manifest should fail validation (no canonical source)")
	}
}

func equalSlices(a, b []string) bool {
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

package manifest_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aienvs/aienvs/internal/manifest"
)

const (
	sha1hex = "1111111111111111111111111111111111111111"
	sha2hex = "2222222222222222222222222222222222222222"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(fixture(t, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

func TestWriteResolvedSHA_FillsTemplate(t *testing.T) {
	orig := readFixture(t, "valid-template-unresolved.yaml")
	out, err := manifest.WriteResolvedSHA(orig, sha1hex, sha1hex)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "commit: "+sha1hex) {
		t.Errorf("commit not written into output:\n%s", s)
	}
	if !strings.Contains(s, "trusted_sha: "+sha1hex) {
		t.Errorf("trusted_sha not written into output:\n%s", s)
	}
	// Re-loading must succeed: the file is internally consistent.
	if _, err := manifest.LoadBytes(out, manifest.LoadOptions{NonInteractive: true}); err != nil {
		t.Errorf("round-trip LoadBytes: %v", err)
	}
}

func TestWriteResolvedSHA_PreservesComments(t *testing.T) {
	orig := readFixture(t, "commented-manifest.yaml")
	out, err := manifest.WriteResolvedSHA(orig, sha1hex, sha1hex)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	s := string(out)

	// Every head comment from the source must still be present after
	// the surgical replace.
	wantComments := []string{
		"Project aienvs manifest",
		"Owned by: platform-eng@example.com",
		"Canonical agent-config source",
		"commit is filled by `aienvs init`",
		"Project trust anchor",
		"primary adapter",
	}
	for _, w := range wantComments {
		if !strings.Contains(s, w) {
			t.Errorf("comment %q missing from output:\n%s", w, s)
		}
	}
	if !strings.Contains(s, "commit: "+sha1hex) {
		t.Errorf("commit not written: %s", s)
	}
	if !strings.Contains(s, "trusted_sha: "+sha1hex) {
		t.Errorf("trusted_sha not written: %s", s)
	}
}

func TestWriteResolvedSHA_KeyMissing(t *testing.T) {
	// valid-minimal has neither canonical.commit nor trusted_sha, so
	// the write must refuse rather than silently append.
	orig := readFixture(t, "valid-minimal.yaml")
	_, err := manifest.WriteResolvedSHA(orig, sha1hex, "")
	if err == nil {
		t.Fatal("expected ErrKeyMissing; got nil")
	}
	if !errors.Is(err, manifest.ErrKeyMissing) {
		t.Errorf("expected ErrKeyMissing, got %v", err)
	}
}

func TestWriteResolvedSHA_TrustedKeyMissing(t *testing.T) {
	// valid-template-unresolved has both keys; drop just trusted_sha
	// by replacing it with a file that keeps only canonical.commit.
	src := []byte("version: 1\ncanonical:\n  url: https://example.com/x.git\n  commit: \"\"\n")
	_, err := manifest.WriteResolvedSHA(src, "", sha1hex)
	if err == nil {
		t.Fatal("expected ErrKeyMissing for absent trusted_sha")
	}
	if !errors.Is(err, manifest.ErrKeyMissing) {
		t.Errorf("expected ErrKeyMissing, got %v", err)
	}
}

func TestWriteResolvedSHA_RejectsBadSHA(t *testing.T) {
	orig := readFixture(t, "valid-template-unresolved.yaml")
	for _, bad := range []string{"NOT-HEX", "short", "11111111111111111111111111111111111111111", "1111111111111111111111111111111111111111X"} {
		_, err := manifest.WriteResolvedSHA(orig, bad, "")
		if err == nil {
			t.Errorf("expected rejection of bad commit %q", bad)
			continue
		}
		if !errors.Is(err, manifest.ErrWriteInvalid) {
			t.Errorf("bad commit %q: wanted ErrWriteInvalid, got %v", bad, err)
		}
	}
}

func TestWriteResolvedSHA_NoOpReturnsCopy(t *testing.T) {
	// valid-template-unresolved has both canonical.commit and trusted_sha keys.
	orig := readFixture(t, "valid-template-unresolved.yaml")
	out, err := manifest.WriteResolvedSHA(orig, "", "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if string(out) != string(orig) {
		t.Error("no-op write should round-trip source bytes")
	}
	// Must be a copy, not a shared buffer.
	if len(out) > 0 {
		out[0] = 'X'
		if orig[0] == 'X' {
			t.Error("output aliases input buffer")
		}
	}
}

func TestWriteResolvedSHA_NoOpStillValidatesKeys(t *testing.T) {
	// A manifest without trusted_sha must fail even when both args are empty.
	src := []byte("version: 1\ncanonical:\n  url: https://example.com/x.git\n  commit: \"\"\n")
	_, err := manifest.WriteResolvedSHA(src, "", "")
	if err == nil {
		t.Fatal("expected ErrKeyMissing; got nil")
	}
	if !errors.Is(err, manifest.ErrKeyMissing) {
		t.Errorf("expected ErrKeyMissing, got %v", err)
	}
}

func TestWriteTrustedSHA_OnlyUpdatesTrusted(t *testing.T) {
	orig := readFixture(t, "valid-template-unresolved.yaml")
	out, err := manifest.WriteTrustedSHA(orig, sha2hex)
	if err != nil {
		t.Fatalf("write trusted: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "trusted_sha: "+sha2hex) {
		t.Errorf("trusted_sha not written:\n%s", s)
	}
	// commit: "" should remain unchanged (empty string, preserved).
	if strings.Contains(s, "commit: "+sha2hex) {
		t.Errorf("WriteTrustedSHA must not touch commit:\n%s", s)
	}
}

func TestWriteFile_AtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aienv.yaml")
	content := []byte("version: 1\ncanonical:\n  url: https://example.com/x.git\n  ref: main\n")
	if err := manifest.WriteFile(path, content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch:\nwant: %s\ngot:  %s", content, got)
	}
	// Verify the manifest is loadable — smoke-test the full cycle.
	if _, err := manifest.LoadFile(path, manifest.LoadOptions{}); err != nil {
		t.Errorf("LoadFile after WriteFile: %v", err)
	}
}

func TestWriteFile_OverwriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aienv.yaml")
	if err := manifest.WriteFile(path, []byte("version: 1\ncanonical:\n  url: https://a.example.com/x.git\n")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := manifest.WriteFile(path, []byte("version: 1\ncanonical:\n  url: https://b.example.com/y.git\n")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(got), "b.example.com") {
		t.Errorf("overwrite lost second content: %s", got)
	}
}

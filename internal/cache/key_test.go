package cache_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/cache"
)

func TestKey_Deterministic(t *testing.T) {
	k1 := cache.Key("https://github.com/foo/bar.git")
	k2 := cache.Key("https://github.com/foo/bar.git")
	if k1 != k2 {
		t.Errorf("Key non-deterministic: %s vs %s", k1, k2)
	}
	// SHA-256 hex is 64 chars.
	if len(k1) != 64 {
		t.Errorf("Key length = %d, want 64", len(k1))
	}
}

func TestKeyPrefix_Participates(t *testing.T) {
	const url = "https://github.com/foo/bar.git"
	sum := sha256.Sum256([]byte(cache.KeyPrefix + url))
	want := hex.EncodeToString(sum[:])
	got := cache.Key(url)
	if got != want {
		t.Fatalf("Key(%q) = %q, want %q (prefix+url must be hashed)", url, got, want)
	}
	// Also verify that bare url hash differs from prefixed hash
	bareSum := sha256.Sum256([]byte(url))
	if hex.EncodeToString(bareSum[:]) == got {
		t.Error("Key appears to ignore KeyPrefix")
	}
}

func TestResolve_UsesOverride(t *testing.T) {
	tmp := t.TempDir()
	loc, err := cache.Resolve("https://github.com/foo/bar.git", cache.ResolveOptions{Override: tmp})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasPrefix(loc.Root, tmp) {
		t.Errorf("root %q not under override %q", loc.Root, tmp)
	}
	if !strings.HasSuffix(filepath.ToSlash(loc.Root), cache.DirName) {
		t.Errorf("root %q missing DirName suffix", loc.Root)
	}
	if loc.Dir != filepath.Join(loc.Root, loc.Key) {
		t.Errorf("Dir = %q, want %q", loc.Dir, filepath.Join(loc.Root, loc.Key))
	}
	if loc.AuditPath != filepath.Join(loc.Dir, cache.AuditFileName) {
		t.Errorf("AuditPath = %q, want %q", loc.AuditPath, filepath.Join(loc.Dir, cache.AuditFileName))
	}
}

func TestResolve_RejectsRelativeOverride(t *testing.T) {
	_, err := cache.Resolve("https://github.com/foo/bar.git", cache.ResolveOptions{Override: "relative/path"})
	if err == nil {
		t.Fatal("expected rejection of relative override")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("error does not name the invariant: %v", err)
	}
}

func TestResolve_RejectsEmptyCanonical(t *testing.T) {
	_, err := cache.Resolve("", cache.ResolveOptions{Override: t.TempDir()})
	if !errors.Is(err, cache.ErrUnsupportedURL) {
		t.Errorf("empty canonical: want ErrUnsupportedURL, got %v", err)
	}
}

func TestResolve_XDGDefault(t *testing.T) {
	// With no override, Resolve should produce a root under XDG. We
	// don't hard-code the XDG path (it varies per OS and per test
	// host) but we can assert the DirName suffix and absolute-ness.
	loc, err := cache.Resolve("https://github.com/foo/bar.git", cache.ResolveOptions{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !filepath.IsAbs(loc.Root) {
		t.Errorf("XDG-resolved root should be absolute: %q", loc.Root)
	}
	if !strings.HasSuffix(filepath.ToSlash(loc.Root), cache.DirName) {
		t.Errorf("root %q missing DirName suffix", loc.Root)
	}
}

func TestResolve_RejectsNonCanonicalInput(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		name string
		url  string
	}{
		{"with-credentials", "https://user:token@github.com/foo/bar.git"},
		{"trailing-slash", "https://github.com/foo/bar/"},
		{"uppercase-host", "https://GITHUB.COM/foo/bar.git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cache.Resolve(tc.url, cache.ResolveOptions{Override: tmp})
			if err == nil {
				t.Fatalf("expected rejection of non-canonical input %q", tc.url)
			}
			if !errors.Is(err, cache.ErrUnsupportedURL) {
				t.Errorf("expected ErrUnsupportedURL, got %v", err)
			}
			if !strings.Contains(err.Error(), "canonical") {
				t.Errorf("error should mention canonical: %v", err)
			}
		})
	}
}

func TestWriteAudit_RejectsForgedCanonical(t *testing.T) {
	tmp := t.TempDir()
	// Construct a Location where Key does not match what Key(Canonical) would produce.
	locA, err := cache.Resolve("https://a.example.com/x.git", cache.ResolveOptions{Override: tmp})
	if err != nil {
		t.Fatalf("resolve A: %v", err)
	}
	locB, err := cache.Resolve("https://b.example.com/x.git", cache.ResolveOptions{Override: tmp})
	if err != nil {
		t.Fatalf("resolve B: %v", err)
	}
	// Forge: use locB's key with locA's canonical URL.
	forged := &cache.Location{
		Root:      locA.Root,
		Dir:       locB.Dir,
		AuditPath: locB.AuditPath,
		Key:       locB.Key,
		Canonical: locA.Canonical,
	}
	if err := forged.WriteAudit(); err == nil {
		t.Fatal("expected error for forged Location; got nil")
	} else if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("error should mention 'does not match': %v", err)
	}
}

func TestWriteAudit_ConcurrentWrites(t *testing.T) {
	tmp := t.TempDir()
	loc, err := cache.Resolve("https://github.com/foo/bar.git", cache.ResolveOptions{Override: tmp})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Pre-create the directory; concurrent WriteAudit callers expect it to exist.
	if err := os.MkdirAll(loc.Dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			errs <- loc.WriteAudit()
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent WriteAudit: %v", err)
		}
	}

	// Final content must be the canonical URL followed by a newline.
	got, err := os.ReadFile(loc.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	want := loc.Canonical + "\n"
	if string(got) != want {
		t.Errorf("audit content = %q, want %q (possible torn write)", got, want)
	}
}

func TestWriteAudit_RefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Root symlink semantics on Windows differ; skipped")
	}
	tmp := t.TempDir()
	loc, err := cache.Resolve("https://github.com/foo/bar.git", cache.ResolveOptions{Override: tmp})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := os.MkdirAll(loc.Dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create a symlink target outside Dir, then plant it at AuditPath.
	target := filepath.Join(tmp, "outside-target.txt")
	if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, loc.AuditPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// WriteAudit must either error cleanly (os.Root rejects symlink
	// traversal) OR land the file inside Dir without following the symlink.
	// In either case, the symlink target must NOT be overwritten.
	_ = loc.WriteAudit()

	gotTarget, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read symlink target: %v", err)
	}
	if string(gotTarget) != "original\n" {
		t.Errorf("symlink target was overwritten: %q (os.Root must prevent this)", gotTarget)
	}
}

func TestWriteAudit_CreatesFile(t *testing.T) {
	tmp := t.TempDir()
	loc, err := cache.Resolve("https://github.com/foo/bar.git", cache.ResolveOptions{Override: tmp})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := loc.WriteAudit(); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}
	got, err := os.ReadFile(loc.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if strings.TrimSpace(string(got)) != loc.Canonical {
		t.Errorf("audit content = %q, want %q", strings.TrimSpace(string(got)), loc.Canonical)
	}
	// Audit file must not contain credentials (ensured by canonicalize,
	// but worth a belt-and-suspenders assertion here).
	if strings.Contains(string(got), "@") && !strings.HasPrefix(loc.Canonical, "ssh://") && !strings.HasPrefix(loc.Canonical, "git://") {
		t.Errorf("audit contains `@` on non-SSH URL: %q", got)
	}
}

func TestWriteAudit_OverwriteIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	loc, err := cache.Resolve("https://github.com/foo/bar.git", cache.ResolveOptions{Override: tmp})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := loc.WriteAudit(); err != nil {
		t.Fatalf("first audit: %v", err)
	}
	if err := loc.WriteAudit(); err != nil {
		t.Fatalf("second audit: %v", err)
	}
}

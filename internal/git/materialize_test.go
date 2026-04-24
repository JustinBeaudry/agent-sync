package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aienvs/aienvs/internal/cache"
)

// buildCacheLocation constructs a cache.Location whose Canonical field
// is the given URL (possibly a local file path, not a full canonical
// form). This lets materialize tests use local-path "URLs" — git
// accepts absolute paths directly — without routing through
// cache.Canonicalize, which rejects file:// and local paths.
//
// Integrity is preserved for WriteAudit: the Key field is the hash of
// whatever the test passes as canonical, so the post-materialize audit
// write still satisfies WriteAudit's self-consistency check.
func buildCacheLocation(t *testing.T, root, canonical string) *cache.Location {
	t.Helper()
	key := cache.Key(canonical)
	dir := filepath.Join(root, key)
	return &cache.Location{
		Root:      root,
		Dir:       dir,
		AuditPath: filepath.Join(dir, cache.AuditFileName),
		Key:       key,
		Canonical: canonical,
	}
}

func TestMaterialize_CloneFreshCache(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	res, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		PinnedSHA:    src.SecondSHA,
		Ref:          src.HeadBranch,
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if res.ResolvedSHA != src.SecondSHA {
		t.Fatalf("ResolvedSHA = %q, want %q", res.ResolvedSHA, src.SecondSHA)
	}
	if res.FromCache {
		t.Fatal("FromCache must be false on fresh clone")
	}
	if res.LocalPath != loc.Dir {
		t.Fatalf("LocalPath = %q, want %q", res.LocalPath, loc.Dir)
	}
	if _, err := os.Stat(filepath.Join(loc.Dir, "HEAD")); err != nil {
		t.Fatalf("HEAD missing from materialized cache: %v", err)
	}
	// Audit file was written.
	if _, err := os.Stat(loc.AuditPath); err != nil {
		t.Fatalf("audit file missing: %v", err)
	}
}

func TestMaterialize_OfflinePinnedCachedSucceeds(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	// First online pass populates cache.
	if _, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		PinnedSHA:    src.SecondSHA,
		Ref:          src.HeadBranch,
	}); err != nil {
		t.Fatalf("online Materialize: %v", err)
	}

	// Now point at a canonical URL git cannot reach. Offline + cached
	// SHA must succeed without a network call.
	res, err := Materialize(testCtx(t), Input{
		CanonicalURL: "https://offline.invalid/nope.git",
		Cache:        loc,
		PinnedSHA:    src.SecondSHA,
		Ref:          src.HeadBranch,
		Offline:      true,
	})
	if err != nil {
		t.Fatalf("offline Materialize: %v", err)
	}
	if !res.FromCache {
		t.Fatal("FromCache must be true for offline-pinned-cached")
	}
	if res.ResolvedSHA != src.SecondSHA {
		t.Fatalf("ResolvedSHA = %q, want %q", res.ResolvedSHA, src.SecondSHA)
	}
}

func TestMaterialize_OfflineNoCache(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		PinnedSHA:    src.SecondSHA,
		Ref:          src.HeadBranch,
		Offline:      true,
	})
	if !errors.Is(err, ErrUnresolvablePinOffline) {
		t.Fatalf("expected ErrUnresolvablePinOffline, got %v", err)
	}
}

func TestMaterialize_FloatingOffline(t *testing.T) {
	withDetectReset(t)

	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, "https://example.invalid/r.git")

	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: "https://example.invalid/r.git",
		Cache:        loc,
		Ref:          "main",
		Floating:     true,
		Offline:      true,
	})
	if err == nil {
		t.Fatal("expected error for floating+offline")
	}
	if errors.Is(err, ErrUnresolvablePinOffline) {
		t.Fatalf("floating+offline should not surface as ErrUnresolvablePinOffline: %v", err)
	}
}

func TestMaterialize_FloatingAndPinnedMutuallyExclusive(t *testing.T) {
	withDetectReset(t)

	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, "https://example.invalid/r.git")

	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: "https://example.invalid/r.git",
		Cache:        loc,
		PinnedSHA:    "1234567890abcdef1234567890abcdef12345678",
		Floating:     true,
	})
	if err == nil {
		t.Fatal("expected error: Floating and PinnedSHA are mutually exclusive")
	}
}

func TestMaterialize_FloatingResolvesRef(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	res, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		Ref:          src.HeadBranch,
		Floating:     true,
	})
	if err != nil {
		t.Fatalf("Materialize floating: %v", err)
	}
	if res.ResolvedSHA != src.SecondSHA {
		t.Fatalf("ResolvedSHA = %q, want %q", res.ResolvedSHA, src.SecondSHA)
	}
}

func TestMaterialize_InvalidSHA(t *testing.T) {
	withDetectReset(t)

	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, "https://example.invalid/r.git")

	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: "https://example.invalid/r.git",
		Cache:        loc,
		PinnedSHA:    "not-a-sha",
	})
	if err == nil {
		t.Fatal("expected error for malformed SHA")
	}
}

func TestMaterialize_InlineCredentialRejected(t *testing.T) {
	withDetectReset(t)

	cacheRoot := t.TempDir()
	url := "https://u:p@example.com/r.git"
	loc := buildCacheLocation(t, cacheRoot, url)

	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: url,
		Cache:        loc,
		PinnedSHA:    "1234567890abcdef1234567890abcdef12345678",
	})
	if !errors.Is(err, ErrInlineCredential) {
		t.Fatalf("expected ErrInlineCredential, got %v", err)
	}
}

func TestMaterialize_EmptyRefWithNoPin(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	// No PinnedSHA and no Ref: defer-resolve without a ref is an
	// illegal combination; the caller must supply at least one.
	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
	})
	if err == nil {
		t.Fatal("expected error: empty ref with no pin")
	}
}

func TestMaterialize_ReachabilityCheck_ForcePushedRef(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	// First pass: cache the original SHA.
	if _, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		PinnedSHA:    src.SecondSHA,
		Ref:          src.HeadBranch,
	}); err != nil {
		t.Fatalf("initial Materialize: %v", err)
	}

	// Force-push the ref to a divergent history.
	divergent := src.forcePushDivergent(t)
	if divergent == src.SecondSHA {
		t.Fatal("force-push did not change branch tip")
	}

	// Second pass: with the old pin, the ref on the remote no longer
	// reaches our pin. Materialize must refuse.
	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		PinnedSHA:    src.SecondSHA,
		Ref:          src.HeadBranch,
	})
	if !errors.Is(err, ErrReachabilityCheckFailed) {
		t.Fatalf("expected ErrReachabilityCheckFailed after force-push, got %v", err)
	}
}

func TestMaterialize_ScratchCleanupOnCloneFailure(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	// Point at a non-existent source to force Clone to fail.
	src := filepath.Join(t.TempDir(), "does-not-exist")
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src)

	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: src,
		Cache:        loc,
		PinnedSHA:    "1234567890abcdef1234567890abcdef12345678",
		Ref:          "main",
	})
	if err == nil {
		t.Fatal("expected error for unreachable source")
	}

	// No scratch dir should remain next to Cache.Dir.
	entries, readErr := os.ReadDir(cacheRoot)
	if readErr != nil {
		t.Fatalf("read cache root: %v", readErr)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".staging.") {
			t.Fatalf("scratch dir leaked: %s", e.Name())
		}
	}
}

func TestMaterialize_SecondPassFromCache_NoFetchNeeded(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	// First pass online.
	if _, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		PinnedSHA:    src.SecondSHA,
		Ref:          src.HeadBranch,
	}); err != nil {
		t.Fatalf("initial Materialize: %v", err)
	}

	// Second pass online: the cache already has the SHA. Materialize
	// will re-fetch (correct, keeps mirror fresh) but must still return
	// successfully without error.
	res, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		PinnedSHA:    src.SecondSHA,
		Ref:          src.HeadBranch,
	})
	if err != nil {
		t.Fatalf("second Materialize: %v", err)
	}
	if res.ResolvedSHA != src.SecondSHA {
		t.Fatalf("ResolvedSHA = %q, want %q", res.ResolvedSHA, src.SecondSHA)
	}
}

func TestMaterialize_PinMismatchAfterFetch(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	// Pin a SHA that does not exist in the remote at all.
	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		PinnedSHA:    "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Ref:          src.HeadBranch,
	})
	if err == nil {
		t.Fatal("expected error for pin not present in remote")
	}
}

func TestMaterialize_TagPinnedFreshCache(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	// Tag-pinned manifest on a fresh cache: PinnedSHA is the commit the
	// annotated tag points at, Ref is the tag name. The reachability
	// check resolves Ref locally on the bare clone, so the clone must
	// have fetched tags. If `--no-tags` were still set on `Clone`, this
	// path would fail at the IsAncestor step because `v1` would not be
	// present in the local refs.
	res, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		PinnedSHA:    src.TagSHA,
		Ref:          src.TagName,
	})
	if err != nil {
		t.Fatalf("Materialize tag-pinned: %v", err)
	}
	if res.ResolvedSHA != src.TagSHA {
		t.Fatalf("ResolvedSHA = %q, want %q", res.ResolvedSHA, src.TagSHA)
	}
	if res.FromCache {
		t.Fatal("FromCache must be false on fresh clone")
	}
}

func TestMaterialize_TagFloatingFreshCache(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	cacheRoot := t.TempDir()
	loc := buildCacheLocation(t, cacheRoot, src.Path)

	// Floating manifest pointed at an annotated tag: Materialize must
	// resolve the tag via ls-remote (returning the dereferenced commit
	// SHA, not the tag object SHA) and confirm the commit landed in the
	// fresh cache. Both fixes are exercised here:
	//   - firstShaFromLsRemote must prefer the `^{}` peel,
	//   - Clone must materialize tags so the commit is fetched.
	res, err := Materialize(testCtx(t), Input{
		CanonicalURL: src.Path,
		Cache:        loc,
		Ref:          src.TagName,
		Floating:     true,
	})
	if err != nil {
		t.Fatalf("Materialize tag-floating: %v", err)
	}
	if res.ResolvedSHA != src.TagSHA {
		t.Fatalf("ResolvedSHA = %q, want commit SHA %q (tag-object SHA would indicate firstShaFromLsRemote regression)", res.ResolvedSHA, src.TagSHA)
	}
}

func TestMaterialize_NilCache(t *testing.T) {
	withDetectReset(t)

	_, err := Materialize(testCtx(t), Input{
		CanonicalURL: "https://example.com/r.git",
		Ref:          "main",
		Floating:     true,
	})
	if err == nil {
		t.Fatal("expected error for nil Cache")
	}
}

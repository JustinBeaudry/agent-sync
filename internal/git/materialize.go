package git

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-sync/agent-sync/internal/cache"
)

// ErrUnresolvablePinOffline is returned by Materialize when offline mode
// is requested but the local cache cannot satisfy the request. Callers
// should surface the remediation text in the error message verbatim;
// users hitting this typically need to either (a) remove --offline, or
// (b) re-run `sync` online at least once to populate the cache.
var ErrUnresolvablePinOffline = errors.New("git: cannot satisfy pinned canonical source while offline")

// ErrReachabilityCheckFailed is returned when the pinned SHA is not
// reachable from the configured ref after a fetch. This is the
// force-pushed-ref defense: a legitimate pin is always reachable from
// the branch/tag that produced it.
var ErrReachabilityCheckFailed = errors.New("git: pinned SHA is not reachable from configured ref")

// Input describes a single materialization request.
//
// Callers are expected to have already:
//   - canonicalized the URL via cache.Canonicalize,
//   - resolved the cache location via cache.Resolve,
//   - consulted the manifest to decide whether a pin is present, and
//   - chosen an offline/online mode for this invocation.
//
// Materialize does not read the manifest or call cache.Resolve itself:
// keeping those two concerns at the caller layer lets higher layers
// (unit 12, unit 13) orchestrate batch materialization with shared
// state.
type Input struct {
	// CanonicalURL is the normalized canonical-form URL. Local-path
	// canonical sources (R7) are not handled by this function; callers
	// dispatch to a different code path for them.
	CanonicalURL string

	// Cache is the cache location (cache.Resolve output) the result
	// should live at. Must be non-nil.
	Cache *cache.Location

	// PinnedSHA is the SHA from manifest.canonical.commit. Empty for
	// floating manifests and for pre-resolve `--defer-resolve` init.
	PinnedSHA string

	// Ref is the branch/tag from manifest.canonical.ref. Used (a) as the
	// ref to resolve for floating manifests and (b) as the "expected
	// reachability source" when verifying a pin. May be empty.
	Ref string

	// Floating is true when the manifest sets `floating: true`.
	// Floating materializations always re-resolve Ref online; the
	// offline-pinned-cached exception does not apply.
	Floating bool

	// ResolveOrFallback resolves Ref online against the remote, but on a
	// network-reachability failure falls back to PinnedSHA from the local
	// cache instead of erroring. It is the auto-advance materialize mode:
	// PinnedSHA remains the deterministic fallback, while the resolved ref is
	// the preferred target when the network is reachable.
	ResolveOrFallback bool

	// Offline, if true, forbids any network operation. Materialize
	// returns ErrUnresolvablePinOffline if it cannot satisfy the
	// request from cache alone.
	Offline bool
}

// Result describes a successful materialization.
//
// ResolvedSHA is the SHA that was actually materialized. For pinned
// manifests it equals Input.PinnedSHA; for floating manifests it is the
// SHA that Ref resolved to at network-resolve time.
//
// FromCache is true when no network operation was performed (the cache
// already held the commit).
type Result struct {
	ResolvedSHA string
	LocalPath   string
	FromCache   bool
	// FellBackToPinned is true only for ResolveOrFallback requests that could
	// not reach the remote and therefore returned the cached pin instead.
	FellBackToPinned bool
}

// Materialize ensures the cache at in.Cache.Dir holds the commit
// required by the input, and returns the path and SHA the caller
// should read from.
//
// See package doc for the high-level flow; the pinned-cached-succeeds
// offline rule (plan decision) is implemented here.
func Materialize(ctx context.Context, in Input) (*Result, error) {
	if in.Cache == nil {
		return nil, errors.New("git: materialize: cache location is nil")
	}
	if strings.TrimSpace(in.CanonicalURL) != in.CanonicalURL || in.CanonicalURL == "" {
		return nil, errors.New("git: materialize: canonical URL must be non-empty and pre-trimmed")
	}
	if in.PinnedSHA != "" && !shaPattern.MatchString(strings.ToLower(in.PinnedSHA)) {
		return nil, fmt.Errorf("git: materialize: PinnedSHA %q is not a 40-char hex SHA", in.PinnedSHA)
	}
	if in.Floating && in.PinnedSHA != "" {
		return nil, errors.New("git: materialize: Floating and PinnedSHA are mutually exclusive")
	}
	if in.ResolveOrFallback && in.Floating {
		return nil, errors.New("git: materialize: ResolveOrFallback and Floating are mutually exclusive")
	}
	if in.ResolveOrFallback && in.PinnedSHA == "" {
		return nil, errors.New("git: materialize: ResolveOrFallback requires a pinned SHA fallback")
	}
	if in.ResolveOrFallback && in.Offline {
		return nil, errors.New("git: materialize: ResolveOrFallback cannot run in offline mode")
	}
	if in.Floating && in.Offline {
		return nil, errors.New("git: materialize: floating manifests cannot satisfy offline mode")
	}
	// Defer-resolve + offline is also unsatisfiable: with no PinnedSHA and
	// no Floating flag, the only way to discover the SHA is to consult the
	// remote, which Offline forbids. Catch this at the input boundary so
	// we never fall through to clone/fetch/resolve and surface a transport
	// failure instead of a deterministic offline error. This is a plain
	// validation error, not ErrUnresolvablePinOffline — that sentinel is
	// reserved for "pin set but cache miss" (a runtime cache state), while
	// this is a request shape that can never satisfy the offline contract.
	if in.Offline && in.PinnedSHA == "" && !in.Floating {
		return nil, errors.New("git: materialize: Offline requires a pinned SHA (defer-resolve cannot run offline)")
	}
	if err := checkInlineCredential(in.CanonicalURL); err != nil {
		return nil, err
	}

	// Fast path 1: pinned + offline + cache has the SHA. No network.
	if in.PinnedSHA != "" && in.Offline {
		has, err := cacheHasSha(in.Cache.Dir, in.PinnedSHA)
		if err != nil {
			return nil, err
		}
		if has {
			return &Result{
				ResolvedSHA: strings.ToLower(in.PinnedSHA),
				LocalPath:   in.Cache.Dir,
				FromCache:   true,
			}, nil
		}
		return nil, fmt.Errorf("%w: SHA %s not present at %s (run `agent-sync sync` online once to populate)", ErrUnresolvablePinOffline, in.PinnedSHA, in.Cache.Dir)
	}

	// Online path: ensure a usable bare clone exists at Cache.Dir.
	exists, err := isBareClone(in.Cache.Dir)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := cloneViaScratch(ctx, in); err != nil {
			if in.ResolveOrFallback && errors.Is(err, ErrRemoteUnreachable) {
				return fallbackToPinned(in, err)
			}
			return nil, err
		}
	} else {
		// Fetch to pick up any refs that were updated since last sync.
		// For a pinned manifest that is already cached, this is a no-op
		// from the pin's perspective but keeps the local mirror current
		// for the reachability check that follows.
		if err := Fetch(ctx, in.Cache.Dir); err != nil {
			if in.ResolveOrFallback && errors.Is(err, ErrRemoteUnreachable) {
				return fallbackToPinned(in, err)
			}
			return nil, err
		}
	}

	// Determine the target SHA.
	targetSHA := strings.ToLower(in.PinnedSHA)
	if in.ResolveOrFallback {
		if strings.TrimSpace(in.Ref) == "" {
			return nil, errors.New("git: materialize: Ref is required when ResolveOrFallback is set")
		}
		resolved, err := ResolveRef(ctx, in.CanonicalURL, in.Ref)
		if err != nil {
			if errors.Is(err, ErrRemoteUnreachable) {
				return fallbackToPinned(in, err)
			}
			return nil, err
		}
		targetSHA = resolved
	} else if in.Floating || targetSHA == "" {
		// Resolve ref online. Floating callers always hit this branch;
		// pre-resolve `--defer-resolve` manifests with neither pin nor
		// floating flag also route here (the caller will then write the
		// resolved SHA back into the manifest).
		if strings.TrimSpace(in.Ref) == "" {
			return nil, errors.New("git: materialize: Ref is required when PinnedSHA is empty")
		}
		resolved, err := ResolveRef(ctx, in.CanonicalURL, in.Ref)
		if err != nil {
			return nil, err
		}
		targetSHA = resolved
	}

	// Verify the SHA landed in the cache.
	has, err := cacheHasSha(in.Cache.Dir, targetSHA)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("git: materialize: fetch completed but %s is still missing from %s", targetSHA, in.Cache.Dir)
	}

	// Reachability defense: if a ref is configured and the manifest
	// pinned a SHA, confirm that the pin is reachable from the ref on
	// the mirror. A force-push that rewrote the ref's history would
	// leave the pin orphaned; this check catches that case.
	if in.ResolveOrFallback {
		ok, err := IsAncestor(ctx, in.Cache.Dir, strings.ToLower(in.PinnedSHA), targetSHA)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: %s is not a descendant of pinned SHA %s on %s", ErrReachabilityCheckFailed, targetSHA, strings.ToLower(in.PinnedSHA), in.CanonicalURL)
		}
	} else if in.PinnedSHA != "" && strings.TrimSpace(in.Ref) != "" {
		descriptor, err := resolveReachabilityRef(ctx, in.Cache.Dir, in.Ref)
		if err != nil {
			return nil, err
		}
		ok, err := IsAncestor(ctx, in.Cache.Dir, targetSHA, descriptor)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: %s is not reachable from %q at %s", ErrReachabilityCheckFailed, targetSHA, in.Ref, in.CanonicalURL)
		}
	}

	// Write the audit file on every successful materialization. The
	// operation is idempotent (staged rename) so doing it unconditionally
	// keeps stale audit files from lingering if a cache dir is reused
	// with a different canonical URL via override path shenanigans.
	if err := in.Cache.WriteAudit(); err != nil {
		return nil, err
	}

	return &Result{
		ResolvedSHA:      targetSHA,
		LocalPath:        in.Cache.Dir,
		FromCache:        false,
		FellBackToPinned: false,
	}, nil
}

func fallbackToPinned(in Input, cause error) (*Result, error) {
	has, err := cacheHasSha(in.Cache.Dir, in.PinnedSHA)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("git: materialize: remote unreachable and pinned SHA %s is not cached at %s: %w", strings.ToLower(in.PinnedSHA), in.Cache.Dir, cause)
	}
	return &Result{
		ResolvedSHA:      strings.ToLower(in.PinnedSHA),
		LocalPath:        in.Cache.Dir,
		FromCache:        true,
		FellBackToPinned: true,
	}, nil
}

// cloneViaScratch performs a bare clone into a unique scratch sibling
// directory, then renames it into place. Rename-on-success means an
// interrupted clone leaves the scratch dir behind (cleaned up on the
// deferred RemoveAll) and never corrupts the cache dir.
//
// Concurrency: two processes may both scratch-clone and race to rename.
// The loser's rename fails because the target already exists; we detect
// that case and fall through to a no-op, letting the subsequent fetch
// (in Materialize's caller path) pick up anything the winner missed.
func cloneViaScratch(ctx context.Context, in Input) error {
	if err := os.MkdirAll(filepath.Dir(in.Cache.Dir), 0o750); err != nil {
		return fmt.Errorf("git: materialize: mkdir cache root %q: %w", filepath.Dir(in.Cache.Dir), err)
	}

	scratch, err := uniqueScratchPath(in.Cache.Dir)
	if err != nil {
		return err
	}
	// Defer cleanup; the successful-rename path nils `cleanup` out.
	cleanup := func() { _ = os.RemoveAll(scratch) }
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

	if err := Clone(ctx, in.CanonicalURL, scratch); err != nil {
		return err
	}

	if err := os.Rename(scratch, in.Cache.Dir); err != nil {
		// Concurrent-clone tolerance: if the cache dir already exists
		// and is a valid bare clone, drop the scratch and accept the
		// winner's result.
		if ok, existsErr := isBareClone(in.Cache.Dir); existsErr == nil && ok {
			return nil
		}
		return fmt.Errorf("git: materialize: rename %q -> %q: %w", scratch, in.Cache.Dir, err)
	}
	cleanup = nil
	return nil
}

// uniqueScratchPath returns a sibling path to cacheDir with a
// `<key>.staging.<hex>` suffix. Using a sibling guarantees that a
// same-filesystem rename is possible on POSIX. On Windows, the rename
// still works because scratch and target live under the same volume.
func uniqueScratchPath(cacheDir string) (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("git: materialize: rand: %w", err)
	}
	return cacheDir + ".staging." + hex.EncodeToString(buf[:]), nil
}

// isBareClone reports whether dir exists and looks like a bare git
// repository. We accept two shapes:
//   - a bare clone (HEAD file directly under dir, objects/ at dir)
//   - a non-bare clone (.git/ subdirectory containing HEAD + objects)
//
// agent-sync always creates bare clones; non-bare is accepted for
// forward-compat with a user-supplied cache dir that was populated by
// some other means.
func isBareClone(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("git: materialize: stat cache %q: %w", dir, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("git: materialize: cache path %q is not a directory", dir)
	}

	// Bare clone signature.
	if isFile(filepath.Join(dir, "HEAD")) && isDir(filepath.Join(dir, "objects")) {
		return true, nil
	}
	// Non-bare fallback.
	if isFile(filepath.Join(dir, ".git", "HEAD")) && isDir(filepath.Join(dir, ".git", "objects")) {
		return true, nil
	}
	return false, nil
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// cacheHasSha opens the bare clone at dir and checks whether the commit
// is present in its object store. It returns (false, nil) for a
// non-existent cache dir; callers should treat that as "cache miss" and
// decide whether to clone or fail.
func cacheHasSha(dir, sha string) (bool, error) {
	exists, err := isBareClone(dir)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	repo, err := Open(dir)
	if err != nil {
		return false, err
	}
	defer func() { _ = repo.Close() }()
	return repo.HasCommit(sha)
}

// resolveReachabilityRef turns a user-supplied ref (branch or tag) into
// the fully-qualified ref path the reachability check needs.
//
// Already-qualified refs (`refs/...`) pass through unchanged. Bare names
// are disambiguated against the local mirror in the order branch then
// tag: `refs/heads/<name>` wins if it exists, falling back to
// `refs/tags/<name>` if not. Both forms are probed explicitly with
// [HasRef] rather than letting `git merge-base` apply its DWIM rules,
// because for an ambiguous name git's resolver may pick the tag — at
// which point the reachability check evaluates the wrong ref and the
// force-push defense produces a false pass or false fail.
//
// If neither form exists locally the ref is returned unchanged so the
// downstream `merge-base` call surfaces a real "unknown revision" error
// instead of this helper inventing one.
func resolveReachabilityRef(ctx context.Context, repoPath, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "refs/") {
		return ref, nil
	}
	branchRef := "refs/heads/" + ref
	branchExists, err := HasRef(ctx, repoPath, branchRef)
	if err != nil {
		return "", err
	}
	if branchExists {
		return branchRef, nil
	}
	tagRef := "refs/tags/" + ref
	tagExists, err := HasRef(ctx, repoPath, tagRef)
	if err != nil {
		return "", err
	}
	if tagExists {
		return tagRef, nil
	}
	// Neither form exists. Fall through to the bare name and let
	// `merge-base` produce the canonical error.
	return ref, nil
}

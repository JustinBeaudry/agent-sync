package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/agent-sync/agent-sync/internal/cache"
	"github.com/agent-sync/agent-sync/internal/git"
	"github.com/agent-sync/agent-sync/internal/manifest"
)

// advanceResolution is what the shared advance flow discovers before any
// caller-specific gate or messaging runs.
type advanceResolution struct {
	newSHA      string
	sourceURL   string
	refName     string // the ref that was resolved (manifest ref, or HEAD fallback)
	mirrorPath  string // local mirror to inspect for ancestry / change summary
	cacheLoc    *cache.Location
	refFromHEAD bool // true when the manifest had no ref and HEAD was followed
	fastForward bool // true when old pin is an ancestor of newSHA
	// fellBackToPinned is true only on the sync auto-advance path for a URL
	// source whose remote was unreachable and whose cached pin was used.
	fellBackToPinned bool
}

// resolveAdvance resolves the newest SHA for the manifest source, inspects the
// local mirror for fast-forward safety against the current pin, and returns the
// shared advance shape used by update today and sync auto-advance later.
func resolveAdvance(ctx context.Context, m *manifest.Manifest) (advanceResolution, error) {
	res, err := resolveAdvanceTarget(ctx, m)
	if err != nil {
		return advanceResolution{}, err
	}

	res.fastForward = true
	oldPin := m.Canonical.Commit
	if oldPin != "" && res.mirrorPath != "" {
		res.fastForward, err = git.IsAncestor(ctx, res.mirrorPath, oldPin, res.newSHA)
		if err != nil {
			// An unreadable ancestry check is treated the same way update has
			// always treated it: rewritten history signal, not a hard failure.
			res.fastForward = false
		}
	}
	return res, nil
}

// resolveAutoAdvance resolves the newest candidate SHA for sync's auto-advance
// path and proves fast-forward safety against the manifest's trusted_sha. An
// empty trusted_sha is fail-safe: fastForward stays false.
func resolveAutoAdvance(ctx context.Context, m *manifest.Manifest) (advanceResolution, error) {
	res, err := resolveAutoAdvanceTarget(ctx, m)
	if err != nil {
		return advanceResolution{}, err
	}

	res.fastForward = false
	baseline := m.TrustedSHA
	if baseline == "" || res.mirrorPath == "" {
		return res, nil
	}
	res.fastForward, err = git.IsAncestor(ctx, res.mirrorPath, baseline, res.newSHA)
	if err != nil {
		// The auto path treats an unreadable ancestry check as a refusal, the
		// same fail-safe posture update already takes for rewritten history.
		res.fastForward = false
	}
	return res, nil
}

func resolveAdvanceTarget(ctx context.Context, m *manifest.Manifest) (advanceResolution, error) {
	if m.Canonical.LocalPath != "" {
		ref := m.Canonical.Ref
		refName := ref
		fromHEAD := false
		if ref == "" {
			ref = "HEAD"
			refName = "HEAD (local default)"
			fromHEAD = true
		}
		sha, err := git.ResolveLocalRef(ctx, m.Canonical.LocalPath, ref)
		if err != nil {
			return advanceResolution{}, fmt.Errorf("resolve local ref: %w", err)
		}
		return advanceResolution{
			newSHA:      sha,
			sourceURL:   m.Canonical.LocalPath,
			refName:     refName,
			mirrorPath:  m.Canonical.LocalPath,
			refFromHEAD: fromHEAD,
		}, nil
	}

	canonical, err := cache.Canonicalize(m.Canonical.URL)
	if err != nil {
		return advanceResolution{}, fmt.Errorf("canonicalize: %w", err)
	}
	loc, err := cache.Resolve(canonical, cache.ResolveOptions{Override: m.Cache.Override})
	if err != nil {
		return advanceResolution{}, fmt.Errorf("resolve cache: %w", err)
	}
	ref := m.Canonical.Ref
	refName := ref
	fromHEAD := false
	if ref == "" {
		ref = "HEAD"
		refName = "HEAD (remote default)"
		fromHEAD = true
	}
	mres, err := git.Materialize(ctx, git.Input{
		CanonicalURL: canonical,
		Cache:        loc,
		Ref:          ref,
		Floating:     true,
	})
	if err != nil {
		return advanceResolution{}, fmt.Errorf("resolve remote ref: %w", err)
	}
	return advanceResolution{
		newSHA:      mres.ResolvedSHA,
		sourceURL:   canonical,
		refName:     refName,
		mirrorPath:  mres.LocalPath,
		cacheLoc:    loc,
		refFromHEAD: fromHEAD,
	}, nil
}

func resolveAutoAdvanceTarget(ctx context.Context, m *manifest.Manifest) (advanceResolution, error) {
	if m.Canonical.LocalPath != "" {
		return resolveLocalAdvanceTarget(ctx, m, true)
	}
	return resolveRemoteAutoAdvanceTarget(ctx, m)
}

func resolveLocalAdvanceTarget(ctx context.Context, m *manifest.Manifest, localDefault bool) (advanceResolution, error) {
	ref := m.Canonical.Ref
	refName := ref
	fromHEAD := false
	if ref == "" {
		ref = "HEAD"
		if localDefault {
			refName = "HEAD (local default)"
		} else {
			refName = "HEAD"
		}
		fromHEAD = true
	}
	sha, err := git.ResolveLocalRef(ctx, m.Canonical.LocalPath, ref)
	if err != nil {
		return advanceResolution{}, fmt.Errorf("resolve local ref: %w", err)
	}
	return advanceResolution{
		newSHA:      sha,
		sourceURL:   m.Canonical.LocalPath,
		refName:     refName,
		mirrorPath:  m.Canonical.LocalPath,
		refFromHEAD: fromHEAD,
	}, nil
}

func resolveRemoteAutoAdvanceTarget(ctx context.Context, m *manifest.Manifest) (advanceResolution, error) {
	canonical, err := cache.Canonicalize(m.Canonical.URL)
	if err != nil {
		return advanceResolution{}, fmt.Errorf("canonicalize: %w", err)
	}
	loc, err := cache.Resolve(canonical, cache.ResolveOptions{Override: m.Cache.Override})
	if err != nil {
		return advanceResolution{}, fmt.Errorf("resolve cache: %w", err)
	}
	ref := m.Canonical.Ref
	refName := ref
	fromHEAD := false
	if ref == "" {
		ref = "HEAD"
		refName = "HEAD (remote default)"
		fromHEAD = true
	}

	mres, err := git.Materialize(ctx, git.Input{
		CanonicalURL: canonical,
		Cache:        loc,
		Ref:          ref,
		Floating:     true,
	})
	if err == nil {
		return advanceResolution{
			newSHA:      mres.ResolvedSHA,
			sourceURL:   canonical,
			refName:     refName,
			mirrorPath:  mres.LocalPath,
			cacheLoc:    loc,
			refFromHEAD: fromHEAD,
		}, nil
	}
	if !errors.Is(err, git.ErrRemoteUnreachable) {
		return advanceResolution{}, fmt.Errorf("resolve remote ref: %w", err)
	}

	fallback, ferr := git.Materialize(ctx, git.Input{
		CanonicalURL: canonical,
		Cache:        loc,
		PinnedSHA:    m.Canonical.Commit,
		Offline:      true,
	})
	if ferr != nil {
		return advanceResolution{}, fmt.Errorf("resolve remote ref: %w; cached pin fallback failed: %w", err, ferr)
	}
	return advanceResolution{
		newSHA:           fallback.ResolvedSHA,
		sourceURL:        canonical,
		refName:          refName,
		mirrorPath:       fallback.LocalPath,
		cacheLoc:         loc,
		refFromHEAD:      fromHEAD,
		fellBackToPinned: true,
	}, nil
}

// repinManifest rewrites canonical.commit and trusted_sha to newSHA using the
// manifest writer's existing comment-preserving update path.
func repinManifest(manifestPath, newSHA string) error {
	orig, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	updated, err := manifest.WriteResolvedSHA(orig, newSHA, newSHA)
	if err != nil {
		return fmt.Errorf("rewrite pin: %w", err)
	}
	if err := manifest.WriteFile(manifestPath, updated); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

package cli

import (
	"context"
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
	refName     string // the ref that was resolved (manifest ref, or HEAD fallback)
	mirrorPath  string // local mirror to inspect for ancestry / change summary
	refFromHEAD bool   // true when the manifest had no ref and HEAD was followed
	fastForward bool   // true when old pin is an ancestor of newSHA
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
		refName:     refName,
		mirrorPath:  mres.LocalPath,
		refFromHEAD: fromHEAD,
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

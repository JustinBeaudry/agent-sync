package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aienvs/aienvs/internal/cache"
	"github.com/aienvs/aienvs/internal/git"
	"github.com/aienvs/aienvs/internal/ir"
	"github.com/aienvs/aienvs/internal/manifest"
	"github.com/aienvs/aienvs/internal/trust"
)

// ErrFloatingLocalUnsupported is returned when a local_path canonical
// source is not pinned. Resolving a moving local HEAD is deferred; pin
// the manifest (invariant #4: pinning is default) to sync from a local
// path.
var ErrFloatingLocalUnsupported = errors.New("cli: floating local_path sync is not yet supported; pin the manifest with a commit")

// materialized is the decoded canonical source ready to hand to the engine.
type materialized struct {
	Nodes  []ir.Node
	Skills map[string]ir.Skill
	Commit string // the resolved SHA, stamped into the report + staging
	// Warnings are non-fatal decode signals (from ir.Decode and
	// ir.SkillsByID) — e.g. a missing AGENTS.md or an unreadable skill
	// asset. The caller surfaces them so they are not silently dropped.
	Warnings []ir.Warning
}

// materializeOptions controls network posture for materialization.
type materializeOptions struct {
	Offline bool
	Now     time.Time
}

// materialize turns a loaded manifest into decoded IR. It dispatches on
// canonical source kind: a local_path is opened directly (no network); a
// url goes through canonicalize -> cache resolve -> git materialize, with
// the trust gate enforced against the resolved SHA.
//
// Both paths require a pinned commit in v1 — the URL path because the
// offline/cache contract needs it, and the local path because resolving a
// moving HEAD is deferred.
func materialize(ctx context.Context, m *manifest.Manifest, opts materializeOptions) (materialized, error) {
	switch {
	case m.Canonical.LocalPath != "":
		return materializeLocal(m)
	case m.Canonical.URL != "":
		return materializeURL(ctx, m, opts)
	default:
		return materialized{}, errors.New("cli: manifest has neither url nor local_path")
	}
}

func materializeLocal(m *manifest.Manifest) (materialized, error) {
	if m.Canonical.Commit == "" {
		return materialized{}, ErrFloatingLocalUnsupported
	}
	repo, err := git.Open(m.Canonical.LocalPath)
	if err != nil {
		return materialized{}, fmt.Errorf("cli: open local canonical %q: %w", m.Canonical.LocalPath, err)
	}
	defer func() { _ = repo.Close() }()
	return decodeAt(repo, m.Canonical.Commit)
}

func materializeURL(ctx context.Context, m *manifest.Manifest, opts materializeOptions) (materialized, error) {
	canonical, err := cache.Canonicalize(m.Canonical.URL)
	if err != nil {
		return materialized{}, fmt.Errorf("cli: canonicalize %q: %w", m.Canonical.URL, err)
	}
	loc, err := cache.Resolve(canonical, cache.ResolveOptions{Override: m.Cache.Override})
	if err != nil {
		return materialized{}, fmt.Errorf("cli: resolve cache: %w", err)
	}
	res, err := git.Materialize(ctx, git.Input{
		CanonicalURL: canonical,
		Cache:        loc,
		PinnedSHA:    m.Canonical.Commit,
		Ref:          m.Canonical.Ref,
		Floating:     m.Canonical.Commit == "",
		Offline:      opts.Offline,
	})
	if err != nil {
		return materialized{}, fmt.Errorf("cli: materialize %q: %w", canonical, err)
	}

	// Trust gate. For a pinned manifest, ManifestTrustedSHA is
	// authoritative (trust.Decide check #2): a matching resolved SHA
	// proceeds, a mismatch fails closed. Full user-history (TOFU) handling
	// for unpinned sources is deferred to the init wizard (U6).
	dec, derr := trust.Decide(trust.DecideInput{
		URL:                canonical,
		ResolvedSHA:        res.ResolvedSHA,
		ManifestTrustedSHA: m.TrustedSHA,
		Now:                opts.Now,
	})
	if derr != nil {
		return materialized{}, &exitError{code: trust.ExitCodeFor(derr), err: fmt.Errorf("cli: trust: %w", derr)}
	}
	_ = dec // pinned-path decision carries no further action

	repo, err := git.Open(res.LocalPath)
	if err != nil {
		return materialized{}, fmt.Errorf("cli: open materialized repo: %w", err)
	}
	defer func() { _ = repo.Close() }()
	return decodeAt(repo, res.ResolvedSHA)
}

func decodeAt(repo *git.Repository, sha string) (materialized, error) {
	nodes, decodeWarnings, err := ir.Decode(repo, sha, ir.DecodeOptions{})
	if err != nil {
		return materialized{}, fmt.Errorf("cli: decode IR at %s: %w", sha, err)
	}
	skills, skillWarnings := ir.SkillsByID(nodes, repo, sha)
	warnings := append(append([]ir.Warning(nil), decodeWarnings...), skillWarnings...)
	return materialized{Nodes: nodes, Skills: skills, Commit: sha, Warnings: warnings}, nil
}

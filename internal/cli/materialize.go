package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agent-sync/agent-sync/internal/cache"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/git"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/trust"
	"github.com/agent-sync/agent-sync/internal/worktree"
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

// materializeOptions controls network posture and supplies the workspace root
// needed by the in-repo (local_dir) source kind.
type materializeOptions struct {
	Offline bool
	Now     time.Time
	// Root is the opened workspace root, required by the local_dir source kind
	// (it reads the working tree). nil for url/local_path sources.
	Root *fsroot.Root
}

// materialize turns a loaded manifest into decoded IR. It dispatches on
// canonical source kind:
//   - local_dir: read directly from the working tree (no git, no network, no
//     trust, no pin) — see materializeLocalDir.
//   - local_path: open a local git clone directly (no network).
//   - url: canonicalize -> cache resolve -> git materialize, with the trust
//     gate enforced against the resolved SHA.
//
// The git-backed kinds require a pinned commit in v1; the local_dir kind is
// unpinned by nature.
func materialize(ctx context.Context, m *manifest.Manifest, opts materializeOptions) (materialized, error) {
	switch {
	case m.Canonical.LocalDir != "":
		return materializeLocalDir(m, opts)
	case m.Canonical.LocalPath != "":
		return materializeLocal(m)
	case m.Canonical.URL != "":
		return materializeURL(ctx, m, opts)
	default:
		return materialized{}, errors.New("cli: manifest has no canonical source (url, local_path, or local_dir)")
	}
}

// materializeLocalDir reads an in-repo working-tree source. It deliberately
// skips git materialization, the trust (TOFU) gate, and the offline-strict
// SHA requirement: a working-tree source touches no network and has no commit
// to pin or trust. The empty ref flows to the engine as a zero-SHA placeholder.
func materializeLocalDir(m *manifest.Manifest, opts materializeOptions) (materialized, error) {
	if opts.Root == nil {
		return materialized{}, errors.New("cli: local_dir source requires an open workspace root")
	}
	reader, err := worktree.NewReader(opts.Root, m.Canonical.LocalDir)
	if err != nil {
		return materialized{}, fmt.Errorf("cli: local_dir source: %w", err)
	}
	return decodeAt(reader, "")
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

// decodeAt decodes any SourceTree at ref. ref is a 40-hex commit SHA for
// git-backed sources and the empty string for the working-tree source (which
// has no commit); an empty ref flows through to the engine's zero-SHA
// placeholder.
func decodeAt(src ir.SourceTree, ref string) (materialized, error) {
	at := ref
	if at == "" {
		at = "working tree"
	}
	nodes, decodeWarnings, err := ir.Decode(src, ref, ir.DecodeOptions{})
	if err != nil {
		return materialized{}, fmt.Errorf("cli: decode IR at %s: %w", at, err)
	}
	skills, skillWarnings := ir.SkillsByID(nodes, src, ref)
	warnings := append(append([]ir.Warning(nil), decodeWarnings...), skillWarnings...)
	return materialized{Nodes: nodes, Skills: skills, Commit: ref, Warnings: warnings}, nil
}

package git

import (
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ErrBlobNotFound is returned when a requested path does not resolve to
// a blob at the given SHA. A present-but-non-blob entry (tree,
// submodule, symlink with no target blob) produces the same sentinel:
// callers treat the path as missing.
var ErrBlobNotFound = errors.New("git: blob not found")

// ErrShaNotInCache is returned when a commit SHA is not present in the
// local cache. Materialize is responsible for populating the cache;
// this sentinel surfaces to help tests and higher layers distinguish
// "cache miss" from a genuine read error.
var ErrShaNotInCache = errors.New("git: sha not in cache")

// TreeEntry names a single blob reachable from a commit tree.
//
// Path is the POSIX-style forward-slash path from the tree root.
// Mode mirrors the git file mode (`100644`, `100755`, `120000` for a
// symlink). Size is the blob's uncompressed size.
//
// Symlinks are returned with Mode == 0o120000 and the blob content is
// the link target. Callers upstream decide whether to follow them;
// agent-sync's IR decoder treats symlinks as opaque blobs.
type TreeEntry struct {
	Path string
	Mode uint32
	Size int64
	Hash string
}

// Repository wraps a materialized bare clone so the rest of agent-sync can
// read trees and blobs without importing go-git directly.
//
// Instances are cheap to construct but hold an open object store, so
// callers should Close them when done. Repository methods are safe for
// concurrent read-only use.
type Repository struct {
	path string
	repo *gogit.Repository
}

// Open attaches to the bare clone at repoPath. The directory must already
// have been populated by Materialize; Open does not clone.
func Open(repoPath string) (*Repository, error) {
	r, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("git: open %q: %w", repoPath, err)
	}
	return &Repository{path: repoPath, repo: r}, nil
}

// Close releases the underlying object store handles. Safe to call
// multiple times.
func (r *Repository) Close() error {
	// go-git 5.17 PlainOpen returns an in-memory wrapper with no explicit
	// close; nothing to release today. The method exists so a future
	// go-git version that grows a Close() can be wired in without a
	// caller-visible break.
	return nil
}

// Path returns the filesystem path the repository was opened at.
func (r *Repository) Path() string {
	return r.path
}

// HasCommit reports whether the given 40-char SHA resolves to a commit
// object in the local store. It returns [ErrShaNotInCache] as a clean
// sentinel for callers that need to branch on cache-miss.
func (r *Repository) HasCommit(sha string) (bool, error) {
	if !shaPattern.MatchString(strings.ToLower(sha)) {
		return false, fmt.Errorf("git: has-commit: invalid sha %q", sha)
	}
	hash := plumbing.NewHash(sha)
	_, err := r.repo.CommitObject(hash)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		return false, nil
	}
	return false, fmt.Errorf("git: has-commit %s: %w", sha, err)
}

// ReadTree walks the tree reachable from commitSHA and returns every
// blob entry under it, keyed by POSIX path. Sub-trees are expanded
// recursively; submodule gitlinks are skipped (they are not part of the
// canonical source tree from agent-sync's perspective).
//
// Deterministic order: entries are sorted by Path so callers consuming
// the slice for diffing or hashing get stable results across runs.
func (r *Repository) ReadTree(commitSHA string) ([]TreeEntry, error) {
	sha := strings.ToLower(strings.TrimSpace(commitSHA))
	if !shaPattern.MatchString(sha) {
		return nil, fmt.Errorf("git: read-tree: invalid sha %q", commitSHA)
	}
	commit, err := r.repo.CommitObject(plumbing.NewHash(sha))
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrShaNotInCache, sha)
		}
		return nil, fmt.Errorf("git: read-tree: commit %s: %w", sha, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("git: read-tree: tree %s: %w", sha, err)
	}

	entries := make([]TreeEntry, 0, 64)
	walker := object.NewTreeWalker(tree, true, nil)
	defer walker.Close()
	for {
		name, entry, err := walker.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("git: read-tree: walk: %w", err)
		}
		// Skip directories (we only report blobs) and submodule gitlinks.
		if !entry.Mode.IsFile() && entry.Mode != 0o120000 {
			continue
		}
		blob, err := r.repo.BlobObject(entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("git: read-tree: blob %s at %s: %w", entry.Hash, name, err)
		}
		entries = append(entries, TreeEntry{
			Path: path.Clean(name),
			Mode: uint32(entry.Mode),
			Size: blob.Size,
			Hash: entry.Hash.String(),
		})
	}
	sortEntries(entries)
	return entries, nil
}

// BlobContent returns the bytes of the blob at relPath in the tree of
// commitSHA. Missing paths, directories, and submodule gitlinks all
// surface as [ErrBlobNotFound].
//
// The caller is responsible for any size limits; BlobContent loads the
// full blob into memory. The IR decoder in unit 7 will wrap this with
// per-concept caps.
func (r *Repository) BlobContent(commitSHA, relPath string) ([]byte, error) {
	sha := strings.ToLower(strings.TrimSpace(commitSHA))
	if !shaPattern.MatchString(sha) {
		return nil, fmt.Errorf("git: blob: invalid sha %q", commitSHA)
	}
	clean := path.Clean("/" + relPath)
	if clean == "/" {
		return nil, fmt.Errorf("%w: empty path", ErrBlobNotFound)
	}
	clean = strings.TrimPrefix(clean, "/")

	commit, err := r.repo.CommitObject(plumbing.NewHash(sha))
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrShaNotInCache, sha)
		}
		return nil, fmt.Errorf("git: blob: commit %s: %w", sha, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("git: blob: tree %s: %w", sha, err)
	}
	file, err := tree.File(clean)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, fmt.Errorf("%w: %s at %s", ErrBlobNotFound, clean, sha)
		}
		return nil, fmt.Errorf("git: blob: file %q at %s: %w", clean, sha, err)
	}
	rc, err := file.Reader()
	if err != nil {
		return nil, fmt.Errorf("git: blob: reader %q at %s: %w", clean, sha, err)
	}
	defer func() { _ = rc.Close() }()
	buf, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("git: blob: read %q at %s: %w", clean, sha, err)
	}
	return buf, nil
}

// sortEntries sorts TreeEntries by Path in ascending lexicographic order.
// The ordering is part of ReadTree's public contract.
func sortEntries(entries []TreeEntry) {
	slices.SortFunc(entries, func(a, b TreeEntry) int {
		return strings.Compare(a.Path, b.Path)
	})
}

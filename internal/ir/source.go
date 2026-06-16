package ir

import "github.com/agent-sync/agent-sync/internal/git"

// SourceTree is the read surface the decoder needs from a canonical source.
//
// It is deliberately narrow: the decoder only reads a flat tree listing and
// individual blob contents. Defined at the consumer (the IR decoder) rather
// than the producer, per the repo's interface-at-consumer convention, so a
// second implementation can feed the same decode logic.
//
// Two implementations exist:
//   - *git.Repository — ref is the 40-hex commit SHA.
//   - internal/worktree.Reader — reads the working tree directly; ref is
//     opaque (ignored), and tree paths are presented relative to the source
//     root so the decoder's layout matching is unchanged.
type SourceTree interface {
	// ReadTree returns the flat list of tree entries visible at ref.
	ReadTree(ref string) ([]git.TreeEntry, error)
	// BlobContent returns the bytes of the file at path under ref.
	BlobContent(ref, path string) ([]byte, error)
}

// Compile-time assertion that the git repository satisfies the read surface.
// Kept here (the consumer) rather than in internal/git to avoid an import
// cycle: internal/ir already depends on internal/git for git.TreeEntry.
var _ SourceTree = (*git.Repository)(nil)

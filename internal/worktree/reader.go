// Package worktree provides a filesystem-backed implementation of the IR
// decoder's source surface (ir.SourceTree), reading a canonical-layout source
// directly from a workspace's working tree instead of a git object store.
//
// It is the read side of the in-repo / local source kind (canonical.local_dir):
// a workspace can author skills, rules, commands, AGENTS.md, mcp, and plugins
// under a workspace-relative directory (e.g. ".agents") and have agent-sync
// compile them with no git, no clone, and no commit pin.
package worktree

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/git"
	"github.com/agent-sync/agent-sync/internal/ir"
)

// Reader satisfies the IR decoder's source surface.
var _ ir.SourceTree = (*Reader)(nil)

// ownedSkillPrefix is the leaf-name prefix agent-sync uses for the skills it
// emits into the shared ".agents/skills" tree (see the Codex adapter). The
// reader skips "skills/agent-sync-*" so the source is never read from its own
// emitted output — this is what keeps a ".agents"-rooted source and an
// ".agents/skills" target stable across repeated syncs. The prefix is
// therefore reserved for outputs and unavailable for authored skill ids.
const ownedSkillPrefix = "agent-sync-"

// maxBlobSize caps a single source file read (4 MiB) as defense-in-depth
// against a pathologically large file in the source tree.
const maxBlobSize = 4 << 20

// ErrSourceMissing is returned when the configured local_dir does not exist.
// Callers map this to a clear "source directory not found" message rather than
// a git-shaped error.
var ErrSourceMissing = errors.New("worktree: source directory does not exist")

// Reader reads a canonical-layout source from a working-tree directory.
// It satisfies ir.SourceTree. The ref argument to ReadTree/BlobContent is
// ignored: a working tree has no commit to address.
type Reader struct {
	root     *fsroot.Root
	localDir string // cleaned, workspace-relative (e.g. ".agents")
}

// NewReader builds a Reader rooted at localDir within the workspace root.
// localDir must be a safe, non-root, workspace-relative path; root is borrowed,
// not owned (the caller closes it).
func NewReader(root *fsroot.Root, localDir string) (*Reader, error) {
	if root == nil {
		return nil, errors.New("worktree: nil workspace root")
	}
	if err := fsroot.ValidateRelPath(localDir); err != nil {
		return nil, fmt.Errorf("worktree: local_dir: %w", err)
	}
	cleaned := path.Clean(localDir)
	if cleaned == "." {
		return nil, errors.New("worktree: local_dir must be a workspace subdirectory, not the root")
	}
	return &Reader{root: root, localDir: cleaned}, nil
}

// ReadTree returns the source tree as a flat list of file entries, with paths
// relative to local_dir so the decoder sees the canonical layout at its root
// (e.g. "skills/foo/SKILL.md"). Directories, symlinks, and other irregular
// files are not emitted, and the owned-output subtree (skills/agent-sync-*) is
// skipped. The ref argument is ignored.
func (r *Reader) ReadTree(_ string) ([]git.TreeEntry, error) {
	info, err := r.root.Stat(r.localDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %q", ErrSourceMissing, r.localDir)
		}
		return nil, fmt.Errorf("worktree: stat %q: %w", r.localDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("worktree: local_dir %q is not a directory", r.localDir)
	}

	var entries []git.TreeEntry
	if err := r.walk(r.localDir, &entries); err != nil {
		return nil, err
	}
	// Deterministic order — ReadDir order is unspecified, and the decoder's
	// duplicate-id detection is first-seen sensitive.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// walk recurses through dir (workspace-relative), appending one TreeEntry per
// regular file with its path made relative to local_dir.
func (r *Reader) walk(dir string, out *[]git.TreeEntry) error {
	f, err := r.root.Inner().Open(dir)
	if err != nil {
		return fmt.Errorf("worktree: open %q: %w", dir, err)
	}
	dirents, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("worktree: read dir %q: %w", dir, err)
	}

	for _, de := range dirents {
		child := dir + "/" + de.Name()
		if de.IsDir() {
			if r.isOwnedSkillDir(child) {
				continue // emitted output, not source
			}
			if err := r.walk(child, out); err != nil {
				return err
			}
			continue
		}
		info, err := de.Info()
		if err != nil {
			return fmt.Errorf("worktree: stat %q: %w", child, err)
		}
		mode := info.Mode()
		// Never follow or emit symlinks; skip irregular files (devices,
		// sockets, pipes). os.Root already refuses to traverse escaping
		// symlinks; this is defense-in-depth on the listing side.
		if mode&fs.ModeSymlink != 0 || !mode.IsRegular() {
			continue
		}
		rel := strings.TrimPrefix(child, r.localDir+"/")
		*out = append(*out, git.TreeEntry{
			Path: rel,
			Mode: 0o100644,
			Size: info.Size(),
		})
	}
	return nil
}

// isOwnedSkillDir reports whether a workspace-relative directory is one of
// agent-sync's emitted skill outputs in the shared skills tree
// (local_dir/skills/agent-sync-*).
func (r *Reader) isOwnedSkillDir(childWorkspaceRel string) bool {
	rel := strings.TrimPrefix(childWorkspaceRel, r.localDir+"/")
	return strings.HasPrefix(rel, "skills/"+ownedSkillPrefix)
}

// BlobContent returns the bytes of the file at relPath (relative to local_dir,
// as emitted by ReadTree). The ref argument is ignored.
func (r *Reader) BlobContent(_ string, relPath string) ([]byte, error) {
	full := r.localDir + "/" + relPath
	f, err := r.root.Inner().Open(full)
	if err != nil {
		return nil, fmt.Errorf("worktree: open blob %q: %w", full, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxBlobSize+1))
	if err != nil {
		return nil, fmt.Errorf("worktree: read blob %q: %w", full, err)
	}
	if len(data) > maxBlobSize {
		return nil, fmt.Errorf("worktree: blob %q exceeds %d bytes", full, maxBlobSize)
	}
	return data, nil
}

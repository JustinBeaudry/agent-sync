package merge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/aienvs/aienvs/internal/fsroot"
	"github.com/aienvs/aienvs/internal/locks"
	"github.com/aienvs/aienvs/pkg/adapterkit"
)

// ApplyToFile is the one filesystem-touching entry point: it holds the
// Unit 12 per-external-file flock across the whole read-merge-write,
// dispatches to the pure engine by locator kind, and writes the result
// atomically via fsroot (temp+fsync+rename). On any engine error it
// returns fail-closed — the on-disk file is left byte-identical.
//
// relPath is workspace-relative (forward-slash). holder is the adapter
// name, used in the per-file lock's timeout diagnostics.
func ApplyToFile(ctx context.Context, root *fsroot.Root, reg *locks.FileLockRegistry, relPath string, e MergeEntry, holder string) (sliceHash, warning string, err error) {
	abs := filepath.Join(root.Path(), filepath.FromSlash(relPath))
	release, err := reg.Acquire(ctx, abs, holder, locks.FileLockOpts{})
	if err != nil {
		return "", "", err
	}
	defer func() { _ = release() }()

	existing, err := readExisting(root, relPath)
	if err != nil {
		return "", "", err
	}

	var merged []byte
	switch e.Kind {
	case adapterkit.ToolOwnedKindJSONPointer:
		merged, sliceHash, err = mergeJSON(existing, e)
	case adapterkit.ToolOwnedKindTOMLPath:
		merged, sliceHash, err = mergeTOML(existing, e)
	case adapterkit.ToolOwnedKindMarkdownSection:
		merged, sliceHash, warning, err = mergeMarkdown(existing, e)
	default:
		return "", "", fmt.Errorf("merge: unknown locator kind %q", e.Kind)
	}
	if err != nil {
		return "", "", err // fail-closed: nothing written
	}

	// StagedWrite does not create parents; a nested target (.cursor/,
	// .codex/) on first sync needs the dir created first.
	if dir := slashDir(relPath); dir != "" {
		if mkErr := root.Inner().MkdirAll(dir, 0o755); mkErr != nil {
			return "", "", fmt.Errorf("merge: mkdir %s: %w", dir, mkErr)
		}
	}
	if werr := root.StagedWrite(relPath, merged, 0o644); werr != nil {
		return "", "", fmt.Errorf("merge: write %s: %w", relPath, werr)
	}
	return sliceHash, warning, nil
}

func readExisting(root *fsroot.Root, relPath string) ([]byte, error) {
	f, err := root.Inner().Open(relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("merge: read %s: %w", relPath, err)
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// slashDir returns the parent directory of a forward-slash relative
// path, or "" when the path is at the workspace root.
func slashDir(relPath string) string {
	i := strings.LastIndex(relPath, "/")
	if i <= 0 {
		return ""
	}
	return relPath[:i]
}

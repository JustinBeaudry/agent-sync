package sync

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

// walkFiles returns the workspace-relative paths of every regular file
// under dirRel (recursively), sorted lexically (fs.WalkDir order). An
// absent dir yields an empty list, not an error.
func walkFiles(root *fsroot.Root, dirRel string) ([]string, error) {
	var files []string
	err := fs.WalkDir(root.Inner().FS(), dirRel, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("sync: walk %s: %w", dirRel, err)
	}
	return files, nil
}

// readFile reads a workspace-relative file through the root.
func readFile(root *fsroot.Root, relPath string) ([]byte, error) {
	f, err := root.Inner().Open(relPath)
	if err != nil {
		return nil, fmt.Errorf("sync: open %s: %w", relPath, err)
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

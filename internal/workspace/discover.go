package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Find resolves a workspace from cwd (or the process cwd if cwd == "")
// per the rules documented on Options.
//
// Discovery order:
//  1. If opts.Workspace is set, validate it and short-circuit.
//  2. Otherwise, walk up the logical parent chain from cwd.
//  3. Stop at opts.StopAt (if set), filesystem root, or when a
//     manifest is found.
//  4. Abort with ErrMaxWalkExceeded if the hop budget is exhausted.
func Find(cwd string, opts Options) (*Workspace, error) {
	if cwd == "" {
		c, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("workspace: getwd: %w", err)
		}
		cwd = c
	}
	logical := cwd

	if opts.Workspace != "" {
		return resolveExplicit(opts.Workspace, logical)
	}

	cwdAbs, err := absLogical(cwd)
	if err != nil {
		return nil, fmt.Errorf("workspace: cwd %q: %w", cwd, err)
	}
	stopAbs := ""
	if opts.StopAt != "" {
		s, err := absLogical(opts.StopAt)
		if err != nil {
			return nil, fmt.Errorf("workspace: stop-at %q: %w", opts.StopAt, err)
		}
		stopAbs = s
	}

	if stopAbs != "" {
		rel, relErr := filepath.Rel(stopAbs, cwdAbs)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return nil, fmt.Errorf("%w: StopAt %q is not an ancestor of cwd %q", ErrInvalidOptions, stopAbs, cwdAbs)
		}
	}

	budget := opts.MaxHops
	if budget <= 0 {
		budget = DefaultMaxHops
	}

	dir := cwdAbs
	for hops := 0; hops < budget; hops++ {
		manifestPath := filepath.Join(dir, ManifestName)
		ws, found, err := validateManifestFile(manifestPath)
		if err != nil {
			return nil, err
		}
		if found {
			ws.Root = dir
			ws.LogicalCwd = logical
			return ws, nil
		}

		// Terminus: user-supplied stop.
		if stopAbs != "" && dir == stopAbs {
			return nil, fmt.Errorf("%w: searched from %q up to stop-at %q", ErrNotFound, logical, stopAbs)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Filesystem root reached without finding manifest.
			return nil, fmt.Errorf("%w: searched from %q up to filesystem root", ErrNotFound, logical)
		}
		dir = parent
	}

	return nil, fmt.Errorf("%w: cwd=%q stop-at=%q budget=%d", ErrMaxWalkExceeded, logical, stopAbs, budget)
}

func resolveExplicit(path, logical string) (*Workspace, error) {
	abs, err := absLogical(path)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve %q: %w", ErrInvalidOptions, path, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("%w: stat %q: %w", ErrInvalidOptions, path, err)
	}

	if st.IsDir() {
		manifest := filepath.Join(abs, ManifestName)
		ws, found, err := validateManifestFile(manifest)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidOptions, err)
		}
		if !found {
			return nil, fmt.Errorf("%w: %s not present under %q", ErrInvalidOptions, ManifestName, abs)
		}
		ws.Root = abs
		ws.LogicalCwd = logical
		return ws, nil
	}

	// Treat as a direct manifest path.
	if filepath.Base(abs) != ManifestName {
		return nil, fmt.Errorf("%w: %q is not a directory nor a %s file", ErrInvalidOptions, path, ManifestName)
	}
	ws, found, err := validateManifestFile(abs)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOptions, err)
	}
	if !found {
		return nil, fmt.Errorf("%w: manifest %q does not exist", ErrInvalidOptions, abs)
	}
	ws.Root = filepath.Dir(abs)
	ws.LogicalCwd = logical
	return ws, nil
}

// absLogical returns filepath.Abs(p) without resolving symlinks. The
// path is cleaned so filepath.Dir walks behave predictably.
func absLogical(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return filepath.Clean(filepath.Join(wd, p)), nil
}

// validateManifestFile checks whether path is a valid (regular) manifest.
// It uses Lstat first so that a dangling symlink is detected and reported
// as ErrManifestNotRegular rather than silently treated as "not found."
//
// Return values:
//   - (*Workspace, true, nil)  — path is a regular file (or a symlink to one);
//     ManifestPath is set, Root and LogicalCwd are left empty for the caller to fill.
//   - (nil, false, nil)        — path does not exist at all; caller continues walk.
//   - (nil, false, err)        — dangling symlink, non-regular file, or I/O error;
//     caller must propagate the error.
func validateManifestFile(path string) (*Workspace, bool, error) {
	linfo, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Truly no entry — continue the walk.
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("workspace: lstat %q: %w", path, err)
	}

	if linfo.Mode()&os.ModeSymlink != 0 {
		// Symlink present — follow it to check the target.
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			// Dangling symlink: the entry exists but the target does not.
			return nil, false, fmt.Errorf("%w: %q is a dangling symlink", ErrManifestNotRegular, path)
		}
		if err != nil {
			return nil, false, fmt.Errorf("workspace: stat %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, false, fmt.Errorf("%w: %q is a symlink to %s", ErrManifestNotRegular, path, modeLabel(info.Mode()))
		}
		// Symlink to a regular file is accepted.
		return &Workspace{ManifestPath: path}, true, nil
	}

	if !linfo.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%w: %q is %s", ErrManifestNotRegular, path, modeLabel(linfo.Mode()))
	}
	return &Workspace{ManifestPath: path}, true, nil
}

// modeLabel returns a human-readable description of a non-regular file mode
// for use in ErrManifestNotRegular messages.
func modeLabel(mode os.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&os.ModeSymlink != 0:
		return "a symlink"
	case mode&os.ModeDevice != 0:
		return "a device"
	case mode&os.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&os.ModeSocket != 0:
		return "a socket"
	default:
		return "a non-regular file"
	}
}

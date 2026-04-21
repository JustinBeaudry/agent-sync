package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	budget := opts.MaxHops
	if budget <= 0 {
		budget = DefaultMaxHops
	}

	dir := cwdAbs
	for hops := 0; hops <= budget; hops++ {
		manifest := filepath.Join(dir, ManifestName)
		ok, err := regularFileExists(manifest)
		if err != nil {
			return nil, fmt.Errorf("workspace: stat %q: %w", manifest, err)
		}
		if ok {
			return &Workspace{
				ManifestPath: manifest,
				Root:         dir,
				LogicalCwd:   logical,
			}, nil
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
		ok, err := regularFileExists(manifest)
		if err != nil {
			return nil, fmt.Errorf("%w: stat %q: %w", ErrInvalidOptions, manifest, err)
		}
		if !ok {
			return nil, fmt.Errorf("%w: %s not present under %q", ErrInvalidOptions, ManifestName, abs)
		}
		return &Workspace{ManifestPath: manifest, Root: abs, LogicalCwd: logical}, nil
	}

	// Treat as a direct manifest path.
	if filepath.Base(abs) != ManifestName {
		return nil, fmt.Errorf("%w: %q is not a directory nor a %s file", ErrInvalidOptions, path, ManifestName)
	}
	root := filepath.Dir(abs)
	return &Workspace{ManifestPath: abs, Root: root, LogicalCwd: logical}, nil
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
		return "", err
	}
	return filepath.Clean(filepath.Join(wd, p)), nil
}

// regularFileExists returns true iff path resolves to a regular file.
// Symlinks to regular files are followed (os.Stat) — this is expected
// for a user who has symlinked .aienv.yaml into place. Safety during
// writes is enforced by fsroot at a lower layer, not here.
func regularFileExists(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return fi.Mode().IsRegular(), nil
}

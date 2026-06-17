package hierarchy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/agent-sync/agent-sync/internal/workspace"
)

// manifestAt reports the manifest path in dir, if a regular .agent-sync.yaml
// file exists there. A directory or other non-regular entry with the
// manifest name does not count.
func manifestAt(dir string) (string, bool) {
	path := filepath.Join(dir, workspace.ManifestName)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

// hasGit reports whether dir contains a .git entry (directory for a normal
// clone, file for a worktree/submodule). Existence is all we need.
func hasGit(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// findProjectRoot returns the nearest ancestor of cwd (inclusive) that
// contains a .git entry, walking up the logical parent chain. The search
// stops at home: home is the user level and is never a project root, so a
// repo whose .git sits at home yields ok=false. ok is also false when the
// filesystem root or the hop budget is reached without a match.
func findProjectRoot(cwd, home string, maxHops int) (string, bool) {
	dir := cwd
	for hops := 0; hops < maxHops; hops++ {
		if dir == home {
			return "", false
		}
		if hasGit(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
	return "", false
}

// collectEmitScopes walks from cwd up to projectRoot (inclusive) and
// returns a Scope for each directory that holds a manifest. The manifest at
// projectRoot is LevelProject; every other one is LevelDirectory. Results
// are ordered shallow→deep (projectRoot first, cwd last), matching
// ascending precedence. All emit scopes have Emit=true.
//
// projectRoot must be cwd or an ancestor of it; the walk is bounded by
// reaching projectRoot or the filesystem root.
func collectEmitScopes(cwd, projectRoot string) ([]Scope, error) {
	var found []Scope
	dir := cwd
	for {
		if path, ok := manifestAt(dir); ok {
			level := LevelDirectory
			if dir == projectRoot {
				level = LevelProject
			}
			found = append(found, Scope{
				Root:         dir,
				ManifestPath: path,
				Level:        level,
				Emit:         true,
			})
		}
		if dir == projectRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// found is deep→shallow; reverse to shallow→deep.
	for i, j := 0, len(found)-1; i < j; i, j = i+1, j-1 {
		found[i], found[j] = found[j], found[i]
	}
	return found, nil
}

// userScope returns the user-home scope when home holds a manifest. Emit is
// set only when includeUser is true (the --user flag); otherwise the scope
// is returned read-only so callers can still display it in the precedence
// view without writing to the home directory.
func userScope(home string, includeUser bool) (Scope, bool) {
	path, ok := manifestAt(home)
	if !ok {
		return Scope{}, false
	}
	return Scope{
		Root:         home,
		ManifestPath: path,
		Level:        LevelUser,
		Emit:         includeUser,
	}, true
}

// Discover returns every manifest that applies at cwd, ordered from
// broadest (lowest precedence) to most specific (highest precedence):
// the user-home scope first (if present), then the project scope, then any
// directory scopes down to cwd.
//
// Emit scopes are the manifests from cwd up to the project root (the nearest
// .git ancestor). When there is no .git ancestor, only cwd's own manifest is
// an emit scope, classified as project. The user-home scope is included for
// visibility but is Emit=true only when Options.IncludeUser is set.
func Discover(cwd string, opts Options) ([]Scope, error) {
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("hierarchy: resolve cwd %q: %w", cwd, err)
	}
	absCwd = filepath.Clean(absCwd)

	home := opts.Home
	if home == "" {
		h, herr := os.UserHomeDir()
		if herr != nil {
			return nil, fmt.Errorf("hierarchy: resolve home: %w", herr)
		}
		home = h
	}
	home = filepath.Clean(home)

	maxHops := opts.MaxHops
	if maxHops <= 0 {
		maxHops = workspace.DefaultMaxHops
	}

	var emit []Scope
	if root, ok := findProjectRoot(absCwd, home, maxHops); ok {
		emit, err = collectEmitScopes(absCwd, root)
		if err != nil {
			return nil, err
		}
	} else if absCwd != home {
		// No .git ancestor: only cwd's own manifest applies, classified as
		// project. Skipped when cwd is home, where the manifest is the user
		// scope (added below) and must not be double-counted.
		if path, has := manifestAt(absCwd); has {
			emit = []Scope{{Root: absCwd, ManifestPath: path, Level: LevelProject, Emit: true}}
		}
	}

	var out []Scope
	if us, has := userScope(home, opts.IncludeUser); has {
		out = append(out, us)
	}
	out = append(out, emit...)
	return out, nil
}

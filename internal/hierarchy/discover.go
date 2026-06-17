package hierarchy

import (
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

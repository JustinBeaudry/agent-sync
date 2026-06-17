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

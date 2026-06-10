package hooks

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Uninstall removes the managed hook wrappers from repoRoot. Only files
// carrying the agent-sync marker are removed; foreign hooks are left
// untouched. The predecessor preserved by a prior install is restored:
// a `<hook>.agent-sync-backup` (from --replace) or `<hook>.agent-sync-predecessor`
// (from --append). Returns the hook names removed.
func Uninstall(repoRoot string) ([]string, error) {
	dir, err := hooksDir(repoRoot)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, name := range ManagedHooks {
		path := filepath.Join(dir, name)
		content, readErr := os.ReadFile(path) //nolint:gosec // repo's own hooks dir
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				continue // not present
			}
			// A real read error (permission, I/O) must not be swallowed as
			// "absent" — that could leave a hook half-uninstalled silently.
			return removed, fmt.Errorf("hooks: read %s: %w", name, readErr)
		}
		if !isAgentSyncHook(content) {
			continue // foreign hook — never touch
		}
		if rmErr := os.Remove(path); rmErr != nil {
			return removed, fmt.Errorf("hooks: remove %s: %w", name, rmErr)
		}
		removed = append(removed, name)

		// Restore the preserved predecessor: --replace backup first, then
		// the --append sidecar. Whichever exists is moved back into place.
		if restored, rerr := restorePredecessor(path); rerr != nil {
			return removed, fmt.Errorf("hooks: restore predecessor for %s: %w", name, rerr)
		} else if !restored {
			// No predecessor: clean up any orphan --append sidecar.
			_ = os.Remove(path + predecessorSuffix)
		}
	}
	return removed, nil
}

// restorePredecessor moves a preserved predecessor (from --replace backup
// or --append sidecar) back to path, removing the sidecar. Returns true
// when something was restored.
func restorePredecessor(path string) (bool, error) {
	for _, sidecar := range []string{path + ".agent-sync-backup", path + predecessorSuffix} {
		data, berr := os.ReadFile(sidecar) //nolint:gosec // repo's own hooks dir
		if berr != nil {
			continue
		}
		if werr := os.WriteFile(path, data, 0o755); werr != nil { //nolint:gosec // hooks must be executable
			return false, werr
		}
		_ = os.Remove(sidecar)
		return true, nil
	}
	return false, nil
}

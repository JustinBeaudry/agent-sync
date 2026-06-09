package hooks

import (
	"fmt"
	"os"
	"path/filepath"
)

// Uninstall removes the managed hook wrappers from repoRoot. Only files
// carrying the aienvs marker are removed; foreign hooks are left
// untouched. A `<hook>.aienvs-backup` left by a prior --replace install is
// restored when the managed hook is removed. Returns the hook names
// removed.
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
			continue // not present
		}
		if !isAienvsHook(content) {
			continue // foreign hook — never touch
		}
		if rmErr := os.Remove(path); rmErr != nil {
			return removed, fmt.Errorf("hooks: remove %s: %w", name, rmErr)
		}
		removed = append(removed, name)

		// Restore a backed-up predecessor, if any.
		backup := path + ".aienvs-backup"
		if data, berr := os.ReadFile(backup); berr == nil { //nolint:gosec // repo's own hooks dir
			if werr := os.WriteFile(path, data, 0o755); werr != nil { //nolint:gosec // hooks must be executable
				return removed, fmt.Errorf("hooks: restore backup for %s: %w", name, werr)
			}
			_ = os.Remove(backup)
		}
	}
	return removed, nil
}

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/agent-sync/agent-sync/internal/adapter"
)

// discoverTargets probes dir for each bundled adapter's reserved-prefix
// directory (.claude, .cursor, ...) and returns the matching adapter names,
// sorted. It is the target-discovery signal for `init`: a tool's footprint
// directory in the destination workspace means the tool is in use there.
//
// Probes are read-only, exact-name, and directory-only: os.Stat follows
// symlinks, a plain file never matches, and there is no substring or glob
// matching (so a .agents skills dir can never discover antigravity's .agent
// prefix). A NotExist result means "not discovered"; any other stat error is
// also "not discovered" but is surfaced as a warning so a permission problem
// is never silently read as absence. PATH adapters (agent-sync-adapter-*) do
// not participate: reading their reserved_prefix would require subprocess
// execution, which init does not do.
func discoverTargets(dir string, bundled []*adapter.BundledAdapter) (names, warnings []string) {
	for _, b := range bundled {
		prefix := b.Manifest.ReservedPrefix
		if prefix == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, prefix))
		switch {
		case err == nil:
			if info.IsDir() {
				names = append(names, b.Manifest.Name)
			}
		case errors.Is(err, os.ErrNotExist):
			// Not discovered.
		default:
			warnings = append(warnings, fmt.Sprintf("cannot probe %s for %s: %v", prefix, b.Manifest.Name, err))
		}
	}
	sort.Strings(names)
	return names, warnings
}

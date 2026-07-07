package hierarchy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/goccy/go-yaml"

	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/workspace"
)

// manifestMarker keeps discovery light enough to read activation-root hints from
// potentially legacy manifests where a full load would fail validation.
type manifestMarker struct {
	Scope          string `yaml:"scope"`
	ActivationRoot bool   `yaml:"activation_root"`
}

var (
	activationRootScopePattern    = regexp.MustCompile(`(?m)^\s*scope\s*:\s*(?:workspace|"workspace"|'workspace')\s*(#.*)?$`)
	activationRootEnabledPattern  = regexp.MustCompile(`(?m)^\s*activation_root\s*:\s*[Tt][Rr][Uu][Ee]\s*(#.*)?$`)
	activationRootMarkerMalformed = "hierarchy: malformed activation-root marker"
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

// markerAt reports whether dir has a manifest and, if so, returns marker fields
// from that manifest when YAML parses successfully. Unlike manifest.LoadFile, it
// is intentionally lightweight: only scope and activation_root are read. YAML
// parse failures are tolerated as non-fatal so discovery remains presence-based
// except when raw bytes still look like an activation-root marker, which is
// treated as a boundary and fails closed.
func markerAt(dir string) (manifestMarker, string, bool, error) {
	path, has := manifestAt(dir)
	if !has {
		return manifestMarker{}, "", false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return manifestMarker{}, path, false, fmt.Errorf("hierarchy: stat manifest marker %s: %w", path, err)
	}
	if info.Size() > manifest.MaxManifestSize {
		return manifestMarker{}, path, false, fmt.Errorf("hierarchy: manifest marker %s exceeds %d bytes (got %d)", path, manifest.MaxManifestSize, info.Size())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return manifestMarker{}, path, false, fmt.Errorf("hierarchy: read manifest marker %s: %w", path, err)
	}
	if len(b) > manifest.MaxManifestSize {
		return manifestMarker{}, path, false, fmt.Errorf("hierarchy: manifest marker %s exceeds %d bytes (got %d)", path, manifest.MaxManifestSize, len(b))
	}
	var marker manifestMarker
	if err := yaml.Unmarshal(b, &marker); err != nil {
		if looksLikeActivationRootMarker(b) {
			return manifestMarker{}, path, true, fmt.Errorf("%s %s: %w", activationRootMarkerMalformed, path, err)
		}
		// Keep discovery behavior presence-based when the malformed manifest is
		// not an activation-root marker.
		return manifestMarker{}, path, true, nil
	}
	return marker, path, true, nil
}

// looksLikeActivationRootMarker performs a conservative marker-only check for
// activation-root manifests, avoiding any full YAML parsing in the error path.
func looksLikeActivationRootMarker(raw []byte) bool {
	s := string(raw)
	return activationRootScopePattern.MatchString(s) && activationRootEnabledPattern.MatchString(s)
}

// hasGit reports whether dir contains a .git entry (directory for a normal
// clone, file for a worktree/submodule). Existence is all we need.
func hasGit(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// findProjectRoot returns the nearest ancestor of cwd (inclusive) that
// contains a .git entry, walking up the logical parent chain. The search
// stops at stopRoot: that level is not a project root. A repo whose .git sits
// at stopRoot yields ok=false. ok is also false when the filesystem root or the
// hop budget is reached without a match.
func findProjectRoot(cwd, stopRoot string, maxHops int) (string, bool) {
	dir := cwd
	for hops := 0; hops < maxHops; hops++ {
		if dir == stopRoot {
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
//
//nolint:unparam // error return is the stable contract Discover propagates; reserved for future I/O.
func collectEmitScopes(cwd, projectRoot string) ([]Scope, error) {
	var found []Scope
	dir := cwd
	// The cwd→projectRoot distance is already bounded by findProjectRoot's
	// hop budget (projectRoot is a verified ancestor of cwd), so this walk
	// needs no separate hop cap; the parent==dir check is the root backstop.
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

// activationRootsBetween scans from cwd upward toward home or the filesystem root
// for workspace activation roots and returns them as scopes. It collects from
// deep→shallow then reverses, so the caller gets shallow→deep (outermost→innermost).
func activationRootsBetween(cwd, home string, maxHops int) ([]Scope, error) {
	var roots []Scope
	dir := cwd
	for hops := 0; hops < maxHops; hops++ {
		marker, path, ok, err := markerAt(dir)
		if err != nil {
			return nil, err
		}
		if ok && marker.Scope == manifest.ScopeWorkspace && marker.ActivationRoot {
			roots = append(roots, Scope{
				Root:         dir,
				ManifestPath: path,
				Level:        LevelWorkspace,
				Emit:         true,
			})
		}
		if dir == home {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	for i, j := 0, len(roots)-1; i < j; i, j = i+1, j-1 {
		roots[i], roots[j] = roots[j], roots[i]
	}
	if len(roots) > 1 {
		return nil, fmt.Errorf("hierarchy: nested activation roots are invalid: %s and %s", roots[0].ManifestPath, roots[len(roots)-1].ManifestPath)
	}
	return roots, nil
}

// dedupeScopes removes duplicate scopes by manifest path while preserving the
// existing order. This avoids accidentally returning the same scope twice when
// the activation root and emit walk resolve to the same manifest.
func dedupeScopes(in []Scope) []Scope {
	seen := make(map[string]struct{}, len(in))
	out := make([]Scope, 0, len(in))
	for _, scope := range in {
		if _, ok := seen[scope.ManifestPath]; ok {
			continue
		}
		seen[scope.ManifestPath] = struct{}{}
		out = append(out, scope)
	}
	return out
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

// UserScope returns the user-home scope when home holds a manifest, always
// read-only (Emit=false). It lets callers on the single-scope path (validate,
// watch, sync --workspace) locate the user manifest for composition without
// running full hierarchy discovery from a cwd. ok is false when home has no
// manifest.
func UserScope(home string) (Scope, bool) {
	return userScope(home, false)
}

// Discover returns every manifest that applies at cwd, ordered from
// broadest (lowest precedence) to most specific (highest precedence).
// Normally this is user -> project -> directory. When inside a workspace
// activation root, user is omitted and workspace is returned instead.
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
	// Resolve home to absolute before cleaning. os.UserHomeDir already
	// returns an absolute path, but a caller may pass a relative
	// Options.Home; without this, the dir == home comparison in
	// findProjectRoot (which walks the absolute absCwd chain) would never
	// match, breaking root/home detection.
	absHome, err := filepath.Abs(home)
	if err != nil {
		return nil, fmt.Errorf("hierarchy: resolve home %q: %w", home, err)
	}
	home = filepath.Clean(absHome)

	maxHops := opts.MaxHops
	if maxHops <= 0 {
		maxHops = workspace.DefaultMaxHops
	}

	var emit []Scope

	activation, err := activationRootsBetween(absCwd, home, maxHops)
	if err != nil {
		return nil, err
	}
	if len(activation) == 1 {
		stopRoot := activation[0].Root
		if root, ok := findProjectRoot(absCwd, stopRoot, maxHops); ok {
			emit, err = collectEmitScopes(absCwd, root)
			if err != nil {
				return nil, err
			}
		} else if absCwd != stopRoot {
			// No .git ancestor before the activation root: only cwd's own manifest
			// applies, classified as project scope.
			if path, has := manifestAt(absCwd); has {
				emit = []Scope{{Root: absCwd, ManifestPath: path, Level: LevelProject, Emit: true}}
			}
		}
		out := append([]Scope(nil), activation...)
		out = append(out, emit...)
		return dedupeScopes(out), nil
	}

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

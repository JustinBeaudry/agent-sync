// Package hierarchy discovers the agent-sync manifests that apply at a
// given location, ordered from broadest (lowest precedence) to most
// specific (highest precedence).
//
// Unlike internal/workspace (which resolves the single nearest manifest),
// this package collects every manifest in the scope chain: the user-home
// manifest, the project manifest (at the nearest .git ancestor), and any
// intermediate directory manifests between the project root and cwd.
//
// It is a pure leaf package: it walks the filesystem and returns data. It
// performs no writes, opens no fsroot, and knows nothing about adapters or
// the engine. The multi-scope sync orchestrator consumes its output.
package hierarchy

// Level classifies a scope by where its manifest sits in the hierarchy.
type Level int

const (
	// LevelUser is the manifest at the user's home directory (~). It is
	// the broadest scope and has the lowest precedence.
	LevelUser Level = iota
	// LevelProject is the manifest at the project root (the nearest
	// ancestor of cwd containing a .git entry).
	LevelProject
	// LevelDirectory is a manifest in a directory strictly between the
	// project root and cwd. Deeper directories have higher precedence.
	LevelDirectory
)

// String returns the lowercase label used in status output and warnings.
func (l Level) String() string {
	switch l {
	case LevelUser:
		return "user"
	case LevelProject:
		return "project"
	case LevelDirectory:
		return "directory"
	default:
		return "unknown"
	}
}

// MarshalText renders the level as its lowercase label so a Level embedded in
// JSON (e.g. coverage warnings) serializes consistently with other level
// fields rather than as a raw integer.
func (l Level) MarshalText() ([]byte, error) {
	return []byte(l.String()), nil
}

// Scope is one discovered manifest and the directory it anchors.
type Scope struct {
	// Root is the absolute directory containing the manifest. It is the
	// boundary an fsroot is later opened against for this scope.
	Root string
	// ManifestPath is the absolute path to the scope's .agent-sync.yaml.
	ManifestPath string
	// Level classifies the scope (user / project / directory).
	Level Level
	// Emit is true when this scope should be synced in the current run.
	// Project and directory scopes are always Emit=true; the user scope is
	// Emit=true only when Options.IncludeUser is set (the --user flag).
	Emit bool
}

// Options controls discovery. All fields are optional.
type Options struct {
	// Home overrides the user home directory. Empty means os.UserHomeDir.
	// Injectable so tests do not depend on the real home directory.
	Home string
	// IncludeUser marks the user-home scope Emit=true. It corresponds to
	// the `sync --user` flag. When false the user scope is still returned
	// (for status/precedence display) but with Emit=false.
	IncludeUser bool
	// MaxHops caps the upward walk. Zero means workspace.DefaultMaxHops.
	MaxHops int
}

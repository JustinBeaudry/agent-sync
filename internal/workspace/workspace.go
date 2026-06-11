// Package workspace resolves and represents a single agent-sync workspace —
// the directory anchored by a `.agent-sync.yaml` manifest.
//
// Every agent-sync invocation operates on exactly one resolved workspace
// (plan decision R1). Multi-workspace isolation (plan decision #22) is
// enforced at the filesystem layer by fsroot: writes from this process
// must stay inside Workspace.Root.
//
// Discovery is cwd-relative by default and follows the user's *logical*
// path (no EvalSymlinks) so a user who `cd`'d through a symlinked
// directory gets the workspace they expect.
package workspace

import (
	"errors"
	"os"
)

// ManifestName is the literal filename agent-sync looks for when walking up
// from cwd. The suffix is singular by design — see docs/spec/manifest-v1.md.
const ManifestName = ".agent-sync.yaml"

// DefaultMaxHops caps the upward walk. Picked as a safety net against
// unbounded cycles on pathological filesystems (symlink loops,
// overlayfs layering bugs, etc.). Real workspaces are never more than
// a handful of directories deep inside a user's home directory.
const DefaultMaxHops = 64

// Sentinel errors. Callers branch on these with errors.Is.
var (
	// ErrNotFound is returned when no manifest was discovered up to the
	// walk terminus (filesystem root or Options.StopAt).
	ErrNotFound = errors.New("no .agent-sync.yaml workspace found")

	// ErrMaxWalkExceeded is returned when discovery exceeds
	// Options.MaxHops (or DefaultMaxHops) traversals. This is the v1
	// cycle-safety net; true inode-based cycle detection is deferred
	// per origin R8.
	ErrMaxWalkExceeded = errors.New("workspace discovery exceeded maximum hop limit")

	// ErrInvalidOptions is returned when Options describe a workspace
	// override that does not resolve to a directory containing a
	// manifest.
	ErrInvalidOptions = errors.New("invalid workspace discovery options")

	// ErrManifestNotRegular is returned when a path matching the manifest
	// filename exists but is not a regular file (e.g. a directory, symlink
	// to a missing target, or device node). The walk stops immediately
	// rather than skipping, so the user sees an actionable error.
	ErrManifestNotRegular = errors.New("workspace: manifest is not a regular file")
)

// Workspace is the resolved binding of a manifest to a directory.
//
// Fields are populated by Find. A caller holding a *Workspace can
// safely pass Root to fsroot.OpenWorkspaceRoot and use ManifestPath
// with manifest.LoadFile.
type Workspace struct {
	// ManifestPath is the absolute path to the resolved `.agent-sync.yaml`.
	ManifestPath string

	// Root is the absolute path to the directory containing
	// ManifestPath. This is the boundary fsroot scopes to for any
	// write this process makes inside the workspace.
	Root string

	// LogicalCwd is the cwd Find was given (or the user's actual cwd
	// if none was provided). Preserved for diagnostics; callers should
	// not treat it as authoritative for filesystem containment checks —
	// use Root.
	LogicalCwd string
}

// Options controls discovery. All fields are optional.
type Options struct {
	// Workspace, if non-empty, short-circuits discovery. Accepts either
	// a directory path (expected to contain a `.agent-sync.yaml`) or the
	// path to a `.agent-sync.yaml` file directly.
	Workspace string

	// StopAt, if non-empty, terminates the upward walk at this
	// directory. Discovery refuses to look above it. Empty means
	// "filesystem root."
	StopAt string

	// MaxHops caps the upward walk. Zero means DefaultMaxHops.
	// Maximum number of directories to inspect during the upward walk
	// (cwd counts as the first).
	MaxHops int
}

// OptionsFromEnv reads AGENT_SYNC_WORKSPACE_STOP_AT into Options.StopAt.
// Callers (typically the CLI layer) merge these with flag-derived
// options before calling Find.
func OptionsFromEnv() Options {
	return Options{
		StopAt: os.Getenv("AGENT_SYNC_WORKSPACE_STOP_AT"),
	}
}

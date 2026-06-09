package fsroot

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Root is the aienvs-wrapped [os.Root] handle. It is the sole legitimate
// way for agent-sync code to touch paths inside a user workspace.
//
// A Root is safe for concurrent use insofar as [os.Root] is: reads and
// writes may run in parallel, but the caller is responsible for
// serializing operations that must be ordered (for example, a rename
// that depends on a prior write).
type Root struct {
	inner *os.Root
	// absPath is the cleaned absolute path the root is scoped to.
	// Stored only so error messages can name the root without a
	// filesystem call; never used for lookups.
	absPath string
}

// Sentinel errors. Callers may inspect these with [errors.Is].
var (
	// ErrEscapesRoot is returned when a caller attempts an operation on
	// a path that [os.Root] refuses because it would escape the root.
	// We wrap [os.Root]'s raw error with this sentinel so higher layers
	// can branch on it without importing os/syscall.
	ErrEscapesRoot = errors.New("path escapes fsroot containment")

	// ErrCrossVolume is returned when a filesystem operation would cross
	// a filesystem boundary that agent-sync refuses to traverse — notably a
	// rename across filesystems. The authoritative signal is the kernel
	// EXDEV / ERROR_NOT_SAME_DEVICE, not a statfs pre-flight.
	ErrCrossVolume = errors.New("cross-filesystem operation refused")

	// ErrIrregular is returned when a target is a reparse point, device
	// file, socket, or other irregular filesystem object that agent-sync
	// refuses to write through.
	ErrIrregular = errors.New("target is an irregular filesystem object")

	// ErrUnsafeRelPath is returned when a caller supplies a relative
	// path that is syntactically unsafe (empty, absolute, or contains a
	// "..") before we even ask the kernel.
	ErrUnsafeRelPath = errors.New("unsafe relative path")
)

// OpenWorkspaceRoot opens dir as an [os.Root]-backed containment scope.
// dir must exist and be a directory; symlinks along dir are resolved in
// the usual OS manner (this is the one place we tolerate following
// symlinks, because it defines the boundary itself).
//
// The name is descriptive of the most common caller (workspace
// discovery). Nothing in [Root] actually requires the scope to be a
// workspace; unit 13 opens roots at each target's parent directory.
func OpenWorkspaceRoot(dir string) (*Root, error) {
	if dir == "" {
		return nil, fmt.Errorf("fsroot: %w: empty directory", ErrUnsafeRelPath)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("fsroot: resolve %q: %w", dir, err)
	}
	inner, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("fsroot: open root %q: %w", abs, err)
	}
	return &Root{inner: inner, absPath: abs}, nil
}

// Close releases the underlying [os.Root] handle. It is safe to call
// Close on a nil Root.
func (r *Root) Close() error {
	if r == nil || r.inner == nil {
		return nil
	}
	return r.inner.Close()
}

// Path returns the absolute, cleaned directory the root is scoped to.
// This is informational; callers must never feed Path back into a
// filesystem call — use the [os.Root] methods on Inner instead.
func (r *Root) Path() string {
	if r == nil {
		return ""
	}
	return r.absPath
}

// Inner exposes the wrapped [os.Root] for callers that need operations
// fsroot does not yet wrap. New call sites should prefer adding a
// method here over reaching through Inner, so the containment invariants
// stay in one place.
func (r *Root) Inner() *os.Root {
	if r == nil {
		return nil
	}
	return r.inner
}

// Lstat is a convenience wrapper around [os.Root.Lstat] that translates
// the raw [os.Root] escape error into [ErrEscapesRoot].
func (r *Root) Lstat(relPath string) (fs.FileInfo, error) {
	if err := ValidateRelPath(relPath); err != nil {
		return nil, err
	}
	fi, err := r.inner.Lstat(relPath)
	if err != nil {
		return nil, translateErr(err)
	}
	return fi, nil
}

// Stat wraps [os.Root.Stat] with the same error translation as Lstat.
func (r *Root) Stat(relPath string) (fs.FileInfo, error) {
	if err := ValidateRelPath(relPath); err != nil {
		return nil, err
	}
	fi, err := r.inner.Stat(relPath)
	if err != nil {
		return nil, translateErr(err)
	}
	return fi, nil
}

// ValidateRelPath rejects relative paths that are structurally unsafe
// before they reach the kernel. [os.Root] also refuses them, but an
// early sentinel error makes test failures and log lines easier to
// read.
//
// A valid relPath:
//   - is non-empty
//   - is not absolute
//   - is not "." and does not start with ".." or contain a ".." segment
//   - is forward-slash normalized (backslashes are allowed and
//     normalized away here so Windows callers can pass either)
func ValidateRelPath(relPath string) error {
	if relPath == "" {
		return fmt.Errorf("%w: empty", ErrUnsafeRelPath)
	}
	if filepath.IsAbs(relPath) {
		return fmt.Errorf("%w: absolute path %q", ErrUnsafeRelPath, relPath)
	}
	// Reject bare-rooted paths like "/etc/passwd" on Windows where
	// filepath.IsAbs is false (Windows absolute paths require a drive
	// letter) but the leading separator still means "root of the current
	// drive" and must not be accepted as a relative path.
	if strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, `\`) {
		return fmt.Errorf("%w: rooted path %q", ErrUnsafeRelPath, relPath)
	}
	cleaned := filepath.ToSlash(relPath)
	for _, seg := range strings.Split(cleaned, "/") {
		switch seg {
		case "", ".":
			// Allowed in intermediate positions; Clean would strip them.
		case "..":
			return fmt.Errorf("%w: contains %q: %q", ErrUnsafeRelPath, "..", relPath)
		}
	}
	return nil
}

// translateErr maps raw os.Root containment errors into sentinel errors
// the rest of agent-sync can branch on. Anything we do not recognize passes
// through unchanged so callers still see the underlying syscall detail.
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		// os.Root returns a PathError wrapping an opaque "path escapes
		// from parent" error; the stable way to detect it is the
		// package's exported os.ErrInvalid plus a message match. The
		// more robust path is to check the specific syscall error on
		// POSIX, but we stay portable by leaving unrecognized paths
		// alone and only up-converting the clear escape cases.
		if strings.Contains(pathErr.Err.Error(), "path escapes") ||
			strings.Contains(pathErr.Err.Error(), "outside the root") {
			return fmt.Errorf("%w: %w", ErrEscapesRoot, pathErr)
		}
	}
	return err
}

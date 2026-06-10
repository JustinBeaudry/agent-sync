package cache

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// DirName is the fixed agent-sync-owned subdirectory inside the chosen
// cache root. All materialized clones live under <root>/<DirName>/<key>.
const DirName = "agent-sync/repos"

// AuditFileName is the name of the audit file each cache entry writes
// recording the plain canonical URL associated with the key. The file
// is for human diagnosis only; hashing stays one-way.
const AuditFileName = "canonical-url.txt"

// ErrNoCacheRoot is returned when neither a manifest override nor XDG
// can produce a usable cache root. This is extremely unusual in
// practice — xdg falls back to os.TempDir() on fully-unconfigured
// systems — but we surface a clean sentinel rather than a cryptic
// XDG error.
var ErrNoCacheRoot = errors.New("no cache root available")

// Location describes where a materialized canonical-source clone lives.
//
// Root is the top-level cache directory (e.g.
// "/home/alice/.cache/agent-sync/repos"); Dir is <Root>/<key>; AuditPath is
// the file inside Dir that records the plain canonical URL for human
// diagnosis.
type Location struct {
	Root      string
	Dir       string
	AuditPath string
	Key       string
	Canonical string
}

// ResolveOptions controls where the cache lives on disk.
//
// Override, if non-empty, takes precedence over XDG. It must be an
// absolute path; agent-sync does not resolve it relative to cwd so a
// relative cache path in the manifest never silently shifts with the
// invoking user's shell state.
type ResolveOptions struct {
	Override string
}

// Resolve computes the on-disk Location for a canonical URL. It does
// NOT create the directory — callers (the git layer in unit 5) create
// the parent lazily when they actually clone.
//
// canonical must already be in canonical form (as returned by Canonicalize).
// If it is not, Resolve returns ErrUnsupportedURL wrapping the form invariant.
// This prevents forged or un-cleaned URLs from generating audit entries with
// credentials or non-normalized forms.
func Resolve(canonical string, opts ResolveOptions) (*Location, error) {
	if canonical == "" {
		return nil, fmt.Errorf("%w: empty canonical URL", ErrUnsupportedURL)
	}

	recanon, err := Canonicalize(canonical)
	if err != nil {
		return nil, fmt.Errorf("%w: input not in canonical form: %w", ErrUnsupportedURL, err)
	}
	if recanon != canonical {
		return nil, fmt.Errorf("%w: input not in canonical form (got %q, canonical form is %q)", ErrUnsupportedURL, canonical, recanon)
	}

	root, err := rootDir(opts)
	if err != nil {
		return nil, err
	}

	key := Key(canonical)
	dir := filepath.Join(root, key)
	return &Location{
		Root:      root,
		Dir:       dir,
		AuditPath: filepath.Join(dir, AuditFileName),
		Key:       key,
		Canonical: canonical,
	}, nil
}

// WriteAudit writes (or overwrites) the audit file recording the plain
// canonical URL inside an already-existing cache directory. Callers
// typically invoke this once at materialization time so the plain URL
// is visible in the directory alongside the bare clone.
//
// Three invariants are enforced:
//  1. Integrity: Key(l.Canonical) must match l.Key — prevents forged Locations.
//  2. Atomicity: the write is staged via a unique temp file + rename so
//     concurrent writers never produce a torn read.
//  3. Symlink hardening: the write goes through os.Root so any symlink
//     planted at AuditPath cannot redirect writes outside Dir.
func (l *Location) WriteAudit() error {
	if l == nil {
		return errors.New("cache: nil Location")
	}
	// Integrity check: the Location must be self-consistent.
	if Key(l.Canonical) != l.Key {
		return fmt.Errorf("audit: canonical %q does not match key %q", l.Canonical, l.Key)
	}
	if err := os.MkdirAll(l.Dir, 0o750); err != nil {
		return fmt.Errorf("cache: mkdir %q: %w", l.Dir, err)
	}
	return stagedWriteCache(l.Dir, AuditFileName, []byte(l.Canonical+"\n"), 0o644)
}

// stagedWriteCache writes content atomically to dir/name by creating a
// unique sibling temp file, fsyncing, and renaming into place. It uses
// os.Root so any symlink at the destination cannot escape dir.
func stagedWriteCache(dir, name string, content []byte, mode fs.FileMode) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("cache: open root %q: %w", dir, err)
	}
	defer func() { _ = root.Close() }()

	// Generate a unique temp filename inside dir.
	var randBytes [8]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return fmt.Errorf("cache: rand: %w", err)
	}
	tmpName := "." + name + ".tmp." + hex.EncodeToString(randBytes[:])

	tmpFile, err := root.OpenFile(tmpName, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("cache: create temp %q: %w", tmpName, err)
	}
	// Clean up the temp file on any error path.
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = root.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(content); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("cache: write temp: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("cache: sync temp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("cache: close temp: %w", err)
	}

	if err := root.Rename(tmpName, name); err != nil {
		return fmt.Errorf("cache: rename temp to %q: %w", name, err)
	}
	cleanupTmp = false

	// Best-effort directory fsync for crash-safety parity with
	// fsroot.StagedWrite. Syncing the directory makes the rename durable
	// across a power loss or crash; the file data itself is already
	// fsync'd above. Errors are intentionally ignored: Windows and some
	// network filesystems return errors when syncing a directory handle,
	// and the audit file data is already durable at this point.
	if df, err := root.Open("."); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}

	return nil
}

// rootDir returns the top-level cache directory, honoring an override
// if supplied.
func rootDir(opts ResolveOptions) (string, error) {
	if opts.Override != "" {
		if !filepath.IsAbs(opts.Override) {
			return "", fmt.Errorf("cache: override %q must be absolute", opts.Override)
		}
		return filepath.Join(opts.Override, DirName), nil
	}
	// xdg.CacheHome is guaranteed non-empty on every supported OS.
	if xdg.CacheHome == "" {
		return "", ErrNoCacheRoot
	}
	return filepath.Join(xdg.CacheHome, DirName), nil
}

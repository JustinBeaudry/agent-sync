package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// DirName is the fixed aienvs-owned subdirectory inside the chosen
// cache root. All materialized clones live under <root>/<DirName>/<key>.
const DirName = "aienvs/repos"

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
// "/home/alice/.cache/aienvs/repos"); Dir is <Root>/<key>; AuditPath is
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
// absolute path; aienvs does not resolve it relative to cwd so a
// relative cache path in the manifest never silently shifts with the
// invoking user's shell state.
type ResolveOptions struct {
	Override string
}

// Resolve computes the on-disk Location for a canonical URL. It does
// NOT create the directory — callers (the git layer in unit 5) create
// the parent lazily when they actually clone.
func Resolve(canonical string, opts ResolveOptions) (*Location, error) {
	if canonical == "" {
		return nil, fmt.Errorf("%w: empty canonical URL", ErrUnsupportedURL)
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

// ResolveFromRaw is a convenience wrapper: canonicalize then resolve.
func ResolveFromRaw(rawURL string, opts ResolveOptions) (*Location, error) {
	c, err := Canonicalize(rawURL)
	if err != nil {
		return nil, err
	}
	return Resolve(c, opts)
}

// WriteAudit writes (or overwrites) the audit file recording the plain
// canonical URL inside an already-existing cache directory. Callers
// typically invoke this once at materialization time so the plain URL
// is visible in the directory alongside the bare clone.
//
// The write goes through os.WriteFile (not fsroot) because the cache
// directory is outside the workspace — fsroot's containment would
// reject an absolute path.
func (l *Location) WriteAudit() error {
	if l == nil {
		return errors.New("cache: nil Location")
	}
	if err := os.MkdirAll(l.Dir, 0o750); err != nil {
		return fmt.Errorf("cache: mkdir %q: %w", l.Dir, err)
	}
	return os.WriteFile(l.AuditPath, []byte(l.Canonical+"\n"), fs.FileMode(0o644))
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

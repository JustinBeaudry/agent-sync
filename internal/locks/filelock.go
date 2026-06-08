package locks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/gofrs/flock"

	"github.com/aienvs/aienvs/internal/fsroot"
)

const fileLocksDirRel = ".aienv/state/filelocks"

// FileLockRegistry serializes the read-merge-write of shared tool-owned
// files (workspace-root AGENTS.md, .mcp.json, etc.) across goroutines
// and processes. It locks a dedicated hashed sidecar under
// .aienv/state/filelocks/, never the target file itself, so the merge's
// own temp+rename (Unit 12a) is not fighting the lock.
type FileLockRegistry struct {
	dir string // absolute filelocks dir

	mu   sync.Mutex
	held map[string]*keyLock
}

// keyLock is the per-canonical-path lock: an in-process gate (buffered
// channel of size 1) layered over the cross-process OS flock, plus the
// current holder for timeout diagnostics.
type keyLock struct {
	gate   chan struct{}
	fl     *flock.Flock
	holder string // current in-process holder; guarded by registry.mu
}

// NewFileLockRegistry guards the state-dir prefix against symlinks,
// ensures the filelocks dir exists, and resolves its absolute path.
func NewFileLockRegistry(root *fsroot.Root) (*FileLockRegistry, error) {
	if err := guardStatePrefix(root); err != nil {
		return nil, err
	}
	if err := root.Inner().MkdirAll(fileLocksDirRel, 0o755); err != nil {
		return nil, fmt.Errorf("locks: mkdir %s: %w", fileLocksDirRel, err)
	}
	dir := filepath.Join(root.Path(), filepath.FromSlash(fileLocksDirRel))
	return &FileLockRegistry{dir: dir, held: map[string]*keyLock{}}, nil
}

// Acquire takes the lock for the file at absPath on behalf of holder
// (an adapter name, used in the timeout error). It serializes both
// in-process goroutines and cross-process acquirers on the same
// canonical path. Returns a release func; on bounded-timeout returns
// ErrFileLockTimeout naming the (verbatim) path and the holder.
func (r *FileLockRegistry) Acquire(ctx context.Context, absPath, holder string, opts AcquireOpts) (release func() error, err error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	hash := hashKey(canonicalize(absPath))
	kl := r.keyLockFor(hash)

	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// In-process gate first, so two goroutines in one process serialize
	// without both contending for the OS flock.
	select {
	case kl.gate <- struct{}{}:
	case <-actx.Done():
		return nil, r.timeoutErr(kl, absPath)
	}

	locked, ferr := kl.fl.TryLockContext(actx, retryDelay)
	if ferr != nil && !errors.Is(ferr, context.DeadlineExceeded) {
		<-kl.gate
		return nil, fmt.Errorf("locks: file lock %s: %w", absPath, ferr)
	}
	if !locked {
		<-kl.gate
		return nil, r.timeoutErr(kl, absPath)
	}

	r.setHolder(kl, holder)
	var once sync.Once
	return func() error {
		var uerr error
		once.Do(func() {
			r.setHolder(kl, "")
			uerr = kl.fl.Unlock()
			<-kl.gate
		})
		return uerr
	}, nil
}

func (r *FileLockRegistry) keyLockFor(hash string) *keyLock {
	r.mu.Lock()
	defer r.mu.Unlock()
	kl := r.held[hash]
	if kl == nil {
		kl = &keyLock{
			gate: make(chan struct{}, 1),
			fl:   flock.New(filepath.Join(r.dir, hash+".lock")),
		}
		r.held[hash] = kl
	}
	return kl
}

func (r *FileLockRegistry) setHolder(kl *keyLock, holder string) {
	r.mu.Lock()
	kl.holder = holder
	r.mu.Unlock()
}

func (r *FileLockRegistry) timeoutErr(kl *keyLock, absPath string) error {
	r.mu.Lock()
	holder := kl.holder
	r.mu.Unlock()
	if holder != "" {
		return fmt.Errorf("%w: %s held by %q", ErrFileLockTimeout, absPath, holder)
	}
	return fmt.Errorf("%w: %s held by another process", ErrFileLockTimeout, absPath)
}

func hashKey(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// canonicalize converges two spellings of the same file to one key:
// Abs + Clean, strip trailing separator, resolve symlinks on the
// longest existing prefix then re-append the (not-yet-existing)
// remainder, and case-fold on case-insensitive filesystems. The
// longest-existing-prefix step is what makes a not-yet-created file
// (the first-emission case, where EvalSymlinks on the full path fails)
// still converge with its already-resolved spelling.
func canonicalize(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	resolved := resolveLongestPrefix(p)
	if caseInsensitiveFS() {
		return strings.ToLower(resolved)
	}
	return resolved
}

// resolveLongestPrefix EvalSymlinks the longest existing ancestor of p
// and re-appends the non-existent remainder, so a path whose leaf does
// not exist yet still resolves symlinks in its existing parents.
func resolveLongestPrefix(p string) string {
	var remainder []string
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			parts := []string{r}
			for i := len(remainder) - 1; i >= 0; i-- {
				parts = append(parts, remainder[i])
			}
			return filepath.Join(parts...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p // reached the volume root with nothing resolvable
		}
		remainder = append(remainder, filepath.Base(cur))
		cur = parent
	}
}

// caseInsensitiveFS reports whether the host's default filesystem folds
// case (macOS APFS default, Windows NTFS). Conservative: only the two
// platforms where case-insensitivity is the documented default.
func caseInsensitiveFS() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

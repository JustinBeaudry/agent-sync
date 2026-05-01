package locks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// DefaultFileLockTimeout is the maximum duration AcquireFile will wait.
const DefaultFileLockTimeout = 30 * time.Second

// ErrFileLockTimeout is returned when AcquireFile cannot obtain the lock
// within the allowed duration.
var ErrFileLockTimeout = fmt.Errorf("file lock timeout")

// FileLockOptions controls AcquireFile behaviour.
type FileLockOptions struct {
	// Timeout is how long to wait. Zero means DefaultFileLockTimeout.
	Timeout time.Duration

	// RetryInterval is the polling interval. Zero means DefaultRetryInterval.
	RetryInterval time.Duration
}

// FileLock is the held lock returned by AcquireFile. Call Release when the
// read-merge-write window is complete.
type FileLock struct {
	fl      *flock.Flock
	absPath string
	reg     *fileRegistry
}

// Release releases the file lock and removes the process from the registry.
func (fl *FileLock) Release() error {
	if fl == nil || fl.fl == nil {
		return nil
	}
	fl.reg.remove(fl.absPath)
	if err := fl.fl.Unlock(); err != nil {
		return fmt.Errorf("locks: unlock %s: %w", fl.absPath, err)
	}
	// Best-effort remove the flock file; ignore errors (other processes may
	// be waiting on it or the file may never have been created on this OS).
	_ = os.Remove(fl.fl.Path())
	return nil
}

// fileRegistry tracks in-process per-file lock ownership to prevent
// re-entrant acquisition from within the same process.
type fileRegistry struct {
	mu   sync.Mutex
	held map[string]*flock.Flock
}

// global is the process-wide file lock registry. Using a package-level
// singleton is correct here: file locks must be serialised process-wide,
// not just goroutine-wide, because two goroutines in the same process share
// the same OS file-descriptor table and would both "succeed" at a kernel-level
// flock if we allowed re-entrant acquisition.
var global = &fileRegistry{held: make(map[string]*flock.Flock)}

func (r *fileRegistry) remove(absPath string) {
	r.mu.Lock()
	delete(r.held, absPath)
	r.mu.Unlock()
}

// flockPath returns the lock file path for an external file. It is a
// sibling of the target file with a ".aienvs-flock" suffix, so:
//   - it stays in the same directory (intra-FS rename is guaranteed)
//   - it is never confused with aienvs managed content (different extension)
func flockPath(absPath string) string {
	return absPath + ".aienvs-flock"
}

// AcquireFile acquires a cross-process + in-process lock for the file at
// absPath.
//
// absPath must be absolute. The function computes a canonical key via
// filepath.Clean so callers do not need to pre-normalise.
//
// The function creates the flock sidecar file (and its parent directories)
// if needed.
func AcquireFile(ctx context.Context, absPath string, opts FileLockOptions) (*FileLock, error) {
	if !filepath.IsAbs(absPath) {
		return nil, fmt.Errorf("locks: AcquireFile requires an absolute path, got %q", absPath)
	}
	canonical := filepath.Clean(absPath)

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultFileLockTimeout
	}
	retryInterval := opts.RetryInterval
	if retryInterval == 0 {
		retryInterval = DefaultRetryInterval
	}

	// In-process serialisation: block until no other goroutine in this
	// process holds the lock, then mark it in the registry before
	// attempting the OS-level lock. This prevents the re-entrant scenario.
	if err := waitInProcess(ctx, canonical, retryInterval, timeout); err != nil {
		return nil, err
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		global.remove(canonical)
		return nil, fmt.Errorf("locks: mkdir for file lock %s: %w", canonical, err)
	}

	lp := flockPath(canonical)
	fl := flock.New(lp)

	deadline := time.Now().Add(timeout)
	ctx2, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	locked, err := fl.TryLockContext(ctx2, retryInterval)
	if err != nil || !locked {
		global.remove(canonical)
		if err == nil {
			err = fmt.Errorf("%w: %s", ErrFileLockTimeout, canonical)
		}
		return nil, fmt.Errorf("locks: acquire file lock %s: %w", canonical, err)
	}

	return &FileLock{fl: fl, absPath: canonical, reg: global}, nil
}

// waitInProcess blocks until no other goroutine in this process holds
// canonical, then registers the current goroutine as the holder.
func waitInProcess(ctx context.Context, canonical string, retryInterval, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		global.mu.Lock()
		if _, held := global.held[canonical]; !held {
			// Mark as held before releasing the mutex so no other goroutine
			// can sneak in.
			global.held[canonical] = nil // placeholder; real flock set in caller
			global.mu.Unlock()
			return nil
		}
		global.mu.Unlock()

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %s (context cancelled)", ErrFileLockTimeout, canonical)
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %s", ErrFileLockTimeout, canonical)
		}
		time.Sleep(retryInterval)
	}
}

package locks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

// runLockRel is the fixed lock leaf for the per-workspace run lock. The leading
// dot keeps it out of the way of per-target lock files (<target>.lock) in the
// same state dir; a target is never named "" so there is no collision.
const runLockRel = stateDirRel + "/.sync.lock"

// RunLock is the per-workspace, whole-run lock. One agent-sync sync holds it
// end-to-end so concurrent syncs on the same workspace serialize. This is what
// makes cross-adapter shared-subdir co-ownership decisions safe across
// processes: the sync path reads sibling ledgers and rewrites its own without a
// run-wide lock, so two overlapping --target-filtered syncs could each defer a
// shared-leaf delete to the other and strand the file (ADV-1). Serializing the
// whole run makes the cross-target read-decide-write atomic across processes.
//
// It deliberately reuses TargetLock's flock + sidecar + symlink-guard machinery
// (advisory flock releases on process death, so a crashed holder never wedges a
// future sync; a sidecar records the holder pid/machine for a useful contention
// message). RunLock is kept separate from TargetLock rather than folded in, to
// leave the data-loss-critical per-target path untouched; if a third
// flock+sidecar user appears, extract a shared core.
//
// AcquireOpts.BreakLock supports force-clearing a stale sidecar (for a
// stuck-but-alive holder), but note it is not yet surfaced as a CLI flag for
// either lock — recover a genuinely wedged holder by ending the process named
// in the sidecar. Wiring a `--break-lock` flag is tracked separately.
type RunLock struct {
	root       *fsroot.Root
	lockRel    string
	sidecarRel string
	lockPath   string // absolute, for gofrs/flock only
	machineID  string
	now        func() time.Time // injectable clock for tests
	notices    io.Writer        // stale/break notices; defaults to os.Stderr
	fl         *flock.Flock
}

// NewRunLock guards the state-dir prefix against symlinks, ensures
// .agent-sync/state/ exists, resolves the stable machine id, and resolves the
// lock paths. It opens no lock FD yet.
func NewRunLock(root *fsroot.Root) (*RunLock, error) {
	if err := guardStatePrefix(root); err != nil {
		return nil, err
	}
	if err := root.Inner().MkdirAll(stateDirRel, 0o755); err != nil {
		return nil, fmt.Errorf("locks: mkdir %s: %w", stateDirRel, err)
	}
	mid, err := machineID(root)
	if err != nil {
		return nil, err
	}
	abs := filepath.Join(root.Path(), filepath.FromSlash(runLockRel))
	return &RunLock{
		root:       root,
		lockRel:    runLockRel,
		sidecarRel: runLockRel + ".pid",
		lockPath:   abs,
		machineID:  mid,
		now:        time.Now,
		notices:    os.Stderr,
		fl:         flock.New(abs),
	}, nil
}

// Acquire takes the run lock. On a free lock it succeeds; on a busy lock (a live
// holder — flock releases on death) it returns ErrRunLocked unless
// opts.BreakLock clears a stale sidecar and the holder turns out to be gone.
func (l *RunLock) Acquire(ctx context.Context, opts AcquireOpts) (release func() error, err error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	// Re-guard the prefix + leaf against a symlink swap in the construct→Acquire
	// window before flock (which follows symlinks) opens the FD.
	if err := guardStatePrefix(l.root); err != nil {
		return nil, err
	}
	if err := guardLeaf(l.root, l.lockRel); err != nil {
		return nil, err
	}

	locked, err := l.tryLock(ctx, timeout)
	if err != nil {
		return nil, err
	}
	if locked {
		return l.onAcquired()
	}
	if !opts.BreakLock {
		return nil, l.lockedErr()
	}
	l.noticef("--break-lock: clearing stale run-lock sidecar and retrying")
	_ = l.removeSidecar()
	locked2, err := l.tryLock(ctx, timeout)
	if err != nil {
		return nil, err
	}
	if !locked2 {
		return nil, l.lockedErr()
	}
	return l.onAcquired()
}

func (l *RunLock) onAcquired() (func() error, error) {
	if err := l.writeSidecar(); err != nil {
		_ = l.fl.Unlock()
		return nil, err
	}
	return l.makeRelease(), nil
}

func (l *RunLock) tryLock(ctx context.Context, timeout time.Duration) (bool, error) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	locked, err := l.fl.TryLockContext(actx, retryDelay)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return false, nil
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("locks: acquire run lock: %w", err)
	}
	return locked, nil
}

func (l *RunLock) makeRelease() func() error {
	var once sync.Once
	return func() error {
		var uerr error
		once.Do(func() {
			_ = l.removeSidecar()
			uerr = l.fl.Unlock()
		})
		return uerr
	}
}

func (l *RunLock) writeSidecar() error {
	sc := sidecar{PID: os.Getpid(), MachineID: l.machineID, StartedAt: l.now().UTC()}
	return writeSidecarFile(l.root, l.sidecarRel, sc)
}

func (l *RunLock) removeSidecar() error {
	return removeSidecarFile(l.root, l.sidecarRel)
}

func (l *RunLock) lockedErr() error {
	sc, ok := readSidecarFile(l.root, l.sidecarRel)
	if !ok {
		return fmt.Errorf("%w (holder unknown)", ErrRunLocked)
	}
	return fmt.Errorf("%w: held by pid=%d machine=%s since %s",
		ErrRunLocked, sc.PID, shortID(sc.MachineID), sc.StartedAt.Format(time.RFC3339))
}

func (l *RunLock) noticef(format string, args ...any) {
	w := l.notices
	if w == nil {
		w = os.Stderr
	}
	_, _ = fmt.Fprintf(w, "agent-sync: "+format+"\n", args...)
}

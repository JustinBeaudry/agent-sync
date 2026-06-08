package locks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/aienvs/aienvs/internal/fsroot"
	"github.com/aienvs/aienvs/internal/ir"
)

const (
	// DefaultTimeout bounds how long Acquire waits for a contended lock.
	DefaultTimeout = 2 * time.Minute
	// StaleFloor is the minimum age before an orphan sidecar is reported
	// as stale, independent of the (possibly short) acquire timeout — a
	// short caller timeout must not make staleness fire eagerly.
	StaleFloor = 4 * time.Minute

	retryDelay  = 50 * time.Millisecond
	stateDirRel = ".aienv/state"
)

// sidecar is the JSON recorded next to a held target lock. machine_id
// (not hostname) gates reconcile decisions; started_at is cross-checked
// against the lock file's mtime to blunt wall-clock skew.
type sidecar struct {
	PID       int       `json:"pid"`
	MachineID string    `json:"machine_id"`
	StartedAt time.Time `json:"started_at"`
}

// AcquireOpts tunes a single TargetLock.Acquire call.
type AcquireOpts struct {
	// Timeout overrides DefaultTimeout when > 0.
	Timeout time.Duration
	// BreakLock clears a stale sidecar and re-attempts acquisition (the
	// --break-lock escape hatch). With advisory flock a live holder
	// cannot be safely stolen — break only succeeds when the holder is
	// actually gone (its flock already released). Without it, a busy
	// lock returns ErrTargetLocked.
	BreakLock bool
}

// TargetLock is the whole-sync lock for one target. Construct with
// NewTargetLock, then Acquire/release around the sync.
//
// The flock itself must use a real absolute path (os.Root cannot hand a
// raw FD to gofrs/flock), but every other touch of the lock file or its
// sidecar is routed through the fsroot Root (os.Root, which refuses
// symlink traversal) so a symlinked .aienv/state prefix or a symlinked
// lock leaf cannot redirect a write outside the workspace.
type TargetLock struct {
	root       *fsroot.Root
	target     string
	lockRel    string // workspace-relative, for fsroot-routed ops + leaf guard
	sidecarRel string // workspace-relative
	lockPath   string // absolute, for gofrs/flock only
	machineID  string
	now        func() time.Time // injectable clock for tests
	notices    io.Writer        // stale/break notices; defaults to os.Stderr
	fl         *flock.Flock
}

// NewTargetLock validates the target, guards the state-dir prefix
// against symlinks, ensures .aienv/state/ exists, resolves the stable
// machine id, and resolves the lock paths. It opens no lock FD yet.
func NewTargetLock(root *fsroot.Root, target string) (*TargetLock, error) {
	if !ir.IsValidID(target) {
		return nil, fmt.Errorf("locks: invalid target %q", target)
	}
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
	lockRel := stateDirRel + "/" + target + ".lock"
	abs := filepath.Join(root.Path(), filepath.FromSlash(lockRel))
	return &TargetLock{
		root:       root,
		target:     target,
		lockRel:    lockRel,
		sidecarRel: lockRel + ".pid",
		lockPath:   abs,
		machineID:  mid,
		now:        time.Now,
		notices:    os.Stderr,
		fl:         flock.New(abs),
	}, nil
}

// Acquire takes the target lock. On a free lock (the common case, incl.
// a crashed prior holder whose flock the OS already released) it
// succeeds and reconciles any orphan sidecar. On a busy lock — which
// means a live holder, since flock releases on death — it returns
// ErrTargetLocked unless opts.BreakLock clears a stale sidecar and the
// holder turns out to be gone.
func (l *TargetLock) Acquire(ctx context.Context, opts AcquireOpts) (release func() error, err error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	// Guard the lock leaf against a pre-planted symlink before flock
	// opens it (gofrs/flock follows symlinks; os.Root-routed Lstat does
	// not). The prefix was guarded at construction; re-check here so a
	// swap in the construct→Acquire window is also caught.
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

	// Contended: a live holder (flock releases on death). BreakLock
	// clears a stale sidecar and retries the SAME flock handle — it
	// never unlinks-and-recreates (that would make a new inode and let
	// a still-live holder and the breaker both "hold" the lock). So a
	// genuinely live holder is never stolen; only an already-released
	// lock with a lingering sidecar is reclaimed.
	if !opts.BreakLock {
		return nil, l.lockedErr()
	}
	l.noticef("--break-lock: clearing stale sidecar and retrying target %q", l.target)
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

// onAcquired reconciles an orphan sidecar, writes our sidecar, and
// returns the release closure. Called only when the flock is held.
func (l *TargetLock) onAcquired() (func() error, error) {
	l.reconcileOrphanSidecar()
	if err := l.writeSidecar(); err != nil {
		_ = l.fl.Unlock()
		return nil, err
	}
	return l.makeRelease(), nil
}

func (l *TargetLock) tryLock(ctx context.Context, timeout time.Duration) (bool, error) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	locked, err := l.fl.TryLockContext(actx, retryDelay)
	if err != nil {
		// Distinguish our bounded-timeout (contended) from a parent
		// context cancellation (caller shutdown), which must propagate.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return false, nil
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("locks: acquire target %q: %w", l.target, err)
	}
	return locked, nil
}

func (l *TargetLock) makeRelease() func() error {
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

// reconcileOrphanSidecar emits a stale-sidecar notice when a
// pre-existing sidecar looks stale. The lock is already held, so the
// sidecar is advisory; writeSidecar overwrites it next.
func (l *TargetLock) reconcileOrphanSidecar() {
	sc, ok := l.readSidecar()
	if !ok {
		return
	}
	if l.sidecarLooksStale(sc) {
		l.noticef("reclaimed stale lock sidecar on target %q (prior pid=%d machine=%s)",
			l.target, sc.PID, shortID(sc.MachineID))
	}
}

// sidecarLooksStale classifies a pre-existing sidecar: a foreign
// machine, a dead PID on our machine, or an age beyond StaleFloor.
func (l *TargetLock) sidecarLooksStale(sc sidecar) bool {
	if sc.MachineID != l.machineID {
		return true
	}
	if !processAlive(sc.PID) {
		return true
	}
	return l.sidecarAge(sc) > StaleFloor
}

func (l *TargetLock) sidecarAge(sc sidecar) time.Duration {
	now := l.now()
	age := now.Sub(sc.StartedAt)
	if fi, err := l.root.Inner().Stat(l.lockRel); err == nil {
		if mAge := now.Sub(fi.ModTime()); mAge > age {
			age = mAge
		}
	}
	return age
}

func (l *TargetLock) readSidecar() (sidecar, bool) {
	f, err := l.root.Inner().Open(l.sidecarRel)
	if err != nil {
		return sidecar{}, false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		return sidecar{}, false
	}
	var sc sidecar
	if err := json.Unmarshal(b, &sc); err != nil {
		return sidecar{}, false
	}
	return sc, true
}

func (l *TargetLock) writeSidecar() error {
	sc := sidecar{PID: os.Getpid(), MachineID: l.machineID, StartedAt: l.now().UTC()}
	b, err := json.Marshal(sc)
	if err != nil {
		return fmt.Errorf("locks: marshal sidecar: %w", err)
	}
	// Route through os.Root (refuses symlinks); O_TRUNC so a reclaimed
	// orphan sidecar is overwritten cleanly.
	f, err := l.root.Inner().OpenFile(l.sidecarRel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("locks: open sidecar: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("locks: write sidecar: %w", err)
	}
	return f.Close()
}

func (l *TargetLock) removeSidecar() error {
	err := l.root.Inner().Remove(l.sidecarRel)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (l *TargetLock) lockedErr() error {
	sc, ok := l.readSidecar()
	if !ok {
		return fmt.Errorf("%w: target %q (holder unknown)", ErrTargetLocked, l.target)
	}
	return fmt.Errorf("%w: target %q held by pid=%d machine=%s since %s",
		ErrTargetLocked, l.target, sc.PID, shortID(sc.MachineID), sc.StartedAt.Format(time.RFC3339))
}

func (l *TargetLock) noticef(format string, args ...any) {
	w := l.notices
	if w == nil {
		w = os.Stderr
	}
	_, _ = fmt.Fprintf(w, "aienvs: "+format+"\n", args...)
}

// guardStatePrefix refuses when any guarded state-dir component is a
// symlink / reparse point. It Lstats each segment through the fsroot
// Root (which detects reparse points) before any absolute lock path is
// opened, so a symlinked prefix cannot redirect a lock file outside the
// workspace (KTD 5). Extra segments (e.g. the filelocks subdir) are
// checked when supplied. A not-yet-existing segment is fine.
func guardStatePrefix(root *fsroot.Root, extra ...string) error {
	segs := append([]string{".aienv", stateDirRel}, extra...)
	for _, seg := range segs {
		if err := guardLeaf(root, seg); err != nil {
			return err
		}
	}
	return nil
}

// guardLeaf refuses when relPath exists and is a symlink. Used on the
// lock-file leaves (which gofrs/flock opens with symlink-following) so
// a pre-planted symlink there cannot redirect the FD outside the
// workspace. A residual construct→open TOCTOU window remains (same-uid,
// own-workspace threat model); the guard closes the pre-planted case.
func guardLeaf(root *fsroot.Root, relPath string) error {
	fi, err := root.Lstat(relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("locks: stat %s: %w", relPath, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrUnsafeStatePrefix, relPath)
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

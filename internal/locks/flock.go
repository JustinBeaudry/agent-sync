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

// AcquireOpts tunes a single Acquire call.
type AcquireOpts struct {
	// Timeout overrides DefaultTimeout when > 0.
	Timeout time.Duration
	// BreakLock forces a contended lock to be broken (the --break-lock
	// escape hatch). Without it, a busy lock returns ErrTargetLocked.
	BreakLock bool
}

// TargetLock is the whole-sync lock for one target. Construct with
// NewTargetLock, then Acquire/release around the sync.
type TargetLock struct {
	target      string
	lockPath    string // absolute
	sidecarPath string // absolute
	machineID   string
	now         func() time.Time // injectable clock for tests
	notices     io.Writer        // stale/break notices; defaults to os.Stderr
	fl          *flock.Flock
}

// NewTargetLock validates the target, guards the state-dir prefix
// against symlinks, ensures .aienv/state/ exists, resolves the stable
// machine id, and resolves the absolute lock paths. It opens no lock
// FD yet — that happens in Acquire.
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
	base := filepath.Join(root.Path(), filepath.FromSlash(stateDirRel))
	lockPath := filepath.Join(base, target+".lock")
	return &TargetLock{
		target:      target,
		lockPath:    lockPath,
		sidecarPath: lockPath + ".pid",
		machineID:   mid,
		now:         time.Now,
		notices:     os.Stderr,
		fl:          flock.New(lockPath),
	}, nil
}

// Acquire takes the target lock. On a free lock (the common case, incl.
// a crashed prior holder whose flock the OS already released) it
// succeeds and reconciles any orphan sidecar. On a busy lock — which
// means a live holder, since flock releases on death — it returns
// ErrTargetLocked unless opts.BreakLock forces a break.
func (l *TargetLock) Acquire(ctx context.Context, opts AcquireOpts) (release func() error, err error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	locked, err := l.tryLock(ctx, timeout)
	if err != nil {
		return nil, err
	}
	if locked {
		l.reconcileOrphanSidecar()
		if werr := l.writeSidecar(); werr != nil {
			_ = l.fl.Unlock()
			return nil, werr
		}
		return l.makeRelease(), nil
	}

	// Contended: a live holder. Only an explicit break overrides.
	if !opts.BreakLock {
		return nil, l.lockedErr()
	}
	l.noticef("forcibly breaking lock on target %q (%s) per --break-lock", l.target, l.lockPath)
	_ = os.Remove(l.lockPath)
	_ = os.Remove(l.sidecarPath)
	l.fl = flock.New(l.lockPath)
	locked2, err := l.tryLock(ctx, timeout)
	if err != nil {
		return nil, err
	}
	if !locked2 {
		return nil, l.lockedErr()
	}
	if werr := l.writeSidecar(); werr != nil {
		_ = l.fl.Unlock()
		return nil, werr
	}
	return l.makeRelease(), nil
}

func (l *TargetLock) tryLock(ctx context.Context, timeout time.Duration) (bool, error) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	locked, err := l.fl.TryLockContext(actx, retryDelay)
	if err != nil {
		// A deadline-exceeded from our bounded context is "contended",
		// not a hard error; surface other errors (e.g. parent ctx
		// cancelled by the caller) up.
		if errors.Is(err, context.DeadlineExceeded) {
			return false, nil
		}
		return false, fmt.Errorf("locks: acquire target %q: %w", l.target, err)
	}
	return locked, nil
}

func (l *TargetLock) makeRelease() func() error {
	return func() error {
		_ = os.Remove(l.sidecarPath)
		return l.fl.Unlock()
	}
}

// reconcileOrphanSidecar emits a stale-sidecar notice when a
// pre-existing sidecar looks stale (foreign machine, dead PID on our
// machine, or older than StaleFloor). The lock is already held, so the
// sidecar is advisory; writeSidecar overwrites it next. Best-effort —
// a missing/unreadable sidecar is the normal fresh case.
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

// sidecarLooksStale classifies a pre-existing sidecar. A foreign
// machine_id is treated as stale-for-notice only (we hold the lock, so
// the foreign holder is gone); a same-machine sidecar is stale if its
// PID is dead or its age exceeds StaleFloor (age cross-checks
// started_at against the lock file mtime to blunt clock skew).
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
	if fi, err := os.Stat(l.lockPath); err == nil {
		if mAge := now.Sub(fi.ModTime()); mAge > age {
			age = mAge
		}
	}
	return age
}

func (l *TargetLock) readSidecar() (sidecar, bool) {
	b, err := os.ReadFile(l.sidecarPath)
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
	if err := os.WriteFile(l.sidecarPath, b, 0o600); err != nil {
		return fmt.Errorf("locks: write sidecar: %w", err)
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

// guardStatePrefix refuses when .aienv or .aienv/state is a symlink /
// reparse point. It Lstats each segment through the fsroot Root (which
// detects reparse points) before any absolute lock path is opened, so
// a symlinked prefix cannot redirect a lock file outside the workspace
// (KTD 5). A not-yet-existing segment is fine (first sync creates real
// dirs).
func guardStatePrefix(root *fsroot.Root) error {
	for _, seg := range []string{".aienv", stateDirRel} {
		fi, err := root.Lstat(seg)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("locks: stat %s: %w", seg, err)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrUnsafeStatePrefix, seg)
		}
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

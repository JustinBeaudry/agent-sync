// Package locks provides cross-platform file locking primitives for aienvs.
//
// There are two kinds of lock:
//
//  1. Per-target lock — one lock per (workspace, target). Held for the
//     entire duration of a sync for that target. Lives at
//     <workspace>/.aienv/state/<target>.lock with a PID sidecar at
//     <workspace>/.aienv/state/<target>.lock.pid.
//
//  2. Per-file lock — one lock per external file (AGENTS.md, .mcp.json, …).
//     Held only during the read-merge-write window inside Unit 12a. Lives at
//     <file>.aienvs-flock (sibling of the external file) and is tracked in
//     an in-process registry to avoid re-entrant acquisition from the same
//     process.
package locks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// DefaultTargetLockTimeout is the maximum duration AcquireTarget will
// wait before returning ErrLockTimeout.
const DefaultTargetLockTimeout = 2 * time.Minute

// DefaultRetryInterval is the polling interval used while waiting for a
// lock to become available.
const DefaultRetryInterval = 100 * time.Millisecond

// StaleLockMultiplier controls the stale-lock threshold: if the sidecar's
// started_at is older than StaleLockMultiplier × timeout and the PID is
// dead, the lock is auto-broken.
const StaleLockMultiplier = 2

// Sentinel errors.
var (
	// ErrLockTimeout is returned when the lock could not be acquired within
	// the allowed duration and the existing holder is a live process.
	ErrLockTimeout = errors.New("lock timeout: another sync is running")

	// ErrLockHeldByLiveProcess is returned when the caller passed
	// BreakLock: false and the PID in the sidecar is still alive.
	ErrLockHeldByLiveProcess = errors.New("lock held by a live process; rerun with --break-lock to force")

	// ErrStaleLockBroken is a non-fatal informational sentinel; it is
	// wrapped into the error returned alongside a successful lock when
	// a stale lock was auto-broken. Callers may log it.
	ErrStaleLockBroken = errors.New("stale lock was auto-broken")
)

// TargetLockOptions controls AcquireTarget behaviour.
type TargetLockOptions struct {
	// Timeout is how long to wait. Zero means DefaultTargetLockTimeout.
	Timeout time.Duration

	// RetryInterval is the polling interval. Zero means DefaultRetryInterval.
	RetryInterval time.Duration

	// BreakLock, when true, breaks a lock held by a dead process even if the
	// sidecar timestamp is newer than StaleLockMultiplier × Timeout. It still
	// refuses if the PID is alive.
	BreakLock bool
}

// TargetLock is the held lock returned by AcquireTarget. Call Release to
// release the filesystem lock; the method is safe to call on a nil
// TargetLock (no-op).
type TargetLock struct {
	fl      *flock.Flock
	pidPath string
}

// Release releases the target lock and removes the PID sidecar.
func (tl *TargetLock) Release() error {
	if tl == nil || tl.fl == nil {
		return nil
	}
	_ = os.Remove(tl.pidPath)
	return tl.fl.Unlock()
}

// pidSidecar is the JSON shape written to <target>.lock.pid.
type pidSidecar struct {
	PID       int       `json:"pid"`
	Host      string    `json:"host"`
	StartedAt time.Time `json:"started_at"`
}

// lockStatePath returns the directory that holds lock and ledger files.
func lockStatePath(workspaceRoot, target string) (lockFile, pidFile string) {
	stateDir := filepath.Join(workspaceRoot, ".aienv", "state")
	lockFile = filepath.Join(stateDir, target+".lock")
	pidFile = filepath.Join(stateDir, target+".lock.pid")
	return lockFile, pidFile
}

// AcquireTarget acquires the per-target lock for (workspaceRoot, target).
//
// It creates <workspaceRoot>/.aienv/state/ if needed, then attempts
// TryLockContext within opts.Timeout. If the lock is held by a stale
// process it is auto-broken (with an ErrStaleLockBroken warning wrapped in
// the returned error chain, but a non-nil TargetLock is still returned).
func AcquireTarget(ctx context.Context, workspaceRoot, target string, opts TargetLockOptions) (*TargetLock, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTargetLockTimeout
	}
	retryInterval := opts.RetryInterval
	if retryInterval == 0 {
		retryInterval = DefaultRetryInterval
	}

	stateDir := filepath.Join(workspaceRoot, ".aienv", "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("locks: mkdir %s: %w", stateDir, err)
	}

	lockFile, pidFile := lockStatePath(workspaceRoot, target)
	fl := flock.New(lockFile)

	// Try a non-blocking lock first. If that fails, inspect the sidecar
	// to decide whether the holder is stale.
	locked, err := fl.TryLock()
	if err != nil {
		return nil, fmt.Errorf("locks: try-lock %s: %w", lockFile, err)
	}

	if !locked {
		// Check sidecar.
		staleBroken, sidecarErr := maybeBreakStaleLock(fl, pidFile, timeout, opts.BreakLock)
		if sidecarErr != nil {
			// Could not determine liveness; fall through to blocking wait.
			_ = sidecarErr
		} else if staleBroken {
			// Lock was broken; re-acquire.
			locked, err = fl.TryLock()
			if err != nil || !locked {
				return nil, fmt.Errorf("locks: re-acquire after stale break %s: %w", lockFile, err)
			}
			if err := writePIDSidecar(pidFile); err != nil {
				_ = fl.Unlock()
				return nil, err
			}
			return &TargetLock{fl: fl, pidPath: pidFile}, fmt.Errorf("%w", ErrStaleLockBroken)
		}

		// Block until timeout.
		deadline := time.Now().Add(timeout)
		ctx2, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()

		locked, err = fl.TryLockContext(ctx2, retryInterval)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("%w: %s", ErrLockTimeout, lockFile)
			}
			return nil, fmt.Errorf("locks: acquire %s: %w", lockFile, err)
		}
		if !locked {
			return nil, fmt.Errorf("%w: %s", ErrLockTimeout, lockFile)
		}
	}

	if err := writePIDSidecar(pidFile); err != nil {
		_ = fl.Unlock()
		return nil, err
	}

	return &TargetLock{fl: fl, pidPath: pidFile}, nil
}

// writePIDSidecar writes the current process's PID + hostname + timestamp.
func writePIDSidecar(pidFile string) error {
	host, _ := os.Hostname()
	sc := pidSidecar{
		PID:       os.Getpid(),
		Host:      host,
		StartedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(sc)
	if err != nil {
		return fmt.Errorf("locks: marshal pid sidecar: %w", err)
	}
	if err := os.WriteFile(pidFile, data, 0o600); err != nil {
		return fmt.Errorf("locks: write pid sidecar %s: %w", pidFile, err)
	}
	return nil
}

// maybeBreakStaleLock reads the sidecar and, if the PID is dead (or
// missing) and either the lock is old enough or breakLock is true,
// removes the lock file and returns staleBroken=true.
//
// If the PID is alive it returns (false, ErrLockHeldByLiveProcess) to
// distinguish "we should break it" from "we should wait".
func maybeBreakStaleLock(fl *flock.Flock, pidFile string, timeout time.Duration, breakLock bool) (bool, error) {
	data, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			// No sidecar — lock is orphaned. Always break.
			if rerr := os.Remove(fl.Path()); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				return false, fmt.Errorf("locks: remove orphaned lock %s: %w", fl.Path(), rerr)
			}
			return true, nil
		}
		return false, readErr
	}

	var sc pidSidecar
	if unmarshalErr := json.Unmarshal(data, &sc); unmarshalErr != nil {
		// Corrupt sidecar: treat as orphaned. unmarshalErr is intentionally
		// discarded — we handled it by removing the files; nil is the correct
		// second return (no error in the break operation itself).
		_ = os.Remove(fl.Path())
		_ = os.Remove(pidFile)
		return true, nil //nolint:nilerr
	}

	alive := isPIDAlive(sc.PID)
	if alive {
		return false, fmt.Errorf("%w: pid %d host %s started %s",
			ErrLockHeldByLiveProcess, sc.PID, sc.Host, sc.StartedAt.Format(time.RFC3339))
	}

	// PID is dead.
	staleThreshold := StaleLockMultiplier * timeout
	isOld := time.Since(sc.StartedAt) > staleThreshold

	if !isOld && !breakLock {
		// Dead but too recent — respect BreakLock flag.
		return false, fmt.Errorf("%w: pid %d (dead) started %s — rerun with --break-lock",
			ErrLockHeldByLiveProcess, sc.PID, sc.StartedAt.Format(time.RFC3339))
	}

	_ = os.Remove(fl.Path())
	_ = os.Remove(pidFile)
	return true, nil
}

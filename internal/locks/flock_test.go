package locks_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aienvs/aienvs/internal/locks"
)

func TestAcquireTarget_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	lock, err := locks.AcquireTarget(context.Background(), dir, "claude", locks.TargetLockOptions{})
	if err != nil {
		t.Fatalf("AcquireTarget: %v", err)
	}
	if lock == nil {
		t.Fatal("expected non-nil lock")
	}

	// PID sidecar must exist.
	pidPath := filepath.Join(dir, ".aienv", "state", "claude.lock.pid")
	if _, err := os.Stat(pidPath); err != nil {
		t.Errorf("pid sidecar not found: %v", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Sidecar removed after release.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("pid sidecar should be removed after Release")
	}
}

func TestAcquireTarget_DoubleLock_Timeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := locks.AcquireTarget(context.Background(), dir, "claude", locks.TargetLockOptions{})
	if err != nil {
		t.Fatalf("first AcquireTarget: %v", err)
	}
	defer first.Release()

	// Second acquisition must time out quickly.
	_, err = locks.AcquireTarget(context.Background(), dir, "claude", locks.TargetLockOptions{
		Timeout:       200 * time.Millisecond,
		RetryInterval: 20 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestAcquireTarget_DifferentTargets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	l1, err := locks.AcquireTarget(context.Background(), dir, "claude", locks.TargetLockOptions{})
	if err != nil {
		t.Fatalf("lock claude: %v", err)
	}
	defer l1.Release()

	l2, err := locks.AcquireTarget(context.Background(), dir, "cursor", locks.TargetLockOptions{})
	if err != nil {
		t.Fatalf("lock cursor: %v", err)
	}
	defer l2.Release()
}

func TestAcquireTarget_ContextCancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := locks.AcquireTarget(context.Background(), dir, "claude", locks.TargetLockOptions{})
	if err != nil {
		t.Fatalf("first AcquireTarget: %v", err)
	}
	defer first.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = locks.AcquireTarget(ctx, dir, "claude", locks.TargetLockOptions{
		RetryInterval: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestAcquireTarget_StaleLock_NoPID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".aienv", "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Write a lock file with no sidecar (simulates an orphaned lock from a
	// previous run that never wrote a sidecar).
	lockPath := filepath.Join(stateDir, "claude.lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// Should acquire successfully since there's no sidecar → orphaned lock.
	lock, err := locks.AcquireTarget(context.Background(), dir, "claude", locks.TargetLockOptions{
		Timeout: 500 * time.Millisecond,
	})
	if err != nil && !errors.Is(err, locks.ErrStaleLockBroken) {
		t.Fatalf("unexpected error: %v", err)
	}
	if lock != nil {
		lock.Release()
	}
}

func TestRelease_NilSafe(t *testing.T) {
	t.Parallel()
	var tl *locks.TargetLock
	if err := tl.Release(); err != nil {
		t.Errorf("Release on nil: %v", err)
	}
}

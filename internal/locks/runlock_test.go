package locks

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunLock_HappyPath(t *testing.T) {
	root := openRoot(t)
	l, err := NewRunLock(root)
	if err != nil {
		t.Fatalf("NewRunLock: %v", err)
	}
	release, err := l.Acquire(context.Background(), AcquireOpts{})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Sidecar is removed on release, and the lock can be re-acquired.
	l2, err := NewRunLock(root)
	if err != nil {
		t.Fatalf("NewRunLock (2): %v", err)
	}
	release2, err := l2.Acquire(context.Background(), AcquireOpts{})
	if err != nil {
		t.Fatalf("re-Acquire after release: %v", err)
	}
	_ = release2()
}

func TestRunLock_ContendedReturnsLocked(t *testing.T) {
	root := openRoot(t)
	first, err := NewRunLock(root)
	if err != nil {
		t.Fatalf("NewRunLock: %v", err)
	}
	release, err := first.Acquire(context.Background(), AcquireOpts{})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = release() }()

	second, err := NewRunLock(root)
	if err != nil {
		t.Fatalf("NewRunLock (2): %v", err)
	}
	_, err = second.Acquire(context.Background(), AcquireOpts{Timeout: 200 * time.Millisecond})
	if !errors.Is(err, ErrRunLocked) {
		t.Fatalf("second Acquire err = %v, want ErrRunLocked", err)
	}
}

func TestRunLock_UnsafePrefixRefused(t *testing.T) {
	root := openRoot(t)
	// A symlinked .agent-sync/state prefix must be refused before any FD opens.
	if err := root.Inner().MkdirAll(".agent-sync", 0o755); err != nil {
		t.Fatal(err)
	}
	// Symlink .agent-sync/state -> /tmp (outside the workspace).
	if err := root.Inner().Symlink("/tmp", ".agent-sync/state"); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := NewRunLock(root); !errors.Is(err, ErrUnsafeStatePrefix) {
		t.Fatalf("NewRunLock err = %v, want ErrUnsafeStatePrefix", err)
	}
}

package locks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aienvs/aienvs/internal/fsroot"
)

func openRoot(t *testing.T) *fsroot.Root {
	t.Helper()
	root, err := fsroot.OpenWorkspaceRoot(t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func newLockWithBuf(t *testing.T, root *fsroot.Root, target string) (*TargetLock, *bytes.Buffer) {
	t.Helper()
	l, err := NewTargetLock(root, target)
	if err != nil {
		t.Fatalf("NewTargetLock: %v", err)
	}
	buf := &bytes.Buffer{}
	l.notices = buf
	return l, buf
}

func TestTargetLock_HappyPath(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	l, buf := newLockWithBuf(t, root, "claude")

	release, err := l.Acquire(context.Background(), AcquireOpts{})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Sidecar written with our pid + machine id.
	sc, ok := l.readSidecar()
	if !ok {
		t.Fatal("sidecar not written")
	}
	if sc.PID != os.Getpid() || sc.MachineID != l.machineID {
		t.Errorf("sidecar=%+v want pid=%d machine=%s", sc, os.Getpid(), l.machineID)
	}
	if buf.Len() != 0 {
		t.Errorf("fresh acquire should emit no notice; got %q", buf.String())
	}
	// Release unlocks and removes the sidecar.
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(l.sidecarPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sidecar should be removed on release; stat err=%v", err)
	}
}

func TestTargetLock_ReconcilesOrphanSidecar(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	l, buf := newLockWithBuf(t, root, "claude")

	// Pre-write an orphan sidecar: a dead PID, old timestamp, our
	// machine. No real flock is held (the crashed holder's flock was
	// auto-released by the OS) — so Acquire succeeds on the success
	// path and reconciles the orphan.
	writeRawSidecar(t, l.sidecarPath, sidecar{
		PID:       deadPID(),
		MachineID: l.machineID,
		StartedAt: time.Now().Add(-10 * time.Minute).UTC(),
	})

	release, err := l.Acquire(context.Background(), AcquireOpts{})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = release() }()

	if !strings.Contains(buf.String(), "stale lock sidecar") {
		t.Errorf("expected stale-sidecar notice; got %q", buf.String())
	}
	sc, _ := l.readSidecar()
	if sc.PID != os.Getpid() {
		t.Errorf("orphan sidecar not overwritten; pid=%d", sc.PID)
	}
}

func TestTargetLock_ForeignMachineSidecarNoticed(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	l, buf := newLockWithBuf(t, root, "claude")
	writeRawSidecar(t, l.sidecarPath, sidecar{
		PID:       os.Getpid(), // alive locally, but...
		MachineID: "ffffffffffffffffffffffffffffffff",
		StartedAt: time.Now().UTC(), // ...fresh
	})
	release, err := l.Acquire(context.Background(), AcquireOpts{})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = release() }()
	if !strings.Contains(buf.String(), "stale lock sidecar") {
		t.Errorf("foreign machine_id should be reconciled with a notice; got %q", buf.String())
	}
}

func TestTargetLock_ContendedReturnsLocked(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	first, _ := newLockWithBuf(t, root, "claude")
	release, err := first.Acquire(context.Background(), AcquireOpts{})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = release() }()

	second, _ := newLockWithBuf(t, root, "claude")
	_, err = second.Acquire(context.Background(), AcquireOpts{Timeout: 200 * time.Millisecond})
	if !errors.Is(err, ErrTargetLocked) {
		t.Fatalf("second Acquire err=%v want ErrTargetLocked", err)
	}
	// The locked error should name the holder pid.
	if !strings.Contains(err.Error(), "pid=") {
		t.Errorf("locked error should name holder; got %q", err.Error())
	}
}

func TestTargetLock_ShortTimeoutDoesNotShrinkStaleFloor(t *testing.T) {
	t.Parallel()
	// A 1s timeout must not make a recently-acquired lock's sidecar
	// look stale. We assert the StaleFloor classification independent
	// of the acquire timeout via sidecarLooksStale on a fresh sidecar.
	root := openRoot(t)
	l, _ := newLockWithBuf(t, root, "claude")
	fresh := sidecar{PID: os.Getpid(), MachineID: l.machineID, StartedAt: time.Now().UTC()}
	if l.sidecarLooksStale(fresh) {
		t.Error("a fresh same-machine live-pid sidecar must not be classified stale")
	}
}

func TestTargetLock_ClockSkewUsesMtimeCrossCheck(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	l, _ := newLockWithBuf(t, root, "claude")
	// started_at in the FUTURE (negative age) but live pid + our
	// machine: must not be classified stale (age clamps via mtime,
	// never goes stale on a future timestamp alone).
	future := sidecar{PID: os.Getpid(), MachineID: l.machineID, StartedAt: time.Now().Add(time.Hour).UTC()}
	if l.sidecarLooksStale(future) {
		t.Error("future started_at on a live same-machine sidecar must not be stale")
	}
}

func TestTargetLock_UnsafePrefixRefused(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	// Make .aienv a symlink to a sibling dir.
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root.Path(), ".aienv")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, err := NewTargetLock(root, "claude")
	if !errors.Is(err, ErrUnsafeStatePrefix) {
		t.Fatalf("err=%v want ErrUnsafeStatePrefix", err)
	}
}

func TestTargetLock_ForceBreak(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	first, _ := newLockWithBuf(t, root, "claude")
	release, err := first.Acquire(context.Background(), AcquireOpts{})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = release() }()

	second, buf := newLockWithBuf(t, root, "claude")
	rel2, err := second.Acquire(context.Background(), AcquireOpts{Timeout: 200 * time.Millisecond, BreakLock: true})
	if err != nil {
		t.Fatalf("force-break Acquire: %v", err)
	}
	defer func() { _ = rel2() }()
	if !strings.Contains(buf.String(), "forcibly breaking") {
		t.Errorf("expected force-break notice; got %q", buf.String())
	}
}

func TestTargetLock_InvalidTarget(t *testing.T) {
	t.Parallel()
	root := openRoot(t)
	if _, err := NewTargetLock(root, "../escape"); err == nil {
		t.Error("invalid target must be rejected")
	}
}

func TestProcessAlive(t *testing.T) {
	t.Parallel()
	if !processAlive(os.Getpid()) {
		t.Error("current process must be alive")
	}
	if processAlive(deadPID()) {
		t.Error("a definitely-unused PID must not be alive")
	}
	if processAlive(0) || processAlive(-1) {
		t.Error("non-positive PIDs are never alive")
	}
}

// --- helpers ---

func writeRawSidecar(t *testing.T, path string, sc sidecar) {
	t.Helper()
	b, err := json.Marshal(sc)
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write raw sidecar: %v", err)
	}
}

// deadPID returns a PID that is almost certainly not a live process.
// PIDs near the 32-bit max are not assigned by mainstream OSes.
func deadPID() int { return 0x7FFF_FFF0 }

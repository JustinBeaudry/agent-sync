//go:build unix

package fsroot_test

import (
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/aienvs/aienvs/internal/fsroot"
)

// TestDetectReparsePoint_SocketRefused creates a Unix-domain socket file
// inside the root and verifies DetectReparsePoint classifies it as
// irregular. This exercises the ModeSocket branch (ModeIrregular on some
// filesystems, ModeSocket on most) without needing to construct a
// platform-specific reparse point or FIFO.
func TestDetectReparsePoint_SocketRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// A sockaddr_un path on Linux is typically capped at 108 bytes; keep
	// the filename short and rely on t.TempDir being bounded.
	sockPath := filepath.Join(dir, "s")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("net.Listen(unix): %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	r := mustOpenRoot(t, dir)
	t.Cleanup(func() { _ = r.Close() })

	err = r.DetectReparsePoint("s")
	if !errors.Is(err, fsroot.ErrIrregular) {
		t.Fatalf("DetectReparsePoint(unix-socket) = %v, want wraps ErrIrregular", err)
	}

	// StagedWrite must also refuse to write through the socket target.
	err = r.StagedWrite("s", []byte("x"), 0o644)
	if !errors.Is(err, fsroot.ErrIrregular) {
		t.Fatalf("StagedWrite over socket = %v, want wraps ErrIrregular", err)
	}
}

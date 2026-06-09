//go:build windows

package fsroot_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

// TestDetectReparsePoint_JunctionRefused creates a directory junction
// inside the root and verifies DetectReparsePoint refuses it. Junctions
// are distinct from Windows symlinks: they carry IO_REPARSE_TAG_MOUNT_POINT,
// do not require SeCreateSymbolicLinkPrivilege, and surface in Go as
// fs.ModeIrregular (+ sometimes fs.ModeDir) via os.Lstat.
//
// The test shells out to `cmd /c mklink /J` because Go's stdlib does not
// expose junction creation. We skip the test if mklink is unavailable
// (for example, when cmd.exe is not on PATH in a minimal CI container).
func TestDetectReparsePoint_JunctionRefused(t *testing.T) {
	t.Parallel()
	_, err := exec.LookPath("cmd")
	if err != nil {
		t.Skipf("cmd.exe not available: %v", err)
	}

	target := t.TempDir()
	rootDir := t.TempDir()
	junction := filepath.Join(rootDir, "junk")

	out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput()
	if err != nil {
		t.Skipf("mklink /J failed (elevation or policy): %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = os.RemoveAll(junction) })

	r := mustOpenRoot(t, rootDir)
	t.Cleanup(func() { _ = r.Close() })

	if err := r.DetectReparsePoint("junk"); !errors.Is(err, fsroot.ErrIrregular) {
		t.Fatalf("DetectReparsePoint(junction) = %v, want wraps ErrIrregular", err)
	}

	if err := r.StagedWrite("junk", []byte("nope"), 0o644); !errors.Is(err, fsroot.ErrIrregular) {
		t.Fatalf("StagedWrite(junction) = %v, want wraps ErrIrregular", err)
	}
}

package fsroot_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

func TestDetectReparsePoint_RegularFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r := mustOpenRoot(t, dir)
	defer r.Close()

	if err := r.DetectReparsePoint("plain"); err != nil {
		t.Errorf("DetectReparsePoint(regular) = %v, want nil", err)
	}
}

func TestDetectReparsePoint_Directory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	r := mustOpenRoot(t, dir)
	defer r.Close()

	if err := r.DetectReparsePoint("sub"); err != nil {
		t.Errorf("DetectReparsePoint(dir) = %v, want nil", err)
	}
}

func TestDetectReparsePoint_Missing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := mustOpenRoot(t, dir)
	defer r.Close()

	err := r.DetectReparsePoint("nope")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("DetectReparsePoint(missing) = %v, want wraps fs.ErrNotExist", err)
	}
}

func TestDetectReparsePoint_SymlinkAllowed(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation on Windows requires elevated privileges")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := mustOpenRoot(t, dir)
	defer r.Close()

	if err := r.DetectReparsePoint("link"); err != nil {
		t.Errorf("DetectReparsePoint(symlink) = %v, want nil (symlinks allowed)", err)
	}
}

func TestDetectReparsePoint_UnsafeRel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := mustOpenRoot(t, dir)
	defer r.Close()

	err := r.DetectReparsePoint("../x")
	if !errors.Is(err, fsroot.ErrUnsafeRelPath) {
		t.Errorf("DetectReparsePoint(../x) = %v, want wraps ErrUnsafeRelPath", err)
	}
}

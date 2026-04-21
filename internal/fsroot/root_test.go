package fsroot_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aienvs/aienvs/internal/fsroot"
)

func TestOpenWorkspaceRoot_Succeeds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, err := fsroot.OpenWorkspaceRoot(dir)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot(%q): %v", dir, err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if got, want := r.Path(), mustAbs(t, dir); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
	if r.Inner() == nil {
		t.Error("Inner() == nil, want non-nil")
	}
}

func TestOpenWorkspaceRoot_EmptyDir(t *testing.T) {
	t.Parallel()
	_, err := fsroot.OpenWorkspaceRoot("")
	if !errors.Is(err, fsroot.ErrUnsafeRelPath) {
		t.Fatalf("OpenWorkspaceRoot(\"\") err = %v, want wraps ErrUnsafeRelPath", err)
	}
}

func TestOpenWorkspaceRoot_MissingDir(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := fsroot.OpenWorkspaceRoot(missing)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenWorkspaceRoot(missing) err = %v, want wraps fs.ErrNotExist", err)
	}
}

func TestOpenWorkspaceRoot_FileNotDir(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	_, err := fsroot.OpenWorkspaceRoot(f)
	if err == nil {
		t.Fatal("OpenWorkspaceRoot(file) err = nil, want error")
	}
}

func TestRoot_CloseNil(t *testing.T) {
	t.Parallel()
	var r *fsroot.Root
	if err := r.Close(); err != nil {
		t.Errorf("(*fsroot.Root)(nil).Close() = %v, want nil", err)
	}
}

func TestValidateRelPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"empty", "", true},
		{"dot", ".", false},
		{"simple", "file.txt", false},
		{"nested", "a/b/c.txt", false},
		{"trailing-slash", "a/b/", false},
		{"dot-segment", "a/./b", false},
		{"parent-segment", "a/../b", true},
		{"leading-parent", "../b", true},
		{"lone-parent", "..", true},
		{"absolute-unix", "/etc/passwd", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := fsroot.ValidateRelPath(tc.in)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateRelPath(%q) = nil, want error", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateRelPath(%q) = %v, want nil", tc.in, err)
			}
			if tc.wantErr && err != nil && !errors.Is(err, fsroot.ErrUnsafeRelPath) {
				t.Errorf("ValidateRelPath(%q) = %v, want wraps ErrUnsafeRelPath", tc.in, err)
			}
		})
	}
}

func TestRoot_Lstat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r, err := fsroot.OpenWorkspaceRoot(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	fi, err := r.Lstat("hello.txt")
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if fi.Name() != "hello.txt" {
		t.Errorf("Lstat name = %q, want %q", fi.Name(), "hello.txt")
	}

	if _, err := r.Lstat("../outside"); err == nil {
		t.Error("Lstat(../outside) err = nil, want error")
	} else if !errors.Is(err, fsroot.ErrUnsafeRelPath) {
		t.Errorf("Lstat(../outside) err = %v, want wraps ErrUnsafeRelPath", err)
	}
}

// mustAbs is a small helper to avoid spreading filepath.Abs calls across tests.
func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", p, err)
	}
	// Some OSes add a trailing separator via clean; trim for equality checks.
	return strings.TrimSuffix(abs, string(os.PathSeparator))
}

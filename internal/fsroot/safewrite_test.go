package fsroot_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

func TestStagedWrite_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := mustOpenRoot(t, dir)
	defer r.Close()

	payload := []byte("hello agent-sync\n")
	if err := r.StagedWrite("note.txt", payload, 0o644); err != nil {
		t.Fatalf("StagedWrite: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("written bytes = %q, want %q", got, payload)
	}

	// No temp files should survive a successful write.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "agent-sync-stage") {
			t.Errorf("orphaned temp after success: %q", e.Name())
		}
	}
}

func TestStagedWrite_AtomicReplace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := mustOpenRoot(t, dir)
	defer r.Close()

	if err := r.StagedWrite("file.txt", []byte("v1"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := r.StagedWrite("file.txt", []byte("v2-longer"), 0o644); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "v2-longer" {
		t.Errorf("content = %q, want %q", got, "v2-longer")
	}
}

func TestStagedWrite_NestedRelPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	r := mustOpenRoot(t, dir)
	defer r.Close()

	if err := r.StagedWrite("a/b/c.txt", []byte("ok"), 0o644); err != nil {
		t.Fatalf("StagedWrite: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a", "b", "c.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "ok" {
		t.Errorf("content = %q, want %q", got, "ok")
	}
}

func TestStagedWrite_RejectsUnsafeRelPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := mustOpenRoot(t, dir)
	defer r.Close()

	cases := []string{"../escape", "/etc/passwd", "", "a/../b"}
	for _, rel := range cases {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			err := r.StagedWrite(rel, []byte("x"), 0o644)
			if !errors.Is(err, fsroot.ErrUnsafeRelPath) {
				t.Fatalf("StagedWrite(%q) = %v, want wraps ErrUnsafeRelPath", rel, err)
			}
		})
	}
}

// TestStagedWrite_EscapingDirSymlinkRefused verifies that writing to a
// path whose parent directory is a symlink escaping the root is refused
// by os.Root traversal checks. This is the "open refuses" case in the
// plan's Unit 1 edge-case list.
func TestStagedWrite_EscapingDirSymlinkRefused(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		// Symlink creation on Windows needs SeCreateSymbolicLinkPrivilege
		// or Developer Mode; skip to keep the default test run privilege-free.
		t.Skip("symlink creation on Windows requires elevated privileges")
	}

	outside := t.TempDir()
	rootDir := t.TempDir()
	// escape_dir -> outside (absolute). os.Root must refuse traversal
	// through this link when writing anything under it.
	if err := os.Symlink(outside, filepath.Join(rootDir, "escape_dir")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := mustOpenRoot(t, rootDir)
	defer r.Close()

	err := r.StagedWrite("escape_dir/file.txt", []byte("should-not-land"), 0o644)
	if err == nil {
		t.Fatal("StagedWrite(escape_dir/file.txt) = nil, want refusal")
	}
	// Whether this is surfaced as ErrEscapesRoot depends on os.Root's
	// error shape; the load-bearing invariant is that nothing was
	// written outside the root.
	if _, statErr := os.Stat(filepath.Join(outside, "file.txt")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("wrote outside root: stat(%q) = %v, want fs.ErrNotExist",
			filepath.Join(outside, "file.txt"), statErr)
	}
}

// TestStagedWrite_EscapingFileSymlinkReplaced documents and locks in the
// POSIX rename(2) behavior: if the target path is itself a symlink
// pointing outside the root, StagedWrite replaces the symlink in place
// via atomic rename and does NOT dereference it. The outside target is
// untouched; the inside path now contains the new content. This is the
// safe outcome — an attacker who plants an escaping symlink cannot cause
// agent-sync to overwrite anything outside the root.
func TestStagedWrite_EscapingFileSymlinkReplaced(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation on Windows requires elevated privileges")
	}

	outside := t.TempDir()
	outsideTarget := filepath.Join(outside, "target")
	if err := os.WriteFile(outsideTarget, []byte("outside-original"), 0o600); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	rootDir := t.TempDir()
	inside := filepath.Join(rootDir, "escape")
	if err := os.Symlink(outsideTarget, inside); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := mustOpenRoot(t, rootDir)
	defer r.Close()

	if err := r.StagedWrite("escape", []byte("inside-new"), 0o644); err != nil {
		t.Fatalf("StagedWrite: %v", err)
	}

	// Outside target must be completely unchanged.
	got, err := os.ReadFile(outsideTarget)
	if err != nil {
		t.Fatalf("read outside target: %v", err)
	}
	if string(got) != "outside-original" {
		t.Errorf("outside target mutated: %q, want %q", got, "outside-original")
	}

	// Inside path is now a regular file with the new content (the
	// symlink has been atomically replaced).
	fi, err := os.Lstat(inside)
	if err != nil {
		t.Fatalf("lstat inside: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("inside path is still a symlink; want regular file")
	}
	in, err := os.ReadFile(inside)
	if err != nil {
		t.Fatalf("read inside: %v", err)
	}
	if string(in) != "inside-new" {
		t.Errorf("inside content = %q, want %q", in, "inside-new")
	}
}

func TestStagedWrite_MissingParent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := mustOpenRoot(t, dir)
	defer r.Close()

	err := r.StagedWrite("no-such-dir/file.txt", []byte("x"), 0o644)
	if err == nil {
		t.Fatal("StagedWrite(missing-parent) = nil, want error")
	}
	// We expect ENOENT from the open, not a cleanup-residue artifact.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want wraps fs.ErrNotExist", err)
	}
}

func TestSameFilesystem_ReflectsSelf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	same, err := fsroot.SameFilesystem(dir, dir)
	if err != nil {
		t.Fatalf("SameFilesystem(self,self): %v", err)
	}
	if !same {
		t.Error("SameFilesystem(self,self) = false, want true")
	}
}

func TestSameFilesystem_MissingPath(t *testing.T) {
	t.Parallel()
	_, err := fsroot.SameFilesystem(filepath.Join(t.TempDir(), "nope"), t.TempDir())
	if err == nil {
		t.Fatal("SameFilesystem(missing,_) = nil, want error")
	}
}

func mustOpenRoot(t *testing.T, dir string) *fsroot.Root {
	t.Helper()
	r, err := fsroot.OpenWorkspaceRoot(dir)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot(%q): %v", dir, err)
	}
	return r
}

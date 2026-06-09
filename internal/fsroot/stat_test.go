package fsroot_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

func TestStat_FileAndInvalidPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := fsroot.OpenWorkspaceRoot(dir)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	fi, err := r.Stat("f.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size() != int64(len("hello")) {
		t.Fatalf("size = %d, want %d", fi.Size(), len("hello"))
	}

	// A structurally unsafe relative path is rejected before the kernel.
	if _, err := r.Stat("../escape"); err == nil {
		t.Fatal("expected error stat-ing a traversal path")
	}
}

package engine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

func TestReadHash_SuccessAndNotExist(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := fsroot.OpenWorkspaceRoot(dir)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })

	got, err := readHash(root, "f.txt")
	if err != nil {
		t.Fatalf("readHash: %v", err)
	}
	// Deterministic: same content hashes identically via the package helper.
	if want := sha256Hex([]byte("hello")); got != want {
		t.Fatalf("hash = %q, want %q", got, want)
	}

	// Missing file surfaces fs.ErrNotExist (callers branch on it), not "".
	if _, err := readHash(root, "absent.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("readHash(absent) err = %v, want fs.ErrNotExist", err)
	}
}

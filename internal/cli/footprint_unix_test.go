//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverTargets_SymlinkToDirIsDiscovered(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-claude")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(real, filepath.Join(dir, ".claude")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, _ := discoverTargets(dir, bundledAdapters())
	if len(got) != 1 || got[0] != "claude" {
		t.Fatalf("symlinked dir should be discovered; got %v", got)
	}
}

func TestDiscoverTargets_DanglingSymlinkIsNotDiscovered(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, ".cursor")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, _ := discoverTargets(dir, bundledAdapters())
	if len(got) != 0 {
		t.Fatalf("dangling symlink must not be discovered; got %v", got)
	}
}

func TestDiscoverTargets_UnreadableParentWarnsAndSkips(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not apply to root")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(sub, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	got, warns := discoverTargets(sub, bundledAdapters())
	if len(got) != 0 {
		t.Fatalf("unreadable dir must yield no discoveries; got %v", got)
	}
	if len(warns) == 0 {
		t.Fatal("expected a warning for a non-NotExist stat error")
	}
	for _, w := range warns {
		if !strings.Contains(w, "permission") && !strings.Contains(w, "denied") {
			t.Logf("warning text: %q", w)
		}
	}
}

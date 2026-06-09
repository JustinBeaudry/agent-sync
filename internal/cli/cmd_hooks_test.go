package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitWorkspace makes a temp dir that is both a git repo and an aienvs
// workspace (has .aienv.yaml), so workspace.Find resolves it.
func gitWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".aienv.yaml"),
		[]byte("version: 1\ncanonical:\n  local_path: /x\n  commit: abc\ntargets:\n  - claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runHooks(t *testing.T, ws string, args ...string) (string, string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	full := append([]string{"hooks", args[0], "--workspace", ws, "--non-interactive"}, args[1:]...)
	root.SetArgs(full)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestHooksInstall_RequiresOptIn(t *testing.T) {
	ws := gitWorkspace(t)
	// Non-interactive without --install-hooks → fail fast.
	_, _, err := runHooks(t, ws, "install")
	if err == nil {
		t.Fatal("expected fail-fast without --install-hooks")
	}
}

func TestHooksInstall_WritesHooks(t *testing.T) {
	ws := gitWorkspace(t)
	out, _, err := runHooks(t, ws, "install", "--install-hooks")
	if err != nil {
		t.Fatalf("hooks install: %v", err)
	}
	if !strings.Contains(out, "installed hooks") {
		t.Fatalf("unexpected output: %q", out)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".git", "hooks", "post-merge")); statErr != nil {
		t.Fatalf("post-merge hook not written: %v", statErr)
	}

	// Uninstall removes them.
	out2, _, err := runHooks(t, ws, "uninstall")
	if err != nil {
		t.Fatalf("hooks uninstall: %v", err)
	}
	if !strings.Contains(out2, "post-merge") {
		t.Fatalf("uninstall output = %q", out2)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".git", "hooks", "post-merge")); !os.IsNotExist(statErr) {
		t.Fatalf("post-merge hook should be removed, stat err = %v", statErr)
	}
}

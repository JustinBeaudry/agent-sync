package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func opts(repo string) Options {
	return Options{AienvsPath: "/usr/local/bin/aienvs", WorkspacePath: repo}
}

func TestInstall_FreshRepo(t *testing.T) {
	repo := gitRepo(t)
	res, err := Install(repo, opts(repo))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Installed) != len(ManagedHooks) {
		t.Fatalf("installed %v, want %v", res.Installed, ManagedHooks)
	}
	for _, name := range ManagedHooks {
		path := filepath.Join(repo, ".git", "hooks", name)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		if !strings.Contains(string(data), marker) {
			t.Errorf("%s missing marker", name)
		}
		if !strings.Contains(string(data), "sync --post-merge") {
			t.Errorf("%s missing sync invocation", name)
		}
		if !strings.HasPrefix(string(data), "#!/bin/sh") {
			t.Errorf("%s missing shebang", name)
		}
		if runtime.GOOS != "windows" {
			info, _ := os.Stat(path)
			if info.Mode().Perm()&0o100 == 0 {
				t.Errorf("%s is not executable: %v", name, info.Mode())
			}
		}
	}
}

func TestInstall_NotGitRepo(t *testing.T) {
	if _, err := Install(t.TempDir(), opts("/ws")); err == nil {
		t.Fatal("expected ErrNotGitRepo")
	}
}

func TestInstall_ForeignHookRefusedThenReplace(t *testing.T) {
	repo := gitRepo(t)
	hookPath := filepath.Join(repo, ".git", "hooks", "post-merge")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho foreign\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Without --replace/--append: refused.
	if _, err := Install(repo, opts(repo)); err == nil {
		t.Fatal("expected ErrForeignHook")
	}
	// With --replace: backs up + overwrites.
	o := opts(repo)
	o.Replace = true
	res, err := Install(repo, o)
	if err != nil {
		t.Fatalf("Install --replace: %v", err)
	}
	if len(res.BackedUp) == 0 {
		t.Fatal("expected a backup")
	}
	backup, _ := os.ReadFile(hookPath + ".aienvs-backup")
	if !strings.Contains(string(backup), "echo foreign") {
		t.Fatal("backup does not contain the foreign hook")
	}
}

func TestInstall_AppendPreservesPredecessor(t *testing.T) {
	repo := gitRepo(t)
	hookPath := filepath.Join(repo, ".git", "hooks", "post-merge")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho predecessor\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := opts(repo)
	o.Append = true
	if _, err := Install(repo, o); err != nil {
		t.Fatalf("Install --append: %v", err)
	}
	data, _ := os.ReadFile(hookPath)
	s := string(data)
	if !strings.Contains(s, "echo predecessor") {
		t.Error("append wrapper dropped the predecessor")
	}
	if !strings.Contains(s, "sync --post-merge") {
		t.Error("append wrapper missing aienvs sync")
	}
}

func TestInstall_ReinstallOverwritesOwnHookCleanly(t *testing.T) {
	repo := gitRepo(t)
	if _, err := Install(repo, opts(repo)); err != nil {
		t.Fatal(err)
	}
	// Second install must not refuse (it's our own marked hook).
	if _, err := Install(repo, opts(repo)); err != nil {
		t.Fatalf("reinstall should succeed: %v", err)
	}
}

func TestUninstall_RemovesOnlyMarkedHooksAndRestoresBackup(t *testing.T) {
	repo := gitRepo(t)
	hookPath := filepath.Join(repo, ".git", "hooks", "post-merge")
	// Foreign predecessor, then --replace install (creates a backup).
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho foreign\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := opts(repo)
	o.Replace = true
	if _, err := Install(repo, o); err != nil {
		t.Fatal(err)
	}

	// A foreign post-checkout that aienvs also manages: simulate a foreign
	// one to confirm uninstall leaves it alone.
	foreignCheckout := filepath.Join(repo, ".git", "hooks", "post-checkout")
	// Install wrote our marked post-checkout; replace it with a foreign one.
	if err := os.WriteFile(foreignCheckout, []byte("#!/bin/sh\necho foreign-checkout\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	removed, err := Uninstall(repo)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	// post-merge (marked) removed; foreign post-checkout left alone.
	if len(removed) != 1 || removed[0] != "post-merge" {
		t.Fatalf("removed = %v, want [post-merge]", removed)
	}
	// Backup restored at post-merge.
	data, _ := os.ReadFile(hookPath)
	if !strings.Contains(string(data), "echo foreign") {
		t.Fatalf("backup not restored at post-merge: %q", data)
	}
	// Foreign post-checkout untouched.
	cdata, _ := os.ReadFile(foreignCheckout)
	if !strings.Contains(string(cdata), "echo foreign-checkout") {
		t.Fatal("foreign post-checkout should be untouched")
	}
}

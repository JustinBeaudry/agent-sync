package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireGit skips when git is unavailable (dev hosts; CI always has git).
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=aienvs-test", "GIT_AUTHOR_EMAIL=test@agent-sync.invalid",
		"GIT_COMMITTER_NAME=aienvs-test", "GIT_COMMITTER_EMAIL=test@agent-sync.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// makeCanonicalRepo builds a local git repo with one rule file and returns
// its path + HEAD sha.
func makeCanonicalRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main", "--quiet")
	ruleDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(ruleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ruleDir, "no-fri.md"), []byte("No PRs on Friday.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "--quiet", "-m", "canonical")
	return dir, mustGit(t, dir, "rev-parse", "HEAD")
}

// writeWorkspace creates a workspace dir with a pinned local_path manifest
// targeting claude.
func writeWorkspace(t *testing.T, canonicalPath, sha string) string {
	t.Helper()
	ws := t.TempDir()
	manifest := "version: 1\n" +
		"canonical:\n" +
		"  local_path: " + canonicalPath + "\n" +
		"  commit: " + sha + "\n" +
		"trusted_sha: " + sha + "\n" +
		"targets:\n" +
		"  - claude\n"
	if err := os.WriteFile(filepath.Join(ws, ".aienv.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func runSync(t *testing.T, ws string, extraArgs ...string) (string, string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	args := append([]string{"sync", "--workspace", ws, "--non-interactive"}, extraArgs...)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestSync_LocalPathEndToEnd(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	out, errOut, err := runSync(t, ws)
	if err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	// The rule file landed in the workspace.
	ruleFile := filepath.Join(ws, ".claude", "rules", "aienvs", "no-fri.md")
	if _, statErr := os.Stat(ruleFile); statErr != nil {
		t.Fatalf("expected rule file %s: %v", ruleFile, statErr)
	}
	// The ledger was written.
	if _, statErr := os.Stat(filepath.Join(ws, ".aienv", "state", "claude.json")); statErr != nil {
		t.Fatalf("expected ledger: %v", statErr)
	}
	_ = out
}

func TestSync_JSONOutput(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	out, errOut, err := runSync(t, ws, "--output", "json")
	if err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}
	var doc struct {
		SchemaVersion int    `json:"schema_version"`
		Commit        string `json:"commit"`
		Summary       struct {
			ExitCode int `json:"exit_code"`
		} `json:"summary"`
	}
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	if doc.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", doc.SchemaVersion)
	}
	if doc.Commit != sha {
		t.Fatalf("commit = %q, want %q", doc.Commit, sha)
	}
	if doc.Summary.ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", doc.Summary.ExitCode)
	}
}

func TestSync_SecondRunUnchanged(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("first sync: %v\n%s", err, errOut)
	}
	out, errOut, err := runSync(t, ws, "--output", "json")
	if err != nil {
		t.Fatalf("second sync: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "\"unchanged\"") {
		t.Fatalf("second sync should report an unchanged target:\n%s", out)
	}
}

func TestSync_FloatingLocalPathUnsupported(t *testing.T) {
	requireGit(t)
	canonical, _ := makeCanonicalRepo(t)
	ws := t.TempDir()
	// Manifest with local_path but no commit → floating, unsupported.
	m := "version: 1\ncanonical:\n  local_path: " + canonical + "\ntargets:\n  - claude\n"
	if err := os.WriteFile(filepath.Join(ws, ".aienv.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runSync(t, ws)
	if err == nil {
		t.Fatal("expected error for floating local_path")
	}
}

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCmd(t *testing.T, ws string, args ...string) (string, string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	full := append([]string{args[0], "--workspace", ws, "--non-interactive"}, args[1:]...)
	root.SetArgs(full)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestValidate_CleanWorkspaceNoDrift(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	// Sync first so the workspace matches the canonical source.
	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("sync: %v\n%s", err, errOut)
	}
	out, _, err := runCmd(t, ws, "validate", "--output", "text")
	if err != nil {
		t.Fatalf("validate should exit 0 on a clean workspace: %v", err)
	}
	if !strings.Contains(out, "No drift") {
		t.Fatalf("expected no-drift message, got:\n%s", out)
	}
}

func TestValidate_PendingCreateIsDrift(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	// No prior sync → the rule file would be created → drift → exit 1.
	_, _, err := runCmd(t, ws, "validate")
	if err == nil {
		t.Fatal("expected non-zero exit on pending create")
	}
	if got := MapExit(err); got != 1 {
		t.Fatalf("exit code = %d, want 1 (drift)", got)
	}
}

func TestValidate_JSONContract(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	out, _, _ := runCmd(t, ws, "validate", "--output", "json")
	var doc struct {
		SchemaVersion int  `json:"schema_version"`
		DriftDetected bool `json:"drift_detected"`
		Targets       []struct {
			Target      string   `json:"target"`
			WouldCreate []string `json:"would_create"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("validate json invalid: %v\n%s", err, out)
	}
	if doc.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", doc.SchemaVersion)
	}
	if !doc.DriftDetected {
		t.Fatal("expected drift_detected=true before any sync")
	}
}

func TestValidate_PerTargetErrorIsOperationalFailure(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := t.TempDir()
	// Manifest targets an adapter that does not exist → engine.Plan records
	// a per-target error (nil command error). validate must exit 2
	// (operational), not 0 or 1.
	m := "version: 1\ncanonical:\n  local_path: " + canonical + "\n  commit: " + sha +
		"\ntrusted_sha: " + sha + "\ntargets:\n  - nonexistent-adapter\n"
	if err := os.WriteFile(filepath.Join(ws, ".aienv.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmd(t, ws, "validate")
	if err == nil {
		t.Fatal("expected non-zero exit for a per-target operational error")
	}
	if got := MapExit(err); got != 2 {
		t.Fatalf("exit code = %d, want 2 (operational), not drift", got)
	}
}

func TestStatus_ReportsSourceAndTargets(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	out, _, err := runCmd(t, ws, "status", "--output", "text")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, canonical) {
		t.Fatalf("status should show the source path:\n%s", out)
	}
	if !strings.Contains(out, "target claude: untracked") {
		t.Fatalf("status should show claude as untracked before sync:\n%s", out)
	}

	// After a sync, claude is tracked with managed files.
	if _, errOut, serr := runSync(t, ws); serr != nil {
		t.Fatalf("sync: %v\n%s", serr, errOut)
	}
	out2, _, err := runCmd(t, ws, "status", "--output", "text")
	if err != nil {
		t.Fatalf("status #2: %v", err)
	}
	if !strings.Contains(out2, "managed file") {
		t.Fatalf("status should show managed files after sync:\n%s", out2)
	}
}

func TestStatus_WatchFailedBanner(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)
	// Plant the watch-failure marker.
	stateDir := filepath.Join(ws, ".aienv", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "last-watch.failed"), []byte("boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := runCmd(t, ws, "status", "--output", "text")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "watch-mode sync failed") {
		t.Fatalf("expected watch-failed banner:\n%s", out)
	}
}

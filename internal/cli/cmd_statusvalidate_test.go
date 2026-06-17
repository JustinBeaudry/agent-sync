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
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte(m), 0o644); err != nil {
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

// runStatusHierarchy drives the real status cobra command on the hierarchy
// path: it sets cwd (so discovery walks from there), swaps the home seam to
// keep the suite hermetic, and deliberately omits --workspace so discovery
// runs across all scopes.
func runStatusHierarchy(t *testing.T, cwd, home string, extraArgs ...string) (string, string, error) {
	t.Helper()
	t.Chdir(cwd)
	prev := resolveHome
	resolveHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { resolveHome = prev })

	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	args := append([]string{"status", "--non-interactive"}, extraArgs...)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestStatusShowsHierarchy(t *testing.T) {
	home, repo, nested := hierarchyTree(t)
	// Add a user-level manifest + authored skill at home so the user scope is
	// discovered (read-only, never emitted).
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(hierarchyLocalDirManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/skills/user-skill/SKILL.md", "user skill body\n")

	// Sync the whole hierarchy (no --user) so the project + directory scopes get
	// ledgers; the user scope stays unsynced (untracked).
	if _, errOut, err := runSyncHierarchy(t, nested, home); err != nil {
		t.Fatalf("seed sync failed: %v\nstderr: %s", err, errOut)
	}

	out, _, err := runStatusHierarchy(t, nested, home, "--output", "text")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	// All three scopes are listed with their levels and roots.
	for _, want := range []string{"user", "project", "directory", home, repo, nested} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
	// The user scope is marked read-only / not emitted.
	if !strings.Contains(out, "read-only") {
		t.Errorf("status should mark the user scope read-only:\n%s", out)
	}
	// The project + directory scopes were synced → managed files.
	if !strings.Contains(out, "managed file") {
		t.Errorf("status should show managed files for synced scopes:\n%s", out)
	}
	// The user scope was not synced → untracked.
	if !strings.Contains(out, "untracked") {
		t.Errorf("status should show the unsynced user scope as untracked:\n%s", out)
	}
}

func TestStatusHierarchyJSONListsScopes(t *testing.T) {
	home, _, nested := hierarchyTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(hierarchyLocalDirManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/skills/user-skill/SKILL.md", "user skill body\n")

	out, _, err := runStatusHierarchy(t, nested, home, "--output", "json")
	if err != nil {
		t.Fatalf("status json: %v", err)
	}
	var doc struct {
		Scopes []struct {
			Level    string `json:"level"`
			Root     string `json:"root"`
			ReadOnly bool   `json:"read_only"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("status json invalid: %v\n%s", err, out)
	}
	if len(doc.Scopes) != 3 {
		t.Fatalf("got %d scopes, want 3: %+v", len(doc.Scopes), doc.Scopes)
	}
	if doc.Scopes[0].Level != "user" || !doc.Scopes[0].ReadOnly {
		t.Errorf("first scope = %+v, want user/read-only", doc.Scopes[0])
	}
}

// TestStatusContinuesPastMalformedScope drives the status continue-and-report
// path: a valid project scope at the git root and a nested directory scope
// whose .agent-sync.yaml is malformed YAML. The bad scope fails inside
// scopeTargets (manifest.LoadFile rejects the YAML), so the failure layer is
// the manifest load. status must still render the valid scope with its
// level/source and surface an error line for the bad scope without aborting
// with a top-level error.
func TestStatusContinuesPastMalformedScope(t *testing.T) {
	home, repo, nested := hierarchyTree(t)
	// Corrupt the nested (directory) scope's manifest so scopeTargets fails for
	// that scope only. Discovery keys off manifest presence, not validity, so
	// the scope is still discovered and entered into the loop.
	if err := os.WriteFile(filepath.Join(nested, ".agent-sync.yaml"), []byte(":\n  not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := runStatusHierarchy(t, nested, home, "--output", "text")
	if err != nil {
		t.Fatalf("status must not abort with a top-level error when a scope is malformed: %v", err)
	}

	// The valid project scope still renders with its level, root, and source.
	for _, want := range []string{"project", repo, "source:"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q for the valid scope:\n%s", want, out)
		}
	}
	// The malformed directory scope surfaces an error line under its header.
	if !strings.Contains(out, "directory") || !strings.Contains(out, nested) {
		t.Errorf("status output missing the directory scope header:\n%s", out)
	}
	if !strings.Contains(out, "ERROR:") {
		t.Errorf("status should surface an error line for the malformed scope:\n%s", out)
	}

	// JSON confirms the bad scope records a per-scope error field while the
	// valid scope does not, and the run still succeeds.
	jout, _, jerr := runStatusHierarchy(t, nested, home, "--output", "json")
	if jerr != nil {
		t.Fatalf("status json must not abort: %v", jerr)
	}
	var doc struct {
		Scopes []struct {
			Level  string `json:"level"`
			Source string `json:"source"`
			Error  string `json:"error"`
		} `json:"scopes"`
	}
	if uerr := json.Unmarshal([]byte(jout), &doc); uerr != nil {
		t.Fatalf("status json invalid: %v\n%s", uerr, jout)
	}
	var sawValid, sawBad bool
	for _, sc := range doc.Scopes {
		switch sc.Level {
		case "project":
			if sc.Error == "" && sc.Source != "" {
				sawValid = true
			}
		case "directory":
			if sc.Error != "" {
				sawBad = true
			}
		}
	}
	if !sawValid {
		t.Errorf("expected a valid project scope with a source and no error: %+v", doc.Scopes)
	}
	if !sawBad {
		t.Errorf("expected the directory scope to record a per-scope error: %+v", doc.Scopes)
	}
}

func TestStatus_WatchFailedBanner(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)
	// Plant the watch-failure marker.
	stateDir := filepath.Join(ws, ".agent-sync", "state")
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

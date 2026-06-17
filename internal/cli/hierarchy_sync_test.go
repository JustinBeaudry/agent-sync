package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/coverage"
	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/report"
	"github.com/agent-sync/agent-sync/internal/trust"
)

// hierarchyLocalDirManifest is the in-repo (local_dir) manifest used by the hierarchy
// tests: no git, no network, no pin.
const hierarchyLocalDirManifest = "version: 1\n" +
	"canonical:\n" +
	"  local_dir: .agents\n" +
	"targets:\n" +
	"  - claude\n"

// hierarchyTree builds a home/repo/packages/api tree with a project manifest
// at the repo root and a directory manifest in packages/api, each with one
// authored skill. The repo root carries a .git dir so it is the project root.
// Returns (home, repoRoot, nestedDir).
func hierarchyTree(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	nested := filepath.Join(repo, "packages", "api")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// Project scope.
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(hierarchyLocalDirManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/skills/proj-skill/SKILL.md", "project skill body\n")
	// Directory scope.
	if err := os.WriteFile(filepath.Join(nested, ".agent-sync.yaml"), []byte(hierarchyLocalDirManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, nested, ".agents/skills/api-skill/SKILL.md", "api skill body\n")
	return home, repo, nested
}

// newTestRuntime builds a runtimeContext suitable for driving prepareScope /
// runHierarchySync directly, mirroring how the root command populates rc.
func newTestRuntime() *runtimeContext {
	return &runtimeContext{
		Access: Access{NonInteractive: true},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Flags:  PersistentFlags{NonInteractive: true},
	}
}

func TestRunHierarchySyncEmitsEachScope(t *testing.T) {
	home, repo, nested := hierarchyTree(t)
	rc := newTestRuntime()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	outcomes, err := runHierarchySync(
		context.Background(), rc, nested, home,
		hierarchySyncOptions{IncludeUser: false, EngineOpts: engine.Options{
			Mode:   report.ModeAtomic,
			Now:    func() time.Time { return now },
			Logger: rc.Logger,
		}},
		now,
	)
	if err != nil {
		t.Fatalf("runHierarchySync: %v", err)
	}

	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(outcomes))
	}
	if outcomes[0].Scope.Level != hierarchy.LevelProject {
		t.Errorf("first scope level = %s, want project", outcomes[0].Scope.Level)
	}
	if outcomes[1].Scope.Level != hierarchy.LevelDirectory {
		t.Errorf("second scope level = %s, want directory", outcomes[1].Scope.Level)
	}
	for _, o := range outcomes {
		if o.Err != nil {
			t.Fatalf("scope %s errored: %v", o.Scope.Root, o.Err)
		}
	}

	// Project scope emitted under repo/.claude.
	mustExist(t, filepath.Join(repo, ".claude", "skills", "agent-sync-proj-skill", "SKILL.md"))
	// Directory scope emitted under packages/api/.claude.
	mustExist(t, filepath.Join(nested, ".claude", "skills", "agent-sync-api-skill", "SKILL.md"))
	// The user scope was NOT emitted: no .claude under home itself.
	mustNotExist(t, filepath.Join(home, ".claude"))

	if got := hierarchyExitCode(outcomes); got != 0 {
		t.Errorf("aggregate exit = %d, want 0", got)
	}
}

// runSyncHierarchy drives the real sync cobra command on the hierarchy path:
// it sets cwd (so discovery walks from there), swaps the home seam to keep the
// suite hermetic, and deliberately omits --workspace so discovery runs.
func runSyncHierarchy(t *testing.T, cwd, home string, extraArgs ...string) (string, string, error) {
	t.Helper()
	t.Chdir(cwd)
	prev := resolveHome
	resolveHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { resolveHome = prev })

	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	args := append([]string{"sync", "--non-interactive"}, extraArgs...)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestSyncCommandHierarchy(t *testing.T) {
	home, repo, nested := hierarchyTree(t)

	if _, errOut, err := runSyncHierarchy(t, nested, home); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	mustExist(t, filepath.Join(repo, ".claude", "skills", "agent-sync-proj-skill", "SKILL.md"))
	mustExist(t, filepath.Join(nested, ".claude", "skills", "agent-sync-api-skill", "SKILL.md"))
	// A repo sync without --user must never write under the home directory.
	mustNotExist(t, filepath.Join(home, ".claude"))
}

// TestRunHierarchySyncContinuesPastMalformedScope drives the real
// continue-and-report path end-to-end: a valid project-level manifest at the
// git root and a nested directory-level scope whose manifest is malformed YAML.
// The malformed scope fails inside prepareScope (manifest.LoadFile rejects the
// YAML before any sync runs), so the failure layer is "prepare". The run must
// still emit the valid scope and record the bad scope's error without aborting.
func TestRunHierarchySyncContinuesPastMalformedScope(t *testing.T) {
	home, repo, nested := hierarchyTree(t)
	// Corrupt the nested (directory) scope's manifest so prepareScope fails
	// for that scope only. Discovery keys off manifest presence, not validity,
	// so the scope is still discovered and entered into the loop.
	if err := os.WriteFile(filepath.Join(nested, ".agent-sync.yaml"), []byte(":\n  not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := newTestRuntime()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	outcomes, err := runHierarchySync(
		context.Background(), rc, nested, home,
		hierarchySyncOptions{IncludeUser: false, EngineOpts: engine.Options{
			Mode:   report.ModeAtomic,
			Now:    func() time.Time { return now },
			Logger: rc.Logger,
		}},
		now,
	)
	// Discovery succeeded (both manifests are present), so the orchestrator
	// returns no top-level error — the bad scope is reported per-scope.
	if err != nil {
		t.Fatalf("runHierarchySync returned a top-level error: %v", err)
	}

	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(outcomes))
	}
	// Outcomes are shallow→deep: project (valid) first, directory (bad) second.
	valid, bad := outcomes[0], outcomes[1]
	if valid.Scope.Level != hierarchy.LevelProject {
		t.Fatalf("first scope level = %s, want project", valid.Scope.Level)
	}
	if bad.Scope.Level != hierarchy.LevelDirectory {
		t.Fatalf("second scope level = %s, want directory", bad.Scope.Level)
	}

	// (b) Exactly one outcome errored (the malformed scope), and the valid
	// scope has a nil error with a populated summary.
	if valid.Err != nil {
		t.Errorf("valid scope errored: %v", valid.Err)
	}
	if valid.Summary.Workspace == "" {
		t.Errorf("valid scope summary not populated: %+v", valid.Summary)
	}
	if bad.Err == nil {
		t.Fatal("malformed scope must report an error")
	}
	errored := 0
	for _, o := range outcomes {
		if o.Err != nil {
			errored++
		}
	}
	if errored != 1 {
		t.Errorf("got %d errored outcomes, want exactly 1", errored)
	}

	// (a) The valid scope still emitted its .claude files on disk despite the
	// sibling scope failing.
	mustExist(t, filepath.Join(repo, ".claude", "skills", "agent-sync-proj-skill", "SKILL.md"))
	// The malformed scope emitted nothing.
	mustNotExist(t, filepath.Join(nested, ".claude"))

	// (c) The aggregate exit code is non-zero because a scope failed.
	if got := hierarchyExitCode(outcomes); got == 0 {
		t.Error("aggregate exit code must be non-zero when a scope fails")
	}
}

// TestHierarchySyncEmitsCoverageWarning checks that a directory-level scope
// emitting a skill for target claude carries a coverage warning (claude does
// not read nested skills natively), while the project-level scope emitting the
// same skill carries none (project level is always native).
func TestHierarchySyncEmitsCoverageWarning(t *testing.T) {
	home, _, nested := hierarchyTree(t)
	rc := newTestRuntime()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	outcomes, err := runHierarchySync(
		context.Background(), rc, nested, home,
		hierarchySyncOptions{IncludeUser: false, EngineOpts: engine.Options{
			Mode:   report.ModeAtomic,
			Now:    func() time.Time { return now },
			Logger: rc.Logger,
		}},
		now,
	)
	if err != nil {
		t.Fatalf("runHierarchySync: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(outcomes))
	}
	proj, dir := outcomes[0], outcomes[1]
	if proj.Scope.Level != hierarchy.LevelProject {
		t.Fatalf("first scope level = %s, want project", proj.Scope.Level)
	}
	if dir.Scope.Level != hierarchy.LevelDirectory {
		t.Fatalf("second scope level = %s, want directory", dir.Scope.Level)
	}

	// Project scope: skill at project level is native, so no warnings.
	if len(proj.Warnings) != 0 {
		t.Errorf("project scope should carry no coverage warnings, got: %+v", proj.Warnings)
	}
	// Directory scope: claude does not read nested skills natively → 1 warning.
	if len(dir.Warnings) != 1 {
		t.Fatalf("directory scope should carry 1 coverage warning, got %d: %+v", len(dir.Warnings), dir.Warnings)
	}
	w := dir.Warnings[0]
	if w.Target != "claude" || w.Kind != ir.KindSkill || w.Level != hierarchy.LevelDirectory {
		t.Errorf("warning = %+v, want claude/skill/directory", w)
	}

	// The warning surfaces in text output, scoped under the directory header.
	var buf bytes.Buffer
	if err := renderHierarchyText(&buf, outcomes); err != nil {
		t.Fatalf("renderHierarchyText: %v", err)
	}
	out := buf.String()
	if !bytes.Contains([]byte(out), []byte("warning:")) || !bytes.Contains([]byte(out), []byte("claude")) {
		t.Errorf("text output missing coverage warning:\n%s", out)
	}

	// The coverage warning's embedded level must serialize as a string in the
	// aggregate JSON (e.g. "level":"directory"), not as a raw integer
	// ("level":2), staying consistent with the CLI's other level fields.
	var jbuf bytes.Buffer
	if err := renderHierarchyJSON(&jbuf, outcomes); err != nil {
		t.Fatalf("renderHierarchyJSON: %v", err)
	}
	js := jbuf.String()
	if !bytes.Contains([]byte(js), []byte(`"level":"directory"`)) {
		t.Errorf("coverage_warnings JSON should carry a string level (\"level\":\"directory\"):\n%s", js)
	}
	if bytes.Contains([]byte(js), []byte(`"level":2`)) {
		t.Errorf("coverage_warnings JSON must not serialize level as an integer:\n%s", js)
	}
}

func TestSyncCommandUserFlag(t *testing.T) {
	home, _, nested := hierarchyTree(t)
	// Add a user-level manifest + authored skill at home.
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(hierarchyLocalDirManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/skills/user-skill/SKILL.md", "user skill body\n")

	// Without --user: the user scope is NOT emitted.
	if _, errOut, err := runSyncHierarchy(t, nested, home); err != nil {
		t.Fatalf("sync (no --user) failed: %v\nstderr: %s", err, errOut)
	}
	mustNotExist(t, filepath.Join(home, ".claude"))

	// With --user: the user scope IS emitted.
	if _, errOut, err := runSyncHierarchy(t, nested, home, "--user"); err != nil {
		t.Fatalf("sync --user failed: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, filepath.Join(home, ".claude", "skills", "agent-sync-user-skill", "SKILL.md"))
}

func TestSyncCommandUserWithWorkspaceIsError(t *testing.T) {
	ws := writeLocalDirWorkspace(t)
	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	root.SetArgs([]string{"sync", "--workspace", ws, "--non-interactive", "--user"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error when --user is combined with --workspace")
	}
}

func TestHierarchyExitCode(t *testing.T) {
	clean := scopeOutcome{Summary: report.Summary{Outcome: report.Outcome{ExitCode: 0}}}
	failedSync := scopeOutcome{Summary: report.Summary{Outcome: report.Outcome{ExitCode: 1}}}
	prepareErr := scopeOutcome{Err: errors.New("boom")}

	if got := hierarchyExitCode([]scopeOutcome{clean, clean}); got != 0 {
		t.Errorf("all-clean exit = %d, want 0", got)
	}
	if got := hierarchyExitCode([]scopeOutcome{clean, failedSync}); got == 0 {
		t.Error("a failed-sync scope must yield non-zero exit")
	}
	if got := hierarchyExitCode([]scopeOutcome{clean, prepareErr}); got == 0 {
		t.Error("a prepare-error scope must yield non-zero exit")
	}
}

// TestHierarchyExitCodePreservesOperationalCodes asserts a scope whose prepare
// fails with a trust/operational error surfaces that specific exit code (3/4/5
// for trust, 2 for an unclassified operational error) rather than collapsing to
// a flat 1 — matching the code the single-scope path would surface via MapExit.
func TestHierarchyExitCodePreservesOperationalCodes(t *testing.T) {
	trustErr := scopeOutcome{Err: &exitError{
		code: trust.ExitFirstUseDenied,
		err:  errors.New("cli: trust: first use denied"),
	}}
	failedSync := scopeOutcome{Summary: report.Summary{Outcome: report.Outcome{ExitCode: 1}}}
	clean := scopeOutcome{Summary: report.Summary{Outcome: report.Outcome{ExitCode: 0}}}

	if got := hierarchyExitCode([]scopeOutcome{clean, trustErr}); got != trust.ExitFirstUseDenied {
		t.Errorf("trust-error scope exit = %d, want %d", got, trust.ExitFirstUseDenied)
	}
	// Highest-severity specific code wins when several scopes fail: a trust
	// failure (5) outranks an ordinary sync-failure summary (1).
	if got := hierarchyExitCode([]scopeOutcome{failedSync, trustErr}); got != trust.ExitFirstUseDenied {
		t.Errorf("mixed-failure exit = %d, want %d (highest severity)", got, trust.ExitFirstUseDenied)
	}
	// An unclassified prepare error (e.g. a malformed manifest) maps to the
	// operational/usage code, not a flat 1.
	plainErr := scopeOutcome{Err: errors.New("malformed manifest")}
	if got := hierarchyExitCode([]scopeOutcome{clean, plainErr}); got != exitUsage {
		t.Errorf("unclassified prepare-error exit = %d, want %d", got, exitUsage)
	}
}

func TestRenderHierarchyText(t *testing.T) {
	outcomes := []scopeOutcome{
		{
			Scope:   hierarchy.Scope{Root: "/repo", Level: hierarchy.LevelProject},
			Summary: report.Summary{Workspace: "/repo", Outcome: report.Outcome{Line: "all good", ExitCode: 0}},
		},
		{
			Scope: hierarchy.Scope{Root: "/repo/pkg", Level: hierarchy.LevelDirectory},
			Err:   errors.New("kaboom"),
		},
		// A scope that fails at SYNC after a successful prepare still has
		// computed coverage warnings; they must render even though Err is set.
		{
			Scope:    hierarchy.Scope{Root: "/repo/nested", Level: hierarchy.LevelDirectory},
			Err:      errors.New("sync exploded"),
			Warnings: []coverage.Warning{{Detail: "cursor does not read agent from a nested directory"}},
		},
	}
	var buf bytes.Buffer
	if err := renderHierarchyText(&buf, outcomes); err != nil {
		t.Fatalf("renderHierarchyText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"project: /repo",
		"directory: /repo/pkg",
		"ERROR: kaboom",
		"ERROR: sync exploded",
		"warning: cursor does not read agent from a nested directory",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderHierarchyJSON(t *testing.T) {
	outcomes := []scopeOutcome{
		{
			Scope:   hierarchy.Scope{Root: "/repo", Level: hierarchy.LevelProject},
			Summary: report.Summary{Workspace: "/repo", Outcome: report.Outcome{ExitCode: 0}},
		},
		{
			Scope: hierarchy.Scope{Root: "/repo/pkg", Level: hierarchy.LevelDirectory},
			Err:   errors.New("kaboom"),
		},
	}
	var buf bytes.Buffer
	if err := renderHierarchyJSON(&buf, outcomes); err != nil {
		t.Fatalf("renderHierarchyJSON: %v", err)
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		ExitCode      int `json:"exit_code"`
		Scopes        []struct {
			Root  string `json:"root"`
			Level string `json:"level"`
			Error string `json:"error"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if doc.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", doc.SchemaVersion)
	}
	if doc.ExitCode == 0 {
		t.Error("exit_code should be non-zero when a scope errored")
	}
	if len(doc.Scopes) != 2 {
		t.Fatalf("got %d scopes, want 2", len(doc.Scopes))
	}
	if doc.Scopes[1].Error != "kaboom" {
		t.Errorf("second scope error = %q, want kaboom", doc.Scopes[1].Error)
	}
}

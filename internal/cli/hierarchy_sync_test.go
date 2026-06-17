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

	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/report"
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
	}
	var buf bytes.Buffer
	if err := renderHierarchyText(&buf, outcomes); err != nil {
		t.Fatalf("renderHierarchyText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"project: /repo", "directory: /repo/pkg", "ERROR: kaboom"} {
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

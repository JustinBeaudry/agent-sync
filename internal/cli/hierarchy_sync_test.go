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
	"strings"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/coverage"
	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/harness"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/manifest"
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

func hierarchyURLTree(t *testing.T, projectURL, projectSHA string, projectAuto *bool, dirURL, dirSHA string, dirAuto *bool) (string, string, string) {
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
	writeURLManifestAt(t, filepath.Join(repo, ".agent-sync.yaml"), projectURL, projectSHA, "main", projectAuto)
	writeURLManifestAt(t, filepath.Join(nested, ".agent-sync.yaml"), dirURL, dirSHA, "main", dirAuto)
	return home, repo, nested
}

func writeURLManifestAt(t *testing.T, path, canonicalURL, sha, ref string, auto *bool) {
	t.Helper()
	var b strings.Builder
	b.WriteString("version: 1\ncanonical:\n")
	b.WriteString("  url: " + canonicalURL + "\n")
	if ref != "" {
		b.WriteString("  ref: " + ref + "\n")
	}
	b.WriteString("  commit: " + sha + "\n")
	if auto != nil {
		if *auto {
			b.WriteString("  auto: true\n")
		} else {
			b.WriteString("  auto: false\n")
		}
	}
	b.WriteString("trusted_sha: " + sha + "\n")
	b.WriteString("targets:\n  - claude\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
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

func TestPrepareScope_UsesManifestScopeWhenPresent(t *testing.T) {
	ws := t.TempDir()
	manifestPath := filepath.Join(ws, ".agent-sync.yaml")
	manifestText := "version: 1\n" +
		"scope: " + manifest.ScopeWorkspace + "\n" +
		"activation_root: true\n" +
		"canonical:\n" +
		"  local_dir: .agents\n" +
		"targets:\n" +
		"  - codex\n"
	if err := os.WriteFile(manifestPath, []byte(manifestText), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, ws, ".agents/AGENTS.md", "team standards\n")

	rc := newTestRuntime()
	prep, err := prepareScope(
		context.Background(),
		rc,
		ws,
		manifestPath,
		"project",
		time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("prepareScope: %v", err)
	}
	t.Cleanup(prep.Close)

	if prep.Request.Scope != manifest.ScopeWorkspace {
		t.Fatalf("request scope = %q, want %q", prep.Request.Scope, manifest.ScopeWorkspace)
	}
}

func TestRunHierarchySyncEmitsClosestScope(t *testing.T) {
	home, repo, nested := hierarchyTree(t)
	rc := newTestRuntime()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	outcomes, _, err := runHierarchySync(
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

	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(outcomes))
	}
	if outcomes[0].Scope.Level != hierarchy.LevelDirectory {
		t.Fatalf("scope level = %s, want directory", outcomes[0].Scope.Level)
	}
	for _, o := range outcomes {
		if o.Err != nil {
			t.Fatalf("scope %s errored: %v", o.Scope.Root, o.Err)
		}
	}

	// Closest scope (directory) emitted under packages/api/.claude.
	mustExist(t, filepath.Join(nested, ".claude", "skills", "agent-sync-api-skill", "SKILL.md"))
	// Project scope was inherited for layer resolution but not synced.
	mustNotExist(t, filepath.Join(repo, ".claude", "skills", "agent-sync-proj-skill", "SKILL.md"))
	// The user scope was NOT emitted: no .claude under home itself.
	mustNotExist(t, filepath.Join(home, ".claude"))

	if got := hierarchyExitCode(outcomes); got != 0 {
		t.Errorf("aggregate exit = %d, want 0", got)
	}
}

func TestRunHierarchySyncContinuesWhenAncestorLayerCannotMaterialize(t *testing.T) {
	home, repo, nested := hierarchyTree(t)
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte("version: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := newTestRuntime()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	outcomes, _, err := runHierarchySync(
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
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %d, want 1: %+v", len(outcomes), outcomes)
	}
	if outcomes[0].Err != nil {
		t.Fatalf("descendant sync should continue despite ancestor materialize error: %v", outcomes[0].Err)
	}
	data, err := os.ReadFile(filepath.Join(nested, ".claude", "skills", "agent-sync-api-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("read synced descendant skill: %v", err)
	}
	if !strings.Contains(string(data), "api skill body") {
		t.Fatalf("descendant skill missing body:\n%s", data)
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

	mustNotExist(t, filepath.Join(repo, ".claude", "skills", "agent-sync-proj-skill", "SKILL.md"))
	mustExist(t, filepath.Join(nested, ".claude", "skills", "agent-sync-api-skill", "SKILL.md"))
	// A repo sync without --user must never write under the home directory.
	mustNotExist(t, filepath.Join(home, ".claude"))
}

func TestSyncInsideActivationRootInheritsWorkspaceCodexFragmentsAndSkipsUser(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ActualReality")
	repo := filepath.Join(ws, "apps", "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: .git/worktrees/api\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeWS(t, home, ".agent-sync.yaml", "version: 1\nscope: user\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, home, ".agents/configs/codex/features/hooks/fragment.yaml", "id: hooks\ntarget: codex\npath: .codex/config.toml\nmerge: toml-key\nlocator: features.hooks\nvisibility: team\ninheritance: descendants\npayload: payload.toml\n")
	writeWS(t, home, ".agents/configs/codex/features/hooks/payload.toml", "[features]\nhooks = false\n")

	writeWS(t, ws, ".agent-sync.yaml", "version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, ws, ".agents/configs/codex/features/hooks/fragment.yaml", "id: hooks\ntarget: codex\npath: .codex/config.toml\nmerge: toml-key\nlocator: features.hooks\npayload: payload.toml\n")
	writeWS(t, ws, ".agents/configs/codex/features/hooks/payload.toml", "[features]\nhooks = true\n")

	writeWS(t, repo, ".agent-sync.yaml", "version: 1\nscope: project\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, repo, ".agents/AGENTS.md", "project instructions\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, errOut)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read project codex config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "hooks = true") {
		t.Fatalf("project config did not inherit workspace fragment:\n%s", text)
	}
	if strings.Contains(text, "hooks = false") {
		t.Fatalf("project config inherited user fragment despite activation root:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plain sync wrote user config, err=%v", err)
	}
}

func TestSyncInsideActivationRootInheritsWorkspaceSkill(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ActualReality")
	repo := filepath.Join(ws, "apps", "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: .git/worktrees/api\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeWS(t, ws, ".agent-sync.yaml", "version: 1\nscope: workspace\nactivation_root: true\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, ws, ".agents/skills/code-review/SKILL.md", "workspace code review skill\n")

	writeWS(t, repo, ".agent-sync.yaml", "version: 1\nscope: project\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, repo, ".agents/AGENTS.md", "project instructions\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, errOut)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".agents", "skills", "agent-sync-code-review", "SKILL.md"))
	if err != nil {
		t.Fatalf("read inherited skill: %v", err)
	}
	if !strings.Contains(string(data), "workspace code review skill") {
		t.Fatalf("inherited skill content mismatch:\n%s", data)
	}
}

func TestSyncCodexHooksFragmentGeneratesHooksJSON(t *testing.T) {
	home, repo, _ := hierarchyTree(t)
	writeWS(t, repo, ".agent-sync.yaml", "version: 1\nscope: project\ncanonical:\n  local_dir: .agents\ntargets:\n  - codex\n")
	writeWS(t, repo, ".agents/configs/codex/hooks/pre-tool-policy/fragment.yaml", "id: pre-tool-policy\ntarget: codex\npath: .codex/hooks.json\nmerge: codex-hooks\nlocator: PreToolUse/pre-tool-policy\nsafety: executable\npayload: payload.json\n")
	writeWS(t, repo, ".agents/configs/codex/hooks/pre-tool-policy/payload.json", `{"matcher":"Bash","hooks":[{"type":"command","command":"python3 .codex/hooks/check.py","statusMessage":"Checking Bash command"}]}`)
	writeWS(t, repo, ".agents/configs/codex/hooks/pre-tool-edit/fragment.yaml", "id: pre-tool-edit\ntarget: codex\npath: .codex/hooks.json\nmerge: codex-hooks\nlocator: PreToolUse/pre-tool-edit\nsafety: executable\npayload: payload.json\n")
	writeWS(t, repo, ".agents/configs/codex/hooks/pre-tool-edit/payload.json", `{"matcher":"Edit","hooks":[{"type":"command","command":"python3 .codex/hooks/check_edit.py","statusMessage":"Checking Edit command"}]}`)

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, errOut)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"hooks": {`, `"PreToolUse"`, `"statusMessage": "Checking Bash command"`, `"statusMessage": "Checking Edit command"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("hooks.json missing %q:\n%s", want, text)
		}
	}
	var doc struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse hooks.json: %v\n%s", err, text)
	}
	if got := len(doc.Hooks["PreToolUse"]); got != 2 {
		t.Fatalf("PreToolUse hooks = %d, want 2:\n%s", got, text)
	}
	if strings.Contains(text, "_agent_sync_generated") {
		t.Fatalf("hooks.json should not include agent-sync marker:\n%s", text)
	}
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

	outcomes, _, err := runHierarchySync(
		context.Background(), rc, nested, home,
		hierarchySyncOptions{IncludeUser: false, EngineOpts: engine.Options{
			Mode:   report.ModeAtomic,
			Now:    func() time.Time { return now },
			Logger: rc.Logger,
		}},
		now,
	)
	// Discovery succeeded (both manifests are present), so the orchestrator
	// returns no top-level error — the bad selected scope is reported.
	if err != nil {
		t.Fatalf("runHierarchySync returned a top-level error: %v", err)
	}

	// Directory scope is the selected emit scope and it should report the YAML error.
	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(outcomes))
	}
	if outcomes[0].Scope.Level != hierarchy.LevelDirectory {
		t.Fatalf("scope level = %s, want directory", outcomes[0].Scope.Level)
	}
	if outcomes[0].Err == nil {
		t.Fatal("malformed scope must report an error")
	}

	mustNotExist(t, filepath.Join(nested, ".claude"))
	if got := hierarchyExitCode(outcomes); got == 0 {
		t.Error("aggregate exit code must be non-zero when the selected scope fails")
	}

	// Project scope was not emitted; selection is closest non-user scope.
	mustNotExist(t, filepath.Join(repo, ".claude", "skills", "agent-sync-proj-skill", "SKILL.md"))
}

func TestSelectWriteScopes_DefaultSelectsClosestNonUser(t *testing.T) {
	scopes := []hierarchy.Scope{
		{Level: hierarchy.LevelUser, Emit: true, ManifestPath: "/home/.agent-sync.yaml"},
		{Level: hierarchy.LevelWorkspace, Emit: true, ManifestPath: "/ws/.agent-sync.yaml"},
		{Level: hierarchy.LevelProject, Emit: true, ManifestPath: "/repo/.agent-sync.yaml"},
		{Level: hierarchy.LevelDirectory, Emit: true, ManifestPath: "/repo/pkg/.agent-sync.yaml"},
	}

	got := selectWriteScopes(scopes, false)
	emit := 0
	for _, sc := range got {
		if sc.Emit {
			emit++
		}
	}
	if emit != 1 {
		t.Fatalf("emit scopes = %d, want 1", emit)
	}
	if got[len(got)-1].Level != hierarchy.LevelDirectory || !got[len(got)-1].Emit {
		t.Fatalf("got %#v, want directory scope as selected emit", got[len(got)-1])
	}
}

func TestSelectWriteScopes_UserFlagSelectsOnlyUser(t *testing.T) {
	scopes := []hierarchy.Scope{
		{Level: hierarchy.LevelWorkspace, Emit: true, ManifestPath: "/ws/.agent-sync.yaml"},
		{Level: hierarchy.LevelProject, Emit: true, ManifestPath: "/repo/.agent-sync.yaml"},
		{Level: hierarchy.LevelUser, Emit: false, ManifestPath: "/home/.agent-sync.yaml"},
	}

	got := selectWriteScopes(scopes, true)
	emit := 0
	for _, sc := range got {
		if sc.Emit {
			emit++
		}
	}
	if emit != 1 {
		t.Fatalf("emit scopes = %d, want 1", emit)
	}
	if got[2].Level != hierarchy.LevelUser || !got[2].Emit {
		t.Fatalf("got %#v, want user scope as selected emit", got[2])
	}
}

func TestApplyResolvedLayersUsesRequestScope(t *testing.T) {
	sc := hierarchy.Scope{Level: hierarchy.LevelProject, ManifestPath: "/repo/.agent-sync.yaml"}
	req := engine.Request{
		Scope: manifest.ScopeGlobal,
		Fragments: []harness.Fragment{{
			ID:          "hooks",
			Target:      "codex",
			Path:        ".codex/config.toml",
			Merge:       harness.MergeTOMLKey,
			Locator:     "features.hooks",
			Scope:       manifest.ScopeGlobal,
			Inheritance: harness.InheritanceRootOnly,
			Visibility:  harness.VisibilityTeam,
			Payload:     []byte("current\n"),
		}},
	}

	applyResolvedLayers(&req, sc, []hierarchy.Scope{sc}, nil)
	if len(req.Fragments) != 1 {
		t.Fatalf("fragments = %+v, want current global fragment", req.Fragments)
	}
	if string(req.Fragments[0].Payload) != "current\n" {
		t.Fatalf("payload = %q, want current", req.Fragments[0].Payload)
	}
}

func TestApplyResolvedLayersKeepsCurrentScopeWhenReadOnlyLayerMissing(t *testing.T) {
	workspace := hierarchy.Scope{Level: hierarchy.LevelWorkspace, ManifestPath: "/ws/.agent-sync.yaml"}
	project := hierarchy.Scope{Level: hierarchy.LevelProject, ManifestPath: "/ws/repo/.agent-sync.yaml"}
	req := engine.Request{
		Scope: manifest.ScopeProject,
		Nodes: []ir.Node{{
			ID:   "project-skill",
			Kind: ir.KindSkill,
			Body: []byte("project\n"),
		}},
		Skills: map[string]ir.Skill{
			"project-skill": {Node: ir.Node{ID: "project-skill", Kind: ir.KindSkill}},
		},
		SourceURL: "project-source",
	}
	preparedLayers := []preparedLayer{{
		Scope: workspace,
		Materialized: materialized{
			Nodes: []ir.Node{{ID: "workspace-skill", Kind: ir.KindSkill, Body: []byte("workspace\n")}},
			Skills: map[string]ir.Skill{
				"workspace-skill": {Node: ir.Node{ID: "workspace-skill", Kind: ir.KindSkill}},
			},
			SourceURL: "workspace-source",
		},
	}}

	applyResolvedLayers(&req, project, []hierarchy.Scope{workspace, project}, preparedLayers)
	if len(req.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want workspace plus current project", req.Nodes)
	}
	if _, ok := req.Skills["project-skill"]; !ok {
		t.Fatalf("current project skill missing: %+v", req.Skills)
	}
	if req.Nodes[1].ID != "project-skill" {
		t.Fatalf("last node = %+v, want current project node", req.Nodes[1])
	}
}

func TestRunHierarchySync_ContinuesWhenInheritedWorkspaceLayerFails(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(home, "ActualReality")
	repo := filepath.Join(workspaceRoot, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspaceManifest := "version: 1\n" +
		"scope: " + manifest.ScopeWorkspace + "\n" +
		"activation_root: true\n" +
		"canonical:\n" +
		"  local_dir: .agents\n" +
		"targets:\n" +
		"  - claude\n"
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".agent-sync.yaml"), []byte(workspaceManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, workspaceRoot, ".agents/skills/ws-skill/SKILL.md", "workspace skill\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(hierarchyLocalDirManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/AGENTS.md", "project instructions\n")

	rc := newTestRuntime()
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	if _, _, err := runHierarchySync(
		context.Background(), rc, repo, home,
		hierarchySyncOptions{EngineOpts: hierarchyEngineOpts(rc, now)},
		now,
	); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	inherited := filepath.Join(repo, ".claude", "skills", "agent-sync-ws-skill", "SKILL.md")
	mustExist(t, inherited)

	if err := os.RemoveAll(filepath.Join(workspaceRoot, ".agents")); err != nil {
		t.Fatal(err)
	}
	outcomes, _, err := runHierarchySync(
		context.Background(), rc, repo, home,
		hierarchySyncOptions{EngineOpts: hierarchyEngineOpts(rc, now)},
		now,
	)
	if err != nil {
		t.Fatalf("second sync returned top-level error: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(outcomes))
	}
	if outcomes[0].Err != nil {
		t.Fatalf("selected scope should continue when inherited workspace layer cannot materialize: %v", outcomes[0].Err)
	}
	mustNotExist(t, inherited)
}

// TestHierarchySyncEmitsCoverageWarning checks that a directory-level scope
// emitting a skill for target claude carries a coverage warning (claude does
// not read nested skills natively).
func TestHierarchySyncEmitsCoverageWarning(t *testing.T) {
	home, _, nested := hierarchyTree(t)
	rc := newTestRuntime()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	outcomes, _, err := runHierarchySync(
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
	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(outcomes))
	}
	dir := outcomes[0]
	if dir.Scope.Level != hierarchy.LevelDirectory {
		t.Fatalf("scope level = %s, want directory", dir.Scope.Level)
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
	if err := renderHierarchyText(&buf, outcomes, ""); err != nil {
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
	if err := renderHierarchyJSON(&jbuf, outcomes, ""); err != nil {
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

// TestSyncCommandHomeManifestOnlyHintsUserFlag guards the silent-no-op UX gap:
// a plain `sync` run from the home directory itself, where the only manifest is
// ~/.agent-sync.yaml, discovers the user scope read-only (no --user) and emits
// nothing. The run must stay exit 0 and write nothing under home, but the report
// must say WHY it did nothing and point at `sync --user` instead of printing an
// empty document.
func TestSyncCommandHomeManifestOnlyHintsUserFlag(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(hierarchyLocalDirManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := runSyncHierarchy(t, home, home)
	if err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	var doc struct {
		Notice   string            `json:"notice"`
		Scopes   []json.RawMessage `json:"scopes"`
		ExitCode int               `json:"exit_code"`
	}
	if uerr := json.Unmarshal([]byte(out), &doc); uerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", uerr, out)
	}
	if len(doc.Scopes) != 0 {
		t.Errorf("got %d scopes, want 0 (user scope is read-only without --user)", len(doc.Scopes))
	}
	if doc.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", doc.ExitCode)
	}
	if !strings.Contains(doc.Notice, "--user") {
		t.Errorf("notice %q does not point at --user", doc.Notice)
	}
	// The hint must not change the safety invariant: nothing written under home.
	mustNotExist(t, filepath.Join(home, ".claude"))
}

// TestSyncCommandNoManifestHintsInit: a sync that discovers no manifest at all
// (no project, no user) must explain itself and point at `agent-sync init`
// rather than printing an empty document.
func TestSyncCommandNoManifestHintsInit(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := runSyncHierarchy(t, cwd, home)
	if err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	var doc struct {
		Notice   string            `json:"notice"`
		Scopes   []json.RawMessage `json:"scopes"`
		ExitCode int               `json:"exit_code"`
	}
	if uerr := json.Unmarshal([]byte(out), &doc); uerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", uerr, out)
	}
	if len(doc.Scopes) != 0 {
		t.Errorf("got %d scopes, want 0", len(doc.Scopes))
	}
	if !strings.Contains(doc.Notice, "agent-sync init") {
		t.Errorf("notice %q does not point at agent-sync init", doc.Notice)
	}
}

func TestRunHierarchySync_MixedFrozenAndAutoScopes(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	projectWT, _, projectHead := makeUpdateRepo(t)
	projectSrv := serveCanonicalRepo(t, projectWT)
	dirWT, _, dirHead := makeUpdateRepo(t)
	dirSrv := serveCanonicalRepo(t, dirWT)
	autoFalse := false
	home, repo, nested := hierarchyURLTree(t, projectSrv.URL, projectHead, &autoFalse, dirSrv.URL, dirHead, nil)

	if _, errOut, err := runSyncHierarchy(t, nested, home); err != nil {
		t.Fatalf("initial hierarchy sync: %v\nstderr: %s", err, errOut)
	}

	_ = commitFile(t, projectWT, "rules/project-new.md", "Project new.\n", "project second")
	projectSrv.PushMain(t)
	dirNew := commitFile(t, dirWT, "rules/dir-new.md", "Dir new.\n", "dir second")
	dirSrv.PushMain(t)

	if _, errOut, err := runSyncHierarchy(t, nested, home); err != nil {
		t.Fatalf("second hierarchy sync: %v\nstderr: %s", err, errOut)
	}
	projectManifest := string(readManifest(t, repo))
	if !strings.Contains(projectManifest, "commit: "+projectHead) {
		t.Fatalf("frozen project scope should stay pinned:\n%s", projectManifest)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".claude", "rules", "agent-sync", "project-new.md")); !os.IsNotExist(statErr) {
		t.Fatalf("frozen project scope should not land the new rule, stat err = %v", statErr)
	}
	dirManifest := string(readManifest(t, nested))
	if !strings.Contains(dirManifest, "commit: "+dirNew) {
		t.Fatalf("auto directory scope should advance:\n%s", dirManifest)
	}
	mustExist(t, filepath.Join(nested, ".claude", "rules", "agent-sync", "dir-new.md"))
}

// TestRunHierarchySync_AdvanceRefusalReportsScopeAndContinues verifies that when
// the single write scope's upstream is force-pushed (rewritten history), the
// auto-advance refuses fast-forward-only, reports the refusal with the
// trust-decision exit code, keeps the cached pin, and does not emit the
// rewritten content. Under the harness-hierarchy model exactly one scope writes
// per run (the nearest non-user scope); ancestors are read-only layers.
func TestRunHierarchySync_AdvanceRefusalReportsScopeAndContinues(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	projectWT, _, projectHead := makeUpdateRepo(t)
	projectSrv := serveCanonicalRepo(t, projectWT)
	dirWT, dirRoot, dirHead := makeUpdateRepo(t)
	dirSrv := serveCanonicalRepo(t, dirWT)
	home, _, nested := hierarchyURLTree(t, projectSrv.URL, projectHead, nil, dirSrv.URL, dirHead, nil)
	rc := newTestRuntime()
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	if _, errOut, err := runSyncHierarchy(t, nested, home); err != nil {
		t.Fatalf("initial hierarchy sync: %v\nstderr: %s", err, errOut)
	}

	// Rewrite the write scope's (nested) upstream history so the newest SHA is
	// not a fast-forward from the pin.
	mustGit(t, dirWT, "reset", "--hard", dirRoot)
	_ = commitFile(t, dirWT, "rules/rewritten.md", "Rewritten.\n", "rewritten history")
	dirSrv.PushMain(t)

	outcomes, _, err := runHierarchySync(
		context.Background(), rc, nested, home,
		hierarchySyncOptions{IncludeUser: false, Frozen: false, EngineOpts: engine.Options{
			Mode:   report.ModeAtomic,
			Now:    func() time.Time { return now },
			Logger: rc.Logger,
		}},
		now,
	)
	if err != nil {
		t.Fatalf("runHierarchySync: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1 (single write scope)", len(outcomes))
	}
	if outcomes[0].Err == nil {
		t.Fatal("rewritten-history write scope should report a refusal")
	}
	if !strings.Contains(outcomes[0].Err.Error(), "fast-forward") {
		t.Fatalf("scope error should name the fast-forward refusal, got: %v", outcomes[0].Err)
	}
	if got := hierarchyExitCode(outcomes); got != trust.ExitTrustDecisionRequired {
		t.Fatalf("hierarchy exit code = %d, want %d", got, trust.ExitTrustDecisionRequired)
	}

	dirManifest := string(readManifest(t, nested))
	if !strings.Contains(dirManifest, "commit: "+dirHead) {
		t.Fatalf("rewritten-history scope should keep its cached pin:\n%s", dirManifest)
	}
	if _, statErr := os.Stat(filepath.Join(nested, ".claude", "rules", "agent-sync", "rewritten.md")); !os.IsNotExist(statErr) {
		t.Fatalf("rewritten-history scope should not land the rewritten rule, stat err = %v", statErr)
	}
}

// TestSyncCommandNoticeAbsentWhenScopesEmit: the empty-run notice is strictly
// for zero-emit runs; a normal sync must not carry one.
func TestSyncCommandNoticeAbsentWhenScopesEmit(t *testing.T) {
	home, _, nested := hierarchyTree(t)

	out, errOut, err := runSyncHierarchy(t, nested, home)
	if err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	var doc struct {
		Notice string            `json:"notice"`
		Scopes []json.RawMessage `json:"scopes"`
	}
	if uerr := json.Unmarshal([]byte(out), &doc); uerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", uerr, out)
	}
	if len(doc.Scopes) == 0 {
		t.Fatal("expected emit scopes, got none")
	}
	if doc.Notice != "" {
		t.Errorf("notice = %q, want empty on a run with emit scopes", doc.Notice)
	}
}

// TestSyncUserScope_ClaudeTargetsRealUserPaths is the U4 end-to-end proof:
// a `sync --user` for the Claude target writes the agents-md overlay to
// ~/.claude/CLAUDE.md and merges the mcp-server-entry into ~/.claude.json
// (preserving foreign keys), and writes NONE of the dead project-scope paths
// (~/CLAUDE.md, ~/.mcp.json, ~/.agent-sync-managed). Re-running is idempotent.
func TestSyncUserScope_ClaudeTargetsRealUserPaths(t *testing.T) {
	home, _, nested := hierarchyTree(t)

	// User-level manifest + canonical content authored in-repo at home/.agents:
	// one agents-md overlay and one mcp-server-entry.
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(hierarchyLocalDirManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/AGENTS.md", "team standards body\n")
	writeWS(t, home, ".agents/mcp/teamserver.json", `{"command":"echo","args":["hi"]}`+"\n")

	// Seed ~/.claude.json with foreign content (a sibling MCP server and an
	// unrelated top-level key) that the surgical merge must preserve byte-intact.
	claudeJSON := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claudeJSON, []byte(`{"existingKey":"keepme","mcpServers":{"other":{"command":"x","type":"stdio"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, errOut, err := runSyncHierarchy(t, nested, home, "--user"); err != nil {
		t.Fatalf("sync --user failed: %v\nstderr: %s", err, errOut)
	}

	// R-US1: agents-md lands at ~/.claude/CLAUDE.md as a managed section.
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	mustExist(t, claudeMD)
	body, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("team standards body")) {
		t.Errorf("~/.claude/CLAUDE.md missing managed body:\n%s", body)
	}
	if !bytes.Contains(body, []byte("agent-sync:begin")) {
		t.Errorf("~/.claude/CLAUDE.md missing managed-section markers:\n%s", body)
	}

	// R-US2: mcp-server-entry merges into ~/.claude.json, foreign keys survive.
	raw, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("~/.claude.json no longer valid JSON: %v\n%s", err, raw)
	}
	if doc["existingKey"] != "keepme" {
		t.Errorf("foreign top-level key clobbered: %v", doc["existingKey"])
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Errorf("foreign mcp server 'other' clobbered: %v", servers)
	}
	if _, ok := servers["agentsync_teamserver"]; !ok {
		t.Errorf("agent-sync mcp entry not merged into ~/.claude.json: %v", servers)
	}

	// Dead-path guard: the project-scope outputs and sidecar are NOT written at home.
	mustNotExist(t, filepath.Join(home, "CLAUDE.md"))
	mustNotExist(t, filepath.Join(home, ".mcp.json"))
	mustNotExist(t, filepath.Join(home, ".agent-sync-managed"))

	// Idempotent re-run: succeeds, foreign content stays intact, and the
	// managed outputs are unchanged (no duplicated sections, no dropped entry).
	claudeMDBefore, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := runSyncHierarchy(t, nested, home, "--user"); err != nil {
		t.Fatalf("second sync --user failed: %v\nstderr: %s", err, errOut)
	}
	raw2, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	var doc2 map[string]any
	if err := json.Unmarshal(raw2, &doc2); err != nil {
		t.Fatalf("~/.claude.json invalid after re-sync: %v", err)
	}
	if doc2["existingKey"] != "keepme" {
		t.Errorf("re-sync clobbered foreign key: %v", doc2["existingKey"])
	}
	servers2, _ := doc2["mcpServers"].(map[string]any)
	if _, ok := servers2["agentsync_teamserver"]; !ok {
		t.Errorf("re-sync dropped agent-sync mcp entry: %v", servers2)
	}
	claudeMDAfter, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(claudeMDBefore, claudeMDAfter) {
		t.Errorf("re-sync changed ~/.claude/CLAUDE.md unexpectedly:\nbefore:\n%s\nafter:\n%s", claudeMDBefore, claudeMDAfter)
	}
}

// TestSyncUserScope_CursorCodexTargetRealUserPaths is the cursor+codex sibling
// of the claude user-scope E2E. It exercises the full `sync --user` flow and
// asserts the scope-aware destinations:
//   - codex agents-md → ~/.codex/AGENTS.md (NOT ~/AGENTS.md), mcp → ~/.codex/config.toml
//   - cursor mcp → ~/.cursor/mcp.json (foreign keys preserved), no sidecar
//   - neither writes a user-global rule or a project-root AGENTS.md (no home for them)
func TestSyncUserScope_CursorCodexTargetRealUserPaths(t *testing.T) {
	home, _, nested := hierarchyTree(t)

	// User-level manifest targeting cursor + codex, canonical content in home/.agents.
	userManifest := "version: 1\n" +
		"canonical:\n" +
		"  local_dir: .agents\n" +
		"targets:\n" +
		"  - cursor\n" +
		"  - codex\n"
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(userManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/AGENTS.md", "team standards body\n")
	writeWS(t, home, ".agents/mcp/teamserver.json", `{"command":"echo","args":["hi"]}`+"\n")
	writeWS(t, home, ".agents/rules/style.md", "use tabs\n")

	// Seed ~/.cursor/mcp.json with foreign content the surgical merge must keep.
	cursorMCP := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursorMCP, []byte(`{"existingKey":"keepme","mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, errOut, err := runSyncHierarchy(t, nested, home, "--user"); err != nil {
		t.Fatalf("sync --user failed: %v\nstderr: %s", err, errOut)
	}

	// Codex agents-md → ~/.codex/AGENTS.md (the remap), as a managed section.
	codexAgentsMD := filepath.Join(home, ".codex", "AGENTS.md")
	mustExist(t, codexAgentsMD)
	cBody, err := os.ReadFile(codexAgentsMD)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(cBody, []byte("team standards body")) {
		t.Errorf("~/.codex/AGENTS.md missing managed body:\n%s", cBody)
	}
	if !bytes.Contains(cBody, []byte("agent-sync:begin")) || !bytes.Contains(cBody, []byte("agent-sync:end")) {
		t.Errorf("~/.codex/AGENTS.md missing managed-section markers:\n%s", cBody)
	}

	// Codex mcp → ~/.codex/config.toml.
	codexConfig := filepath.Join(home, ".codex", "config.toml")
	mustExist(t, codexConfig)
	tomlBody, err := os.ReadFile(codexConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(tomlBody, []byte("mcp_servers.agentsync_teamserver")) {
		t.Errorf("~/.codex/config.toml missing agent-sync mcp table:\n%s", tomlBody)
	}

	// Cursor mcp → ~/.cursor/mcp.json, foreign keys survive.
	raw, err := os.ReadFile(cursorMCP)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("~/.cursor/mcp.json no longer valid JSON: %v\n%s", err, raw)
	}
	if doc["existingKey"] != "keepme" {
		t.Errorf("cursor foreign top-level key clobbered: %v", doc["existingKey"])
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Errorf("cursor foreign mcp server 'other' clobbered: %v", servers)
	}
	if _, ok := servers["agentsync_teamserver"]; !ok {
		t.Errorf("agent-sync mcp entry not merged into ~/.cursor/mcp.json: %v", servers)
	}

	// Dead-path guards: no inert user-scope writes.
	mustNotExist(t, filepath.Join(home, ".cursor", ".agent-sync-managed")) // sidecar suppressed
	mustNotExist(t, filepath.Join(home, "AGENTS.md"))                      // cursor has no user-global AGENTS.md; codex remaps under .codex/
	mustNotExist(t, filepath.Join(home, ".cursor", "rules", "agent-sync")) // cursor has no user-global rules home
}

// TestSyncUserScope_CursorRuleOnlyDoesNotFail is a regression test for the
// capability-lied gate interaction (PR #31 review): a `sync --user` manifest
// targeting Cursor with ONLY a rule node (no MCP entry) must succeed as an
// honest no-op, not fail. Cursor skips rule at user scope and emits zero
// non-warning ops; if the adapter still declared rule "supported" at user
// scope, the runtime's capability-lied gate would reject the session. The fix
// declares rule/agents-md unsupported at user scope, so this stays a clean
// no-op surfaced via a coverage warning.
func TestSyncUserScope_CursorRuleOnlyDoesNotFail(t *testing.T) {
	home, _, nested := hierarchyTree(t)

	userManifest := "version: 1\n" +
		"canonical:\n" +
		"  local_dir: .agents\n" +
		"targets:\n" +
		"  - cursor\n"
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(userManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only a rule — no MCP entry, no agents-md. This is the exact gate-tripping shape.
	writeWS(t, home, ".agents/rules/style.md", "use tabs\n")

	if _, errOut, err := runSyncHierarchy(t, nested, home, "--user"); err != nil {
		t.Fatalf("sync --user with cursor rule-only must not fail (capability-lied gate regression): %v\nstderr: %s", err, errOut)
	}

	// The rule has no user-global home, so nothing is written under ~/.cursor/rules.
	mustNotExist(t, filepath.Join(home, ".cursor", "rules", "agent-sync"))
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
	if err := renderHierarchyText(&buf, outcomes, ""); err != nil {
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

// TestRenderHierarchyTextNotice: a zero-emit run's notice renders as a single
// explanatory line instead of empty output.
func TestRenderHierarchyTextNotice(t *testing.T) {
	var buf bytes.Buffer
	if err := renderHierarchyText(&buf, nil, "manifest at /home/u/.agent-sync.yaml is the user scope and a plain sync never writes to the home directory; run 'agent-sync sync --user' to sync it"); err != nil {
		t.Fatalf("renderHierarchyText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "nothing to sync:") || !strings.Contains(out, "sync --user") {
		t.Errorf("text output missing notice line:\n%s", out)
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
	if err := renderHierarchyJSON(&buf, outcomes, ""); err != nil {
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

// writeUserManifest adds a user-level manifest + authored skill at home.
func writeUserManifest(t *testing.T, home string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(hierarchyLocalDirManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/skills/user-skill/SKILL.md", "user skill body\n")
}

func hierarchyEngineOpts(rc *runtimeContext, now time.Time) engine.Options {
	return engine.Options{
		Mode:   report.ModeAtomic,
		Now:    func() time.Time { return now },
		Logger: rc.Logger,
	}
}

// TestRunHierarchySync_SkippedUserNotice pins plan R17: without a prompt
// (non-interactive), a user manifest that was not synced yields a persistent
// notice even though project scopes emitted fine.
func TestRunHierarchySync_SkippedUserNotice(t *testing.T) {
	home, _, nested := hierarchyTree(t)
	writeUserManifest(t, home)
	rc := newTestRuntime()
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	outcomes, notice, err := runHierarchySync(
		context.Background(), rc, nested, home,
		hierarchySyncOptions{IncludeUser: false, EngineOpts: hierarchyEngineOpts(rc, now)},
		now,
	)
	if err != nil {
		t.Fatalf("runHierarchySync: %v", err)
	}
	if len(outcomes) == 0 {
		t.Fatal("expected one emitted scope")
	}
	if !strings.Contains(notice, "--user") {
		t.Fatalf("notice = %q, want a skipped-user notice pointing at --user", notice)
	}
	mustNotExist(t, filepath.Join(home, ".claude"))
}

// TestRunHierarchySync_NoNoticeWithUserFlag: --user syncs the user scope, so
// there is nothing to notice.
func TestRunHierarchySync_NoNoticeWithUserFlag(t *testing.T) {
	home, _, nested := hierarchyTree(t)
	writeUserManifest(t, home)
	rc := newTestRuntime()
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	_, notice, err := runHierarchySync(
		context.Background(), rc, nested, home,
		hierarchySyncOptions{IncludeUser: true, EngineOpts: hierarchyEngineOpts(rc, now)},
		now,
	)
	if err != nil {
		t.Fatalf("runHierarchySync: %v", err)
	}
	if notice != "" {
		t.Fatalf("notice = %q, want empty under --user", notice)
	}
}

// TestRenderHierarchyText_NoticePlacement pins plan R17/R18 rendering: with
// outcomes, the notice trails the labeled scope blocks as a note: line (and
// each scope renders exactly one == header); with zero outcomes the existing
// nothing-to-sync prefix is preserved.
func TestRenderHierarchyText_NoticePlacement(t *testing.T) {
	outcomes := []scopeOutcome{
		{Scope: hierarchy.Scope{Root: "/repo", Level: hierarchy.LevelProject}},
		{Scope: hierarchy.Scope{Root: "/home/u", Level: hierarchy.LevelUser}},
	}

	var b bytes.Buffer
	if err := renderHierarchyText(&b, outcomes, "user-level manifest at /home/u/.agent-sync.yaml was not synced; pass --user to include it"); err != nil {
		t.Fatalf("renderHierarchyText: %v", err)
	}
	out := b.String()
	if got := strings.Count(out, "== "); got != 2 {
		t.Fatalf("scope headers = %d, want exactly 2:\n%s", got, out)
	}
	if !strings.Contains(out, "note: user-level manifest") {
		t.Fatalf("notice should render as a trailing note::\n%s", out)
	}
	if strings.Contains(out, "nothing to sync") {
		t.Fatalf("nothing-to-sync prefix is reserved for zero-emit runs:\n%s", out)
	}
	if idx := strings.Index(out, "note:"); idx < strings.LastIndex(out, "== ") {
		t.Fatalf("note must trail the scope blocks:\n%s", out)
	}

	b.Reset()
	if err := renderHierarchyText(&b, nil, "no .agent-sync.yaml found"); err != nil {
		t.Fatalf("renderHierarchyText: %v", err)
	}
	if !strings.Contains(b.String(), "nothing to sync: no .agent-sync.yaml found") {
		t.Fatalf("zero-emit rendering changed:\n%s", b.String())
	}
}

// TestSyncCommandSkippedUserNoticeJSON drives the real command end-to-end:
// a piped (non-interactive) sync with an unsynced user manifest carries the
// notice in the JSON document alongside the emitted scopes.
func TestSyncCommandSkippedUserNoticeJSON(t *testing.T) {
	home, _, nested := hierarchyTree(t)
	writeUserManifest(t, home)

	out, errOut, err := runSyncHierarchy(t, nested, home)
	if err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}
	var doc struct {
		Notice string            `json:"notice"`
		Scopes []json.RawMessage `json:"scopes"`
	}
	if uerr := json.Unmarshal([]byte(out), &doc); uerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", uerr, out)
	}
	if len(doc.Scopes) != 1 {
		t.Fatalf("scopes = %d, want 1", len(doc.Scopes))
	}
	if !strings.Contains(doc.Notice, "--user") {
		t.Fatalf("notice = %q, want skipped-user notice", doc.Notice)
	}
	mustNotExist(t, filepath.Join(home, ".claude"))
}

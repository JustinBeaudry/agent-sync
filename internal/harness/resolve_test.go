package harness

import (
	"testing"

	"github.com/agent-sync/agent-sync/internal/ir"
)

func TestResolveNodes_WorkspaceSkillInheritedIntoProject(t *testing.T) {
	ls := ir.Node{ID: "code-review", Kind: ir.KindSkill, Body: []byte("workspace skill\n")}
	skills := map[string]ir.Skill{
		"code-review": {
			Node: ls,
		},
	}

	outNodes, outSkills := ResolveNodes([]Layer{{
		Scope:     "workspace",
		Nodes:     []ir.Node{ls},
		Skills:    skills,
		SourceURL: "workspace-source",
	}, {
		Scope: "project",
		Nodes: nil,
	}}, "project")

	if len(outNodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(outNodes))
	}
	if outNodes[0].SourceURL != "workspace-source" {
		t.Fatalf("SourceURL = %q, want workspace-source", outNodes[0].SourceURL)
	}
	if _, ok := outSkills["code-review"]; !ok {
		t.Fatalf("skill assets missing: %+v", outSkills)
	}
}

func TestResolveNodes_ProjectShadowsWorkspaceByKindAndID(t *testing.T) {
	workspaceNode := ir.Node{ID: "code-review", Kind: ir.KindSkill, Body: []byte("workspace skill\n")}
	projectNode := ir.Node{ID: "code-review", Kind: ir.KindSkill, Body: []byte("project skill\n")}

	outNodes, _ := ResolveNodes([]Layer{
		{Scope: "workspace", Nodes: []ir.Node{workspaceNode}, SourceURL: "workspace-source"},
		{Scope: "project", Nodes: []ir.Node{projectNode}, SourceURL: "project-source"},
	}, "project")

	if len(outNodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(outNodes))
	}
	if string(outNodes[0].Body) != "project skill\n" {
		t.Fatalf("body = %q, want project skill", string(outNodes[0].Body))
	}
}

func TestResolveNodes_UserLayerDoesNotInheritIntoProjectByDefault(t *testing.T) {
	userNode := ir.Node{ID: "personal", Kind: ir.KindSkill, Body: []byte("personal skill\n")}

	outNodes, _ := ResolveNodes([]Layer{
		{Scope: "user", Nodes: []ir.Node{userNode}, SourceURL: "user-source"},
		{Scope: "project", Nodes: nil},
	}, "project")
	if len(outNodes) != 0 {
		t.Fatalf("nodes = %d, want 0", len(outNodes))
	}
}

func TestResolveFragments_ClosestScopeWinsByIdentity(t *testing.T) {
	ws := Fragment{
		ID:          "hooks",
		Target:      "codex",
		Path:        ".codex/config.toml",
		Merge:       MergeTOMLKey,
		Locator:     "features.hooks",
		Inheritance: InheritanceDescendants,
		Visibility:  VisibilityTeam,
		Payload:     []byte("[features]\nhooks = true\n"),
	}
	project := Fragment{
		ID:          "hooks",
		Target:      "codex",
		Path:        ".codex/config.toml",
		Merge:       MergeTOMLKey,
		Locator:     "features.hooks",
		Inheritance: InheritanceRootOnly,
		Visibility:  VisibilityTeam,
		Payload:     []byte("[features]\nhooks = false\n"),
	}

	got := ResolveFragments([]Layer{
		{Scope: "workspace", Fragments: []Fragment{ws}},
		{Scope: "project", Fragments: []Fragment{project}},
	}, "project")

	if len(got) != 1 {
		t.Fatalf("fragments = %d, want 1", len(got))
	}
	if string(got[0].Payload) != string(project.Payload) {
		t.Fatalf("payload = %q, want %q", string(got[0].Payload), string(project.Payload))
	}
}

func TestResolveFragments_SkipsPersonalAncestorForProject(t *testing.T) {
	user := Fragment{
		ID:          "hooks",
		Target:      "codex",
		Path:        ".codex/config.toml",
		Merge:       MergeTOMLKey,
		Locator:     "features.hooks",
		Inheritance: InheritanceDescendants,
		Visibility:  VisibilityPersonal,
		Payload:     []byte("[features]\nhooks = false\n"),
		Scope:       "user",
	}

	got := ResolveFragments([]Layer{
		{Scope: "user", Fragments: []Fragment{user}},
		{Scope: "project", Fragments: nil},
	}, "project")
	if len(got) != 0 {
		t.Fatalf("fragments = %+v, want none", got)
	}
}

func TestResolveFragments_IncludesTeamDescendantAncestor(t *testing.T) {
	ws := Fragment{
		ID:          "hooks",
		Target:      "codex",
		Path:        ".codex/config.toml",
		Merge:       MergeTOMLKey,
		Locator:     "features.hooks",
		Inheritance: InheritanceDescendants,
		Visibility:  VisibilityTeam,
		Payload:     []byte("[features]\nhooks = true\n"),
		Scope:       "workspace",
	}

	got := ResolveFragments([]Layer{
		{Scope: "workspace", Fragments: []Fragment{ws}},
		{Scope: "project", Fragments: nil},
	}, "project")
	if len(got) != 1 {
		t.Fatalf("fragments = %+v, want workspace fragment", got)
	}
}

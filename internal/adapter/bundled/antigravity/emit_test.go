package antigravity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// captureOps replicates handleEmit's flow (sort + dedup + dispatch) at project
// scope but returns the raw emittedOps so tests can inspect op-specific fields.
func captureOps(raw json.RawMessage) (*emittedOps, error) {
	return captureOpsScope(raw, "project")
}

func captureOpsScope(raw json.RawMessage, scope string) (*emittedOps, error) {
	doc, err := decodeIRDocument(raw)
	if err != nil {
		return nil, err
	}
	if err := rejectDuplicateNodes(doc.Nodes); err != nil {
		return nil, err
	}
	emitted := &emittedOps{}
	state := &emitState{
		readmeEmitted:   map[string]bool{},
		emittedFilePath: map[string]struct{}{},
		paths:           resolvePathSet(scope),
	}
	sort.Slice(doc.Nodes, func(i, j int) bool {
		if doc.Nodes[i].Kind != doc.Nodes[j].Kind {
			return doc.Nodes[i].Kind < doc.Nodes[j].Kind
		}
		return doc.Nodes[i].ID < doc.Nodes[j].ID
	})
	for _, n := range doc.Nodes {
		if !nodeTargetsAntigravity(n) {
			continue
		}
		if err := dispatchNode(emitted, n, state); err != nil {
			return nil, err
		}
	}
	return emitted, nil
}

func emitFixture(t *testing.T, name string) (adapterkit.EmitResult, []adapterkit.Op) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ir", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	emitted, err := captureOps(raw)
	if err != nil {
		t.Fatalf("captureOps %s: %v", name, err)
	}
	return adapterkit.EmitResult{OpsPerformed: emitted.records()}, emitted.ops
}

// emitDocScope runs the production handleEmit over a raw IR document at the given
// initialize scope.
func emitDocScope(t *testing.T, raw, scope string) (adapterkit.EmitResult, error) {
	t.Helper()
	return handleEmit(context.Background(), adapterkit.EmitParams{Target: adapterName, IR: json.RawMessage(raw)}, scope, "", "")
}

func TestEmitAgentsMD_HappyPath(t *testing.T) {
	t.Parallel()

	_, ops := emitFixture(t, "agents-md-only.json")
	op, ok := findToolOwned(ops, "GEMINI.md")
	if !ok {
		t.Fatal("no write_tool_owned op for GEMINI.md")
	}
	if op.Kind != adapterkit.ToolOwnedKindMarkdownSection {
		t.Errorf("GEMINI.md op kind=%q want markdown-section", op.Kind)
	}
	if op.Locator != "agent-sync:team" {
		t.Errorf("GEMINI.md locator=%q want agent-sync:team", op.Locator)
	}
	content := string(op.Content)
	if strings.Contains(content, "<!-- agent-sync:") {
		t.Errorf("GEMINI.md content must be inner body without markers; got %q", content)
	}
	if !strings.Contains(content, "Use conventional commits.") {
		t.Errorf("GEMINI.md content missing body; got %q", content)
	}
}

func TestEmitAgentsMD_RejectsMarkerBody(t *testing.T) {
	t.Parallel()

	raw := `{"nodes":[{"id":"team","kind":"agents-md","body":"<!-- agent-sync:end id=x -->"}]}`
	if _, err := emitDocScope(t, raw, "project"); err == nil {
		t.Fatal("expected error for body containing marker syntax")
	}
}

func TestEmitRule_HappyPath(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "rule-only.json")
	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".agent/rules/agent-sync"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agent/rules/agent-sync/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agent/rules/agent-sync/no-fri.md"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}
	ruleOp := findWriteFile(t, ops, ".agent/rules/agent-sync/no-fri.md")
	if !strings.Contains(string(ruleOp.Content), "<!-- Managed by agent-sync") {
		t.Errorf("rule file missing managed header; got %q", ruleOp.Content)
	}
	if !strings.Contains(string(ruleOp.Content), "No deploys on Fridays.") {
		t.Errorf("rule file missing body; got %q", ruleOp.Content)
	}
}

func TestEmitCommand_HappyPath(t *testing.T) {
	t.Parallel()

	raw := `{"nodes":[{"id":"fmt","kind":"command","body":"# /fmt\n\nFormat the codebase."}]}`
	emitted, err := captureOps(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("captureOps: %v", err)
	}
	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".agent/workflows/agent-sync"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agent/workflows/agent-sync/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agent/workflows/agent-sync/fmt.md"},
	}
	if !reflect.DeepEqual(emitted.records(), wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", emitted.records(), wantRecords)
	}
}

func TestEmitSkill_WithAssets(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "skill-with-assets.json")
	// Assets sorted by rel_path: examples/usage.md before templates/foo.txt.
	// Skill has an authored description → no missing-description warning.
	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".agents/skills/agent-sync-coder"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/agent-sync-coder/SKILL.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/agent-sync-coder/examples/usage.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/agent-sync-coder/templates/foo.txt"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}
	skillOp := findWriteFile(t, ops, ".agents/skills/agent-sync-coder/SKILL.md")
	if !strings.HasPrefix(string(skillOp.Content), "---\nname: agent-sync-coder\n") {
		t.Errorf("SKILL.md must start with frontmatter at byte 0; got %q", skillOp.Content)
	}
}

func TestEmitSkill_NoDescriptionWarns(t *testing.T) {
	t.Parallel()

	raw := `{"nodes":[{"id":"coder","kind":"skill","body":"# c"}]}`
	emitted, err := captureOps(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("captureOps: %v", err)
	}
	if _, ok := findWarning(emitted.ops, "coder"); !ok {
		t.Error("description-less skill must emit a degraded warning")
	}
	// Still emits the SKILL.md (warning-plus-emit, never warning-only).
	findWriteFile(t, emitted.ops, ".agents/skills/agent-sync-coder/SKILL.md")
}

func TestEmitMCP_HappyPath(t *testing.T) {
	t.Parallel()

	_, ops := emitFixture(t, "mcp-only.json")
	op, ok := findToolOwned(ops, ".agents/mcp_config.json")
	if !ok {
		t.Fatal("no write_tool_owned op for .agents/mcp_config.json")
	}
	if op.Kind != adapterkit.ToolOwnedKindJSONPointer {
		t.Errorf("mcp op kind=%q want json-pointer", op.Kind)
	}
	if op.Locator != "/mcpServers/agentsync_lsp" {
		t.Errorf("mcp locator=%q want /mcpServers/agentsync_lsp", op.Locator)
	}
}

func TestEmitMCP_RejectsNonObjectBody(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{"nodes":[{"id":"lsp","kind":"mcp-server-entry","body":"[1,2,3]"}]}`,    // array
		`{"nodes":[{"id":"lsp","kind":"mcp-server-entry","body":"42"}]}`,         // number
		`{"nodes":[{"id":"lsp","kind":"mcp-server-entry","body":"not json {"}]}`, // invalid JSON
	} {
		if _, err := emitDocScope(t, body, "project"); err == nil {
			t.Errorf("expected error for non-object mcp body %q", body)
		}
	}
}

func TestEmitPluginReference_WarnsNoFiles(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "plugin-reference-warn.json")
	for _, r := range res.OpsPerformed {
		if r.Op != adapterkit.OpKindWarning {
			t.Errorf("plugin-reference must emit only warnings; got op %q path %q", r.Op, r.Path)
		}
	}
	w, ok := findWarning(ops, "some-plugin")
	if !ok {
		t.Fatal("expected degradation warning for plugin-reference")
	}
	if w.Status != adapterkit.WarningStatusDegraded {
		t.Errorf("warning status=%q want degraded", w.Status)
	}
}

func TestEmitMixed_EmitsAllKinds(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "mixed-everything.json")
	wantPaths := map[string]bool{
		"GEMINI.md":                                false,
		".agent/rules/agent-sync/no-fri.md":        false,
		".agent/workflows/agent-sync/fmt.md":       false,
		".agents/skills/agent-sync-coder/SKILL.md": false,
		".agents/mcp_config.json":                  false,
	}
	for _, r := range res.OpsPerformed {
		if _, ok := wantPaths[r.Path]; ok {
			wantPaths[r.Path] = true
		}
	}
	for p, seen := range wantPaths {
		if !seen {
			t.Errorf("mixed emit missing op for %q", p)
		}
	}
	if _, ok := findWarning(ops, "some-plugin"); !ok {
		t.Error("mixed emit must warn for the unsupported plugin-reference node")
	}
}

// TestEmit_UserScope pins the user-scope (sync --user) remaps: GEMINI.md, mcp,
// and skills move under ~/.gemini/, while the .agent/ rules dir is unchanged.
func TestEmit_UserScope(t *testing.T) {
	t.Parallel()

	raw := `{"nodes":[
		{"id":"team","kind":"agents-md","body":"Use conventional commits."},
		{"id":"no-fri","kind":"rule","body":"No Fridays."},
		{"id":"coder","kind":"skill","description":"d","body":"# c"},
		{"id":"lsp","kind":"mcp-server-entry","body":"{\"command\":\"node\"}"}
	]}`
	res, err := emitDocScope(t, raw, "user")
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}
	paths := map[string]bool{}
	for _, r := range res.OpsPerformed {
		paths[r.Path] = true
	}
	for _, want := range []string{
		".gemini/GEMINI.md",
		".gemini/config/mcp_config.json",
		".gemini/skills/agent-sync-coder/SKILL.md",
		".agent/rules/agent-sync/no-fri.md",
	} {
		if !paths[want] {
			t.Errorf("user-scope missing expected path %q; got %+v", want, res.OpsPerformed)
		}
	}
	for _, notWant := range []string{"GEMINI.md", ".agents/mcp_config.json", ".agents/skills/agent-sync-coder/SKILL.md"} {
		if paths[notWant] {
			t.Errorf("user-scope must NOT emit project-scope path %q; got %+v", notWant, res.OpsPerformed)
		}
	}
}

func TestEmit_TargetFilterSkipsOtherAdapters(t *testing.T) {
	t.Parallel()

	raw := `{"nodes":[{"id":"team","kind":"agents-md","targets":["claude"],"body":"x"}]}`
	emitted, err := captureOps(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("captureOps: %v", err)
	}
	if len(emitted.ops) != 0 {
		t.Errorf("node targeted at claude must be skipped; got %+v", emitted.ops)
	}
}

func TestEmit_DuplicateNodesRejected(t *testing.T) {
	t.Parallel()

	raw := `{"nodes":[
		{"id":"team","kind":"agents-md","body":"a"},
		{"id":"team","kind":"agents-md","body":"b"}
	]}`
	if _, err := emitDocScope(t, raw, "project"); err == nil {
		t.Fatal("expected error for duplicate (kind,id) nodes")
	}
}

func findWriteFile(t *testing.T, ops []adapterkit.Op, path string) adapterkit.OpWriteFile {
	t.Helper()
	for _, op := range ops {
		if wf, ok := op.(adapterkit.OpWriteFile); ok && wf.Path == path {
			return wf
		}
	}
	t.Fatalf("no write_file op for %q", path)
	return adapterkit.OpWriteFile{}
}

func findToolOwned(ops []adapterkit.Op, path string) (adapterkit.OpWriteToolOwned, bool) {
	for _, op := range ops {
		if to, ok := op.(adapterkit.OpWriteToolOwned); ok && to.Path == path {
			return to, true
		}
	}
	return adapterkit.OpWriteToolOwned{}, false
}

func findWarning(ops []adapterkit.Op, conceptID string) (adapterkit.OpWarning, bool) {
	for _, op := range ops {
		if w, ok := op.(adapterkit.OpWarning); ok && w.ConceptID == conceptID {
			return w, true
		}
	}
	return adapterkit.OpWarning{}, false
}

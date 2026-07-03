package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// captureOps replicates handleEmit's flow (sort + dedup + dispatch) but returns
// the raw emittedOps so tests can inspect op-specific fields.
func captureOps(raw json.RawMessage) (*emittedOps, error) {
	doc, err := decodeIRDocument(raw)
	if err != nil {
		return nil, err
	}
	if err := rejectDuplicateNodes(doc.Nodes); err != nil {
		return nil, err
	}
	emitted := &emittedOps{}
	state := &emitState{emittedFilePath: map[string]struct{}{}, paths: resolvePathSet("project")}
	sort.Slice(doc.Nodes, func(i, j int) bool {
		if doc.Nodes[i].Kind != doc.Nodes[j].Kind {
			return doc.Nodes[i].Kind < doc.Nodes[j].Kind
		}
		return doc.Nodes[i].ID < doc.Nodes[j].ID
	})
	for _, n := range doc.Nodes {
		if !nodeTargetsCodex(n) {
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

func TestEmitSkill_HappyPath(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "skill-only.json")
	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".agents/skills/agent-sync-coder"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/agent-sync-coder/SKILL.md"},
		{Op: adapterkit.OpKindWarning},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}
	skillOp := findWriteFile(t, ops, ".agents/skills/agent-sync-coder/SKILL.md")
	if !strings.HasPrefix(string(skillOp.Content), "---\nname: agent-sync-coder\n") {
		t.Errorf("SKILL.md must start with frontmatter at byte 0; got %q", skillOp.Content)
	}
	if !strings.Contains(string(skillOp.Content), "description: ") {
		t.Errorf("SKILL.md frontmatter missing description key; got %q", skillOp.Content)
	}
	if !strings.Contains(string(skillOp.Content), "<!-- Managed by agent-sync") {
		t.Errorf("SKILL.md missing managed header; got %q", skillOp.Content)
	}
	if !strings.Contains(string(skillOp.Content), "Write production code.") {
		t.Errorf("SKILL.md missing body; got %q", skillOp.Content)
	}
}

func TestEmitSkill_WithAssets(t *testing.T) {
	t.Parallel()

	res, _ := emitFixture(t, "skill-with-assets.json")
	// Assets sorted by rel_path: examples/usage.md before templates/foo.txt.
	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".agents/skills/agent-sync-coder"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/agent-sync-coder/SKILL.md"},
		{Op: adapterkit.OpKindWarning},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/agent-sync-coder/examples/usage.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/agent-sync-coder/templates/foo.txt"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}
}

func TestEmitAgentsMD_HappyPath(t *testing.T) {
	t.Parallel()

	_, ops := emitFixture(t, "agents-md-only.json")
	op, ok := findToolOwned(ops, "AGENTS.md")
	if !ok {
		t.Fatal("no write_tool_owned op for AGENTS.md")
	}
	if op.Kind != adapterkit.ToolOwnedKindMarkdownSection {
		t.Errorf("AGENTS.md op kind=%q want markdown-section", op.Kind)
	}
	if op.Locator != "agent-sync:team" {
		t.Errorf("AGENTS.md locator=%q want agent-sync:team", op.Locator)
	}
	content := string(op.Content)
	// The engine owns the begin/end markers (rendered from the locator during
	// the merge); the adapter sends the INNER body only.
	if strings.Contains(content, "<!-- agent-sync:") {
		t.Errorf("AGENTS.md content must be inner body without markers; got %q", content)
	}
	if !strings.Contains(content, "Use conventional commits.") {
		t.Errorf("AGENTS.md content missing body; got %q", content)
	}
}

// TestEmit_UserScope_AgentsMDRemapped pins the user-scope (sync --user)
// behavior: agents-md is emitted to .codex/AGENTS.md (→ ~/.codex/AGENTS.md,
// Codex's user-global instructions path — NOT ~/AGENTS.md), while MCP
// (.codex/config.toml) and skills (.agents/skills/) are unchanged (already
// correct under $HOME at user scope).
func TestEmit_UserScope_AgentsMDRemapped(t *testing.T) {
	t.Parallel()

	raw := `{"nodes":[
		{"id":"team","kind":"agents-md","body":"Use conventional commits."},
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
	if !paths[".codex/AGENTS.md"] {
		t.Errorf("user-scope agents-md must emit to .codex/AGENTS.md; got %+v", res.OpsPerformed)
	}
	if paths["AGENTS.md"] {
		t.Errorf("user-scope must NOT emit project-root AGENTS.md; got %+v", res.OpsPerformed)
	}
	if !paths[".codex/config.toml"] {
		t.Errorf("user-scope mcp must still emit to .codex/config.toml; got %+v", res.OpsPerformed)
	}
}

func TestEmitMCP_RendersValidTOMLBody(t *testing.T) {
	t.Parallel()

	_, ops := emitFixture(t, "mcp-server-entry-only.json")
	op, ok := findToolOwned(ops, ".codex/config.toml")
	if !ok {
		t.Fatal("no write_tool_owned op for .codex/config.toml")
	}
	if op.Kind != adapterkit.ToolOwnedKindTOMLPath {
		t.Errorf("mcp op kind=%q want toml-path", op.Kind)
	}
	if op.Locator != "mcp_servers.agentsync_lsp" {
		t.Errorf("mcp locator=%q want mcp_servers.agentsync_lsp", op.Locator)
	}
	body := string(op.Content)
	// Body is the table body (no header). Keys sorted: args, command, env.
	for _, want := range []string{
		`command = "node"`,
		`args = ["server.js", "--stdio"]`,
		`env = { LOG = "debug" }`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("toml body missing %q; got:\n%s", want, body)
		}
	}
	// The body must parse as valid TOML once placed under a table header
	// (this is what the string-aware line-splice merge validates).
	var m map[string]any
	if err := toml.Unmarshal([]byte("[mcp_servers.agentsync_lsp]\n"+body), &m); err != nil {
		t.Fatalf("rendered toml body does not parse under a header: %v\nbody:\n%s", err, body)
	}
}

func TestEmitUnsupported_WarnsNoFiles(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "unsupported-kinds.json")
	// No file/mkdir/tool-owned ops — only warnings.
	for _, r := range res.OpsPerformed {
		if r.Op != adapterkit.OpKindWarning {
			t.Errorf("unsupported kinds must emit only warnings; got op %q path %q", r.Op, r.Path)
		}
	}
	for _, conceptID := range []string{"no-fri", "draftpr", "some-plugin"} {
		w, ok := findWarning(ops, conceptID)
		if !ok {
			t.Errorf("expected degradation warning for %q", conceptID)
			continue
		}
		if w.Status != adapterkit.WarningStatusDegraded {
			t.Errorf("warning %q status=%q want degraded", conceptID, w.Status)
		}
	}
}

func TestEmitMixed_EmitsAllSupported(t *testing.T) {
	t.Parallel()

	res, _ := emitFixture(t, "mixed-everything.json")
	wantPaths := map[string]bool{
		".agents/skills/agent-sync-coder":          false,
		".agents/skills/agent-sync-coder/SKILL.md": false,
		".codex/config.toml":                       false,
		"AGENTS.md":                                false,
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
}

func TestEmitMCP_RejectsNonObjectBody(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"nodes":[{"id":"x","kind":"mcp-server-entry","body":"\"just-a-string\""}]}`)
	if _, err := captureOps(raw); err == nil {
		t.Fatal("expected error for non-object mcp body")
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

// TestEmitSkill_AuthoredDescription: an authored description lands in the
// emitted frontmatter verbatim (quoted scalar), replacing the fallback.
func TestEmitSkill_AuthoredDescription(t *testing.T) {
	t.Parallel()

	_, ops := emitFixture(t, "skill-described.json")
	skillOp := findWriteFile(t, ops, ".agents/skills/agent-sync-coder/SKILL.md")
	content := string(skillOp.Content)
	if !strings.Contains(content, `description: "Reviews and writes production code: tests first"`) {
		t.Errorf("authored description missing from frontmatter; got %q", content)
	}
	if strings.Contains(content, "no description authored") {
		t.Errorf("fallback description leaked despite authored value; got %q", content)
	}
	for _, op := range ops {
		if op.OpKind() == adapterkit.OpKindWarning {
			t.Errorf("described skill must not emit the missing-description warning; got %+v", op)
		}
	}
}

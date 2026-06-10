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
	state := &emitState{emittedFilePath: map[string]struct{}{}}
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
		{Op: adapterkit.OpKindMkdir, Path: ".agents/skills/aienvs-coder"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/aienvs-coder/SKILL.md"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}
	skillOp := findWriteFile(t, ops, ".agents/skills/aienvs-coder/SKILL.md")
	if !strings.HasPrefix(string(skillOp.Content), "<!-- Managed by agent-sync") {
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
		{Op: adapterkit.OpKindMkdir, Path: ".agents/skills/aienvs-coder"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/aienvs-coder/SKILL.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/aienvs-coder/examples/usage.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agents/skills/aienvs-coder/templates/foo.txt"},
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
	if op.Locator != "aienvs:team" {
		t.Errorf("AGENTS.md locator=%q want aienvs:team", op.Locator)
	}
	content := string(op.Content)
	// The engine owns the begin/end markers (rendered from the locator during
	// the merge); the adapter sends the INNER body only.
	if strings.Contains(content, "<!-- aienvs:") {
		t.Errorf("AGENTS.md content must be inner body without markers; got %q", content)
	}
	if !strings.Contains(content, "Use conventional commits.") {
		t.Errorf("AGENTS.md content missing body; got %q", content)
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
	if op.Locator != "mcp_servers.aienvs_lsp" {
		t.Errorf("mcp locator=%q want mcp_servers.aienvs_lsp", op.Locator)
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
	if err := toml.Unmarshal([]byte("[mcp_servers.aienvs_lsp]\n"+body), &m); err != nil {
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
		".agents/skills/aienvs-coder":          false,
		".agents/skills/aienvs-coder/SKILL.md": false,
		".codex/config.toml":                   false,
		"AGENTS.md":                            false,
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

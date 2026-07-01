package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

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
	state := &emitState{paths: resolvePathSet("project")}
	sort.Slice(doc.Nodes, func(i, j int) bool {
		if doc.Nodes[i].Kind != doc.Nodes[j].Kind {
			return doc.Nodes[i].Kind < doc.Nodes[j].Kind
		}
		return doc.Nodes[i].ID < doc.Nodes[j].ID
	})
	for _, n := range doc.Nodes {
		if !nodeTargetsPi(n) {
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
	// The engine owns the begin/end markers; the adapter sends inner body only.
	if strings.Contains(content, "<!-- agent-sync:") {
		t.Errorf("AGENTS.md content must be inner body without markers; got %q", content)
	}
	if !strings.Contains(content, "Use conventional commits.") {
		t.Errorf("AGENTS.md content missing body; got %q", content)
	}
}

// TestEmit_UserScope_AgentsMDRemapped pins the user-scope (sync --user)
// behavior: agents-md is emitted to .pi/agent/AGENTS.md (→ ~/.pi/agent/AGENTS.md,
// Pi's user-global instructions path — NOT ~/AGENTS.md).
func TestEmit_UserScope_AgentsMDRemapped(t *testing.T) {
	t.Parallel()

	raw := `{"nodes":[{"id":"team","kind":"agents-md","body":"Use conventional commits."}]}`
	res, err := emitDocScope(t, raw, "user")
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}

	paths := map[string]bool{}
	for _, r := range res.OpsPerformed {
		paths[r.Path] = true
	}
	if !paths[".pi/agent/AGENTS.md"] {
		t.Errorf("user-scope agents-md must emit to .pi/agent/AGENTS.md; got %+v", res.OpsPerformed)
	}
	if paths["AGENTS.md"] {
		t.Errorf("user-scope must NOT emit project-root AGENTS.md; got %+v", res.OpsPerformed)
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
	for _, conceptID := range []string{"no-fri", "draftpr", "lsp", "some-plugin", "helper"} {
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

// TestEmit_NoMCPWarningCitesRationale pins the canonical capability-honesty
// example: a pi-targeted MCP node surfaces a degradation warning citing Pi's
// no-MCP rationale, and emits no file.
func TestEmit_NoMCPWarningCitesRationale(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"nodes":[{"id":"lsp","kind":"mcp-server-entry","body":"{\"command\":\"node\"}"}]}`)
	emitted, err := captureOps(raw)
	if err != nil {
		t.Fatalf("captureOps: %v", err)
	}
	w, ok := findWarning(emitted.ops, "lsp")
	if !ok {
		t.Fatal("expected no-MCP degradation warning for mcp-server-entry")
	}
	if !strings.Contains(w.Note, "does not load MCP servers by design") {
		t.Errorf("no-MCP warning should cite the deliberate-exclusion rationale; got %q", w.Note)
	}
	for _, op := range emitted.ops {
		if _, isWarn := op.(adapterkit.OpWarning); !isWarn {
			t.Errorf("MCP node must emit only a warning, no file op; got %T", op)
		}
	}
}

// TestEmit_SkillWarnsAndEmitsNoFile pins that skill is unsupported in PR1: it
// degrades to a warning (planned for PR2 with the ADV-1 fix) and writes no file
// into the shared .agents/skills tree.
func TestEmit_SkillWarnsAndEmitsNoFile(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"nodes":[{"id":"helper","kind":"skill","body":"# Helper"}]}`)
	emitted, err := captureOps(raw)
	if err != nil {
		t.Fatalf("captureOps: %v", err)
	}
	w, ok := findWarning(emitted.ops, "helper")
	if !ok {
		t.Fatal("expected degradation warning for unsupported skill")
	}
	if !strings.Contains(w.Note, "does not emit skills yet") {
		t.Errorf("skill warning should note it is planned; got %q", w.Note)
	}
	for _, op := range emitted.ops {
		if _, isWarn := op.(adapterkit.OpWarning); !isWarn {
			t.Errorf("skill node must emit only a warning, no file op; got %T", op)
		}
	}
}

func TestEmitMixed_EmitsAgentsMDWarnsUnsupported(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "mixed-everything.json")
	if _, ok := findToolOwned(ops, "AGENTS.md"); !ok {
		t.Error("mixed emit must produce the AGENTS.md tool-owned op")
	}
	// The skill and mcp-server-entry nodes degrade to warnings, not files.
	for _, conceptID := range []string{"coder", "lsp"} {
		if _, ok := findWarning(ops, conceptID); !ok {
			t.Errorf("mixed emit must warn for unsupported node %q", conceptID)
		}
	}
	for _, r := range res.OpsPerformed {
		if r.Op == adapterkit.OpKindWriteFile || r.Op == adapterkit.OpKindMkdir {
			t.Errorf("pi PR1 must not emit files/mkdirs (only AGENTS.md tool-owned + warnings); got %q %q", r.Op, r.Path)
		}
	}
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

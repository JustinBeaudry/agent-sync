package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// emitFixture loads an IR document from testdata/ir and runs the
// production handleEmit (including its sort + dedup invariants) so
// the test path matches the wire path. Returns both the OpRecord
// summary and the raw Op list so tests can assert on op-specific
// fields (warning concept_id, write_tool_owned locator, content
// bytes) the OpRecord summary does not carry.
func emitFixture(t *testing.T, name string) (adapterkit.EmitResult, []adapterkit.Op) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ir", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	// Drive the same code path the wire-side runtime drives so the
	// fixture-vs-wire assertion is meaningful. handleEmit owns the
	// sort + dedup behavior; emitting via dispatchNode directly would
	// silently bypass both and mask future regressions.
	emitted, err := captureOps(raw)
	if err != nil {
		t.Fatalf("captureOps %s: %v", name, err)
	}
	return adapterkit.EmitResult{OpsPerformed: emitted.records()}, emitted.ops
}

// captureOps replicates handleEmit's flow but returns the raw
// emittedOps so tests can inspect op-specific fields. Keeps test
// logic in one place rather than duplicating the sort + dedup
// across every fixture-driven test.
func captureOps(raw json.RawMessage) (*emittedOps, error) {
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
		sidecarEmitted:  false,
		emittedFilePath: map[string]struct{}{},
		paths:           resolvePathSet("project"),
	}
	sort.Slice(doc.Nodes, func(i, j int) bool {
		if doc.Nodes[i].Kind != doc.Nodes[j].Kind {
			return doc.Nodes[i].Kind < doc.Nodes[j].Kind
		}
		return doc.Nodes[i].ID < doc.Nodes[j].ID
	})
	for _, n := range doc.Nodes {
		if !nodeTargetsClaude(n) {
			continue
		}
		if err := dispatchNode(emitted, n, state); err != nil {
			return nil, err
		}
	}
	return emitted, nil
}

func TestEmitRule_HappyPath(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "rule-only.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".claude/rules/agent-sync"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/agent-sync/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/agent-sync/no-fri.md"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	ruleOp := findWriteFile(t, ops, ".claude/rules/agent-sync/no-fri.md")
	if !strings.HasPrefix(string(ruleOp.Content), "<!-- Managed by agent-sync") {
		t.Errorf("rule content missing managed header; got %q", ruleOp.Content)
	}
	if !strings.Contains(string(ruleOp.Content), "do not edit") {
		t.Errorf("rule content missing 'do not edit' clause; got %q", ruleOp.Content)
	}
	if !strings.Contains(string(ruleOp.Content), "Regenerate: agent-sync sync") {
		t.Errorf("rule content missing regenerate instruction; got %q", ruleOp.Content)
	}
	if !strings.Contains(string(ruleOp.Content), "No PRs on Friday.") {
		t.Errorf("rule content missing body; got %q", ruleOp.Content)
	}

	readmeOp := findWriteFile(t, ops, ".claude/rules/agent-sync/README.md")
	if !strings.Contains(string(readmeOp.Content), "agent-sync unmanage claude") {
		t.Errorf("README content missing exit path; got %q", readmeOp.Content)
	}
}

func TestEmitRule_PathsFrontmatterTriggersWarning(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "rule-with-paths-frontmatter.json")

	wantKinds := []adapterkit.OpKind{
		adapterkit.OpKindMkdir,
		adapterkit.OpKindWriteFile,
		adapterkit.OpKindWriteFile,
		adapterkit.OpKindWarning,
	}
	if got := opKinds(res.OpsPerformed); !reflect.DeepEqual(got, wantKinds) {
		t.Fatalf("op kinds mismatch:\n got: %v\nwant: %v", got, wantKinds)
	}

	warning, ok := findWarning(ops, "scoped-rule")
	if !ok {
		t.Fatal("missing warning for scoped-rule")
	}
	if warning.Status != adapterkit.WarningStatusDegraded {
		t.Errorf("warning status=%q want %q", warning.Status, adapterkit.WarningStatusDegraded)
	}
	if !strings.Contains(warning.Note, "paths:") {
		t.Errorf("warning note must reference paths:; got %q", warning.Note)
	}
}

func TestEmitCommand_HappyPath(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "command-only.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".claude/commands/agent-sync"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/commands/agent-sync/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/commands/agent-sync/deploy.md"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	cmdOp := findWriteFile(t, ops, ".claude/commands/agent-sync/deploy.md")
	if !strings.HasPrefix(string(cmdOp.Content), "<!-- Managed by agent-sync") {
		t.Errorf("command content missing managed header; got %q", cmdOp.Content)
	}
	if !strings.Contains(string(cmdOp.Content), "Run the deploy script") {
		t.Errorf("command content missing body; got %q", cmdOp.Content)
	}
}

func TestEmitSkill_WithAssets(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "skill-with-assets.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".claude/skills/agent-sync-coder"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/skills/agent-sync-coder/SKILL.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/skills/agent-sync-coder/examples/usage.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/skills/agent-sync-coder/templates/foo.txt"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	skillOp := findWriteFile(t, ops, ".claude/skills/agent-sync-coder/SKILL.md")
	if !strings.HasPrefix(string(skillOp.Content), "---\nname: agent-sync-coder\n") {
		t.Errorf("SKILL.md must start with frontmatter at byte 0; got %q", skillOp.Content)
	}
	if !strings.Contains(string(skillOp.Content), "description: ") {
		t.Errorf("SKILL.md frontmatter missing description key; got %q", skillOp.Content)
	}
	if !strings.Contains(string(skillOp.Content), "<!-- Managed by agent-sync") {
		t.Errorf("skill SKILL.md missing managed header; got %q", skillOp.Content)
	}

	tmplOp := findWriteFile(t, ops, ".claude/skills/agent-sync-coder/templates/foo.txt")
	if string(tmplOp.Content) != "template body" {
		t.Errorf("template asset content=%q want %q", tmplOp.Content, "template body")
	}
}

func TestEmitSkill_NoREADMEAtSkillsParent(t *testing.T) {
	t.Parallel()

	res, _ := emitFixture(t, "skill-with-assets.json")

	for _, op := range res.OpsPerformed {
		if op.Path == ".claude/skills/README.md" {
			t.Errorf("skill emission must not create %q", op.Path)
		}
		if op.Path == ".claude/skills/agent-sync-coder/README.md" {
			t.Errorf("per-skill README would clash with skill-discovery semantics; got %q", op.Path)
		}
	}
}

func TestEmitSkill_ZeroAssetsEmitsOnlySKILLMd(t *testing.T) {
	t.Parallel()

	emitted, err := captureOps(json.RawMessage(`{"nodes":[{"id":"empty","kind":"skill","body":"# Empty skill"}]}`))
	if err != nil {
		t.Fatalf("captureOps: %v", err)
	}
	got := emitted.records()
	want := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".claude/skills/agent-sync-empty"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/skills/agent-sync-empty/SKILL.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("zero-asset skill ops mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestEmitSkill_RejectsAssetRelPathTraversal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		relPath string
	}{
		{"sibling skill", "../agent-sync-victim/SKILL.md"},
		{"workspace escape", "../../../etc/passwd"},
		{"absolute path", "/etc/passwd"},
		{"backslash", "subdir\\foo.txt"},
		{"dot-dot only", ".."},
		{"trailing dot-dot", "subdir/.."},
		{"embedded dot-dot", "subdir/../escape.txt"},
		{"dot-slash prefix", "./foo.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ir := []byte(`{"nodes":[{"id":"victim","kind":"skill","body":"x","assets":[{"rel_path":` + jsonQuote(tc.relPath) + `,"content":"x"}]}]}`)
			_, err := captureOps(ir)
			if err == nil {
				t.Fatalf("RelPath %q must be rejected; was accepted", tc.relPath)
			}
			var aerr *adapterkit.Error
			if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
				t.Fatalf("RelPath %q got err=%v want CodeInvalidParams", tc.relPath, err)
			}
		})
	}
}

func TestEmitSkill_RejectsEmptyAssetRelPath(t *testing.T) {
	t.Parallel()

	emitted := &emittedOps{}
	err := emitSkill(emitted, irNode{
		ID:   "x",
		Kind: "skill",
		Assets: []irAsset{
			{RelPath: "", Content: json.RawMessage(`"x"`)},
		},
	}, newEmitState())
	if err == nil {
		t.Fatal("empty rel_path must be rejected")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
		t.Fatalf("empty rel_path got err=%v want CodeInvalidParams", err)
	}
}

func TestEmitMCPServerEntry_HappyPath(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "mcp-server-entry-only.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindWriteToolOwned, Path: ".mcp.json"},
		{Op: adapterkit.OpKindWriteFile, Path: ".agent-sync-managed"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	mcp, ok := findToolOwned(ops, ".mcp.json")
	if !ok {
		t.Fatal("missing write_tool_owned for .mcp.json")
	}
	if mcp.Kind != adapterkit.ToolOwnedKindJSONPointer {
		t.Errorf("locator kind=%q want %q", mcp.Kind, adapterkit.ToolOwnedKindJSONPointer)
	}
	if mcp.Locator != "/mcpServers/agentsync_lsp" {
		t.Errorf("locator=%q want %q", mcp.Locator, "/mcpServers/agentsync_lsp")
	}
	if !json.Valid(mcp.Content) {
		t.Errorf(".mcp.json entry content is not valid JSON: %q", mcp.Content)
	}
}

func TestEmitSkill_RejectsDotRelPath(t *testing.T) {
	t.Parallel()

	_, err := captureOps(json.RawMessage(`{"nodes":[{"id":"x","kind":"skill","body":"x","assets":[{"rel_path":".","content":"x"}]}]}`))
	if err == nil {
		t.Fatal("rel_path \".\" must be rejected")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
		t.Fatalf("got err=%v want CodeInvalidParams", err)
	}
}

func TestEmitSkill_RejectsAssetCollidingWithSKILLMd(t *testing.T) {
	t.Parallel()

	_, err := captureOps(json.RawMessage(`{"nodes":[{"id":"x","kind":"skill","body":"x","assets":[{"rel_path":"SKILL.md","content":"x"}]}]}`))
	if err == nil {
		t.Fatal("rel_path SKILL.md must be rejected; collides with skill entrypoint")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
		t.Fatalf("got err=%v want CodeInvalidParams", err)
	}
}

func TestEmitSkill_RejectsDuplicateAssetRelPaths(t *testing.T) {
	t.Parallel()

	_, err := captureOps(json.RawMessage(`{"nodes":[{"id":"x","kind":"skill","body":"x","assets":[
		{"rel_path":"foo.txt","content":"a"},
		{"rel_path":"foo.txt","content":"b"}
	]}]}`))
	if err == nil {
		t.Fatal("duplicate asset rel_path must be rejected")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
		t.Fatalf("got err=%v want CodeInvalidParams", err)
	}
}

func TestEmitMCPServerEntry_RejectsNonObjectBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"plain string", `"not an object"`},
		{"number scalar", `42`},
		{"boolean", `true`},
		{"json array", `[1,2,3]`},
		{"json null", `null`},
		{"open-brace string fragment", `"{"`},
		{"unterminated object string", `"{\"command\": "`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ir := []byte(`{"nodes":[{"id":"bad","kind":"mcp-server-entry","body":` + tc.body + `}]}`)
			_, err := captureOps(ir)
			if err == nil {
				t.Fatalf("body %q must be rejected; was accepted", tc.body)
			}
			var aerr *adapterkit.Error
			if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
				t.Fatalf("body %q got err=%v want CodeInvalidParams", tc.body, err)
			}
		})
	}
}

func TestEmitMCPServerEntry_DedupsSidecarAcrossMultipleNodes(t *testing.T) {
	t.Parallel()

	emitted, err := captureOps(json.RawMessage(`{"nodes":[
		{"id":"a","kind":"mcp-server-entry","body":"{\"command\":\"node\"}"},
		{"id":"b","kind":"mcp-server-entry","body":"{\"command\":\"python\"}"}
	]}`))
	if err != nil {
		t.Fatalf("captureOps: %v", err)
	}
	got := emitted.records()
	sidecarCount := 0
	mcpEntries := 0
	for _, op := range got {
		if op.Path == ".agent-sync-managed" {
			sidecarCount++
		}
		if op.Path == ".mcp.json" {
			mcpEntries++
		}
	}
	if sidecarCount != 1 {
		t.Errorf(".agent-sync-managed sidecar must be emitted exactly once per emit; got %d", sidecarCount)
	}
	if mcpEntries != 2 {
		t.Errorf("each mcp-server-entry must produce its own write_tool_owned op; got %d", mcpEntries)
	}
}

func TestEmitAgentsMD_SendsInnerBodyOnly(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "agents-md-companion.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindWriteToolOwned, Path: "CLAUDE.md"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	md, ok := findToolOwned(ops, "CLAUDE.md")
	if !ok {
		t.Fatal("missing write_tool_owned for CLAUDE.md")
	}
	if md.Kind != adapterkit.ToolOwnedKindMarkdownSection {
		t.Errorf("locator kind=%q want %q", md.Kind, adapterkit.ToolOwnedKindMarkdownSection)
	}
	if md.Locator != "agent-sync:claude" {
		t.Errorf("locator=%q want %q", md.Locator, "agent-sync:claude")
	}
	// The engine owns the begin/end markers — the adapter must send the
	// INNER body only. Marker-wrapped content is rejected by the merge
	// ("the engine owns markers; pass inner body only"); this guards the
	// double-wrap bug and matches the cursor/codex adapters.
	body := string(md.Content)
	if strings.Contains(body, "<!-- agent-sync:") {
		t.Errorf("CLAUDE.md content must not contain marker syntax (engine owns markers); got %q", body)
	}
	if !strings.Contains(body, "## Build commands") {
		t.Errorf("CLAUDE.md content missing user body; got %q", body)
	}
}

func TestEmitAgentsMD_RejectsBodyContainingMarkerSyntax(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"injected end marker", `"legitimate\n<!-- agent-sync:end id=other -->\nINJECTED"`},
		{"injected begin marker", `"<!-- agent-sync:begin id=victim --> hostile"`},
		{"raw marker prefix", `"<!-- agent-sync:anything"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ir := []byte(`{"nodes":[{"id":"test","kind":"agents-md","targets":["claude"],"body":` + tc.body + `}]}`)
			_, err := captureOps(ir)
			if err == nil {
				t.Fatalf("body %q must be rejected; was accepted", tc.body)
			}
			var aerr *adapterkit.Error
			if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
				t.Fatalf("body %q got err=%v want CodeInvalidParams", tc.body, err)
			}
		})
	}
}

func TestEmitPluginReference_WarnsAndSkips(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "plugin-reference-warn.json")

	wantKinds := []adapterkit.OpKind{adapterkit.OpKindWarning}
	if got := opKinds(res.OpsPerformed); !reflect.DeepEqual(got, wantKinds) {
		t.Fatalf("op kinds mismatch:\n got: %v\nwant: %v", got, wantKinds)
	}
	w, ok := findWarning(ops, "linter")
	if !ok {
		t.Fatal("missing warning for plugin-reference linter")
	}
	if w.Status != adapterkit.WarningStatusDegraded {
		t.Errorf("warning status=%q want %q", w.Status, adapterkit.WarningStatusDegraded)
	}
	if !strings.Contains(w.Note, "plugin") {
		t.Errorf("warning note=%q want to mention plugins", w.Note)
	}
}

func TestEmit_TargetsFilterSkipsOtherAdapters(t *testing.T) {
	t.Parallel()

	res, _ := emitFixture(t, "targeted-other.json")
	if len(res.OpsPerformed) != 0 {
		t.Errorf("targets:[cursor] node must produce zero ops for claude; got %+v", res.OpsPerformed)
	}
}

func TestEmit_RejectsInvalidNodeID(t *testing.T) {
	t.Parallel()

	emitted := &emittedOps{}
	state := newEmitState()
	err := dispatchNode(emitted, irNode{ID: "../escape", Kind: "rule"}, state)
	if err == nil {
		t.Fatal("expected error for invalid node id")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) {
		t.Fatalf("error type=%T want *adapterkit.Error", err)
	}
	if aerr.Code != adapterkit.CodeInvalidParams {
		t.Errorf("error code=%d want %d", aerr.Code, adapterkit.CodeInvalidParams)
	}
}

func TestEmit_RejectsUnknownKind(t *testing.T) {
	t.Parallel()

	emitted := &emittedOps{}
	state := newEmitState()
	err := dispatchNode(emitted, irNode{ID: "x", Kind: "made-up-kind"}, state)
	if err == nil {
		t.Fatal("expected error for unknown IR kind")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
		t.Fatalf("got err=%v want CodeInvalidParams", err)
	}
	if !strings.Contains(aerr.Message, "unknown IR kind") {
		t.Errorf("error message must name unknown IR kind; got %q", aerr.Message)
	}
}

func TestEmit_RejectsDuplicateNodes(t *testing.T) {
	t.Parallel()

	_, err := captureOps(json.RawMessage(`{"nodes":[
		{"id":"x","kind":"rule","body":"first"},
		{"id":"x","kind":"rule","body":"second"}
	]}`))
	if err == nil {
		t.Fatal("duplicate (kind, id) must be rejected")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
		t.Fatalf("got err=%v want CodeInvalidParams", err)
	}
}

func TestEmit_DeduplicatesREADMEPerSubdir(t *testing.T) {
	t.Parallel()

	emitted, err := captureOps(json.RawMessage(`{"nodes":[
		{"id":"first","kind":"rule","body":"first"},
		{"id":"second","kind":"rule","body":"second"}
	]}`))
	if err != nil {
		t.Fatalf("captureOps: %v", err)
	}
	got := emitted.records()
	want := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".claude/rules/agent-sync"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/agent-sync/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/agent-sync/first.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/agent-sync/second.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestEmit_HandleEmitFullRoundTrip(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("testdata", "ir", "mixed-everything.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res, err := handleEmit(context.Background(), adapterkit.EmitParams{
		Target: adapterName,
		IR:     raw,
	}, "project", "", "")
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}
	if len(res.OpsPerformed) == 0 {
		t.Fatal("mixed-everything fixture must produce ops")
	}

	expectPaths := []string{
		".claude/rules/agent-sync/house-style.md",
		".claude/commands/agent-sync/deploy.md",
		".claude/skills/agent-sync-coder/SKILL.md",
		".mcp.json",
		"CLAUDE.md",
		".agent-sync-managed",
	}
	for _, want := range expectPaths {
		if !containsPath(res.OpsPerformed, want) {
			t.Errorf("expected op for path %q; got %+v", want, res.OpsPerformed)
		}
	}
	// Plugin-reference must surface as a warning, not a write.
	if !containsKind(res.OpsPerformed, adapterkit.OpKindWarning) {
		t.Errorf("mixed-everything must include warning op for plugin-reference; got %+v", res.OpsPerformed)
	}
}

func TestEmit_HandleEmit_RejectsMalformedIRJSON(t *testing.T) {
	t.Parallel()

	_, err := handleEmit(context.Background(), adapterkit.EmitParams{
		Target: adapterName,
		IR:     json.RawMessage(`{not json}`),
	}, "project", "", "")
	if err == nil {
		t.Fatal("malformed IR JSON must be rejected")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
		t.Fatalf("got err=%v want CodeInvalidParams", err)
	}
}

func TestEmit_HandleEmit_HonorsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := handleEmit(ctx, adapterkit.EmitParams{
		Target: adapterName,
		IR:     json.RawMessage(`{"nodes":[{"id":"x","kind":"rule","body":"x"}]}`),
	}, "project", "", "")
	if err == nil {
		t.Fatal("cancelled context must abort emit")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) {
		t.Fatalf("err=%T want *adapterkit.Error", err)
	}
	if !strings.Contains(aerr.Message, "cancelled") {
		t.Errorf("error message must mention cancellation; got %q", aerr.Message)
	}
}

func TestHasPathsFrontmatter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"no frontmatter", "regular body", false},
		{"opens with paths LF", "---\npaths:\n  - foo\n---\nbody", true},
		{"opens with paths CRLF", "---\r\npaths:\r\n  - foo\r\n---\r\nbody", true},
		{"opens with description", "---\ndescription: foo\npaths:\n  - bar\n---\n", false},
		{"empty body", "", false},
		{"empty frontmatter line then paths", "---\n\npaths: foo\n---\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := hasPathsFrontmatter([]byte(tc.in))
			if got != tc.want {
				t.Errorf("hasPathsFrontmatter(%q) = %v want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestHandleEmit_UserScopePaths is the U3 characterization test: at user
// scope the agents-md overlay emits at .claude/CLAUDE.md and the
// mcp-server-entry at .claude.json, the .agent-sync-managed sidecar is
// suppressed (OQ-1), and every emitted path is inside the user-scope declared
// outputs (the path-safety gate-coupling invariant).
func TestHandleEmit_UserScopePaths(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"nodes":[
		{"id":"team","kind":"agents-md","body":"# team standards"},
		{"id":"actual-plane","kind":"mcp-server-entry","body":{"command":"uvx","args":["plane"]}}
	]}`)

	res, err := handleEmit(context.Background(), adapterkit.EmitParams{Target: adapterName, IR: raw}, "user", "", "")
	if err != nil {
		t.Fatalf("handleEmit(user): %v", err)
	}

	var gotClaudeMD, gotMCP bool
	for _, op := range res.OpsPerformed {
		switch op.Path {
		case ".claude/CLAUDE.md":
			gotClaudeMD = true
		case ".claude.json":
			gotMCP = true
		case ".agent-sync-managed", "CLAUDE.md", ".mcp.json":
			t.Errorf("user scope emitted project/sidecar path %q (op=%s)", op.Path, op.Op)
		}
	}
	if !gotClaudeMD {
		t.Errorf("agents-md must emit at .claude/CLAUDE.md at user scope; got %+v", res.OpsPerformed)
	}
	if !gotMCP {
		t.Errorf("mcp-server-entry must emit at .claude.json at user scope; got %+v", res.OpsPerformed)
	}

	// Gate-coupling invariant: emitted paths must be inside the user-scope
	// declared outputs, else the runtime path-safety gate rejects the emit.
	declared := declaredOutputs("user")
	for _, op := range res.OpsPerformed {
		if op.Op == adapterkit.OpKindWarning {
			continue
		}
		if !pathInDeclaredOutputs(op.Path, declared) {
			t.Errorf("user-scope emitted path %q (kind=%s) not inside any declared output", op.Path, op.Op)
		}
	}
}

// --- test helpers ----------------------------------------------------

func newEmitState() *emitState {
	return &emitState{
		readmeEmitted:   map[string]bool{},
		emittedFilePath: map[string]struct{}{},
		paths:           resolvePathSet("project"),
	}
}

func opKinds(records []adapterkit.OpRecord) []adapterkit.OpKind {
	out := make([]adapterkit.OpKind, 0, len(records))
	for _, r := range records {
		out = append(out, r.Op)
	}
	return out
}

func findWriteFile(t *testing.T, ops []adapterkit.Op, path string) adapterkit.OpWriteFile {
	t.Helper()
	for _, op := range ops {
		if wf, ok := op.(adapterkit.OpWriteFile); ok && wf.Path == path {
			return wf
		}
	}
	t.Fatalf("no write_file op for path %q", path)
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

func containsPath(records []adapterkit.OpRecord, path string) bool {
	for _, r := range records {
		if r.Path == path {
			return true
		}
	}
	return false
}

func containsKind(records []adapterkit.OpRecord, kind adapterkit.OpKind) bool {
	for _, r := range records {
		if r.Op == kind {
			return true
		}
	}
	return false
}

// jsonQuote returns the JSON-string form of s. Used in inline IR
// fixtures so test cases can pass arbitrary strings without manual
// escape juggling.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestEmitSkill_AuthoredDescription: an authored description lands in the
// emitted frontmatter verbatim (quoted scalar), replacing the fallback.
func TestEmitSkill_AuthoredDescription(t *testing.T) {
	t.Parallel()

	_, ops := emitFixture(t, "skill-described.json")
	skillOp := findWriteFile(t, ops, ".claude/skills/agent-sync-coder/SKILL.md")
	content := string(skillOp.Content)
	if !strings.Contains(content, `description: "Reviews and writes production code: tests first"`) {
		t.Errorf("authored description missing from frontmatter; got %q", content)
	}
	if strings.Contains(content, "no description authored") {
		t.Errorf("fallback description leaked despite authored value; got %q", content)
	}
}

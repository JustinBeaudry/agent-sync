package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aienvs/aienvs/pkg/adapterkit"
)

// emitFixture loads an IR document from testdata/ir and runs
// handleEmit against it. Returns both the OpRecord summary and the
// raw Op list so tests can assert on op-specific fields (warning
// concept_id, write_tool_owned locator, content bytes) the OpRecord
// summary does not carry.
func emitFixture(t *testing.T, name string) (adapterkit.EmitResult, []adapterkit.Op) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ir", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	doc, err := decodeIRDocument(raw)
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	emitted := &emittedOps{}
	readmeEmitted := map[string]bool{}
	// Re-sort so the assertion is deterministic regardless of how
	// the fixture authored the order.
	for _, n := range doc.Nodes {
		if !nodeTargetsClaude(n) {
			continue
		}
		if err := dispatchNode(emitted, n, readmeEmitted); err != nil {
			t.Fatalf("dispatchNode %s: %v", n.ID, err)
		}
	}
	res := adapterkit.EmitResult{OpsPerformed: emitted.records()}
	return res, emitted.ops
}

func TestEmitRule_HappyPath(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "rule-only.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".claude/rules/aienvs"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/aienvs/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/aienvs/no-fri.md"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	// Verify the rule body has the managed-file header prepended.
	ruleOp := findWriteFile(t, ops, ".claude/rules/aienvs/no-fri.md")
	if !strings.HasPrefix(string(ruleOp.Content), "<!-- Managed by aienvs") {
		t.Errorf("rule content missing managed header; got %q", ruleOp.Content)
	}
	if !strings.Contains(string(ruleOp.Content), "No PRs on Friday.") {
		t.Errorf("rule content missing body; got %q", ruleOp.Content)
	}

	// Verify README content is the per-subdir README.
	readmeOp := findWriteFile(t, ops, ".claude/rules/aienvs/README.md")
	if !strings.Contains(string(readmeOp.Content), "aienvs unmanage claude") {
		t.Errorf("README content missing exit path; got %q", readmeOp.Content)
	}
}

func TestEmitRule_PathsFrontmatterTriggersWarning(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "rule-with-paths-frontmatter.json")

	// Expected ops: mkdir + README + rule write_file + warning.
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
		{Op: adapterkit.OpKindMkdir, Path: ".claude/commands/aienvs"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/commands/aienvs/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/commands/aienvs/deploy.md"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	cmdOp := findWriteFile(t, ops, ".claude/commands/aienvs/deploy.md")
	if !strings.HasPrefix(string(cmdOp.Content), "<!-- Managed by aienvs") {
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
		{Op: adapterkit.OpKindMkdir, Path: ".claude/skills/aienvs-coder"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/skills/aienvs-coder/SKILL.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/skills/aienvs-coder/examples/usage.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/skills/aienvs-coder/templates/foo.txt"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	skillOp := findWriteFile(t, ops, ".claude/skills/aienvs-coder/SKILL.md")
	if !strings.HasPrefix(string(skillOp.Content), "<!-- Managed by aienvs") {
		t.Errorf("skill SKILL.md missing managed header; got %q", skillOp.Content)
	}

	// Asset writes carry asset content, not a managed header.
	tmplOp := findWriteFile(t, ops, ".claude/skills/aienvs-coder/templates/foo.txt")
	if string(tmplOp.Content) != "template body" {
		t.Errorf("template asset content=%q want %q", tmplOp.Content, "template body")
	}
}

func TestEmitSkill_NoREADMEAtSkillsParent(t *testing.T) {
	t.Parallel()

	res, _ := emitFixture(t, "skill-with-assets.json")

	// Skill emission must NOT write a README at .claude/skills/ —
	// that directory can hold user skills, and we don't own it.
	for _, op := range res.OpsPerformed {
		if op.Path == ".claude/skills/README.md" {
			t.Errorf("skill emission must not create %q", op.Path)
		}
		if op.Path == ".claude/skills/aienvs-coder/README.md" {
			t.Errorf("per-skill README would clash with skill-discovery semantics; got %q", op.Path)
		}
	}
}

func TestEmitMCPServerEntry_HappyPath(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "mcp-server-entry-only.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindWriteToolOwned, Path: ".mcp.json"},
		{Op: adapterkit.OpKindWriteFile, Path: ".aienvs-managed"},
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
	if mcp.Locator != "/mcpServers/aienvs_lsp" {
		t.Errorf("locator=%q want %q", mcp.Locator, "/mcpServers/aienvs_lsp")
	}
	// Body is JSON; verify it round-trips.
	if !json.Valid(mcp.Content) {
		t.Errorf(".mcp.json entry content is not valid JSON: %q", mcp.Content)
	}
}

func TestEmitMCPServerEntry_RejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	emitted := &emittedOps{}
	err := emitMCPServerEntry(emitted, irNode{
		ID:   "broken",
		Kind: "mcp-server-entry",
		Body: json.RawMessage(`"this is not an mcp object — but is a json string"`),
	})
	// nodeBodyBytes will string-decode this and produce
	// `this is not an mcp object — but is a json string` which is
	// not valid JSON, so the validator should reject.
	if err == nil {
		t.Fatal("expected error for non-JSON mcp-server-entry body")
	}
	var aerr *adapterkit.Error
	if !errorsAs(err, &aerr) {
		t.Fatalf("error type=%T want *adapterkit.Error", err)
	}
	if aerr.Code != adapterkit.CodeInvalidParams {
		t.Errorf("error code=%d want %d", aerr.Code, adapterkit.CodeInvalidParams)
	}
}

func TestEmitAgentsMD_WrapsBodyInSection(t *testing.T) {
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
	if md.Locator != "aienvs:claude" {
		t.Errorf("locator=%q want %q", md.Locator, "aienvs:claude")
	}
	body := string(md.Content)
	if !strings.Contains(body, "<!-- aienvs:begin id=claude -->") {
		t.Errorf("CLAUDE.md content missing begin marker; got %q", body)
	}
	if !strings.Contains(body, "<!-- aienvs:end id=claude -->") {
		t.Errorf("CLAUDE.md content missing end marker; got %q", body)
	}
	if !strings.Contains(body, "## Build commands") {
		t.Errorf("CLAUDE.md content missing user body; got %q", body)
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
	err := dispatchNode(emitted, irNode{ID: "../escape", Kind: "rule"}, map[string]bool{})
	if err == nil {
		t.Fatal("expected error for invalid node id")
	}
	var aerr *adapterkit.Error
	if !errorsAs(err, &aerr) {
		t.Fatalf("error type=%T want *adapterkit.Error", err)
	}
	if aerr.Code != adapterkit.CodeInvalidParams {
		t.Errorf("error code=%d want %d", aerr.Code, adapterkit.CodeInvalidParams)
	}
}

func TestEmit_DeduplicatesREADMEPerSubdir(t *testing.T) {
	t.Parallel()

	// Two rule nodes share the .claude/rules/aienvs subdir; the
	// README + mkdir pair must be emitted exactly once.
	doc := irDocument{Nodes: []irNode{
		{ID: "first", Kind: "rule", Body: json.RawMessage(`"first"`)},
		{ID: "second", Kind: "rule", Body: json.RawMessage(`"second"`)},
	}}
	emitted := &emittedOps{}
	readmeEmitted := map[string]bool{}
	for _, n := range doc.Nodes {
		if err := dispatchNode(emitted, n, readmeEmitted); err != nil {
			t.Fatalf("dispatchNode: %v", err)
		}
	}
	got := emitted.records()
	want := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".claude/rules/aienvs"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/aienvs/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/aienvs/first.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".claude/rules/aienvs/second.md"},
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
	})
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}
	if len(res.OpsPerformed) == 0 {
		t.Fatal("mixed-everything fixture must produce ops")
	}

	// Spot-check that all expected paths are present at least once.
	expectPaths := []string{
		".claude/rules/aienvs/house-style.md",
		".claude/commands/aienvs/deploy.md",
		".claude/skills/aienvs-coder/SKILL.md",
		".mcp.json",
		"CLAUDE.md",
		".aienvs-managed",
	}
	for _, want := range expectPaths {
		if !containsPath(res.OpsPerformed, want) {
			t.Errorf("expected op for path %q; got %+v", want, res.OpsPerformed)
		}
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
		{"opens with paths", "---\npaths:\n  - foo\n---\nbody", true},
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

// --- test helpers ----------------------------------------------------

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

// errorsAs is a tiny shim around errors.As that returns bool. Lets
// the assertion read like the standard library check without
// importing errors at every test call site.
func errorsAs(err error, target **adapterkit.Error) bool {
	return errors.As(err, target)
}

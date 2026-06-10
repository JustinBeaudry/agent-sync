package cursor

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
// production handleEmit flow (sort + dedup invariants) so the test
// path matches the wire path. Returns both the OpRecord summary and
// the raw Op list so tests can assert on op-specific fields (warning
// concept_id, write_tool_owned locator, content bytes) the OpRecord
// summary does not carry.
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

// captureOps replicates handleEmit's flow but returns the raw
// emittedOps so tests can inspect op-specific fields.
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
	}
	sort.Slice(doc.Nodes, func(i, j int) bool {
		if doc.Nodes[i].Kind != doc.Nodes[j].Kind {
			return doc.Nodes[i].Kind < doc.Nodes[j].Kind
		}
		return doc.Nodes[i].ID < doc.Nodes[j].ID
	})
	for _, n := range doc.Nodes {
		if !nodeTargetsCursor(n) {
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
		{Op: adapterkit.OpKindMkdir, Path: ".cursor/rules/aienvs"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/rules/aienvs/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/rules/aienvs/no-fri.mdc"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	ruleOp := findWriteFile(t, ops, ".cursor/rules/aienvs/no-fri.mdc")
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

	readmeOp := findWriteFile(t, ops, ".cursor/rules/aienvs/README.md")
	if !strings.Contains(string(readmeOp.Content), "agent-sync unmanage cursor") {
		t.Errorf("README content missing exit path; got %q", readmeOp.Content)
	}
}

// TestEmitRule_PathsFrontmatterEmitsNoWarning pins the cursor-specific
// difference from claude: the `paths:` frontmatter ward is dropped
// because it guards a Claude Code activation bug with no Cursor .mdc
// equivalent. The rule is emitted with no degradation warning.
func TestEmitRule_PathsFrontmatterEmitsNoWarning(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "rule-with-paths-frontmatter.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindMkdir, Path: ".cursor/rules/aienvs"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/rules/aienvs/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/rules/aienvs/scoped-rule.mdc"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch (expected no warning op):\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}
	for _, r := range res.OpsPerformed {
		if r.Op == adapterkit.OpKindWarning {
			t.Errorf("cursor must NOT emit a paths: warning; got %+v", res.OpsPerformed)
		}
	}

	// Pin the cursor-specific invariant: the paths: frontmatter is
	// passed through to the .mdc body unmodified (not stripped), so a
	// regression that silently dropped frontmatter would fail here.
	ruleOp := findWriteFile(t, ops, ".cursor/rules/aienvs/scoped-rule.mdc")
	if !strings.Contains(string(ruleOp.Content), "paths:") {
		t.Errorf("paths: frontmatter must be preserved in the emitted .mdc; got %q", ruleOp.Content)
	}
}

func TestEmitMCPServerEntry_HappyPath(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "mcp-server-entry-only.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindWriteToolOwned, Path: ".cursor/mcp.json"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/.aienvs-managed"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	mcp, ok := findToolOwned(ops, ".cursor/mcp.json")
	if !ok {
		t.Fatal("missing write_tool_owned for .cursor/mcp.json")
	}
	if mcp.Kind != adapterkit.ToolOwnedKindJSONPointer {
		t.Errorf("locator kind=%q want %q", mcp.Kind, adapterkit.ToolOwnedKindJSONPointer)
	}
	if mcp.Locator != "/mcpServers/agentsync_lsp" {
		t.Errorf("locator=%q want %q", mcp.Locator, "/mcpServers/agentsync_lsp")
	}
	if !json.Valid(mcp.Content) {
		t.Errorf(".cursor/mcp.json entry content is not valid JSON: %q", mcp.Content)
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
		if op.Path == ".cursor/.aienvs-managed" {
			sidecarCount++
		}
		if op.Path == ".cursor/mcp.json" {
			mcpEntries++
		}
	}
	if sidecarCount != 1 {
		t.Errorf(".cursor/.aienvs-managed sidecar must be emitted exactly once per emit; got %d", sidecarCount)
	}
	if mcpEntries != 2 {
		t.Errorf("each mcp-server-entry must produce its own write_tool_owned op; got %d", mcpEntries)
	}
}

func TestEmitAgentsMD_SendsInnerBody(t *testing.T) {
	t.Parallel()

	res, ops := emitFixture(t, "agents-md-only.json")

	wantRecords := []adapterkit.OpRecord{
		{Op: adapterkit.OpKindWriteToolOwned, Path: "AGENTS.md"},
	}
	if !reflect.DeepEqual(res.OpsPerformed, wantRecords) {
		t.Fatalf("OpsPerformed mismatch:\n got: %+v\nwant: %+v", res.OpsPerformed, wantRecords)
	}

	md, ok := findToolOwned(ops, "AGENTS.md")
	if !ok {
		t.Fatal("missing write_tool_owned for AGENTS.md")
	}
	if md.Kind != adapterkit.ToolOwnedKindMarkdownSection {
		t.Errorf("locator kind=%q want %q", md.Kind, adapterkit.ToolOwnedKindMarkdownSection)
	}
	if md.Locator != "agent-sync:team" {
		t.Errorf("locator=%q want %q", md.Locator, "agent-sync:team")
	}
	body := string(md.Content)
	// The engine owns the begin/end markers (rendered from the locator during
	// the merge); the adapter sends the INNER body only. Marker-wrapped content
	// is rejected by the merge.
	if strings.Contains(body, "<!-- agent-sync:") {
		t.Errorf("AGENTS.md content must be inner body without markers; got %q", body)
	}
	if !strings.Contains(body, "## Conventions") {
		t.Errorf("AGENTS.md content missing user body; got %q", body)
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
			ir := []byte(`{"nodes":[{"id":"test","kind":"agents-md","targets":["cursor"],"body":` + tc.body + `}]}`)
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

func TestEmitUnsupported_WarnsAndSkips(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fixture   string
		conceptID string
		noteWord  string
	}{
		{"skill-unsupported.json", "coder", "skill"},
		{"command-unsupported.json", "deploy", "command"},
		{"plugin-reference-unsupported.json", "linter", "plugin"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			res, ops := emitFixture(t, tc.fixture)

			wantKinds := []adapterkit.OpKind{adapterkit.OpKindWarning}
			if got := opKinds(res.OpsPerformed); !reflect.DeepEqual(got, wantKinds) {
				t.Fatalf("op kinds mismatch:\n got: %v\nwant: %v", got, wantKinds)
			}
			w, ok := findWarning(ops, tc.conceptID)
			if !ok {
				t.Fatalf("missing warning for %q", tc.conceptID)
			}
			if w.Status != adapterkit.WarningStatusDegraded {
				t.Errorf("warning status=%q want %q", w.Status, adapterkit.WarningStatusDegraded)
			}
			if !strings.Contains(strings.ToLower(w.Note), tc.noteWord) {
				t.Errorf("warning note=%q want to mention %q", w.Note, tc.noteWord)
			}
			// No file writes for an unsupported kind.
			for _, op := range res.OpsPerformed {
				if op.Op == adapterkit.OpKindWriteFile || op.Op == adapterkit.OpKindWriteToolOwned {
					t.Errorf("unsupported kind must not emit writes; got %+v", res.OpsPerformed)
				}
			}
		})
	}
}

func TestEmit_TargetsFilterSkipsOtherAdapters(t *testing.T) {
	t.Parallel()

	res, _ := emitFixture(t, "targeted-other.json")
	if len(res.OpsPerformed) != 0 {
		t.Errorf("targets:[claude] node must produce zero ops for cursor; got %+v", res.OpsPerformed)
	}
}

// TestEmit_OffTargetUnsupportedProducesNoWarning verifies the targets
// filter wins before kind dispatch: a skill node targeted at another
// adapter is skipped silently, with no degradation warning.
func TestEmit_OffTargetUnsupportedProducesNoWarning(t *testing.T) {
	t.Parallel()

	emitted, err := captureOps(json.RawMessage(`{"nodes":[{"id":"x","kind":"skill","targets":["claude"],"body":"x"}]}`))
	if err != nil {
		t.Fatalf("captureOps: %v", err)
	}
	if len(emitted.records()) != 0 {
		t.Errorf("off-target skill must produce zero ops (filtered before dispatch); got %+v", emitted.records())
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
		{Op: adapterkit.OpKindMkdir, Path: ".cursor/rules/aienvs"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/rules/aienvs/README.md"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/rules/aienvs/first.mdc"},
		{Op: adapterkit.OpKindWriteFile, Path: ".cursor/rules/aienvs/second.mdc"},
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

	expectPaths := []string{
		".cursor/rules/aienvs/house-style.mdc",
		".cursor/mcp.json",
		".cursor/.aienvs-managed",
		"AGENTS.md",
	}
	for _, want := range expectPaths {
		if !containsPath(res.OpsPerformed, want) {
			t.Errorf("expected op for path %q; got %+v", want, res.OpsPerformed)
		}
	}
	// Skill must surface as a warning, not a write.
	if !containsKind(res.OpsPerformed, adapterkit.OpKindWarning) {
		t.Errorf("mixed-everything must include warning op for skill; got %+v", res.OpsPerformed)
	}
}

func TestEmit_HandleEmit_RejectsMalformedIRJSON(t *testing.T) {
	t.Parallel()

	_, err := handleEmit(context.Background(), adapterkit.EmitParams{
		Target: adapterName,
		IR:     json.RawMessage(`{not json}`),
	})
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
	})
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

// TestEmit_HandleEmit_SkipsOffTargetNodes exercises the targets filter
// through the real handleEmit boundary (not the captureOps helper): a
// cursor-targeted rule emits, a claude-targeted rule is silently
// skipped.
func TestEmit_HandleEmit_SkipsOffTargetNodes(t *testing.T) {
	t.Parallel()

	ir := json.RawMessage(`{"nodes":[
		{"id":"mine","kind":"rule","targets":["cursor"],"body":"cursor rule"},
		{"id":"theirs","kind":"rule","targets":["claude"],"body":"claude rule"}
	]}`)
	res, err := handleEmit(context.Background(), adapterkit.EmitParams{Target: adapterName, IR: ir})
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}
	if !containsPath(res.OpsPerformed, ".cursor/rules/aienvs/mine.mdc") {
		t.Errorf("cursor-targeted rule must emit; got %+v", res.OpsPerformed)
	}
	if containsPath(res.OpsPerformed, ".cursor/rules/aienvs/theirs.mdc") {
		t.Errorf("claude-targeted rule must be skipped; got %+v", res.OpsPerformed)
	}
}

// TestEmit_HandleEmit_RejectsDuplicateNodes exercises the duplicate
// rejection through the real handleEmit boundary, not captureOps.
func TestEmit_HandleEmit_RejectsDuplicateNodes(t *testing.T) {
	t.Parallel()

	ir := json.RawMessage(`{"nodes":[
		{"id":"x","kind":"rule","body":"first"},
		{"id":"x","kind":"rule","body":"second"}
	]}`)
	_, err := handleEmit(context.Background(), adapterkit.EmitParams{Target: adapterName, IR: ir})
	if err == nil {
		t.Fatal("duplicate (kind, id) must be rejected through handleEmit")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
		t.Fatalf("got err=%v want CodeInvalidParams", err)
	}
}

// TestEmitRule_WrapsBodyDecodeError covers emitRule's wrapBodyErr
// path: a node body that is neither a JSON string nor a valid JSON
// value surfaces as a structured CodeInvalidParams error rather than
// a raw decode panic. (Unreachable through a fully-parsed document,
// but the wrapper is the contract for any in-process caller passing a
// raw body.)
func TestEmitRule_WrapsBodyDecodeError(t *testing.T) {
	t.Parallel()

	emitted := &emittedOps{}
	state := newEmitState()
	err := emitRule(emitted, irNode{ID: "x", Kind: "rule", Body: json.RawMessage("{bad")}, state)
	if err == nil {
		t.Fatal("malformed rule body must be rejected")
	}
	var aerr *adapterkit.Error
	if !errors.As(err, &aerr) || aerr.Code != adapterkit.CodeInvalidParams {
		t.Fatalf("got err=%v want CodeInvalidParams", err)
	}
	if !strings.Contains(aerr.Message, "body") {
		t.Errorf("error must name the body decode failure; got %q", aerr.Message)
	}
}

// --- test helpers ----------------------------------------------------

func newEmitState() *emitState {
	return &emitState{
		readmeEmitted:   map[string]bool{},
		emittedFilePath: map[string]struct{}{},
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

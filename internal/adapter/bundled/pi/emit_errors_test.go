package pi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// emitDoc runs the production handleEmit over a raw IR document at project scope.
func emitDoc(t *testing.T, raw string) (adapterkit.EmitResult, error) {
	t.Helper()
	return emitDocScope(t, raw, "project")
}

// emitDocScope runs the production handleEmit over a raw IR document at the
// given initialize scope.
func emitDocScope(t *testing.T, raw, scope string) (adapterkit.EmitResult, error) {
	t.Helper()
	return handleEmit(context.Background(), adapterkit.EmitParams{Target: adapterName, IR: json.RawMessage(raw)}, scope)
}

func TestHandleEmit_HappyMixed(t *testing.T) {
	t.Parallel()
	// agents-md (tool-owned) + skill (mkdir + write_file).
	res, err := emitDoc(t, `{"nodes":[
		{"id":"team","kind":"agents-md","body":"## x"},
		{"id":"coder","kind":"skill","body":"# c"}
	]}`)
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}
	if len(res.OpsPerformed) == 0 || len(res.Ops) != len(res.OpsPerformed) {
		t.Fatalf("expected matching OpsPerformed/Ops; got %d/%d", len(res.OpsPerformed), len(res.Ops))
	}
}

func TestEmitSkill_AssetValidation(t *testing.T) {
	t.Parallel()
	bad := []string{
		`{"nodes":[{"id":"s","kind":"skill","body":"x","assets":[{"rel_path":"../escape.txt","content":"y"}]}]}`,
		`{"nodes":[{"id":"s","kind":"skill","body":"x","assets":[{"rel_path":"/abs.txt","content":"y"}]}]}`,
		`{"nodes":[{"id":"s","kind":"skill","body":"x","assets":[{"rel_path":"SKILL.md","content":"y"}]}]}`,
		`{"nodes":[{"id":"s","kind":"skill","body":"x","assets":[{"rel_path":"","content":"y"}]}]}`,
		`{"nodes":[{"id":"s","kind":"skill","body":"x","assets":[{"rel_path":"./","content":"y"}]}]}`,
	}
	for _, raw := range bad {
		if _, err := emitDoc(t, raw); err == nil {
			t.Errorf("expected asset-validation error for: %s", raw)
		}
	}
}

// TestHandleEmit_PiOnlyUnsupportedOnlyDoesNotFail is the capability-lied-gate
// regression for pi: a pi-only manifest containing only unsupported kinds (an
// MCP entry) must succeed as an honest warning, not fail. pi declares
// mcp-server-entry unsupported, so the runtime gate does not fire on a
// zero-non-warning-ops result. See the capability-lie-gate memory.
func TestHandleEmit_UnsupportedOnlyDoesNotFail(t *testing.T) {
	t.Parallel()
	res, err := emitDoc(t, `{"nodes":[{"id":"lsp","kind":"mcp-server-entry","body":"{\"command\":\"node\"}"}]}`)
	if err != nil {
		t.Fatalf("unsupported-only pi emit must not fail: %v", err)
	}
	if len(res.OpsPerformed) != 1 || res.OpsPerformed[0].Op != adapterkit.OpKindWarning {
		t.Fatalf("expected a single warning op; got %+v", res.OpsPerformed)
	}
}

func TestHandleEmit_MalformedIR(t *testing.T) {
	t.Parallel()
	if _, err := emitDoc(t, `{not json`); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestHandleEmit_DuplicateNodes(t *testing.T) {
	t.Parallel()
	_, err := emitDoc(t, `{"nodes":[
		{"id":"a","kind":"skill","body":"x"},
		{"id":"a","kind":"skill","body":"y"}
	]}`)
	if err == nil {
		t.Fatal("expected duplicate-node error")
	}
}

func TestHandleEmit_UnknownKind(t *testing.T) {
	t.Parallel()
	_, err := emitDoc(t, `{"nodes":[{"id":"a","kind":"bogus","body":"x"}]}`)
	if err == nil {
		t.Fatal("expected unknown-kind error")
	}
}

func TestHandleEmit_InvalidNodeID(t *testing.T) {
	t.Parallel()
	_, err := emitDoc(t, `{"nodes":[{"id":"Bad ID","kind":"skill","body":"x"}]}`)
	if err == nil {
		t.Fatal("expected invalid-id error")
	}
}

func TestEmitAgentsMD_RejectsMarkerInjection(t *testing.T) {
	t.Parallel()
	_, err := emitDoc(t, `{"nodes":[{"id":"team","kind":"agents-md","body":"<!-- agent-sync:end id=other -->"}]}`)
	if err == nil {
		t.Fatal("expected marker-injection rejection")
	}
}

func TestEmitSkill_AssetPrefixCollision(t *testing.T) {
	t.Parallel()
	// Individually-valid rel_paths that collide as ancestor/descendant (a file
	// and a dir can't share a path) or with the reserved SKILL.md entrypoint.
	bad := []string{
		`{"nodes":[{"id":"s","kind":"skill","body":"x","assets":[{"rel_path":"docs","content":"a"},{"rel_path":"docs/readme.md","content":"b"}]}]}`,
		`{"nodes":[{"id":"s","kind":"skill","body":"x","assets":[{"rel_path":"docs/readme.md","content":"b"},{"rel_path":"docs","content":"a"}]}]}`,
		`{"nodes":[{"id":"s","kind":"skill","body":"x","assets":[{"rel_path":"SKILL.md/meta.json","content":"b"}]}]}`,
	}
	for _, raw := range bad {
		if _, err := emitDoc(t, raw); err == nil {
			t.Errorf("expected asset prefix-collision error for: %s", raw)
		}
	}
	// A compatible sibling set must still succeed.
	if _, err := emitDoc(t, `{"nodes":[{"id":"s","kind":"skill","body":"x","assets":[{"rel_path":"docs/a.md","content":"a"},{"rel_path":"docs/b.md","content":"b"}]}]}`); err != nil {
		t.Errorf("compatible sibling assets must succeed: %v", err)
	}
}

func TestBundledGetenv(t *testing.T) {
	t.Parallel()
	if got := bundledGetenv(adapterkit.CookieEnvVar); got != bundledCookie {
		t.Errorf("bundledGetenv(cookie)=%q want %q", got, bundledCookie)
	}
	if got := bundledGetenv("SOMETHING_ELSE"); got != "" {
		t.Errorf("bundledGetenv(other)=%q want empty", got)
	}
}

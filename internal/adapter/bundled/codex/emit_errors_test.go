package codex

import (
	"context"
	"encoding/json"
	"strings"
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
	res, err := emitDoc(t, `{"nodes":[
		{"id":"team","kind":"agents-md","body":"## x"},
		{"id":"coder","kind":"skill","body":"# c"},
		{"id":"lsp","kind":"mcp-server-entry","body":"{\"command\":\"node\"}"}
	]}`)
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}
	if len(res.OpsPerformed) == 0 || len(res.Ops) != len(res.OpsPerformed) {
		t.Fatalf("expected matching OpsPerformed/Ops; got %d/%d", len(res.OpsPerformed), len(res.Ops))
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

func TestEmitMCP_RejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	// A body that is a JSON string containing "{" — valid JSON string, but not
	// an object after the string decode.
	_, err := emitDoc(t, `{"nodes":[{"id":"lsp","kind":"mcp-server-entry","body":"\"{\""}]}`)
	if err == nil {
		t.Fatal("expected non-object mcp body rejection")
	}
}

func TestEmitMCP_RejectsNullValue(t *testing.T) {
	t.Parallel()
	// null is not representable in TOML — renderTOMLTableBody must reject it.
	_, err := emitDoc(t, `{"nodes":[{"id":"lsp","kind":"mcp-server-entry","body":"{\"x\":null}"}]}`)
	if err == nil {
		t.Fatal("expected null-value TOML rejection")
	}
}

func TestEmitMCP_RejectsNullBody(t *testing.T) {
	t.Parallel()
	// A wire body of the JSON string "null" decodes to the bytes `null`, which
	// is valid JSON and unmarshals into a nil map without error — must be
	// rejected, not rendered as an empty [mcp_servers.agentsync_<id>] table.
	_, err := emitDoc(t, `{"nodes":[{"id":"lsp","kind":"mcp-server-entry","body":"null"}]}`)
	if err == nil {
		t.Fatal("expected rejection of null mcp body")
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

func TestRenderTOMLTableBody_InlineNesting(t *testing.T) {
	t.Parallel()
	body, err := renderTOMLTableBody(map[string]json.RawMessage{
		"command": json.RawMessage(`"node"`),
		"flags":   json.RawMessage(`true`),
		"port":    json.RawMessage(`8080`),
		"args":    json.RawMessage(`["a","b"]`),
		"env":     json.RawMessage(`{"K":"v"}`),
	})
	if err != nil {
		t.Fatalf("renderTOMLTableBody: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		`command = "node"`, `flags = true`, `port = 8080`,
		`args = ["a", "b"]`, `env = { K = "v" }`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("toml body missing %q; got:\n%s", want, s)
		}
	}
}

func TestRenderTOMLTableBody_DoesNotHTMLEscape(t *testing.T) {
	t.Parallel()
	// Characters common in URLs/args (<, >, &) must render literally, not as
	// < / > / &, so .codex/config.toml stays human-readable.
	body, err := renderTOMLTableBody(map[string]json.RawMessage{
		"url": json.RawMessage(`"https://mcp.example/api?a=1&b=2"`),
		"cmd": json.RawMessage(`"run <in> & echo done"`),
	})
	if err != nil {
		t.Fatalf("renderTOMLTableBody: %v", err)
	}
	s := string(body)
	if strings.Contains(s, `\u00`) {
		t.Errorf("toml body must not contain \\u00xx HTML escapes; got:\n%s", s)
	}
	for _, want := range []string{
		`url = "https://mcp.example/api?a=1&b=2"`,
		`cmd = "run <in> & echo done"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("toml body missing %q; got:\n%s", want, s)
		}
	}
}

func TestTOMLKey_QuotesNonBareKeys(t *testing.T) {
	t.Parallel()
	if got := tomlKey("plain_key-1"); got != "plain_key-1" {
		t.Errorf("bare key requoted: %q", got)
	}
	if got := tomlKey("has.dot"); got != `"has.dot"` {
		t.Errorf("dotted key not quoted: %q", got)
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

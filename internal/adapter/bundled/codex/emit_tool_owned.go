package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	codexConfigPath = ".codex/config.toml"
	mcpTOMLPathBase = "mcp_servers.aienvs_"
	agentsMDPath    = "AGENTS.md"
	sectionIDPrefix = "aienvs:"
)

// markerOpenBytes is the literal HTML-comment opener every agent-sync section
// marker uses. An agents-md body containing this sequence is rejected so a
// hostile body can't forge a section split inside AGENTS.md.
var markerOpenBytes = []byte("<!-- aienvs:")

// bareTOMLKey matches keys that can be written unquoted in TOML.
var bareTOMLKey = regexp.MustCompile(`\A[A-Za-z0-9_-]+\z`)

// emitMCPServerEntry emits one write_tool_owned op into .codex/config.toml as a
// [mcp_servers.aienvs_<id>] table (toml-path locator). The Content is the raw
// TOML table body (key = value lines, no header) the string-aware line-splice
// merge expects; the merge writes the [mcp_servers.aienvs_<id>] header itself.
//
// The IR body must be a JSON object (an MCP server entry: command/args/env or
// url/...). It is rendered to TOML with inline tables and inline arrays so the
// body carries no sub-table headers — a `[env]` header would be interpreted as
// a top-level table once spliced under the server table, breaking nesting.
func emitMCPServerEntry(emitted *emittedOps, node irNode) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if !json.Valid(body) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: mcp-server-entry %q body is not valid JSON; refusing to corrupt .codex/config.toml", node.ID),
		}
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(body, &entry); err != nil {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: mcp-server-entry %q body must be a JSON object (got non-object)", node.ID),
		}
	}

	tomlBody, err := renderTOMLTableBody(entry)
	if err != nil {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: mcp-server-entry %q: %s", node.ID, err.Error()),
		}
	}

	emitted.add(adapterkit.OpWriteToolOwned{
		Path:    codexConfigPath,
		Kind:    adapterkit.ToolOwnedKindTOMLPath,
		Locator: mcpTOMLPathBase + node.ID,
		Content: tomlBody,
	})
	return nil
}

// emitAgentsMD writes the agents-md node into workspace-root AGENTS.md as a
// managed section between aienvs:begin/end markers. AGENTS.md is tool-owned and
// shared (codex, cursor, pi all write their own id-keyed sections); the merge
// step preserves user content and other adapters' sections.
func emitAgentsMD(emitted *emittedOps, node irNode) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if bytes.Contains(body, markerOpenBytes) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: agents-md %q body contains agent-sync marker syntax (%q); refusing to corrupt AGENTS.md", node.ID, string(markerOpenBytes)),
		}
	}
	// The engine owns the begin/end markers (it renders the managed block from
	// the locator during the markdown-section merge). The adapter passes the
	// INNER body only — sending marker-wrapped content is rejected by the merge.
	emitted.add(adapterkit.OpWriteToolOwned{
		Path:    agentsMDPath,
		Kind:    adapterkit.ToolOwnedKindMarkdownSection,
		Locator: sectionIDPrefix + node.ID,
		Content: body,
	})
	return nil
}

// renderTOMLTableBody renders a JSON object as a TOML table body — one
// `key = value` line per top-level key, sorted for determinism, with nested
// objects as inline tables and arrays as inline arrays. No table headers, so
// the result splices cleanly under a [mcp_servers.aienvs_<id>] header.
func renderTOMLTableBody(entry map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(entry))
	for k := range entry {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		var v any
		if err := json.Unmarshal(entry[k], &v); err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		rendered, err := tomlValue(v)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		b.WriteString(tomlKey(k))
		b.WriteString(" = ")
		b.WriteString(rendered)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// tomlKey renders a TOML key, quoting it only when it is not a bare key.
func tomlKey(k string) string {
	if bareTOMLKey.MatchString(k) {
		return k
	}
	return quoteTOMLString(k)
}

// tomlValue renders a decoded JSON value as an inline TOML value.
func tomlValue(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", fmt.Errorf("null is not representable in TOML")
	case string:
		return quoteTOMLString(x), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case float64:
		// json.Marshal renders float64 in TOML-compatible minimal form
		// (e.g. 8, 1.5) for the integer/decimal values MCP configs use.
		out, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return string(out), nil
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			r, err := tomlValue(e)
			if err != nil {
				return "", err
			}
			parts = append(parts, r)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			r, err := tomlValue(x[k])
			if err != nil {
				return "", err
			}
			parts = append(parts, tomlKey(k)+" = "+r)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	default:
		return "", fmt.Errorf("unsupported value type %T", v)
	}
}

// quoteTOMLString renders s as a TOML basic string. JSON string escaping is a
// subset of TOML basic-string escaping (\", \\, \n, \t, \uXXXX, ...), so
// json.Marshal produces a valid TOML basic string for the content MCP configs
// carry.
func quoteTOMLString(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		// json.Marshal of a string never fails; fall back defensively.
		return `"` + s + `"`
	}
	return string(out)
}

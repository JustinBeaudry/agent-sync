package codex

// managedHeaderTemplate is the canonical "do not edit" banner the codex adapter
// prepends to every owned markdown file (skills). The {source-url} and
// {short-sha} placeholders are filled in by the sync engine once it plumbs
// IR-decode context through EmitParams._meta.
//
// AGENTS.md sections carry no inline header: the engine-owned begin/end markers
// are the ownership advertisement there (see emit_tool_owned.go), and the
// markdown-section merge renders those markers from the op locator.
const managedHeaderTemplate = "<!-- Managed by agent-sync — do not edit. Source: {source-url}@{short-sha}. Regenerate: agent-sync sync -->\n\n"

// markdownHeader returns the managed-file banner used at the top of every owned
// markdown file (skills).
func markdownHeader() []byte {
	return []byte(managedHeaderTemplate)
}

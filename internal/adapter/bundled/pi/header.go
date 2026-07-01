package pi

// managedHeaderTemplate is the canonical "do not edit" banner the pi adapter
// prepends to every owned markdown file (skills). The {source-url} and
// {short-sha} placeholders are filled in by the sync engine once it plumbs
// IR-decode context through EmitParams._meta. It is byte-identical to the codex
// adapter's header, so a skill co-emitted to the shared .agents/skills/ tree by
// both adapters produces the same SKILL.md bytes.
//
// AGENTS.md sections carry no inline header: the engine-owned begin/end markers
// are the ownership advertisement there (see emit_tool_owned.go).
const managedHeaderTemplate = "<!-- Managed by agent-sync — do not edit. Source: {source-url}@{short-sha}. Regenerate: agent-sync sync -->\n\n"

// markdownHeader returns the managed-file banner used at the top of every owned
// markdown file (skills).
func markdownHeader() []byte {
	return []byte(managedHeaderTemplate)
}

package claude

import (
	"fmt"
	"strings"

	"github.com/agent-sync/agent-sync/internal/ir"
)

// managedHeaderTemplate is the canonical "do not edit" banner the
// claude adapter prepends to every owned markdown file. The
// {source-url} and {short-sha} placeholders are filled in by the
// sync engine (Unit 13) once it plumbs IR-decode context through
// EmitParams._meta. v1 ships the placeholder so the line shape is
// stable from day one.
//
// Trailing blank line keeps a one-line gap between the header and
// user-visible content; without it the body's first heading would
// fold into the comment block on some markdown renderers.
const managedHeaderTemplate = "<!-- Managed by agent-sync — do not edit. Source: {source-url}@{short-sha}. Regenerate: agent-sync sync -->\n\n"

// jsonSidecarBody is the body for the .aienvs-managed sidecar marker
// the claude adapter writes next to .mcp.json. JSON has no comment
// syntax, so the sidecar advertises ownership instead of editing
// the JSON file with a comment.
//
// The body is intentionally short and human-readable; it is not
// machine-parsed by anything in v1.
const jsonSidecarBody = `Managed by agent-sync.

The sibling .mcp.json file contains entries owned by agent-sync under
the /mcpServers/aienvs_<id> JSON pointers. Other entries are
preserved across syncs.

To remove aienvs-managed entries: agent-sync unmanage claude
`

// markdownHeader returns the managed-file banner used at the top of
// every owned markdown file (rules, commands, skills). Content is
// constant in v1; future versions may inline real source URL +
// short SHA.
func markdownHeader() []byte {
	return []byte(managedHeaderTemplate)
}

// jsonSidecarMarker returns the body for the .aienvs-managed sidecar
// marker placed next to strict-JSON tool-owned files (currently just
// .mcp.json). See managedHeaderTemplate doc for why JSON gets a
// sidecar instead of an inline comment.
func jsonSidecarMarker() []byte {
	return []byte(jsonSidecarBody)
}

// sectionMarkerBegin returns the opening marker for a managed section
// inside a tool-owned markdown file (currently just CLAUDE.md). The
// marker is HTML-comment shaped so any markdown renderer treats it
// as invisible.
//
// id MUST satisfy the IR id grammar; an invalid id is a programming
// error inside the adapter (the IR decoder rejects bad ids upstream)
// and panics here so the failure surfaces immediately rather than
// silently corrupting CLAUDE.md.
func sectionMarkerBegin(id string) []byte {
	mustValidID(id)
	return []byte("<!-- agent-sync:begin id=" + id + " -->")
}

// sectionMarkerEnd returns the closing marker. See sectionMarkerBegin
// for the id-validation contract.
func sectionMarkerEnd(id string) []byte {
	mustValidID(id)
	return []byte("<!-- agent-sync:end id=" + id + " -->")
}

// wrapManagedSection wraps body bytes between the begin/end markers
// for the supplied id and returns the full slice ready for a
// write_tool_owned op. A trailing newline after the end marker keeps
// the merged file POSIX-clean if it is the last entry.
func wrapManagedSection(id string, body []byte) []byte {
	begin := sectionMarkerBegin(id)
	end := sectionMarkerEnd(id)
	out := make([]byte, 0, len(begin)+len(body)+len(end)+3)
	out = append(out, begin...)
	out = append(out, '\n')
	out = append(out, body...)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, end...)
	out = append(out, '\n')
	return out
}

// readmeForSubdir returns the README.md body the claude adapter
// emits into each reserved subdirectory. The README explains
// ownership and the exit path so a human stumbling into
// .claude/rules/aienvs/README.md knows it isn't user content.
//
// subdirLabel is the relative path used inside the body for clarity
// (e.g., ".claude/rules/aienvs"); pass the same string used in the
// declared output.
func readmeForSubdir(subdirLabel string) []byte {
	subdirLabel = strings.TrimSpace(subdirLabel)
	if subdirLabel == "" {
		// Defensive default. Empty subdirLabel is a programmer error;
		// an empty README is less harmful than a panic during a sync
		// that's already trying to recover.
		subdirLabel = "(unknown subdirectory)"
	}
	return fmt.Appendf(nil,
		"# Managed by agent-sync\n\n"+
			"Files inside `%s/` are owned by the agent-sync `claude` adapter.\n"+
			"Editing them by hand is detected as drift on the next sync\n"+
			"and the changes are overwritten.\n\n"+
			"To remove every aienvs-owned file in this directory and\n"+
			"unbind it from the workspace, run:\n\n"+
			"    agent-sync unmanage claude\n",
		subdirLabel,
	)
}

// mustValidID panics with a programmer-targeted message when id
// does not satisfy the IR id grammar. The IR decoder catches user
// inputs upstream; this guard is for adapter bugs.
func mustValidID(id string) {
	if !ir.IsValidID(id) {
		panic(fmt.Sprintf("claude: invalid id %q reached marker construction (IR decoder should have rejected it upstream)", id))
	}
}

package claude

import (
	"fmt"
	"strings"
)

// renderManagedHeader renders the "do not edit" banner with real
// provenance (plan U3). sourceURL is the audit-safe source identity —
// the cache-canonicalized git URL or a local source path — from the
// session, or a per-node override for composed nodes. sourceCommit is
// the resolved canonical SHA, empty for working-tree sources. Forms:
//
//	git-backed: <!-- … Source: <url>@<sha12>. Regenerate: … -->
//	local:      <!-- … Source: <path>. Regenerate: … -->
//	no source:  <!-- … Regenerate: … --> (defensive; a session from a
//	            pre-U2 engine carries no source identity)
//
// The trailing blank line keeps a one-line gap between the header and
// user-visible content; without it the body's first heading would fold
// into the comment block on some markdown renderers.
func renderManagedHeader(sourceURL, sourceCommit string) []byte {
	const pre = "<!-- Managed by agent-sync — do not edit. "
	const post = "Regenerate: agent-sync sync -->\n\n"
	switch {
	case sourceURL == "":
		return []byte(pre + post)
	case sourceCommit == "":
		return []byte(pre + "Source: " + sourceURL + ". " + post)
	default:
		return []byte(pre + "Source: " + sourceURL + "@" + shortSHA(sourceCommit) + ". " + post)
	}
}

// shortSHA truncates a full commit SHA to git's collision-safe 12-char
// display form; shorter inputs pass through unchanged.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// jsonSidecarBody is the body for the .agent-sync-managed sidecar marker
// the claude adapter writes next to .mcp.json. JSON has no comment
// syntax, so the sidecar advertises ownership instead of editing
// the JSON file with a comment.
//
// The body is intentionally short and human-readable; it is not
// machine-parsed by anything in v1.
const jsonSidecarBody = `Managed by agent-sync.

The sibling .mcp.json file contains entries owned by agent-sync under
the /mcpServers/agentsync_<id> JSON pointers. Other entries are
preserved across syncs.

To remove agent-sync-managed entries: agent-sync unmanage claude
`

// jsonSidecarMarker returns the body for the .agent-sync-managed sidecar
// marker placed next to strict-JSON tool-owned files (currently just
// .mcp.json). See managedHeaderTemplate doc for why JSON gets a
// sidecar instead of an inline comment.
func jsonSidecarMarker() []byte {
	return []byte(jsonSidecarBody)
}

// readmeForSubdir returns the README.md body the claude adapter
// emits into each reserved subdirectory. The README explains
// ownership and the exit path so a human stumbling into
// .claude/rules/agent-sync/README.md knows it isn't user content.
//
// subdirLabel is the relative path used inside the body for clarity
// (e.g., ".claude/rules/agent-sync"); pass the same string used in the
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
			"To remove every agent-sync-owned file in this directory and\n"+
			"unbind it from the workspace, run:\n\n"+
			"    agent-sync unmanage claude\n",
		subdirLabel,
	)
}

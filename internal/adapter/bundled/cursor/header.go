package cursor

import (
	"fmt"
	"strings"
)

// managedHeaderTemplate is the canonical "do not edit" banner the
// cursor adapter prepends to every owned markdown / .mdc file. The
// {source-url} and {short-sha} placeholders are filled in by the
// sync engine (Unit 13) once it plumbs IR-decode context through
// EmitParams._meta. v1 ships the placeholder so the line shape is
// stable from day one.
//
// For v1 the emitted .mdc files carry no frontmatter (the IR strips
// it at decode), so a leading HTML comment is safe — it does not
// displace a `---` frontmatter opener off line 1.
//
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
// user-visible content. BYTE-IDENTITY: codex and pi must render the
// exact same bytes for the same inputs — the shared .agents/skills
// tree is co-emitted (ADV-1) and the engine fail-closes on divergence.
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
// the cursor adapter writes next to .cursor/mcp.json. JSON has no
// comment syntax, so the sidecar advertises ownership instead of
// editing the JSON file with a comment.
//
// The body is intentionally short and human-readable; it is not
// machine-parsed by anything in v1.
const jsonSidecarBody = `Managed by agent-sync.

The sibling .cursor/mcp.json file contains entries owned by agent-sync
under the /mcpServers/agentsync_<id> JSON pointers. Other entries are
preserved across syncs.

To remove agent-sync-managed entries: agent-sync unmanage cursor
`

// jsonSidecarMarker returns the body for the .agent-sync-managed sidecar
// marker placed next to strict-JSON tool-owned files (currently just
// .cursor/mcp.json). See managedHeaderTemplate doc for why JSON gets
// a sidecar instead of an inline comment.
func jsonSidecarMarker() []byte {
	return []byte(jsonSidecarBody)
}

// readmeForSubdir returns the README.md body the cursor adapter
// emits into each reserved subdirectory. The README explains
// ownership and the exit path so a human stumbling into
// .cursor/rules/agent-sync/README.md knows it isn't user content.
//
// subdirLabel is the relative path used inside the body for clarity
// (e.g., ".cursor/rules/agent-sync"); pass the same string used in the
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
			"Files inside `%s/` are owned by the agent-sync `cursor` adapter.\n"+
			"Editing them by hand is detected as drift on the next sync\n"+
			"and the changes are overwritten.\n\n"+
			"To remove every agent-sync-owned file in this directory and\n"+
			"unbind it from the workspace, run:\n\n"+
			"    agent-sync unmanage cursor\n",
		subdirLabel,
	)
}

package antigravity

import (
	"fmt"
	"strings"
)

// renderManagedHeader renders the "do not edit" banner with real provenance.
// sourceURL is the audit-safe source identity — the cache-canonicalized git URL
// or a local source path — from the session, or a per-node override for composed
// nodes. sourceCommit is the resolved canonical SHA, empty for working-tree
// sources. Forms:
//
//	git-backed: <!-- … Source: <url>@<sha12>. Regenerate: … -->
//	local:      <!-- … Source: <path>. Regenerate: … -->
//	no source:  <!-- … Regenerate: … --> (defensive)
//
// The trailing blank line keeps a one-line gap between the header and
// user-visible content. BYTE-IDENTITY: codex, pi, and antigravity must render
// the exact same bytes for the same inputs — the shared .agents/skills tree is
// co-emitted (ADV-1) and the engine fail-closes on divergence.
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

// shortSHA truncates a full commit SHA to git's collision-safe 12-char display
// form; shorter inputs pass through unchanged.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// readmeForSubdir returns the README.md body the antigravity adapter emits into
// each reserved subdirectory. The README explains ownership and the exit path so
// a human stumbling into .agent/rules/agent-sync/README.md knows it isn't user
// content.
//
// subdirLabel is the relative path used inside the body for clarity (e.g.
// ".agent/rules/agent-sync"); pass the same string used in the declared output.
func readmeForSubdir(subdirLabel string) []byte {
	subdirLabel = strings.TrimSpace(subdirLabel)
	if subdirLabel == "" {
		// Defensive default. Empty subdirLabel is a programmer error; an empty
		// README is less harmful than a panic during a sync already recovering.
		subdirLabel = "(unknown subdirectory)"
	}
	return fmt.Appendf(nil,
		"# Managed by agent-sync\n\n"+
			"Files inside `%s/` are owned by the agent-sync `antigravity` adapter.\n"+
			"Editing them by hand is detected as drift on the next sync\n"+
			"and the changes are overwritten.\n\n"+
			"To remove every agent-sync-owned file in this directory and\n"+
			"unbind it from the workspace, run:\n\n"+
			"    agent-sync unmanage antigravity\n",
		subdirLabel,
	)
}

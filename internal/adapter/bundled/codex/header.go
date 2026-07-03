package codex

// managedHeaderTemplate is the canonical "do not edit" banner the codex adapter
// prepends to every owned markdown file (skills). The {source-url} and
// {short-sha} placeholders are filled in by the sync engine once it plumbs
// IR-decode context through EmitParams._meta.
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

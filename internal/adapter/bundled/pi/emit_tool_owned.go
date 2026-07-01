package pi

import (
	"bytes"
	"fmt"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	agentsMDPath    = "AGENTS.md"
	sectionIDPrefix = "agent-sync:"

	// scopeUser is the wire value (hierarchy.Level.String()) for the user-home
	// scope. Any other value (incl. "" / "project" / "directory") is project
	// scope.
	scopeUser = "user"

	// userAgentsMDPath is where Pi reads user-global agent instructions:
	// ~/.pi/agent/AGENTS.md (the scope root is $HOME at user scope). Pi does NOT
	// read ~/AGENTS.md at the user level, so this is a genuine remap — the same
	// shape as Codex's AGENTS.md → ~/.codex/AGENTS.md. The global config base is
	// ~/.pi/agent/ (verified 2026-06-30), and can be relocated via
	// PI_CODING_AGENT_DIR (documented assumption; not handled). See plan
	// docs/plans/2026-06-30-002.
	userAgentsMDPath = ".pi/agent/AGENTS.md"
)

// pathSet holds the scope-resolved destination for the agents-md overlay. The
// skills tree (.agents/skills/) is unconditional: its relative path already
// resolves correctly under $HOME at user scope (Pi reads ~/.agents/skills/), so
// only agents-md needs scope-dependent resolution.
type pathSet struct {
	agentsMD string
}

// resolvePathSet maps the initialize scope to Pi's scope-dependent paths.
// declaredOutputs (capabilities.go) and the emitted op paths (here) MUST both
// resolve from this one function so they never drift — a mismatch is rejected
// by the runtime's path-safety gate.
func resolvePathSet(scope string) pathSet {
	if scope == scopeUser {
		return pathSet{agentsMD: userAgentsMDPath}
	}
	return pathSet{agentsMD: agentsMDPath}
}

// markerOpenBytes is the literal HTML-comment opener every agent-sync section
// marker uses. An agents-md body containing this sequence is rejected so a
// hostile body can't forge a section split inside AGENTS.md.
var markerOpenBytes = []byte("<!-- agent-sync:")

// emitAgentsMD writes the agents-md node into AGENTS.md (workspace root at
// project scope; .pi/agent/AGENTS.md at user scope) as a managed section
// between <!-- agent-sync:begin id=<id> --> and <!-- agent-sync:end id=<id> -->
// markers. AGENTS.md is tool-owned (read by Pi and other agents); user content
// outside the managed section is preserved by the merge step.
//
// pi, codex, and cursor all section-merge into the same AGENTS.md, each keyed
// by its node id; the marker syntax is byte-identical so the sections coexist.
//
// The body is rejected when it contains the agent-sync marker opener: a body
// carrying `<!-- agent-sync:end id=other -->` could forge another section,
// leaving the merged AGENTS.md with conflicting markers the merge cannot
// resolve safely.
func emitAgentsMD(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if bytes.Contains(body, markerOpenBytes) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("pi: agents-md %q body contains agent-sync marker syntax (%q); refusing to corrupt %s", node.ID, string(markerOpenBytes), state.paths.agentsMD),
		}
	}
	// The engine owns the begin/end markers (it renders the managed block from
	// the locator during the markdown-section merge). The adapter passes the
	// INNER body only — sending marker-wrapped content is rejected by the merge.
	emitted.add(adapterkit.OpWriteToolOwned{
		Path:    state.paths.agentsMD,
		Kind:    adapterkit.ToolOwnedKindMarkdownSection,
		Locator: sectionIDPrefix + node.ID,
		Content: body,
	})
	return nil
}

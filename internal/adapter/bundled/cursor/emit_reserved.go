package cursor

import (
	"fmt"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	rulesSubdir = ".cursor/rules/agent-sync"
	mdcExt      = ".mdc"
)

// emitRule maps one rule node to:
//   - mkdir(.cursor/rules/agent-sync)               (deduped per-emit)
//   - write_file(.cursor/rules/agent-sync/README.md) (deduped per-emit)
//   - write_file(.cursor/rules/agent-sync/<id>.mdc)
//
// Unlike the claude adapter, there is no `paths:` frontmatter ward:
// that warning exists for a Claude Code activation bug with no Cursor
// equivalent (Cursor's .mdc frontmatter supports globs/description/
// alwaysApply natively). v1 emits frontmatter-less .mdc files because
// the IR strips frontmatter at decode; they behave as manual /
// agent-requested rules until IR frontmatter exposure lands.
//
// Cursor reads project rules from .cursor/rules/ as .mdc files and
// supports nested subdirectories, which is what makes the agent-sync/
// subfolder a valid owned subdirectory.
func emitRule(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	if err := ensureSubdir(emitted, rulesSubdir, state); err != nil {
		return err
	}

	rulePath := rulesSubdir + "/" + node.ID + mdcExt
	if err := state.recordWritePath(rulePath); err != nil {
		return err
	}
	wf, err := adapterkit.NewOpWriteFile(rulePath, 0o644, prependHeader(state, node, body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)
	return nil
}

// ensureSubdir adds the per-emit mkdir + README pair for a reserved
// subdirectory once per emit call. Subsequent calls are no-ops.
//
// Returns InvalidParams if a node ID has already taken the README
// path inside this subdir (e.g., a rule node literally named "README"
// — its emitted path would be .../agent-sync/README.mdc, which does not
// collide, but the README.md guard still protects the reserved file).
func ensureSubdir(emitted *emittedOps, subdir string, state *emitState) error {
	if state.readmeEmitted[subdir] {
		return nil
	}
	state.readmeEmitted[subdir] = true
	emitted.add(adapterkit.OpMkdir{Path: subdir, Mode: 0o755})
	readmePath := subdir + "/README.md"
	if err := state.recordWritePath(readmePath); err != nil {
		return err
	}
	wf, err := adapterkit.NewOpWriteFile(readmePath, 0o644, readmeForSubdir(subdir))
	if err != nil {
		// readmeForSubdir returns a small fixed body; the only way
		// NewOpWriteFile can fail is the payload-too-large guard,
		// which is unreachable here. Panic surfaces the bug.
		panic(fmt.Sprintf("cursor: building README for %s: %v", subdir, err))
	}
	emitted.add(wf)
	return nil
}

// prependHeader inserts the managed-file header before the body. The
// header carries per-node provenance when the node has a source
// override (composed nodes); otherwise the session-level source.
func prependHeader(state *emitState, node irNode, body []byte) []byte {
	url, commit := state.sourceURL, state.sourceCommit
	if node.SourceURL != "" {
		url, commit = node.SourceURL, node.SourceCommit
	}
	header := renderManagedHeader(url, commit)
	out := make([]byte, 0, len(header)+len(body))
	out = append(out, header...)
	out = append(out, body...)
	return out
}

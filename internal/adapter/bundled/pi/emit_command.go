package pi

import (
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// promptsParent is the flat directory Pi reads prompt-template slash commands
// from: .pi/prompts/<name>.md, invoked as /<name>. It is a file-leaf output —
// agent-sync owns only the individual <id>.md files it emits, co-resident with
// the user's own prompt files. The relative path resolves under $HOME at user
// scope (~/.pi/prompts); if a future Pi release relocates the user-global prompt
// dir, this is a one-line change (see resolvePathSet / docs/adapters/pi.md).
const promptsParent = ".pi/prompts"

// emitCommand maps one command node to a single write_file at
// .pi/prompts/<id>.md with the managed-file header prepended. No mkdir and no
// README: file-leaf owns individual files, not the directory. A pre-existing
// unmanaged file at the same path fails closed rather than being clobbered
// (adopt via --adopt-prefix).
func emitCommand(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	cmdPath := promptsParent + "/" + node.ID + ".md"
	if err := state.recordWritePath(cmdPath); err != nil {
		return err
	}
	url, commit := sourceForNode(state, node)
	header := renderManagedHeader(url, commit)
	content := make([]byte, 0, len(header)+len(body))
	content = append(content, header...)
	content = append(content, body...)
	wf, err := adapterkit.NewOpWriteFile(cmdPath, 0o644, content)
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)
	return nil
}

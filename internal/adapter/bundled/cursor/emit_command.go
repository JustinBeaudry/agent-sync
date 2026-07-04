package cursor

import (
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// commandsParent is the flat directory Cursor reads custom slash-commands from:
// .cursor/commands/<name>.md at the project root and ~/.cursor/commands/<name>.md
// at user scope (both read by the Cursor IDE and CLI). It is a file-leaf output:
// agent-sync owns only the individual <id>.md files it emits, co-resident with
// the user's own command files. Nested subdirectories are read by the Cursor IDE
// but NOT the CLI, so agent-sync emits flat, one file per command.
const commandsParent = ".cursor/commands"

// emitCommand maps one command node to a single write_file at
// .cursor/commands/<id>.md with the managed-file header prepended. No mkdir and
// no README: file-leaf owns individual files, not the directory (a README would
// be an agent-sync file dropped into the user's shared commands dir). The engine
// owns the whole file (file-leaf mode); a pre-existing unmanaged file at the same
// path fails closed rather than being clobbered (adopt via --adopt-prefix).
func emitCommand(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	cmdPath := commandsParent + "/" + node.ID + ".md"
	if err := state.recordWritePath(cmdPath); err != nil {
		return err
	}
	wf, err := adapterkit.NewOpWriteFile(cmdPath, 0o644, prependHeader(state, node, body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)
	return nil
}

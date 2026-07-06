package harness

import "github.com/agent-sync/agent-sync/internal/ir"

type Visibility string
type Inheritance string
type Safety string
type MergeKind string

const (
	VisibilityPersonal     Visibility = "personal"
	VisibilityTeam         Visibility = "team"
	VisibilityMachineLocal Visibility = "machine-local"

	InheritanceRootOnly    Inheritance = "root-only"
	InheritanceDescendants Inheritance = "descendants"

	SafetyPassive    Safety = "passive"
	SafetyToolAccess Safety = "tool-access"
	SafetyExecutable Safety = "executable"

	MergeTOMLKey    MergeKind = "toml-key"
	MergeCodexHooks MergeKind = "codex-hooks"
)

type Fragment struct {
	ID          string
	Target      string
	Path        string
	Merge       MergeKind
	Locator     string
	Visibility  Visibility
	Inheritance Inheritance
	Safety      Safety
	PayloadPath string
	Payload     []byte
	Scope       string
	Provenance  ir.Provenance
}

func (f Fragment) Identity() string {
	return f.Target + "\x00" + f.Path + "\x00" + string(f.Merge) + "\x00" + f.Locator
}

type Warning struct {
	Code    string
	Message string
	Path    string
}

type DecodeOptions struct {
	Scope string
}

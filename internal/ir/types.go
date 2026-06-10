// Package ir implements the v1 Intermediate Representation described in
// docs/spec/ir-v1.md.
//
// The decoder reads a canonical-repo at a specific commit SHA and returns
// a deterministic ordered slice of Nodes. Each adapter (claude, cursor,
// codex, …) consumes Nodes; no adapter ever sees raw canonical files.
//
// This package owns the IR types only. The decoder lives in decode.go and
// the per-concept wrappers in kinds.go.
package ir

import (
	"errors"
	"fmt"
	"regexp"
)

// Kind enumerates the closed set of v1 concept kinds. Adding a kind is a
// v2-grade change.
type Kind string

const (
	KindAgentsMD        Kind = "agents-md"
	KindRule            Kind = "rule"
	KindSkill           Kind = "skill"
	KindCommand         Kind = "command"
	KindPluginReference Kind = "plugin-reference"
	KindMCPServerEntry  Kind = "mcp-server-entry"
)

// AllKinds returns the v1 closed set in stable order. Useful for capability
// matrix iteration and tests.
func AllKinds() []Kind {
	return []Kind{
		KindAgentsMD,
		KindRule,
		KindSkill,
		KindCommand,
		KindPluginReference,
		KindMCPServerEntry,
	}
}

// ValidateKind reports nil when k is a recognized v1 kind.
func ValidateKind(k Kind) error {
	for _, want := range AllKinds() {
		if k == want {
			return nil
		}
	}
	return fmt.Errorf("ir: unrecognized kind %q", string(k))
}

// Node is one IR record produced by the decoder. See docs/spec/ir-v1.md for
// the full contract.
type Node struct {
	// ID is the canonical identifier for this node. Pattern: [a-z0-9][a-z0-9-_]{0,63}.
	// Uniqueness is enforced per Kind, not globally.
	ID string

	// Kind is one of the six v1 concept kinds.
	Kind Kind

	// Version is the author-controlled monotonic counter from frontmatter.
	// Default is 1 when frontmatter is absent or omits the field.
	Version int

	// Required, when true, means every targeted adapter MUST mark this Kind
	// as `supported` in its capability matrix. A required-unmet target
	// causes sync to fail.
	Required bool

	// Targets restricts which adapters see this node. Empty means "all
	// registered adapters".
	Targets []string

	// Provenance points back to the canonical-repo file the node came from.
	Provenance Provenance

	// Body is the post-frontmatter content for markdown kinds, or the full
	// (post-`__agentsync_*`-strip) file content for JSON/TOML kinds.
	Body []byte
}

// Provenance is the git-keyed location of the source file in the canonical
// repo. BlobSHA is the SHA git itself computed for the file content; it
// lets ledger / validate / diff cross-check against `git cat-file`.
type Provenance struct {
	// Path is the posix-style path within the canonical repo (e.g.
	// "rules/no-pr-on-friday.md").
	Path string

	// BlobSHA is the 40-hex git blob SHA.
	BlobSHA string
}

// Asset is an auxiliary blob carried alongside a Skill node. Skill
// directories may contain non-SKILL.md files; each becomes an Asset.
type Asset struct {
	// RelPath is the path relative to the skill's directory (e.g.
	// "templates/foo.txt" for skills/<id>/templates/foo.txt).
	RelPath string

	// Provenance is the git location of the asset.
	Provenance Provenance

	// Content is the asset's full byte contents.
	Content []byte
}

// Skill bundles a Node with its asset list. Returned alongside the base
// Node slice via the decoder's SkillsByID accessor.
type Skill struct {
	Node
	Assets []Asset
}

// Warning is a non-fatal observation from the decoder. The CLI surfaces
// warnings in the sync report; they do not fail decode.
type Warning struct {
	Code    string
	Message string

	// Provenance points to the offending file when one is known. Zero
	// value when the warning is global (e.g., "AGENTS.md missing").
	Provenance Provenance
}

// Sentinel errors. Callers branch with errors.Is.
var (
	// ErrUnrecognizedFile is returned for files inside an IR-owned
	// directory whose extension does not match the kind (e.g., rules/foo.txt).
	ErrUnrecognizedFile = errors.New("ir: unrecognized file in IR-owned directory")

	// ErrInvalidID is returned when a node id does not match the
	// idPattern regex.
	ErrInvalidID = errors.New("ir: node id does not match required pattern")

	// ErrDuplicateID is returned when two nodes of the same kind share an id.
	ErrDuplicateID = errors.New("ir: duplicate node id within kind")

	// ErrUnknownTarget is returned when a frontmatter targets: entry names
	// an adapter not in the registered set.
	ErrUnknownTarget = errors.New("ir: targets references unknown adapter")

	// ErrFrontmatterParse is returned when the frontmatter block is
	// malformed (no closing ---, invalid YAML, etc.).
	ErrFrontmatterParse = errors.New("ir: frontmatter parse failed")

	// ErrUnknownFrontmatterField is returned when frontmatter contains a
	// non-x- field outside the recognized set.
	ErrUnknownFrontmatterField = errors.New("ir: unknown frontmatter field")

	// ErrSkillMissingSKILL is returned for a directory under skills/ that
	// lacks a SKILL.md file.
	ErrSkillMissingSKILL = errors.New("ir: skill directory missing SKILL.md")

	// ErrEmptyAgentsMD is returned when AGENTS.md is present but
	// zero-length.
	ErrEmptyAgentsMD = errors.New("ir: AGENTS.md is present but empty")
)

// Warning codes. Spec freezes these strings; CLI / tests reference them.
const (
	WarnAgentsMDMissing      = "agents-md-missing"
	WarnSkillAssetUnreadable = "skill-asset-unreadable"
)

// idPattern is the v1 id grammar: leading alphanumeric, then up to 63 of
// [a-z0-9-_]. Total length 1..64.
var idPattern = regexp.MustCompile(`\A[a-z0-9][a-z0-9_-]{0,63}\z`)

// IsValidID reports whether id matches the v1 grammar.
func IsValidID(id string) bool {
	return idPattern.MatchString(id)
}

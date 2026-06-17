// Package coverage reports when a scope emits a node kind that a target tool
// will not read natively at that scope's hierarchy level.
//
// agent-sync resolves precedence by emitting each scope to its own filesystem
// location and letting the target tool's native config hierarchy apply it
// (see the hierarchy-aware-manifests design). That only works as far as each
// tool actually reads a given kind at a given level. This package encodes the
// known native-read behavior per target and flags the gaps so users are not
// silently surprised by emitted content that never takes effect.
//
// The native-support table is keyed by target NAME and is static: a read-only
// analyzer cannot run an adapter Initialize handshake to learn declared
// outputs, and external adapters cannot be queried at all. Unknown targets and
// unknown kinds default to native (no false warnings). The directory-level
// entries are the project's documented assumptions about each tool's nested-
// read behavior; correct them here if a tool's behavior is verified to differ.
package coverage

import (
	"sort"

	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ir"
)

// Warning is one (target, kind, level) combination that will be emitted but
// not read natively by the target tool at that level.
type Warning struct {
	Target string
	Kind   ir.Kind
	Level  hierarchy.Level
	Detail string
}

// nativeAtDirectory[target] is the set of kinds the target reads natively from
// a NESTED directory. A kind absent from a target's set is non-native at the
// directory level and warns. project/user levels are always native (the tool
// reads its root config), so they are not represented here.
//
// Documented assumptions (verify against current tool behavior, correct here):
//   - claude reads nested CLAUDE.md (agents-md); it does NOT read rules,
//     commands, skills, or mcp entries from nested .claude/ directories.
//   - codex walks nested AGENTS.md (agents-md); nothing else nested.
//   - cursor reads nested .cursor/rules (rule); nothing else nested.
var nativeAtDirectory = map[string]map[ir.Kind]bool{
	"claude": {ir.KindAgentsMD: true},
	"codex":  {ir.KindAgentsMD: true},
	"cursor": {ir.KindRule: true},
}

// known reports whether we have a native-support table for target. Unknown
// targets default to fully native (no warnings).
func known(target string) bool {
	_, ok := nativeAtDirectory[target]
	return ok
}

// Analyze returns the coverage warnings for emitting the given kinds to the
// given targets at the given level. Results are deterministically ordered by
// target then kind. project and user levels never warn; only the directory
// level (nested scopes) can produce gaps. Targets with no table never warn.
func Analyze(level hierarchy.Level, kinds []ir.Kind, targets []string) []Warning {
	if level != hierarchy.LevelDirectory {
		return nil
	}
	var out []Warning
	for _, target := range targets {
		if !known(target) {
			continue
		}
		nativeKinds := nativeAtDirectory[target]
		for _, k := range kinds {
			if nativeKinds[k] {
				continue
			}
			out = append(out, Warning{
				Target: target,
				Kind:   k,
				Level:  level,
				Detail: target + " does not read " + string(k) + " from a nested directory; this will not take effect until per-tool runtime mapping is added",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

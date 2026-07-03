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
// unknown kinds default to native (no false warnings). The directory- and
// user-level entries are the project's documented assumptions about each tool's
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
// directory level and warns. The project level is always native (the tool reads
// its workspace-root config), so it is not represented here.
//
// Documented assumptions (verify against current tool behavior, correct here):
//   - claude reads nested CLAUDE.md (agents-md); it does NOT read rules,
//     commands, skills, or mcp entries from nested .claude/ directories.
//   - codex walks nested AGENTS.md (agents-md); nothing else nested.
//   - cursor reads nested .cursor/rules (rule); nothing else nested.
//   - antigravity walks nested GEMINI.md/AGENTS.md (agents-md); it does NOT read
//     rules, workflows/commands, skills, or mcp entries from nested .agent/ or
//     .agents/ directories.
var nativeAtDirectory = map[string]map[ir.Kind]bool{
	"claude":      {ir.KindAgentsMD: true},
	"codex":       {ir.KindAgentsMD: true},
	"cursor":      {ir.KindRule: true},
	"antigravity": {ir.KindAgentsMD: true},
}

// nonNativeAtUser[target] is the set of kinds the target does NOT read from any
// file-addressable user-global (home) location, so emitting them at user scope
// is inert. This is an inverted table from nativeAtDirectory: only targets with
// a user-scope gap appear, and an absent target/kind ⇒ native at user scope (no
// warning). Most tools read every supported kind from their user-global config
// (the user scope root is $HOME, so e.g. .codex/config.toml resolves to
// ~/.codex/config.toml), so they have no entry.
//
// Documented assumptions (verified against official docs 2026-06-30):
//   - cursor has no file-addressable user-global home for rules (User Rules
//     live in app settings / cloud, not a writable file) or AGENTS.md; only
//     ~/.cursor/mcp.json is file-addressable. So rule and agents-md are inert
//     at user scope.
//   - claude (scope-aware paths target ~/.claude/...) and codex (agents-md
//     remaps to ~/.codex/AGENTS.md; mcp + skills already resolve under $HOME)
//     read every supported kind from a user-global location → no entry.
//   - antigravity (agents-md → ~/.gemini/GEMINI.md; mcp → ~/.gemini/config/…;
//     skills → ~/.gemini/skills) reads those three from a user-global location,
//     but has NO user-global home for rules (.agent/rules folds into
//     ~/.gemini/GEMINI.md) or workflows/commands (global workflows live at a
//     different, untargeted path), so rule and command are inert at user scope.
var nonNativeAtUser = map[string]map[ir.Kind]bool{
	"cursor":      {ir.KindRule: true, ir.KindAgentsMD: true},
	"antigravity": {ir.KindRule: true, ir.KindCommand: true},
}

// known reports whether we have a native-support table for target. Unknown
// targets default to fully native (no warnings).
func known(target string) bool {
	_, ok := nativeAtDirectory[target]
	return ok
}

// Analyze returns the coverage warnings for emitting the given kinds to the
// given targets at the given level. Results are deterministically ordered by
// target then kind. The project level never warns (every tool reads its
// workspace-root config); the directory level warns for kinds not read from a
// nested dir, and the user level warns for kinds with no file-addressable
// user-global home. Targets with no table never warn.
func Analyze(level hierarchy.Level, kinds []ir.Kind, targets []string) []Warning {
	var out []Warning
	switch level {
	case hierarchy.LevelDirectory:
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
	case hierarchy.LevelUser:
		for _, target := range targets {
			gap := nonNativeAtUser[target]
			if gap == nil {
				continue
			}
			for _, k := range kinds {
				if !gap[k] {
					continue
				}
				out = append(out, Warning{
					Target: target,
					Kind:   k,
					Level:  level,
					Detail: target + " has no user-global location for " + string(k) + "; emitted content is inert at user scope (sync it at project scope instead)",
				})
			}
		}
	default:
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

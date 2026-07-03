package ir

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/agent-sync/agent-sync/internal/git"
)

// DecodeOptions configures the decoder.
type DecodeOptions struct {
	// KnownTargets, when non-nil, gates frontmatter `targets:` values: any
	// entry not in the set is rejected with ErrUnknownTarget. When nil,
	// the decoder accepts any string in `targets:` (useful for tests and
	// for the v1 case where the adapter registry isn't wired yet).
	KnownTargets map[string]struct{}
}

// rootAgentsMDOverlay maps root-level overlay filenames to their target
// adapter scope. AGENTS.md itself is unscoped (empty target slice).
// GEMINI.md scopes to antigravity, not gemini: Google retired Gemini CLI
// (2026-06-18) in favor of Antigravity CLI, which still reads GEMINI.md
// as its tool-specific overlay, and no gemini adapter ever shipped.
var rootAgentsMDOverlay = map[string]string{
	"CLAUDE.md": "claude",
	"GEMINI.md": "antigravity",
}

// Decode walks the canonical-repo tree at commitSHA and returns the IR
// produced from it. See docs/spec/ir-v1.md for the contract.
//
// The decoder is deterministic: same commit SHA → byte-identical IR
// (modulo Go map iteration in user-visible fields, which we explicitly
// sort).
func Decode(src SourceTree, ref string, opts DecodeOptions) ([]Node, []Warning, error) {
	entries, err := src.ReadTree(ref)
	if err != nil {
		return nil, nil, fmt.Errorf("ir: read tree: %w", err)
	}

	var (
		nodes    []Node
		warnings []Warning
		// Track which skill directories we've seen and whether each has a
		// SKILL.md, so we can emit ErrSkillMissingSKILL for orphan dirs.
		skillDirs   = map[string]bool{} // dir -> hasSKILL
		hasAgentsMD = false
		seenIDs     = map[Kind]map[string]string{} // kind -> id -> first path
	)

	markSeen := func(k Kind, id, path string) error {
		if _, ok := seenIDs[k]; !ok {
			seenIDs[k] = map[string]string{}
		}
		if prev, dup := seenIDs[k][id]; dup {
			return fmt.Errorf("%w: kind=%s id=%s (first at %q, again at %q)",
				ErrDuplicateID, k, id, prev, path)
		}
		seenIDs[k][id] = path
		return nil
	}

	for _, e := range entries {
		// Skip directory entries — TreeEntry covers regular files; sub-
		// directories surface as their own entries with file-shaped paths.
		if isDir(e.Mode) {
			continue
		}

		p := e.Path
		switch {
		case p == "AGENTS.md":
			node, err := buildAgentsMD(src, ref, e, "" /* no overlay scope */)
			if err != nil {
				return nil, nil, err
			}
			if err := markSeen(KindAgentsMD, node.ID, p); err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, node)
			hasAgentsMD = true

		case isOverlayPath(p):
			node, err := buildAgentsMD(src, ref, e, rootAgentsMDOverlay[p])
			if err != nil {
				return nil, nil, err
			}
			if err := markSeen(KindAgentsMD, node.ID, p); err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, node)
			// An overlay (CLAUDE.md / GEMINI.md) counts as an
			// agents-md file for the purposes of WarnAgentsMDMissing:
			// the warning fires only when neither canonical nor any
			// overlay exists.
			hasAgentsMD = true

		case strings.HasPrefix(p, "rules/"):
			node, err := buildSimpleNode(src, ref, e, KindRule, "rules/", ".md")
			if err != nil {
				return nil, nil, err
			}
			if err := markSeen(KindRule, node.ID, p); err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, node)

		case strings.HasPrefix(p, "skills/"):
			// Path shape: skills/<id>/SKILL.md or skills/<id>/<asset>.
			// Bare skills/<file> (no <id>/ subdir) is an error.
			rel := strings.TrimPrefix(p, "skills/")
			parts := strings.SplitN(rel, "/", 2)
			if len(parts) < 2 {
				return nil, nil, fmt.Errorf("%w: stray file under skills/: %q",
					ErrUnrecognizedFile, p)
			}
			id := parts[0]
			leaf := parts[1]
			dir := "skills/" + id
			// Ensure the directory is seen at least once.
			if _, ok := skillDirs[dir]; !ok {
				skillDirs[dir] = false
			}
			if leaf == "SKILL.md" {
				if !IsValidID(id) {
					return nil, nil, fmt.Errorf("%w: skill id %q (from %q)", ErrInvalidID, id, p)
				}
				node, err := buildSkillNode(src, ref, e, id)
				if err != nil {
					return nil, nil, err
				}
				if err := markSeen(KindSkill, node.ID, p); err != nil {
					return nil, nil, err
				}
				nodes = append(nodes, node)
				skillDirs[dir] = true
			}
			// Non-SKILL.md files inside skills/<id>/ are assets, surfaced
			// via SkillsByID. They do not become Nodes themselves.

		case strings.HasPrefix(p, "commands/"):
			node, err := buildSimpleNode(src, ref, e, KindCommand, "commands/", ".md")
			if err != nil {
				return nil, nil, err
			}
			if err := markSeen(KindCommand, node.ID, p); err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, node)

		case strings.HasPrefix(p, "mcp/"):
			node, err := buildSimpleNode(src, ref, e, KindMCPServerEntry, "mcp/", ".json")
			if err != nil {
				return nil, nil, err
			}
			if err := markSeen(KindMCPServerEntry, node.ID, p); err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, node)

		case strings.HasPrefix(p, "plugins/"):
			node, err := buildSimpleNode(src, ref, e, KindPluginReference, "plugins/", ".toml")
			if err != nil {
				return nil, nil, err
			}
			if err := markSeen(KindPluginReference, node.ID, p); err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, node)

		default:
			// Files outside IR-owned directories are ignored. README,
			// LICENSE, etc. all live here harmlessly.
			continue
		}
	}

	// Validate frontmatter targets against the registry, if provided.
	for _, n := range nodes {
		if err := validateTargets(n.Targets, opts.KnownTargets); err != nil {
			return nil, nil, fmt.Errorf("%w (node %q at %q)", err, n.ID, n.Provenance.Path)
		}
	}

	// Skill orphan-dir check: every dir under skills/ must have SKILL.md.
	// Iterate over sorted keys so that, when multiple skill dirs are
	// orphaned, the error names the lexicographically-first dir
	// deterministically — Go's randomized map iteration would otherwise
	// pick a different one across runs.
	orphanDirs := make([]string, 0, len(skillDirs))
	for dir := range skillDirs {
		orphanDirs = append(orphanDirs, dir)
	}
	sort.Strings(orphanDirs)
	for _, dir := range orphanDirs {
		if !skillDirs[dir] {
			return nil, nil, fmt.Errorf("%w: %s", ErrSkillMissingSKILL, dir)
		}
	}

	// AGENTS.md missing → warning (not error). Only emit if no
	// agents-md file (canonical or overlay) was found at all and the
	// repo has any other IR content; greenfield repos with nothing in
	// them get the warning too — that's the spec's intent.
	if !hasAgentsMD {
		warnings = append(warnings, Warning{
			Code:    WarnAgentsMDMissing,
			Message: "AGENTS.md is not present in the canonical repo",
		})
	}

	sortNodes(nodes)
	return nodes, warnings, nil
}

// SkillsByID re-walks the tree to gather skill assets and returns a map
// keyed by skill ID. Each Skill includes its base Node plus all files
// inside `skills/<id>/` other than SKILL.md as Asset entries.
//
// Returns an empty map (not nil) when no skills are present.
//
// The second return value carries non-fatal warnings — currently
// WarnSkillAssetUnreadable when an asset blob fails to load. The asset
// is omitted from the bundle (best-effort), and the warning lets
// callers surface the failure instead of swallowing it silently.
func SkillsByID(nodes []Node, src SourceTree, ref string) (map[string]Skill, []Warning) {
	var warnings []Warning
	out := map[string]Skill{}
	// Index skill nodes for fast lookup.
	skills := map[string]Node{}
	for _, n := range nodes {
		if n.Kind == KindSkill {
			skills[n.ID] = n
		}
	}
	if len(skills) == 0 {
		return out, warnings
	}

	entries, err := src.ReadTree(ref)
	if err != nil {
		return out, warnings
	}

	// Group asset entries by skill id.
	byID := map[string][]Asset{}
	for _, e := range entries {
		if isDir(e.Mode) {
			continue
		}
		if !strings.HasPrefix(e.Path, "skills/") {
			continue
		}
		rel := strings.TrimPrefix(e.Path, "skills/")
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) < 2 {
			continue
		}
		id := parts[0]
		leaf := parts[1]
		if leaf == "SKILL.md" {
			continue
		}
		if _, ok := skills[id]; !ok {
			continue
		}
		content, err := src.BlobContent(ref, e.Path)
		if err != nil {
			// Best-effort: skip the asset rather than failing the
			// whole skills lookup, but surface the failure as a
			// warning so callers can report it instead of
			// silently dropping the file.
			warnings = append(warnings, Warning{
				Code:    WarnSkillAssetUnreadable,
				Message: fmt.Sprintf("ir: skill asset unreadable at %q: %v", e.Path, err),
				Provenance: Provenance{
					Path:    e.Path,
					BlobSHA: e.Hash,
				},
			})
			continue
		}
		byID[id] = append(byID[id], Asset{
			RelPath: leaf,
			Provenance: Provenance{
				Path:    e.Path,
				BlobSHA: e.Hash,
			},
			Content: content,
		})
	}
	// Sort assets per skill for deterministic output.
	for id, list := range byID {
		sort.Slice(list, func(i, j int) bool { return list[i].RelPath < list[j].RelPath })
		out[id] = Skill{Node: skills[id], Assets: list}
	}
	// Skills without assets still appear in the map.
	for id, n := range skills {
		if _, ok := out[id]; !ok {
			out[id] = Skill{Node: n}
		}
	}
	// Sort warnings deterministically by provenance path.
	sort.Slice(warnings, func(i, j int) bool {
		return warnings[i].Provenance.Path < warnings[j].Provenance.Path
	})
	return out, warnings
}

// --- node builders ---

// buildAgentsMD reads the file content, applies frontmatter, and produces
// an agents-md Node. When overlayTarget is non-empty, it scopes the node
// to that adapter (e.g., "claude" for CLAUDE.md).
func buildAgentsMD(src SourceTree, ref string, e git.TreeEntry, overlayTarget string) (Node, error) {
	body, err := src.BlobContent(ref, e.Path)
	if err != nil {
		return Node{}, fmt.Errorf("ir: read %q: %w", e.Path, err)
	}
	// ErrEmptyAgentsMD is canonical-AGENTS.md-specific. Empty overlay
	// files (CLAUDE.md, GEMINI.md) are acceptable: a placeholder overlay
	// in the repo carries no body but should not fail decode.
	if len(body) == 0 && e.Path == "AGENTS.md" {
		return Node{}, fmt.Errorf("%w: %s", ErrEmptyAgentsMD, e.Path)
	}
	fm, stripped, err := extractFrontmatter(e.Path, body)
	if err != nil {
		return Node{}, fmt.Errorf("%w (in %q)", err, e.Path)
	}
	id := strings.ToLower(strings.TrimSuffix(path.Base(e.Path), path.Ext(e.Path)))
	if !IsValidID(id) {
		return Node{}, fmt.Errorf("%w: derived id %q from %q", ErrInvalidID, id, e.Path)
	}
	targets := fm.Targets
	if overlayTarget != "" {
		targets = uniqAppend(targets, overlayTarget)
	}
	return Node{
		ID:          id,
		Kind:        KindAgentsMD,
		Version:     fm.Version,
		Required:    fm.Required,
		Targets:     targets,
		Description: fm.Description,
		Provenance: Provenance{
			Path:    e.Path,
			BlobSHA: e.Hash,
		},
		Body: stripped,
	}, nil
}

// buildSimpleNode handles the rules/ commands/ mcp/ plugins/ shapes:
// each file is one node, id = basename minus extension, parent dir
// determines kind.
//
// `dirPrefix` is the canonical directory (with trailing slash);
// `requiredExt` is the only allowed extension. Any other extension is
// ErrUnrecognizedFile.
func buildSimpleNode(src SourceTree, ref string, e git.TreeEntry, kind Kind, dirPrefix, requiredExt string) (Node, error) {
	if path.Ext(e.Path) != requiredExt {
		return Node{}, fmt.Errorf("%w: %s (expected %s files)", ErrUnrecognizedFile, e.Path, requiredExt)
	}
	rel := strings.TrimPrefix(e.Path, dirPrefix)
	if strings.Contains(rel, "/") {
		// Nested file under a kind-owned dir other than skills/. v1
		// rejects: rules/sub/foo.md is undefined.
		return Node{}, fmt.Errorf("%w: nested file %q (expected flat layout)", ErrUnrecognizedFile, e.Path)
	}
	id := strings.TrimSuffix(rel, requiredExt)
	if !IsValidID(id) {
		return Node{}, fmt.Errorf("%w: id %q (from %q)", ErrInvalidID, id, e.Path)
	}
	body, err := src.BlobContent(ref, e.Path)
	if err != nil {
		return Node{}, fmt.Errorf("ir: read %q: %w", e.Path, err)
	}
	fm, stripped, err := extractFrontmatter(e.Path, body)
	if err != nil {
		return Node{}, fmt.Errorf("%w (in %q)", err, e.Path)
	}
	return Node{
		ID:          id,
		Kind:        kind,
		Version:     fm.Version,
		Required:    fm.Required,
		Targets:     fm.Targets,
		Description: fm.Description,
		Provenance: Provenance{
			Path:    e.Path,
			BlobSHA: e.Hash,
		},
		Body: stripped,
	}, nil
}

// buildSkillNode reads a skills/<id>/SKILL.md and returns the base skill
// Node. Asset attachment happens later via SkillsByID.
func buildSkillNode(src SourceTree, ref string, e git.TreeEntry, id string) (Node, error) {
	body, err := src.BlobContent(ref, e.Path)
	if err != nil {
		return Node{}, fmt.Errorf("ir: read %q: %w", e.Path, err)
	}
	fm, stripped, err := extractFrontmatter(e.Path, body)
	if err != nil {
		return Node{}, fmt.Errorf("%w (in %q)", err, e.Path)
	}
	return Node{
		ID:          id,
		Kind:        KindSkill,
		Version:     fm.Version,
		Required:    fm.Required,
		Targets:     fm.Targets,
		Description: fm.Description,
		Provenance: Provenance{
			Path:    e.Path,
			BlobSHA: e.Hash,
		},
		Body: stripped,
	}, nil
}

// --- small helpers ---

// isDir reports whether mode is a tree (directory) entry. git tree mode
// 040000 == 0o040000 in the TreeEntry uint32.
func isDir(mode uint32) bool {
	return mode == 0o040000
}

// isOverlayPath reports whether p is one of the recognized AGENTS.md
// overlay names at the canonical-repo root.
func isOverlayPath(p string) bool {
	_, ok := rootAgentsMDOverlay[p]
	return ok
}

// uniqAppend appends s to existing if not already present, preserving
// order. Used to merge overlay-target hints with frontmatter targets.
func uniqAppend(existing []string, s string) []string {
	for _, e := range existing {
		if e == s {
			return existing
		}
	}
	return append(existing, s)
}

// sortNodes orders nodes deterministically by (Kind, ID). Sentinel for
// determinism invariant tests.
func sortNodes(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind < nodes[j].Kind
		}
		return nodes[i].ID < nodes[j].ID
	})
}

// Sanity check: ensure errors.Is is reachable from this package so the
// linter doesn't strip the import after refactors.
var _ = errors.Is

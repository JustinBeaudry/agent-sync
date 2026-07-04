package cursor

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/agent-sync/agent-sync/internal/adapter/bundled/skillmeta"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	// skillsParent is the shared cross-tool skills directory Cursor scans. NOT
	// .cursor/skills — Cursor reads .agents/skills (and ~/.agents/skills at user
	// scope) as well as its own .cursor/skills; agent-sync targets the shared
	// .agents/skills tree, the same convention codex/pi/antigravity emit to, so
	// one emitted skill tree serves every tool. The relative path resolves
	// correctly at user scope (~/.agents/skills/). Cross-adapter co-ownership of a
	// leaf is made safe by the engine's union-aware drift/orphan checks (ADV-1).
	skillsParent   = ".agents/skills"
	skillPrefix    = "agent-sync-"
	skillEntryFile = "SKILL.md"
)

// emitSkill maps one skill node to:
//   - mkdir(.agents/skills/agent-sync-<id>)
//   - write_file(.agents/skills/agent-sync-<id>/SKILL.md)
//   - write_file(.agents/skills/agent-sync-<id>/<asset.RelPath>) for each asset
//
// Byte-for-byte identical to the codex/pi/antigravity adapters' emitSkill (same
// managed header via renderManagedHeader, same skillmeta frontmatter, same
// layout), so when multiple adapters emit the same skill to the shared
// .agents/skills/ tree the SKILL.md bytes match and the second swap is a content
// no-op (ADV-1 co-ownership; the engine fail-closes on byte divergence).
func emitSkill(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	skillDir := skillsParent + "/" + skillPrefix + node.ID
	emitted.add(adapterkit.OpMkdir{Path: skillDir, Mode: 0o755})

	skillPath := skillDir + "/" + skillEntryFile
	if err := state.recordWritePath(skillPath); err != nil {
		return err
	}
	wf, err := adapterkit.NewOpWriteFile(skillPath, 0o644, skillFileContent(state, node, body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)

	// Warning-plus-emit: a description-less canonical skill still emits
	// (skillFileContent substitutes the deterministic fallback) — never
	// warning-only, which would trip the runtime's capability-lie gate — but the
	// gap is surfaced as a degraded warning so authors know their skill shows
	// placeholder text in tool UIs.
	if node.Description == "" {
		emitted.add(adapterkit.OpWarning{
			ConceptID: node.ID,
			Status:    adapterkit.WarningStatusDegraded,
			Note:      "skill has no description: frontmatter — emitted with a placeholder; author one in the canonical SKILL.md",
		})
	}

	assets := slices.Clone(node.Assets)
	slices.SortFunc(assets, func(a, b irAsset) int { return cmp.Compare(a.RelPath, b.RelPath) })

	// seenRelPaths tracks emitted rel_paths within this skill so an asset can't
	// collide with an ancestor/descendant of another (e.g. "docs" + "docs/x.md"):
	// individually valid but unrealizable (a file and a directory can't share a
	// path). recordWritePath only catches EXACT duplicates, so fail closed on
	// prefix collisions here. Seeded with the reserved SKILL.md entrypoint.
	seenRelPaths := map[string]struct{}{skillEntryFile: {}}

	for _, a := range assets {
		if err := validateAssetRelPath(node.ID, a.RelPath); err != nil {
			return err
		}
		if err := rejectAssetPathConflict(node.ID, a.RelPath, seenRelPaths); err != nil {
			return err
		}
		assetPath := skillDir + "/" + a.RelPath
		if err := state.recordWritePath(assetPath); err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("cursor: skill %q asset %q collides with another emitted path: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		assetBody, err := decodeBodyOrPassthrough(a.Content)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("cursor: skill %q asset %q body: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		op, err := adapterkit.NewOpWriteFile(assetPath, 0o644, assetBody)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("cursor: skill %q asset %q: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		emitted.add(op)
	}
	return nil
}

// validateAssetRelPath rejects RelPath shapes that would let a skill asset
// escape its own folder, target a directory rather than a file, or collide with
// the reserved SKILL.md entrypoint.
func validateAssetRelPath(skillID, relPath string) error {
	if relPath == "" {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("cursor: skill %q has asset with empty rel_path", skillID),
		}
	}
	if strings.HasPrefix(relPath, "/") || strings.ContainsRune(relPath, '\\') {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("cursor: skill %q asset %q must be a forward-slash workspace-relative path", skillID, relPath),
		}
	}
	cleaned := path.Clean(relPath)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("cursor: skill %q asset %q escapes skill folder via ..", skillID, relPath),
		}
	}
	if cleaned != relPath {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("cursor: skill %q asset %q is not in canonical form (cleaned: %q)", skillID, relPath, cleaned),
		}
	}
	if cleaned == "." {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("cursor: skill %q asset rel_path must name a file, not the skill directory itself (got %q)", skillID, relPath),
		}
	}
	if cleaned == skillEntryFile {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("cursor: skill %q asset rel_path %q collides with the reserved SKILL.md entrypoint", skillID, relPath),
		}
	}
	return nil
}

// rejectAssetPathConflict fails closed when relPath is an ancestor or descendant
// of an already-emitted rel_path in the same skill. Assumes relPath is already
// canonical (validateAssetRelPath ran first).
func rejectAssetPathConflict(skillID, relPath string, seen map[string]struct{}) error {
	for ancestor := path.Dir(relPath); ancestor != "."; ancestor = path.Dir(ancestor) {
		if _, ok := seen[ancestor]; ok {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("cursor: skill %q asset %q collides with emitted path %q", skillID, relPath, ancestor),
			}
		}
	}
	prefix := relPath + "/"
	for existing := range seen {
		if strings.HasPrefix(existing, prefix) {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("cursor: skill %q asset %q collides with emitted path %q", skillID, relPath, existing),
			}
		}
	}
	seen[relPath] = struct{}{}
	return nil
}

// sourceForNode resolves the provenance identity for a node: the per-node
// override (composed nodes) when present, else the session.
func sourceForNode(state *emitState, node irNode) (url, commit string) {
	if node.SourceURL != "" {
		return node.SourceURL, node.SourceCommit
	}
	return state.sourceURL, state.sourceCommit
}

// skillFileContent renders an emitted SKILL.md: YAML frontmatter at byte 0
// (Cursor and other Agent-Skills consumers parse it only there), the managed
// header below it, then the body. Unauthored descriptions get the deterministic
// skillmeta fallback; the degraded OpWarning makes the gap visible. Byte-identical
// to codex/pi/antigravity for shared-tree parity.
func skillFileContent(state *emitState, node irNode, body []byte) []byte {
	url, commit := sourceForNode(state, node)
	desc := node.Description
	if desc == "" {
		desc = skillmeta.FallbackDescription(url)
	}
	fm := skillmeta.Frontmatter(skillPrefix+node.ID, desc)
	header := renderManagedHeader(url, commit)
	out := make([]byte, 0, len(fm)+len(header)+len(body))
	out = append(out, fm...)
	out = append(out, header...)
	out = append(out, body...)
	return out
}

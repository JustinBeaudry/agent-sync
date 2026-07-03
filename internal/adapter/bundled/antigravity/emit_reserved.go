package antigravity

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
	// rulesSubdir / commandsSubdir are the Antigravity-exclusive .agent/ tree
	// (SINGULAR — Windsurf lineage). Antigravity "workflows" are slash-triggered
	// saved prompts, the closest native shape to an agent-sync command.
	rulesSubdir    = ".agent/rules/agent-sync"
	commandsSubdir = ".agent/workflows/agent-sync"

	// skillsParent is the shared cross-tool skills directory (PLURAL .agents),
	// scanned by Antigravity and co-owned with codex/pi. userSkillsParent is
	// Antigravity's user-global skills directory (~/.gemini/skills), NOT
	// ~/.agents/skills — a genuine remap at user scope. Both resolve via
	// resolvePathSet; emitSkill reads state.paths.skillsParent.
	skillsParent     = ".agents/skills"
	userSkillsParent = ".gemini/skills"

	skillPrefix    = "agent-sync-"
	skillEntryFile = "SKILL.md"
)

// emitRule maps one rule node to:
//   - mkdir(.agent/rules/agent-sync)                  (deduped per-emit)
//   - write_file(.agent/rules/agent-sync/README.md)   (deduped per-emit)
//   - write_file(.agent/rules/agent-sync/<id>.md)
//
// Rule of Three: emitRule and emitCommand are structurally identical; only the
// subdir constant varies. This is the second reserved-subdir kind in this
// adapter — collapse to a shared helper if a third appears.
func emitRule(emitted *emittedOps, node irNode, state *emitState) error {
	return emitReservedSubdirNode(emitted, node, state, rulesSubdir)
}

// emitCommand mirrors emitRule under .agent/workflows/agent-sync.
func emitCommand(emitted *emittedOps, node irNode, state *emitState) error {
	return emitReservedSubdirNode(emitted, node, state, commandsSubdir)
}

// emitReservedSubdirNode writes a markdown node into an agent-sync-owned
// reserved subdir: the per-emit mkdir + README pair, then the node file with the
// managed header prepended.
func emitReservedSubdirNode(emitted *emittedOps, node irNode, state *emitState, subdir string) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}
	if err := ensureSubdir(emitted, subdir, state); err != nil {
		return err
	}
	filePath := subdir + "/" + node.ID + ".md"
	if err := state.recordWritePath(filePath); err != nil {
		return err
	}
	wf, err := adapterkit.NewOpWriteFile(filePath, 0o644, prependHeader(state, node, body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)
	return nil
}

// emitSkill maps one skill node to:
//   - mkdir(<skillsParent>/agent-sync-<id>)
//   - write_file(<skillsParent>/agent-sync-<id>/SKILL.md)
//   - write_file(<skillsParent>/agent-sync-<id>/<asset.RelPath>) for each asset
//
// skillsParent is scope-resolved (.agents/skills at project scope, .gemini/skills
// at user scope). The SKILL.md bytes are byte-identical to the codex/pi adapters
// (same managed header + frontmatter), so a skill co-emitted to the shared
// .agents/skills tree produces matching bytes and the second swap is a content
// no-op (ADV-1).
//
// Asset RelPaths are validated to stay inside the skill's own folder and to not
// collide with the reserved SKILL.md entrypoint, a sibling asset, or an
// ancestor/descendant path — the runtime's declared-outputs gate does not
// enforce per-skill containment, so the adapter is the only line of defense.
func emitSkill(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	skillDir := state.paths.skillsParent + "/" + skillPrefix + node.ID
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
	// those are individually valid but unrealizable (a file and a directory can't
	// share a path). recordWritePath only catches EXACT duplicates, so fail closed
	// on prefix collisions here. Seeded with the reserved SKILL.md entrypoint.
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
				Message: fmt.Sprintf("antigravity: skill %q asset %q collides with another emitted path", node.ID, a.RelPath),
			}
		}
		assetBody, err := decodeBodyOrPassthrough(a.Content)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("antigravity: skill %q asset %q body: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		op, err := adapterkit.NewOpWriteFile(assetPath, 0o644, assetBody)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("antigravity: skill %q asset %q: %s", node.ID, a.RelPath, err.Error()),
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
			Message: fmt.Sprintf("antigravity: skill %q has asset with empty rel_path", skillID),
		}
	}
	if strings.HasPrefix(relPath, "/") || strings.ContainsRune(relPath, '\\') {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("antigravity: skill %q asset %q must be a forward-slash workspace-relative path", skillID, relPath),
		}
	}
	cleaned := path.Clean(relPath)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("antigravity: skill %q asset %q escapes skill folder via ..", skillID, relPath),
		}
	}
	if cleaned != relPath {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("antigravity: skill %q asset %q is not in canonical form (cleaned: %q)", skillID, relPath, cleaned),
		}
	}
	if cleaned == "." {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("antigravity: skill %q asset rel_path must name a file, not the skill directory itself (got %q)", skillID, relPath),
		}
	}
	if cleaned == skillEntryFile {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("antigravity: skill %q asset rel_path %q collides with the reserved SKILL.md entrypoint", skillID, relPath),
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
				Message: fmt.Sprintf("antigravity: skill %q asset %q collides with emitted path %q", skillID, relPath, ancestor),
			}
		}
	}
	prefix := relPath + "/"
	for existing := range seen {
		if strings.HasPrefix(existing, prefix) {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("antigravity: skill %q asset %q collides with emitted path %q", skillID, relPath, existing),
			}
		}
	}
	seen[relPath] = struct{}{}
	return nil
}

// ensureSubdir adds the per-emit mkdir + README pair for a reserved subdirectory
// once per emit call. Subsequent calls are no-ops.
//
// Returns InvalidParams if a node ID has already taken the README path inside
// this subdir (e.g. a rule node literally named "README").
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
		// readmeForSubdir returns a small fixed body; the only way NewOpWriteFile
		// can fail is the payload-too-large guard, unreachable here. Panic
		// surfaces the bug.
		panic(fmt.Sprintf("antigravity: building README for %s: %v", subdir, err))
	}
	emitted.add(wf)
	return nil
}

// prependHeader inserts the managed-file header before the body. The header
// carries per-node provenance when the node has a source override (composed
// nodes); otherwise the session-level source.
func prependHeader(state *emitState, node irNode, body []byte) []byte {
	url, commit := sourceForNode(state, node)
	header := renderManagedHeader(url, commit)
	out := make([]byte, 0, len(header)+len(body))
	out = append(out, header...)
	out = append(out, body...)
	return out
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
// (Antigravity and Agent Skills consumers parse it only there), the managed
// header below it, then the body. Unauthored descriptions get the deterministic
// skillmeta fallback; the degraded OpWarning makes the gap visible. Skills are
// the only header-second kind. Byte-identical to codex/pi for shared-tree parity.
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

package codex

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	// skillsParent is the shared cross-tool skills directory Codex scans from
	// cwd up to the repository root. NOT .codex/skills — Codex reads
	// .agents/skills (validated June 2026), a convention shared with other
	// agents (e.g. pi), so one emitted skill tree serves multiple tools.
	skillsParent   = ".agents/skills"
	skillPrefix    = "agent-sync-"
	skillEntryFile = "SKILL.md"
)

// emitSkill maps one skill node to:
//   - mkdir(.agents/skills/agent-sync-<id>)
//   - write_file(.agents/skills/agent-sync-<id>/SKILL.md)
//   - write_file(.agents/skills/agent-sync-<id>/<asset.RelPath>) for each asset
//
// Asset RelPaths are validated to stay inside the skill's own folder: the
// runtime's declared-outputs gate accepts any cleaned path inside
// .agents/skills, so per-skill containment must be enforced here. The skill
// folder gets no README: a per-skill README would clash with skill-discovery
// semantics, and the parent .agents/skills can hold user / other-tool skills
// we don't own (the agent-sync- prefix is what isolates ours).
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
	wf, err := adapterkit.NewOpWriteFile(skillPath, 0o644, prependHeader(body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)

	assets := slices.Clone(node.Assets)
	slices.SortFunc(assets, func(a, b irAsset) int { return cmp.Compare(a.RelPath, b.RelPath) })

	for _, a := range assets {
		if err := validateAssetRelPath(node.ID, a.RelPath); err != nil {
			return err
		}
		assetPath := skillDir + "/" + a.RelPath
		if err := state.recordWritePath(assetPath); err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("codex: skill %q asset %q collides with another emitted path", node.ID, a.RelPath),
			}
		}
		assetBody, err := decodeBodyOrPassthrough(a.Content)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("codex: skill %q asset %q body: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		op, err := adapterkit.NewOpWriteFile(assetPath, 0o644, assetBody)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("codex: skill %q asset %q: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		emitted.add(op)
	}
	return nil
}

// validateAssetRelPath rejects RelPath shapes that would let a skill asset
// escape its own folder, target a directory rather than a file, or collide
// with the reserved SKILL.md entrypoint.
func validateAssetRelPath(skillID, relPath string) error {
	if relPath == "" {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: skill %q has asset with empty rel_path", skillID),
		}
	}
	if strings.HasPrefix(relPath, "/") || strings.ContainsRune(relPath, '\\') {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: skill %q asset %q must be a forward-slash workspace-relative path", skillID, relPath),
		}
	}
	cleaned := path.Clean(relPath)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: skill %q asset %q escapes skill folder via ..", skillID, relPath),
		}
	}
	if cleaned != relPath {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: skill %q asset %q is not in canonical form (cleaned: %q)", skillID, relPath, cleaned),
		}
	}
	if cleaned == "." {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: skill %q asset rel_path must name a file, not the skill directory itself (got %q)", skillID, relPath),
		}
	}
	if cleaned == skillEntryFile {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("codex: skill %q asset rel_path %q collides with the reserved SKILL.md entrypoint", skillID, relPath),
		}
	}
	return nil
}

// prependHeader inserts the managed-file header before the body.
func prependHeader(body []byte) []byte {
	header := markdownHeader()
	out := make([]byte, 0, len(header)+len(body))
	out = append(out, header...)
	out = append(out, body...)
	return out
}

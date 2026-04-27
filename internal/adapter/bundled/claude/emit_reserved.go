package claude

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/aienvs/aienvs/pkg/adapterkit"
)

const (
	rulesSubdir    = ".claude/rules/aienvs"
	commandsSubdir = ".claude/commands/aienvs"
	skillsParent   = ".claude/skills"
	skillPrefix    = "aienvs-"
)

// emitRule maps one rule node to:
//   - mkdir(.claude/rules/aienvs)            (deduped per-emit)
//   - write_file(.claude/rules/aienvs/README.md)  (deduped per-emit)
//   - write_file(.claude/rules/aienvs/<id>.md)
//   - warning op when the body opens with `paths:` frontmatter
//     (Claude Code activation behavior is inconsistent with that
//     frontmatter as of 2026-04).
//
// Rule of Three: emitRule and emitCommand are structurally identical;
// only the subdir constant varies. Collapse to a shared
// emitReservedSubdirNode helper when the third reserved-subdirectory
// kind appears (Unit 10 cursor adds a second; Unit 11 codex would be
// the third — re-evaluate then).
func emitRule(emitted *emittedOps, node irNode, readmeEmitted map[string]bool) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	ensureSubdir(emitted, rulesSubdir, readmeEmitted)

	wf, err := adapterkit.NewOpWriteFile(rulesSubdir+"/"+node.ID+".md", 0o644, prependHeader(body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)

	if hasPathsFrontmatter(body) {
		emitted.add(adapterkit.OpWarning{
			ConceptID: node.ID,
			Status:    adapterkit.WarningStatusDegraded,
			Note:      "rule has paths: frontmatter — Claude Code activation behavior is inconsistent with that field as of 2026-04",
		})
	}
	return nil
}

// emitCommand mirrors emitRule under .claude/commands/aienvs. See
// emitRule's Rule-of-Three note before collapsing.
func emitCommand(emitted *emittedOps, node irNode, readmeEmitted map[string]bool) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	ensureSubdir(emitted, commandsSubdir, readmeEmitted)

	wf, err := adapterkit.NewOpWriteFile(commandsSubdir+"/"+node.ID+".md", 0o644, prependHeader(body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)
	return nil
}

// emitSkill maps one skill node to:
//   - mkdir(.claude/skills/aienvs-<id>)
//   - write_file(.claude/skills/aienvs-<id>/SKILL.md)
//   - write_file(.claude/skills/aienvs-<id>/<asset.RelPath>) for each asset
//
// Asset RelPaths are validated to stay inside the skill's own folder.
// Without this check a malicious or buggy IR could escape into a
// sibling skill folder via `../aienvs-victim/SKILL.md` — the runtime's
// declared-outputs gate accepts any cleaned path inside .claude/skills,
// so per-skill containment must be enforced here.
//
// The skill folder gets no README.md: a per-skill README would clash
// with skill-discovery semantics, and the parent .claude/skills can
// hold user skills that we don't own.
func emitSkill(emitted *emittedOps, node irNode) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	skillDir := skillsParent + "/" + skillPrefix + node.ID
	emitted.add(adapterkit.OpMkdir{Path: skillDir, Mode: 0o755})

	wf, err := adapterkit.NewOpWriteFile(skillDir+"/SKILL.md", 0o644, prependHeader(body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)

	assets := append([]irAsset(nil), node.Assets...)
	sort.Slice(assets, func(i, j int) bool { return assets[i].RelPath < assets[j].RelPath })

	for _, a := range assets {
		if err := validateAssetRelPath(node.ID, a.RelPath); err != nil {
			return err
		}
		assetBody, err := decodeBodyOrPassthrough(a.Content)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("claude: skill %q asset %q body: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		op, err := adapterkit.NewOpWriteFile(skillDir+"/"+a.RelPath, 0o644, assetBody)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("claude: skill %q asset %q: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		emitted.add(op)
	}
	return nil
}

// validateAssetRelPath rejects RelPath shapes that would let a skill
// asset escape its own folder. Defense in depth: the runtime's
// declared-outputs gate catches workspace escapes but does not
// enforce per-skill containment, so the adapter is the only line of
// defense for this class of issue.
func validateAssetRelPath(skillID, relPath string) error {
	if relPath == "" {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: skill %q has asset with empty rel_path", skillID),
		}
	}
	// Reject absolute paths (POSIX or Windows-volume) and backslash —
	// wire-protocol paths are forward-slash only.
	if strings.HasPrefix(relPath, "/") || strings.ContainsRune(relPath, '\\') {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: skill %q asset %q must be a forward-slash workspace-relative path", skillID, relPath),
		}
	}
	// path.Clean normalizes "./", duplicate "//", and "..". If the
	// cleaned form differs from the input, or starts with "..", or
	// contains a "/.." segment, the path tries to escape the skill
	// folder. Reject.
	cleaned := path.Clean(relPath)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: skill %q asset %q escapes skill folder via ..", skillID, relPath),
		}
	}
	if cleaned != relPath {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: skill %q asset %q is not in canonical form (cleaned: %q)", skillID, relPath, cleaned),
		}
	}
	return nil
}

// ensureSubdir adds the per-emit mkdir + README pair for a reserved
// subdirectory once per emit call. Subsequent calls are no-ops.
func ensureSubdir(emitted *emittedOps, subdir string, seen map[string]bool) {
	if seen[subdir] {
		return
	}
	seen[subdir] = true
	emitted.add(adapterkit.OpMkdir{Path: subdir, Mode: 0o755})
	wf, err := adapterkit.NewOpWriteFile(subdir+"/README.md", 0o644, readmeForSubdir(subdir))
	if err != nil {
		// readmeForSubdir returns a small fixed body; the only way
		// NewOpWriteFile can fail is the payload-too-large guard,
		// which is unreachable here. Panic surfaces the bug.
		panic(fmt.Sprintf("claude: building README for %s: %v", subdir, err))
	}
	emitted.add(wf)
}

// prependHeader inserts the managed-file header before the body.
func prependHeader(body []byte) []byte {
	header := markdownHeader()
	out := make([]byte, 0, len(header)+len(body))
	out = append(out, header...)
	out = append(out, body...)
	return out
}

// hasPathsFrontmatter reports whether body opens with a YAML
// frontmatter block whose first content line is `paths:`. Best-
// effort: a body with frontmatter that places `paths:` after other
// fields is missed. v1 ships the conservative check; full
// frontmatter exposure is a v1.x change to the IR.
//
// Accepts both `---\n` and `---\r\n` separators so Windows-authored
// rule files don't silently slip past the warning.
func hasPathsFrontmatter(body []byte) bool {
	rest, ok := stripFrontmatterOpener(body)
	if !ok {
		return false
	}
	// Look at the first non-blank line of the YAML block; reject if
	// the first key is anything other than `paths`.
	for {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return false
		}
		line := rest[:nl]
		trim := bytes.TrimSpace(line)
		if len(trim) == 0 {
			rest = rest[nl+1:]
			continue
		}
		return bytes.HasPrefix(trim, []byte("paths:"))
	}
}

// stripFrontmatterOpener returns the body bytes after a leading
// `---\n` or `---\r\n` separator. Returns ok=false when no opener is
// present.
func stripFrontmatterOpener(body []byte) ([]byte, bool) {
	if bytes.HasPrefix(body, []byte("---\n")) {
		return body[len("---\n"):], true
	}
	if bytes.HasPrefix(body, []byte("---\r\n")) {
		return body[len("---\r\n"):], true
	}
	return nil, false
}

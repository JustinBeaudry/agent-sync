package claude

import (
	"bytes"
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

const (
	rulesSubdir    = ".claude/rules/agent-sync"
	commandsSubdir = ".claude/commands/agent-sync"
	skillsParent   = ".claude/skills"
	skillPrefix    = "agent-sync-"
	skillEntryFile = "SKILL.md"
)

// emitRule maps one rule node to:
//   - mkdir(.claude/rules/agent-sync)            (deduped per-emit)
//   - write_file(.claude/rules/agent-sync/README.md)  (deduped per-emit)
//   - write_file(.claude/rules/agent-sync/<id>.md)
//   - warning op when the body opens with `paths:` frontmatter
//     (Claude Code activation behavior is inconsistent with that
//     frontmatter as of 2026-04).
//
// Rule of Three: emitRule and emitCommand are structurally identical;
// only the subdir constant varies. Collapse to a shared
// emitReservedSubdirNode helper when the third reserved-subdirectory
// kind appears (Unit 10 cursor adds a second; Unit 11 codex would be
// the third — re-evaluate then).
func emitRule(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	if err := ensureSubdir(emitted, rulesSubdir, state); err != nil {
		return err
	}

	rulePath := rulesSubdir + "/" + node.ID + ".md"
	if err := state.recordWritePath(rulePath); err != nil {
		return err
	}
	wf, err := adapterkit.NewOpWriteFile(rulePath, 0o644, prependHeader(state, node, body))
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

// emitCommand mirrors emitRule under .claude/commands/agent-sync. See
// emitRule's Rule-of-Three note before collapsing.
func emitCommand(emitted *emittedOps, node irNode, state *emitState) error {
	body, err := decodeBodyOrPassthrough(node.Body)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	if err := ensureSubdir(emitted, commandsSubdir, state); err != nil {
		return err
	}

	cmdPath := commandsSubdir + "/" + node.ID + ".md"
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

// emitSkill maps one skill node to:
//   - mkdir(.claude/skills/agent-sync-<id>)
//   - write_file(.claude/skills/agent-sync-<id>/SKILL.md)
//   - write_file(.claude/skills/agent-sync-<id>/<asset.RelPath>) for each asset
//
// Asset RelPaths are validated to stay inside the skill's own folder.
// Without this check a malicious or buggy IR could escape into a
// sibling skill folder via `../agent-sync-victim/SKILL.md` — the runtime's
// declared-outputs gate accepts any cleaned path inside .claude/skills,
// so per-skill containment must be enforced here.
//
// Asset RelPaths are also rejected when they collide with the
// reserved SKILL.md filename or with another asset's path: such
// collisions would silently last-write-wins at the sync layer,
// reintroducing the same overwrite class rejectDuplicateNodes guards
// against at the node level.
//
// The skill folder gets no README.md: a per-skill README would clash
// with skill-discovery semantics, and the parent .claude/skills can
// hold user skills that we don't own.
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
	wf, err := adapterkit.NewOpWriteFile(skillPath, 0o644, prependHeader(state, node, body))
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
				Message: fmt.Sprintf("claude: skill %q asset %q collides with another emitted path", node.ID, a.RelPath),
			}
		}
		assetBody, err := decodeBodyOrPassthrough(a.Content)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("claude: skill %q asset %q body: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		op, err := adapterkit.NewOpWriteFile(assetPath, 0o644, assetBody)
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
// asset escape its own folder, target a directory rather than a file,
// or collide with the reserved SKILL.md entrypoint. Defense in depth:
// the runtime's declared-outputs gate catches workspace escapes but
// does not enforce per-skill containment, so the adapter is the only
// line of defense for this class of issue.
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
	// Reject directory-only paths. path.Clean(".") == "." which would
	// produce an op path like ".claude/skills/agent-sync-x/." pointing at
	// the skill directory itself, not a file. Reject before the sync
	// engine has to handle it.
	if cleaned == "." {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: skill %q asset rel_path must name a file, not the skill directory itself (got %q)", skillID, relPath),
		}
	}
	// Reject the reserved SKILL.md entrypoint path. SKILL.md is owned
	// by the skill body emission above; an asset sharing that name
	// would silently overwrite the skill body itself.
	if cleaned == skillEntryFile {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: skill %q asset rel_path %q collides with the reserved SKILL.md entrypoint", skillID, relPath),
		}
	}
	return nil
}

// ensureSubdir adds the per-emit mkdir + README pair for a reserved
// subdirectory once per emit call. Subsequent calls are no-ops.
//
// Returns InvalidParams if a node ID has already taken the README
// path inside this subdir (e.g., a rule node literally named "README"
// — its emitted path would be .../agent-sync/README.md).
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
		// readmeForSubdir returns a small fixed body; the only way
		// NewOpWriteFile can fail is the payload-too-large guard,
		// which is unreachable here. Panic surfaces the bug.
		panic(fmt.Sprintf("claude: building README for %s: %v", subdir, err))
	}
	emitted.add(wf)
	return nil
}

// prependHeader inserts the managed-file header before the body. The
// header carries per-node provenance when the node has a source
// override (composed nodes); otherwise the session-level source.
func prependHeader(state *emitState, node irNode, body []byte) []byte {
	url, commit := state.sourceURL, state.sourceCommit
	if node.SourceURL != "" {
		url, commit = node.SourceURL, node.SourceCommit
	}
	header := renderManagedHeader(url, commit)
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

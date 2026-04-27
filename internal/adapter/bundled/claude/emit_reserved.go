package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

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
// Body bytes are decoded from the wire JSON via nodeBodyBytes; an
// IR's body field is delivered as a JSON string (for markdown kinds)
// or as raw JSON (for json/toml kinds).
func emitRule(emitted *emittedOps, node irNode, readmeEmitted map[string]bool) error {
	body, err := nodeBodyBytes(node)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	ensureSubdir(emitted, rulesSubdir, readmeEmitted)

	path := rulesSubdir + "/" + node.ID + ".md"
	wf, err := adapterkit.NewOpWriteFile(path, 0o644, prependHeader(body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	if _, err := json.Marshal(wf); err != nil {
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

// emitCommand maps one command node to:
//   - mkdir(.claude/commands/aienvs)
//   - write_file(.claude/commands/aienvs/README.md)
//   - write_file(.claude/commands/aienvs/<id>.md)
//
// Identical shape to emitRule; the only difference is the subdir.
// They are kept as separate functions for now; if a third reserved-
// subdirectory kind appears the Rule of Three says collapse.
func emitCommand(emitted *emittedOps, node irNode, readmeEmitted map[string]bool) error {
	body, err := nodeBodyBytes(node)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	ensureSubdir(emitted, commandsSubdir, readmeEmitted)

	path := commandsSubdir + "/" + node.ID + ".md"
	wf, err := adapterkit.NewOpWriteFile(path, 0o644, prependHeader(body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	if _, err := json.Marshal(wf); err != nil {
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
// The skill folder gets no README.md: the per-skill folder is its own
// scope and a README inside it would clash with skill-discovery
// semantics. The README rule applies to the parent reserved
// directory only — and the parent for skills is .claude/skills,
// which we do NOT own (it can hold user skills). So no README at all
// for skills.
//
// Asset paths are emitted relative to the skill folder, so an asset
// with RelPath "templates/foo.txt" becomes a write at
// .claude/skills/aienvs-<id>/templates/foo.txt. We do not emit
// per-asset-parent mkdirs; the sync engine creates intermediate
// directories as part of write_file execution. (Echo reference
// adapter emits at most one mkdir per emit; we mirror that.)
func emitSkill(emitted *emittedOps, node irNode) error {
	body, err := nodeBodyBytes(node)
	if err != nil {
		return wrapBodyErr(node, err)
	}

	skillDir := skillsParent + "/" + skillPrefix + node.ID
	emitted.add(adapterkit.OpMkdir{Path: skillDir, Mode: 0o755})

	skillPath := skillDir + "/SKILL.md"
	wf, err := adapterkit.NewOpWriteFile(skillPath, 0o644, prependHeader(body))
	if err != nil {
		return wrapOpErr(node, err)
	}
	if _, err := json.Marshal(wf); err != nil {
		return wrapOpErr(node, err)
	}
	emitted.add(wf)

	// Sort assets by RelPath for deterministic output ordering.
	assets := append([]irAsset(nil), node.Assets...)
	sort.Slice(assets, func(i, j int) bool { return assets[i].RelPath < assets[j].RelPath })

	for _, a := range assets {
		if a.RelPath == "" {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("claude: skill %q has asset with empty rel_path", node.ID),
			}
		}
		assetBody, err := assetBodyBytes(a)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("claude: skill %q asset %q body: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		assetPath := skillDir + "/" + a.RelPath
		op, err := adapterkit.NewOpWriteFile(assetPath, 0o644, assetBody)
		if err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("claude: skill %q asset %q: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		if _, err := json.Marshal(op); err != nil {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("claude: skill %q asset %q marshal: %s", node.ID, a.RelPath, err.Error()),
			}
		}
		emitted.add(op)
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
	readmePath := subdir + "/README.md"
	wf, err := adapterkit.NewOpWriteFile(readmePath, 0o644, readmeForSubdir(subdir))
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
func hasPathsFrontmatter(body []byte) bool {
	const sep = "---\n"
	if !bytes.HasPrefix(body, []byte(sep)) {
		return false
	}
	rest := body[len(sep):]
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

// nodeBodyBytes decodes the IR's node.Body field into raw bytes the
// adapter can use as op content. The wire shape allows two body
// encodings:
//   - JSON string (markdown bodies): `"# heading\n..."`
//   - Raw JSON value (json/toml kinds): `{"command":"node",...}`
//
// For markdown kinds (rule/command/skill/agents-md) the body is
// always a JSON string. We try string-decode first; on failure we
// pass the raw bytes through.
func nodeBodyBytes(node irNode) ([]byte, error) {
	return decodeBodyOrPassthrough(node.Body)
}

// assetBodyBytes follows the same convention as nodeBodyBytes for
// skill assets. Assets are arbitrary file content; treat as raw
// bytes after string-decoding when applicable.
func assetBodyBytes(asset irAsset) ([]byte, error) {
	return decodeBodyOrPassthrough(asset.Content)
}

func decodeBodyOrPassthrough(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	// Try string-decode first (markdown kinds).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []byte(s), nil
	}
	// Fall back to raw bytes; validate it parses as JSON so we don't
	// silently pass garbage through.
	var any any
	if err := json.Unmarshal(raw, &any); err != nil {
		return nil, fmt.Errorf("body must be a JSON string or valid JSON value: %w", err)
	}
	return raw, nil
}

func wrapBodyErr(node irNode, err error) error {
	return &adapterkit.Error{
		Code:    adapterkit.CodeInvalidParams,
		Message: fmt.Sprintf("claude: node %q (%s) body: %s", node.ID, node.Kind, err.Error()),
	}
}

func wrapOpErr(node irNode, err error) error {
	return &adapterkit.Error{
		Code:    adapterkit.CodeInvalidParams,
		Message: fmt.Sprintf("claude: node %q (%s) op: %s", node.ID, node.Kind, err.Error()),
	}
}

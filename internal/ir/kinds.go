package ir

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/goccy/go-yaml"
)

// Frontmatter is the recognized metadata block for a Node. The decoder
// extracts it from the source file and applies defaults for any field the
// author didn't set.
//
// Field defaults match the spec:
//   - Required: false
//   - Targets:  empty slice (means "all adapters")
//   - Version:  1
type Frontmatter struct {
	Required bool     `yaml:"required" json:"__aienvs_required"`
	Targets  []string `yaml:"targets"  json:"__aienvs_targets"`
	Version  int      `yaml:"version"  json:"__aienvs_version"`
}

// defaultFrontmatter returns a fresh Frontmatter with the documented
// defaults applied.
func defaultFrontmatter() Frontmatter {
	return Frontmatter{
		Required: false,
		Targets:  nil,
		Version:  1,
	}
}

// applyDefaults fills in zero-value fields with documented defaults. Used
// after parsing a partial frontmatter block.
func (fm *Frontmatter) applyDefaults() {
	if fm.Version == 0 {
		fm.Version = 1
	}
}

// extractMarkdownFrontmatter splits a markdown source into (frontmatter,
// body). When the source has no leading `---\n` block, returns
// defaultFrontmatter and the input unchanged.
//
// Frontmatter syntax: `---\n` on its own line, YAML, `---\n` on its own
// line. CR/LF tolerated. Unknown YAML fields are errors except the `x-`
// prefix (forward-compat).
func extractMarkdownFrontmatter(src []byte) (Frontmatter, []byte, error) {
	const sep = "---"

	// Quick check: source must start with `---` followed by a newline.
	if !startsWithDelimiter(src, sep) {
		fm := defaultFrontmatter()
		return fm, src, nil
	}

	// Skip the opening delimiter line.
	rest := skipFirstLine(src)

	// Find the closing `---` delimiter on its own line.
	closeIdx := findDelimiterLine(rest, sep)
	if closeIdx < 0 {
		return Frontmatter{}, nil, fmt.Errorf("%w: missing closing --- delimiter", ErrFrontmatterParse)
	}

	yamlBytes := rest[:closeIdx]
	body := skipFirstLine(rest[closeIdx:])

	fm, err := parseYAMLFrontmatter(yamlBytes)
	if err != nil {
		return Frontmatter{}, nil, err
	}
	fm.applyDefaults()
	return fm, body, nil
}

// extractJSONFrontmatter parses a JSON file, peels off any reserved
// `__aienvs_*` top-level keys, and returns a Frontmatter plus a re-emitted
// body with those keys stripped. Non-aienvs keys preserve their original
// ordering and structure (encoding/json's map ordering is sorted, which
// is what JSON consumers should already tolerate).
//
// When the body fails to parse as JSON, returns ErrFrontmatterParse — the
// canonical-repo file isn't valid JSON, period.
func extractJSONFrontmatter(src []byte) (Frontmatter, []byte, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		return Frontmatter{}, nil, fmt.Errorf("%w: empty JSON body", ErrFrontmatterParse)
	}

	// Decode as a generic map so we can pick out reserved keys without
	// imposing a schema on the rest of the document.
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return Frontmatter{}, nil, fmt.Errorf("%w: %w", ErrFrontmatterParse, err)
	}

	fm := defaultFrontmatter()

	// Reserved keys are best-effort: if the field exists but doesn't
	// unmarshal cleanly into its target type, return ErrFrontmatterParse.
	if v, ok := raw["__aienvs_required"]; ok {
		if err := json.Unmarshal(v, &fm.Required); err != nil {
			return Frontmatter{}, nil, fmt.Errorf("%w: __aienvs_required must be bool", ErrFrontmatterParse)
		}
		delete(raw, "__aienvs_required")
	}
	if v, ok := raw["__aienvs_targets"]; ok {
		if err := json.Unmarshal(v, &fm.Targets); err != nil {
			return Frontmatter{}, nil, fmt.Errorf("%w: __aienvs_targets must be []string", ErrFrontmatterParse)
		}
		delete(raw, "__aienvs_targets")
	}
	if v, ok := raw["__aienvs_version"]; ok {
		if err := json.Unmarshal(v, &fm.Version); err != nil {
			return Frontmatter{}, nil, fmt.Errorf("%w: __aienvs_version must be int", ErrFrontmatterParse)
		}
		delete(raw, "__aienvs_version")
	}
	fm.applyDefaults()

	// Re-emit the stripped body. Use deterministic key ordering so the
	// output is byte-stable across decode runs (a determinism invariant).
	body, err := marshalJSONStable(raw)
	if err != nil {
		return Frontmatter{}, nil, fmt.Errorf("%w: re-emit failed: %w", ErrFrontmatterParse, err)
	}
	return fm, body, nil
}

// extractTOMLFrontmatter is a v1 stub: it returns default frontmatter and
// the body unchanged. Reserved-key extraction in TOML is deferred per
// docs/spec/ir-v1.md "Frontmatter on non-markdown nodes".
func extractTOMLFrontmatter(src []byte) (Frontmatter, []byte, error) {
	return defaultFrontmatter(), src, nil
}

// parseYAMLFrontmatter decodes the YAML block with strict unknown-field
// detection plus an `x-` prefix allowance for forward-compat experiments.
func parseYAMLFrontmatter(src []byte) (Frontmatter, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		return defaultFrontmatter(), nil
	}
	var fm Frontmatter
	err := yaml.UnmarshalWithOptions(
		src,
		&fm,
		yaml.DisallowUnknownField(),
		yaml.AllowFieldPrefixes("x-"),
	)
	if err != nil {
		// Distinguish unknown-field errors so callers can surface
		// ErrUnknownFrontmatterField specifically.
		if isUnknownFieldErr(err) {
			return Frontmatter{}, fmt.Errorf("%w: %s", ErrUnknownFrontmatterField, yaml.FormatError(err, false, true))
		}
		return Frontmatter{}, fmt.Errorf("%w: %s", ErrFrontmatterParse, yaml.FormatError(err, false, true))
	}
	return fm, nil
}

// isUnknownFieldErr matches goccy/go-yaml's unknown-field error
// signature. The library doesn't expose a typed sentinel for this case at
// 1.19, so we fall back to a substring check on the formatted message.
// Imperfect but good enough for the test surface; if goccy adds a typed
// error in a later release we swap to errors.Is.
func isUnknownFieldErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return bytes.Contains([]byte(msg), []byte("unknown field"))
}

// startsWithDelimiter reports whether src begins with `<delim>` followed
// by `\n` or `\r\n` (CR/LF tolerant).
func startsWithDelimiter(src []byte, delim string) bool {
	d := []byte(delim)
	if !bytes.HasPrefix(src, d) {
		return false
	}
	rest := src[len(d):]
	switch {
	case bytes.HasPrefix(rest, []byte("\n")):
		return true
	case bytes.HasPrefix(rest, []byte("\r\n")):
		return true
	}
	return false
}

// findDelimiterLine returns the byte offset of the next `<delim>` on its
// own line within src, or -1 if none is found. Tolerates CR/LF.
func findDelimiterLine(src []byte, delim string) int {
	d := []byte(delim)
	offset := 0
	for offset < len(src) {
		// Locate the next occurrence at the start of a line.
		idx := bytes.Index(src[offset:], d)
		if idx < 0 {
			return -1
		}
		abs := offset + idx
		// Must be at start-of-line: either offset 0, or preceded by `\n`.
		atLineStart := abs == 0 || src[abs-1] == '\n'
		// Must be followed by `\n` or `\r\n` or EOF.
		afterDelim := abs + len(d)
		atLineEnd := afterDelim == len(src) ||
			src[afterDelim] == '\n' ||
			(afterDelim+1 <= len(src) && src[afterDelim] == '\r')
		if atLineStart && atLineEnd {
			return abs
		}
		offset = abs + 1
	}
	return -1
}

// skipFirstLine returns src with the first line (up to and including the
// first \n) removed. If there is no newline, returns an empty slice.
func skipFirstLine(src []byte) []byte {
	idx := bytes.IndexByte(src, '\n')
	if idx < 0 {
		return src[len(src):]
	}
	return src[idx+1:]
}

// marshalJSONStable serializes a map[string]json.RawMessage with sorted
// keys. encoding/json already sorts map keys, but we go via Marshal
// here for clarity and to make the determinism contract explicit.
func marshalJSONStable(m map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		// Encode the key as JSON to handle escaping.
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(m[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// kindForExt maps a file extension to its expected Kind. Returns ok=false
// for extensions outside the IR-owned vocabulary.
func kindForExt(ext string) (Kind, bool) {
	switch ext {
	case ".md":
		return KindRule, true // also covers AgentsMD/Command/Skill SKILL.md by directory
	case ".json":
		return KindMCPServerEntry, true
	case ".toml":
		return KindPluginReference, true
	}
	return "", false
}

// validateTargets ensures every entry in targets matches a known adapter
// name. The decoder receives `knownTargets` from its caller (the adapter
// registry from Unit 8). Empty targets means "all" and is always valid.
func validateTargets(targets []string, knownTargets map[string]struct{}) error {
	if len(knownTargets) == 0 {
		// Decoder was called without a target registry — accept whatever
		// the file declared. Useful for tests and for the v1 case where
		// adapters haven't been wired yet.
		return nil
	}
	for _, t := range targets {
		if _, ok := knownTargets[t]; !ok {
			return fmt.Errorf("%w: %q", ErrUnknownTarget, t)
		}
	}
	return nil
}

// extractFrontmatter is the dispatcher used by the decoder. It picks the
// right extractor based on the file's path/extension semantics:
//
//   - `.md`, `.markdown` → markdown frontmatter
//   - `.json`            → JSON reserved-key extraction
//   - `.toml`            → TOML stub (defaults only, v1)
//
// Returns ErrFrontmatterParse when the file format is recognized but the
// metadata extraction fails.
func extractFrontmatter(path string, src []byte) (Frontmatter, []byte, error) {
	switch ext(path) {
	case ".md", ".markdown":
		return extractMarkdownFrontmatter(src)
	case ".json":
		return extractJSONFrontmatter(src)
	case ".toml":
		return extractTOMLFrontmatter(src)
	}
	// Unknown extensions don't have frontmatter — return defaults and the
	// raw body.
	return defaultFrontmatter(), src, nil
}

// ext returns the lowercase extension including the leading dot, or "" if
// none. Mirrors filepath.Ext but kept local to avoid pulling filepath into
// the package's hot path.
func ext(p string) string {
	for i := len(p) - 1; i >= 0 && p[i] != '/'; i-- {
		if p[i] == '.' {
			return p[i:]
		}
	}
	return ""
}

// Note: ErrFrontmatterParse already wraps the parse error from goccy or
// json; callers don't need to walk the chain unless they want to display
// the underlying detail. errors.Is(err, ErrFrontmatterParse) holds in all
// frontmatter-parse failure paths.
var _ = errors.Is

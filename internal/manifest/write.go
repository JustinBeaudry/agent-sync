package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

var (
	// ErrKeyMissing is returned by WriteResolvedSHA when a target YAML key
	// is not present in the source manifest. Callers — notably the init
	// wizard (unit 17) — are expected to emit a template that pre-declares
	// `canonical.commit:` and `trusted_sha:` (empty values are fine) so
	// write.go only ever updates, never inserts into nested mappings.
	ErrKeyMissing = errors.New("manifest key missing")

	// ErrWriteInvalid is returned when caller-supplied values fail
	// pre-serialization validation (e.g. SHA format).
	ErrWriteInvalid = errors.New("write input invalid")
)

// WriteResolvedSHA updates `canonical.commit:` and `trusted_sha:` in orig
// and returns the re-serialized bytes. Comments and key order are
// preserved via goccy's AST; keys the caller does not update are left
// untouched byte-for-byte.
//
// Both keys MUST already exist in orig (with any value, including
// empty). This is the init-time contract: the wizard emits a template
// containing the keys, and WriteResolvedSHA fills in the resolved SHA.
// If either key is missing, WriteResolvedSHA returns ErrKeyMissing —
// never silently appends, which would lose indentation/placement
// intent.
//
// Pass commit or trustedSHA as "" to skip updating that key.
func WriteResolvedSHA(orig []byte, commit, trustedSHA string) ([]byte, error) {
	if commit != "" && !reHex40.MatchString(commit) {
		return nil, fmt.Errorf("%w: commit must be 40 lowercase hex (got %q)", ErrWriteInvalid, commit)
	}
	if trustedSHA != "" && !reHex40.MatchString(trustedSHA) {
		return nil, fmt.Errorf("%w: trusted_sha must be 40 lowercase hex (got %q)", ErrWriteInvalid, trustedSHA)
	}
	file, err := parser.ParseBytes(orig, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("%w: parse: %w", ErrWriteInvalid, err)
	}

	// Always verify both target keys exist, even when both args are empty
	// (no-op path). This prevents the caller from mistakenly treating a
	// missing key as a silent success.
	if err := probeKey(file, "$.canonical.commit"); err != nil {
		return nil, fmt.Errorf("update canonical.commit: %w", err)
	}
	if err := probeKey(file, "$.trusted_sha"); err != nil {
		return nil, fmt.Errorf("update trusted_sha: %w", err)
	}

	if commit == "" && trustedSHA == "" {
		// Both keys present; no-op. Return a copy of the original bytes.
		out := make([]byte, len(orig))
		copy(out, orig)
		return out, nil
	}

	if commit != "" {
		if err := replaceScalar(file, "$.canonical.commit", commit); err != nil {
			return nil, fmt.Errorf("update canonical.commit: %w", err)
		}
	}
	if trustedSHA != "" {
		if err := replaceScalar(file, "$.trusted_sha", trustedSHA); err != nil {
			return nil, fmt.Errorf("update trusted_sha: %w", err)
		}
	}

	out := []byte(file.String())
	// ast.File.String() does not guarantee a trailing newline; normalize
	// so subsequent hand-edits and line-diff tools behave.
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out, nil
}

// WriteTrustedSHA is the `agent-sync trust pin` subcommand's writer: it
// updates only `trusted_sha:` and leaves `canonical.commit:` alone.
// See plan decision #9.
func WriteTrustedSHA(orig []byte, trustedSHA string) ([]byte, error) {
	return WriteResolvedSHA(orig, "", trustedSHA)
}

// WriteFile atomically writes content to the manifest at path, using
// fsroot.StagedWrite so the write is crash-safe and contained. path may
// be absolute or relative to cwd.
//
// Permissions are 0o644 — the manifest is not secret.
func WriteFile(path string, content []byte) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", path, err)
	}
	parent := filepath.Dir(abs)
	base := filepath.Base(abs)

	root, err := fsroot.OpenWorkspaceRoot(parent)
	if err != nil {
		return fmt.Errorf("open parent %q: %w", parent, err)
	}
	defer func() { _ = root.Close() }()

	return root.StagedWrite(base, content, fs.FileMode(0o644))
}

// probeKey checks that path exists in file without modifying it. If the
// path is missing, ErrKeyMissing is returned.
func probeKey(file *ast.File, path string) error {
	p, err := yaml.PathString(path)
	if err != nil {
		return fmt.Errorf("bad path %q: %w", path, err)
	}
	if _, err := p.FilterFile(file); err != nil {
		if errors.Is(err, yaml.ErrNotFoundNode) {
			return fmt.Errorf("%w: yaml path %q not found", ErrKeyMissing, path)
		}
		return fmt.Errorf("%w: yaml path %q: %w", ErrWriteInvalid, path, err)
	}
	return nil
}

// replaceScalar locates path inside file and replaces its scalar value
// with the given string. If path does not resolve to an existing node,
// ErrKeyMissing is returned.
func replaceScalar(file *ast.File, path, value string) error {
	p, err := yaml.PathString(path)
	if err != nil {
		return fmt.Errorf("bad path %q: %w", path, err)
	}
	if _, err := p.FilterFile(file); err != nil {
		if errors.Is(err, yaml.ErrNotFoundNode) {
			return fmt.Errorf("%w: yaml path %q not found", ErrKeyMissing, path)
		}
		return fmt.Errorf("%w: yaml path %q: %w", ErrWriteInvalid, path, err)
	}
	if err := p.ReplaceWithReader(file, strings.NewReader(value)); err != nil {
		return fmt.Errorf("replace %q: %w", path, err)
	}
	return nil
}

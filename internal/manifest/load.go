package manifest

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
)

type LoadOptions struct {
	// NonInteractive enforces the CI-safe invariant: if `canonical.commit` is
	// set, `trusted_sha` must also be set.
	NonInteractive bool
}

var (
	ErrInvalidManifest = errors.New("invalid manifest")

	// ErrInvalidReservedPrefix is returned by ValidateReservedPrefix when
	// a reserved_prefix value is not a clean, workspace-relative,
	// forward-slash path. Wrapped by ErrInvalidManifest at call sites in
	// the workspace-manifest validator so existing callers can continue
	// to branch on errors.Is(err, ErrInvalidManifest).
	ErrInvalidReservedPrefix = errors.New("manifest: reserved_prefix must be a clean workspace-relative forward-slash path")

	reHex40 = regexp.MustCompile(`\A[0-9a-f]{40}\z`)

	// reWindowsVolumePrefix matches a leading drive letter ("C:", "d:", ...)
	// — absolute on Windows when interpreted by OS path rules. We reject
	// it on every OS so workspace-relative paths stay portable.
	reWindowsVolumePrefix = regexp.MustCompile(`\A[A-Za-z]:`)
)

// ValidateReservedPrefix is the single canonical validator for
// adapter-style reserved_prefix values. The contract: an empty string
// (no claim) is allowed and returns ""; otherwise the value must be a
// clean, workspace-relative, forward-slash path. The returned string is
// the normalized form (path.Clean with any trailing "/" already
// stripped).
//
// Rejected shapes (all wrap ErrInvalidReservedPrefix):
//   - any backslash (always reserved on POSIX, ambiguous on Windows)
//   - Windows volume prefix ("C:foo", "d:/x")
//   - absolute paths under POSIX rules (leading "/")
//   - any path segment equal to ".." (workspace escape)
func ValidateReservedPrefix(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	// Always-applicable Windows-safety checks: backslashes and volume
	// prefixes never belong in a forward-slash workspace path, and on
	// POSIX path.IsAbs misses both shapes — so we reject them on every
	// OS rather than relying on the caller's runtime.GOOS.
	if strings.ContainsRune(s, '\\') {
		return "", fmt.Errorf("%w: contains backslash: %q", ErrInvalidReservedPrefix, s)
	}
	if reWindowsVolumePrefix.MatchString(s) {
		return "", fmt.Errorf("%w: contains Windows volume prefix: %q", ErrInvalidReservedPrefix, s)
	}
	if path.IsAbs(s) {
		return "", fmt.Errorf("%w: must be relative, got absolute: %q", ErrInvalidReservedPrefix, s)
	}
	// Reject ".." segments before Clean collapses them. path.Clean
	// would silently turn "foo/../bar" into "bar", losing the
	// traversal intent — rejecting on the original segments preserves
	// the operator's signal that they wrote something that escapes the
	// declared subtree.
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: contains parent-directory segment: %q", ErrInvalidReservedPrefix, s)
		}
	}
	// Trim trailing "/" before Clean so a value like ".claude/" round-trips
	// to ".claude" (preserving the existing public contract) rather than
	// being treated as a directory by path.Clean.
	trimmed := strings.TrimRight(s, "/")
	cleaned := path.Clean(trimmed)
	return cleaned, nil
}

// MaxManifestSize is the maximum allowed manifest file size (1 MiB).
const MaxManifestSize = 1 << 20

func LoadFile(path string, opts LoadOptions) (*Manifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: stat %q: %w", path, err)
	}
	if info.Size() > MaxManifestSize {
		return nil, fmt.Errorf("%w: manifest exceeds %d bytes (got %d)", ErrInvalidManifest, MaxManifestSize, info.Size())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: read %q: %w", path, err)
	}
	// Defense-in-depth: file could grow between stat and read.
	if len(b) > MaxManifestSize {
		return nil, fmt.Errorf("%w: manifest exceeds %d bytes (got %d)", ErrInvalidManifest, MaxManifestSize, len(b))
	}
	return LoadBytes(b, opts)
}

func LoadBytes(src []byte, opts LoadOptions) (*Manifest, error) {
	var m Manifest
	if err := yaml.UnmarshalWithOptions(
		src,
		&m,
		yaml.DisallowUnknownField(),
		yaml.AllowFieldPrefixes("x-"),
	); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidManifest, yaml.FormatError(err, false, true))
	}

	if err := Validate(&m, opts); err != nil {
		return nil, err
	}
	return &m, nil
}

func Validate(m *Manifest, opts LoadOptions) error {
	if m == nil {
		return fmt.Errorf("%w: manifest is nil", ErrInvalidManifest)
	}

	if m.Version == 0 {
		// Default to v1 for the greenfield phase. Once agent-sync ships, this
		// should become required.
		m.Version = 1
	}
	if m.Version != 1 {
		return fmt.Errorf("%w: unsupported manifest version %d (want 1)", ErrInvalidManifest, m.Version)
	}

	// Normalize scalar fields before any checks.
	m.Canonical.URL = strings.TrimSpace(m.Canonical.URL)
	m.Canonical.LocalPath = strings.TrimSpace(m.Canonical.LocalPath)

	// Canonical source 1:1 invariant: exactly one of url/local_path.
	hasURL := m.Canonical.URL != ""
	hasLocal := m.Canonical.LocalPath != ""
	switch {
	case hasURL && hasLocal:
		return fmt.Errorf("%w: canonical source must set exactly one of url or local_path (got both)", ErrInvalidManifest)
	case !hasURL && !hasLocal:
		return fmt.Errorf("%w: canonical source must set exactly one of url or local_path (got neither)", ErrInvalidManifest)
	}

	commit := strings.TrimSpace(m.Canonical.Commit)
	trusted := strings.TrimSpace(m.TrustedSHA)

	if trusted != "" && commit == "" {
		return fmt.Errorf("%w: trusted_sha is set but canonical.commit is empty (no floating-with-pin hybrid)", ErrInvalidManifest)
	}
	if commit != "" && !reHex40.MatchString(commit) {
		return fmt.Errorf("%w: canonical.commit must be 40 lowercase hex (got %q)", ErrInvalidManifest, commit)
	}
	m.Canonical.Commit = commit

	if trusted != "" && !reHex40.MatchString(trusted) {
		return fmt.Errorf("%w: trusted_sha must be 40 lowercase hex (got %q)", ErrInvalidManifest, trusted)
	}
	m.TrustedSHA = trusted

	if opts.NonInteractive && commit != "" && trusted == "" {
		return fmt.Errorf("%w: non-interactive mode requires trusted_sha when canonical.commit is set", ErrInvalidManifest)
	}
	if commit != "" && trusted != "" && commit != trusted {
		return fmt.Errorf("%w: trusted_sha must mirror canonical.commit (commit=%q trusted_sha=%q)", ErrInvalidManifest, commit, trusted)
	}

	switch m.Scope {
	case "", "user", "project", "global":
	default:
		return fmt.Errorf("%w: scope must be one of user|project|global (got %q)", ErrInvalidManifest, m.Scope)
	}

	if m.Cache.Override != "" {
		override := strings.TrimSpace(m.Cache.Override)
		m.Cache.Override = override
		if !filepath.IsAbs(override) {
			return fmt.Errorf("%w: cache.override must be absolute, got %q", ErrInvalidManifest, override)
		}
		// Check the original (pre-Clean) path segments for ".." to reject
		// explicit traversal intent (e.g. "/home/alice/../../etc") even when
		// filepath.Clean would collapse it to a valid-looking path.
		// strings.Contains(override, "..") is intentionally NOT used: it would
		// also reject legitimate names like "/var/..cache".
		for _, seg := range strings.Split(override, string(filepath.Separator)) {
			if seg == ".." {
				return fmt.Errorf("%w: cache.override must not contain .. segments, got %q", ErrInvalidManifest, override)
			}
		}
		m.Cache.Override = filepath.Clean(override)
	}

	if err := validateAdapters(m); err != nil {
		return err
	}

	return nil
}

// reAdapterName mirrors the IR-id grammar. Adapter names must satisfy
// it because they appear in PATH lookups (agent-sync-adapter-<name>) and
// must be safe across filesystems.
var reAdapterName = regexp.MustCompile(`\A[a-z0-9][a-z0-9_-]{0,63}\z`)

func validateAdapters(m *Manifest) error {
	seen := make(map[string]bool, len(m.Adapters))
	for i := range m.Adapters {
		a := &m.Adapters[i]
		if !reAdapterName.MatchString(a.Name) {
			return fmt.Errorf("%w: adapters[%d].name=%q does not match required pattern", ErrInvalidManifest, i, a.Name)
		}
		if seen[a.Name] {
			return fmt.Errorf("%w: adapters has duplicate name %q", ErrInvalidManifest, a.Name)
		}
		seen[a.Name] = true
		for j, arg := range a.Command {
			if strings.TrimSpace(arg) == "" {
				return fmt.Errorf("%w: adapters[%d].command[%d] is empty", ErrInvalidManifest, i, j)
			}
		}
		cleaned, err := ValidateReservedPrefix(a.ReservedPrefix)
		if err != nil {
			// Keep ErrInvalidManifest in the chain so existing callers
			// branching on errors.Is(err, ErrInvalidManifest) still match;
			// callers wanting the specific cause can branch on
			// ErrInvalidReservedPrefix as well.
			return fmt.Errorf("%w: adapters[%d].reserved_prefix: %w", ErrInvalidManifest, i, err)
		}
		a.ReservedPrefix = cleaned
	}
	return nil
}

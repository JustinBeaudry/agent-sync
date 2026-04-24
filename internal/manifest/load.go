package manifest

import (
	"errors"
	"fmt"
	"os"
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

	reHex40 = regexp.MustCompile(`\A[0-9a-f]{40}\z`)
)

// MaxManifestSize is the maximum allowed manifest file size (1 MiB).
const MaxManifestSize = 1 << 20

func LoadFile(path string, opts LoadOptions) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: read %q: %w", path, err)
	}
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
		// Default to v1 for the greenfield phase. Once aienvs ships, this
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
		if strings.Contains(override, "..") {
			return fmt.Errorf("%w: cache.override must not contain .. segments, got %q", ErrInvalidManifest, override)
		}
	}

	return nil
}

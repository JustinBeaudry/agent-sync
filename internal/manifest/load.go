package manifest

import (
	"errors"
	"fmt"
	"os"
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

func LoadFile(path string, opts LoadOptions) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := LoadBytes(b, opts)
	if err != nil {
		return nil, err
	}
	return m, nil
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
		// Prefer returning the underlying error wrapped by ErrInvalidManifest
		// so callers can recognize "manifest is invalid" as a single class.
		return nil, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	return &m, nil
}

func Validate(m *Manifest, opts LoadOptions) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}

	if m.Version == 0 {
		// Default to v1 for the greenfield phase. Once aienvs ships, this
		// should become required.
		m.Version = 1
	}
	if m.Version != 1 {
		return fmt.Errorf("unsupported manifest version %d (want 1)", m.Version)
	}

	// Canonical source 1:1 invariant: exactly one of url/local_path.
	hasURL := strings.TrimSpace(m.Canonical.URL) != ""
	hasLocal := strings.TrimSpace(m.Canonical.LocalPath) != ""
	switch {
	case hasURL && hasLocal:
		return fmt.Errorf("canonical source must set exactly one of url or local_path (got both)")
	case !hasURL && !hasLocal:
		return fmt.Errorf("canonical source must set exactly one of url or local_path (got neither)")
	}

	commit := strings.TrimSpace(m.Canonical.Commit)
	trusted := strings.TrimSpace(m.TrustedSHA)

	if trusted != "" && commit == "" {
		return fmt.Errorf("trusted_sha is set but canonical.commit is empty (no floating-with-pin hybrid)")
	}
	if commit != "" && !reHex40.MatchString(commit) {
		return fmt.Errorf("canonical.commit must be 40 lowercase hex (got %q)", commit)
	}
	if trusted != "" && !reHex40.MatchString(trusted) {
		return fmt.Errorf("trusted_sha must be 40 lowercase hex (got %q)", trusted)
	}
	if opts.NonInteractive && commit != "" && trusted == "" {
		return fmt.Errorf("non-interactive mode requires trusted_sha when canonical.commit is set")
	}
	if commit != "" && trusted != "" && commit != trusted {
		return fmt.Errorf("trusted_sha must mirror canonical.commit (commit=%q trusted_sha=%q)", commit, trusted)
	}

	switch m.Scope {
	case "", "user", "project", "global":
	default:
		return fmt.Errorf("scope must be one of user|project|global (got %q)", m.Scope)
	}

	return nil
}

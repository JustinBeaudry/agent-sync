// Package adapter is the runtime layer of Unit 8: subprocess and
// in-process orchestration that turns the wire types in
// internal/adapter/contract into runnable adapter sessions.
//
// The package owns:
//   - adapter.yaml manifest parsing (this file)
//   - adapter discovery (PATH + workspace-manifest + bundled)
//   - subprocess and in-process transports
//   - the lifecycle orchestrator (initialize → initialized → emit → shutdown)
//   - declared-outputs integrity gate enforcement
//
// The wire types live in internal/adapter/contract; this package depends
// on them but does not re-export them.
package adapter

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
)

// MaxAdapterManifestBytes caps the on-disk size of an adapter.yaml.
// 1 MiB is generous: real manifests are well under 1 KiB, and the cap
// is there to defend against pathological or hostile files.
const MaxAdapterManifestBytes = 1 << 20

// ContractVersionV1 is the only contract_version this build of aienvs
// recognizes. Future protocol versions land via Unit 8b under capability
// negotiation; the wire form itself stays at aienvs/v1.
const ContractVersionV1 = "aienvs/v1"

// AdapterManifest describes one adapter as declared by its on-disk
// adapter.yaml. The manifest sits next to the binary on PATH and
// supplies the runtime with the metadata it needs to spawn the adapter
// and route ops correctly.
type AdapterManifest struct {
	// Name is the adapter's identifier. Must be a single token of
	// [a-z0-9][a-z0-9-_]{0,63} — same shape as IR node ids. Used in
	// PATH lookups (aienvs-adapter-<name>) and in the workspace
	// manifest's adapters: block.
	Name string `yaml:"name"`

	// Version is a free-form version string. Compared against by humans;
	// the runtime does not interpret it.
	Version string `yaml:"version,omitempty"`

	// ContractVersion is the wire-protocol version the adapter speaks.
	// Must equal ContractVersionV1; the runtime refuses to load adapters
	// declaring any other value.
	ContractVersion string `yaml:"contract_version"`

	// Command is the argv slice the runtime uses to spawn the adapter.
	// Must be non-empty. The first element is resolved via $PATH unless
	// it contains a path separator.
	Command []string `yaml:"command"`

	// ReservedPrefix is the path prefix (relative to the workspace root)
	// the adapter owns. The runtime uses this to detect nested-prefix
	// violations across adapters and to scope the declared-outputs gate.
	// Trailing slashes are stripped on load.
	ReservedPrefix string `yaml:"reserved_prefix,omitempty"`
}

// Manifest-level sentinel errors. Callers branch with errors.Is.
var (
	// ErrAdapterManifest is returned for any adapter.yaml problem the
	// caller hasn't already classified more specifically.
	ErrAdapterManifest = errors.New("adapter: invalid adapter.yaml")

	// ErrAdapterManifestTooLarge is returned when the file size exceeds
	// MaxAdapterManifestBytes.
	ErrAdapterManifestTooLarge = errors.New("adapter: adapter.yaml exceeds maximum size")

	// ErrAdapterManifestMissingContractVersion is returned when the
	// contract_version field is absent or empty.
	ErrAdapterManifestMissingContractVersion = errors.New("adapter: adapter.yaml missing contract_version")

	// ErrAdapterContractVersionUnsupported is returned when
	// contract_version is set but does not equal ContractVersionV1.
	ErrAdapterContractVersionUnsupported = errors.New("adapter: contract_version not supported by this build")

	// ErrAdapterManifestEmptyCommand is returned when command is missing
	// or an empty slice.
	ErrAdapterManifestEmptyCommand = errors.New("adapter: adapter.yaml command is empty")

	// ErrAdapterManifestInvalidName is returned when name fails the
	// adapter-id grammar.
	ErrAdapterManifestInvalidName = errors.New("adapter: adapter.yaml name does not match required pattern")
)

// adapterNamePattern enforces the same id grammar as IR nodes:
// leading alphanumeric, then up to 63 of [a-z0-9-_]. Total 1..64.
var adapterNamePattern = regexp.MustCompile(`\A[a-z0-9][a-z0-9_-]{0,63}\z`)

// LoadAdapterManifestFile reads adapter.yaml from disk and decodes it.
// File-size cap is enforced before the YAML parse so a hostile file
// cannot consume unbounded memory.
func LoadAdapterManifestFile(path string) (*AdapterManifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("adapter: stat adapter.yaml: %w", err)
	}
	if info.Size() > MaxAdapterManifestBytes {
		return nil, fmt.Errorf("%w: %d bytes > %d", ErrAdapterManifestTooLarge, info.Size(), MaxAdapterManifestBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("adapter: read adapter.yaml: %w", err)
	}
	if len(data) > MaxAdapterManifestBytes {
		// Defense-in-depth: file could grow between stat and read.
		return nil, fmt.Errorf("%w: %d bytes > %d", ErrAdapterManifestTooLarge, len(data), MaxAdapterManifestBytes)
	}
	return LoadAdapterManifestBytes(data)
}

// LoadAdapterManifestBytes decodes a YAML byte slice and validates it.
// Strict mode: unknown non-x- fields are rejected; x-prefixed fields
// are accepted as forward-compatible extensions.
func LoadAdapterManifestBytes(src []byte) (*AdapterManifest, error) {
	if len(src) > MaxAdapterManifestBytes {
		return nil, fmt.Errorf("%w: %d bytes > %d", ErrAdapterManifestTooLarge, len(src), MaxAdapterManifestBytes)
	}
	var m AdapterManifest
	if err := yaml.UnmarshalWithOptions(
		src,
		&m,
		yaml.DisallowUnknownField(),
		yaml.AllowFieldPrefixes("x-"),
	); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrAdapterManifest, yaml.FormatError(err, false, true))
	}
	if err := validateAdapterManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SyntheticAdapterManifest constructs the implicit manifest used when
// PATH discovery finds an aienvs-adapter-<name> binary without a
// sibling adapter.yaml. Name must already be validated by the caller —
// adapter-id grammar is enforced via path matching upstream.
func SyntheticAdapterManifest(name string) *AdapterManifest {
	return &AdapterManifest{
		Name:            name,
		ContractVersion: ContractVersionV1,
		Command:         []string{"aienvs-adapter-" + name},
	}
}

func validateAdapterManifest(m *AdapterManifest) error {
	if !adapterNamePattern.MatchString(m.Name) {
		return fmt.Errorf("%w: name=%q", ErrAdapterManifestInvalidName, m.Name)
	}
	if m.ContractVersion == "" {
		return ErrAdapterManifestMissingContractVersion
	}
	if m.ContractVersion != ContractVersionV1 {
		return fmt.Errorf("%w: %q (this build supports %q only)",
			ErrAdapterContractVersionUnsupported, m.ContractVersion, ContractVersionV1)
	}
	if len(m.Command) == 0 {
		return ErrAdapterManifestEmptyCommand
	}
	for i, arg := range m.Command {
		if arg == "" {
			return fmt.Errorf("%w: command[%d] is empty", ErrAdapterManifestEmptyCommand, i)
		}
	}
	m.ReservedPrefix = strings.TrimRight(m.ReservedPrefix, "/")
	return nil
}

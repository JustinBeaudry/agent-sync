package adapter

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadAdapterManifest_MinimalRoundTrips(t *testing.T) {
	t.Parallel()

	src := `name: claude
version: 0.1.0
contract_version: aienvs/v1
command:
  - aienvs-adapter-claude
`
	m, err := LoadAdapterManifestBytes([]byte(src))
	if err != nil {
		t.Fatalf("LoadAdapterManifestBytes: %v", err)
	}
	if m.Name != "claude" {
		t.Errorf("name: want claude, got %q", m.Name)
	}
	if m.ContractVersion != "aienvs/v1" {
		t.Errorf("contract_version: want aienvs/v1, got %q", m.ContractVersion)
	}
	if len(m.Command) != 1 || m.Command[0] != "aienvs-adapter-claude" {
		t.Errorf("command: %v", m.Command)
	}
}

func TestLoadAdapterManifest_FullManifestRoundTrips(t *testing.T) {
	t.Parallel()

	src := `name: cursor
version: 1.2.3
contract_version: aienvs/v1
command:
  - aienvs-adapter-cursor
  - --strict
reserved_prefix: .cursor
`
	m, err := LoadAdapterManifestBytes([]byte(src))
	if err != nil {
		t.Fatalf("LoadAdapterManifestBytes: %v", err)
	}
	if m.Version != "1.2.3" {
		t.Errorf("version: %q", m.Version)
	}
	if m.ReservedPrefix != ".cursor" {
		t.Errorf("reserved_prefix: %q", m.ReservedPrefix)
	}
	if len(m.Command) != 2 {
		t.Fatalf("command len: %d", len(m.Command))
	}
}

func TestSyntheticAdapterManifest_DefaultsFromBinaryName(t *testing.T) {
	t.Parallel()

	m := SyntheticAdapterManifest("foo")
	if m.Name != "foo" {
		t.Errorf("name: %q", m.Name)
	}
	if m.ContractVersion != "aienvs/v1" {
		t.Errorf("contract_version: %q", m.ContractVersion)
	}
	if len(m.Command) != 1 || m.Command[0] != "aienvs-adapter-foo" {
		t.Errorf("command: %v", m.Command)
	}
}

func TestLoadAdapterManifest_EmptyCommandRejected(t *testing.T) {
	t.Parallel()

	src := `name: foo
contract_version: aienvs/v1
command: []
`
	_, err := LoadAdapterManifestBytes([]byte(src))
	if !errors.Is(err, ErrAdapterManifestEmptyCommand) {
		t.Fatalf("want ErrAdapterManifestEmptyCommand, got %v", err)
	}
}

func TestLoadAdapterManifest_MissingContractVersion(t *testing.T) {
	t.Parallel()

	src := `name: foo
command:
  - aienvs-adapter-foo
`
	_, err := LoadAdapterManifestBytes([]byte(src))
	if !errors.Is(err, ErrAdapterManifestMissingContractVersion) {
		t.Fatalf("want ErrAdapterManifestMissingContractVersion, got %v", err)
	}
}

func TestLoadAdapterManifest_UnsupportedContractVersion(t *testing.T) {
	t.Parallel()

	src := `name: foo
contract_version: aienvs/v0
command:
  - aienvs-adapter-foo
`
	_, err := LoadAdapterManifestBytes([]byte(src))
	if !errors.Is(err, ErrAdapterContractVersionUnsupported) {
		t.Fatalf("want ErrAdapterContractVersionUnsupported, got %v", err)
	}
}

func TestLoadAdapterManifest_MalformedYAMLWrapsParserError(t *testing.T) {
	t.Parallel()

	src := `name: foo
contract_version: aienvs/v1
command: [unterminated
`
	_, err := LoadAdapterManifestBytes([]byte(src))
	if err == nil {
		t.Fatal("want error from malformed YAML, got nil")
	}
}

func TestLoadAdapterManifest_InvalidNameWithSeparators(t *testing.T) {
	t.Parallel()

	cases := []string{
		"foo/bar",
		"foo\\bar",
		"foo bar",
		"",
		".foo",
		"-foo",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := "name: " + name + "\ncontract_version: aienvs/v1\ncommand: [x]\n"
			_, err := LoadAdapterManifestBytes([]byte(src))
			if !errors.Is(err, ErrAdapterManifestInvalidName) {
				t.Fatalf("name=%q: want ErrAdapterManifestInvalidName, got %v", name, err)
			}
		})
	}
}

func TestLoadAdapterManifest_ReservedPrefixNormalized(t *testing.T) {
	t.Parallel()

	src := `name: foo
contract_version: aienvs/v1
command: [aienvs-adapter-foo]
reserved_prefix: .foo/
`
	m, err := LoadAdapterManifestBytes([]byte(src))
	if err != nil {
		t.Fatalf("LoadAdapterManifestBytes: %v", err)
	}
	if m.ReservedPrefix != ".foo" {
		t.Errorf("reserved_prefix should be normalized (no trailing slash), got %q", m.ReservedPrefix)
	}
}

func TestLoadAdapterManifest_AcceptsXPrefixedExtensions(t *testing.T) {
	// Forward-compat: x-prefixed fields are not rejected by strict mode.
	t.Parallel()

	src := `name: foo
contract_version: aienvs/v1
command: [aienvs-adapter-foo]
x-future-field: experiment
`
	if _, err := LoadAdapterManifestBytes([]byte(src)); err != nil {
		t.Fatalf("x-prefixed field should be accepted, got %v", err)
	}
}

func TestLoadAdapterManifest_RejectsUnknownNonXFields(t *testing.T) {
	// Strict mode: unknown non-x- fields fail to load.
	t.Parallel()

	src := `name: foo
contract_version: aienvs/v1
command: [aienvs-adapter-foo]
totally_made_up: yes
`
	_, err := LoadAdapterManifestBytes([]byte(src))
	if err == nil {
		t.Fatal("want error from unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "totally_made_up") {
		t.Errorf("error should mention the offending field: %v", err)
	}
}

func TestLoadAdapterManifest_RejectsOversizedFile(t *testing.T) {
	t.Parallel()

	bigName := strings.Repeat("x", MaxAdapterManifestBytes)
	src := []byte("name: " + bigName + "\n")
	_, err := LoadAdapterManifestBytes(src)
	if !errors.Is(err, ErrAdapterManifestTooLarge) {
		t.Fatalf("want ErrAdapterManifestTooLarge, got %v", err)
	}
}

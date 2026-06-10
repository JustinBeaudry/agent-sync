package adapter

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestLoadAdapterManifest_MinimalRoundTrips(t *testing.T) {
	t.Parallel()

	src := `name: claude
version: 0.1.0
contract_version: aienvs/v1
command:
  - agent-sync-adapter-claude
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
	if len(m.Command) != 1 || m.Command[0] != "agent-sync-adapter-claude" {
		t.Errorf("command: %v", m.Command)
	}
}

func TestLoadAdapterManifest_FullManifestRoundTrips(t *testing.T) {
	t.Parallel()

	src := `name: cursor
version: 1.2.3
contract_version: aienvs/v1
command:
  - agent-sync-adapter-cursor
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

	// Empty binaryPath -> bare PATH-relative kubectl-plugin name.
	m := SyntheticAdapterManifest("foo", "")
	if m.Name != "foo" {
		t.Errorf("name: %q", m.Name)
	}
	if m.ContractVersion != "aienvs/v1" {
		t.Errorf("contract_version: %q", m.ContractVersion)
	}
	if len(m.Command) != 1 || m.Command[0] != "agent-sync-adapter-foo" {
		t.Errorf("command: %v", m.Command)
	}
}

func TestSyntheticAdapterManifest_PinsResolvedBinaryPath(t *testing.T) {
	// PATH discovery resolves a concrete location for the adapter
	// binary; the manifest must record that path so spawn-time exec
	// hits the same binary even if PATH changes between discovery and
	// run.
	t.Parallel()

	m := SyntheticAdapterManifest("foo", "/opt/aienvs/bin/agent-sync-adapter-foo")
	if len(m.Command) != 1 || m.Command[0] != "/opt/aienvs/bin/agent-sync-adapter-foo" {
		t.Errorf("command should be the resolved absolute path, got %v", m.Command)
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
  - agent-sync-adapter-foo
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
  - agent-sync-adapter-foo
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
command: [agent-sync-adapter-foo]
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

func TestLoadAdapterManifest_ReservedPrefixRejectedShapes(t *testing.T) {
	// SEC: reserved_prefix is workspace-relative forward-slash only.
	// Reject absolute paths, Windows volume prefixes, backslashes, and
	// any ".." segment — these are not safe regardless of host OS.
	t.Parallel()

	cases := []struct {
		name string
		v    string
	}{
		{"absolute-posix", "/abs/path"},
		{"windows-volume-uppercase", "C:/foo"},
		{"windows-volume-lowercase", "d:foo"},
		{"backslash-only", `foo\bar`},
		{"backslash-as-separator", `.\foo`},
		{"dotdot-segment", "../etc"},
		{"dotdot-nested", "foo/../bar"},
		{"leading-dotdot", "../../escape"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := "name: foo\ncontract_version: aienvs/v1\ncommand: [agent-sync-adapter-foo]\nreserved_prefix: " + strconv.Quote(tc.v) + "\n"
			_, err := LoadAdapterManifestBytes([]byte(src))
			if !errors.Is(err, ErrAdapterManifestInvalidReservedPrefix) {
				t.Fatalf("v=%q: want ErrAdapterManifestInvalidReservedPrefix, got %v", tc.v, err)
			}
		})
	}
}

func TestValidateReservedPrefix_AcceptsCleanRelativePaths(t *testing.T) {
	// Empty (no claim) and well-formed relative paths must round-trip
	// cleanly. Trailing slashes are stripped; leading "./" is normalized
	// away by path.Clean.
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{".claude", ".claude"},
		{".claude/", ".claude"},
		{".claude/skills", ".claude/skills"},
		{"./.claude", ".claude"},
		{".", "."}, // workspace root claim — allowed at the manifest layer
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateReservedPrefix(tc.in)
			if err != nil {
				t.Fatalf("ValidateReservedPrefix(%q): unexpected error %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ValidateReservedPrefix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLoadAdapterManifest_AcceptsXPrefixedExtensions(t *testing.T) {
	// Forward-compat: x-prefixed fields are not rejected by strict mode.
	t.Parallel()

	src := `name: foo
contract_version: aienvs/v1
command: [agent-sync-adapter-foo]
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
command: [agent-sync-adapter-foo]
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

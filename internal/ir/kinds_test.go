package ir

import (
	"errors"
	"strings"
	"testing"
)

func TestExtractMarkdownFrontmatter_NoFrontmatter(t *testing.T) {
	t.Parallel()

	body := []byte("# Just markdown\n\nNo frontmatter here.\n")
	fm, rest, err := extractMarkdownFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Required || fm.Version != 1 || len(fm.Targets) != 0 {
		t.Errorf("expected defaults, got %+v", fm)
	}
	if string(rest) != string(body) {
		t.Errorf("body should be unchanged, got %q", rest)
	}
}

func TestExtractMarkdownFrontmatter_AllFields(t *testing.T) {
	t.Parallel()

	body := []byte("---\nrequired: true\ntargets: [claude, cursor]\nversion: 3\n---\n\nThe body.\n")
	fm, rest, err := extractMarkdownFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fm.Required {
		t.Errorf("Required = false, want true")
	}
	if fm.Version != 3 {
		t.Errorf("Version = %d, want 3", fm.Version)
	}
	if len(fm.Targets) != 2 || fm.Targets[0] != "claude" || fm.Targets[1] != "cursor" {
		t.Errorf("Targets = %v, want [claude cursor]", fm.Targets)
	}
	if !strings.HasPrefix(string(rest), "\nThe body.") {
		t.Errorf("body wrong: %q", rest)
	}
}

func TestExtractMarkdownFrontmatter_UnknownField(t *testing.T) {
	t.Parallel()

	body := []byte("---\nrequired: true\nbogus_field: yes\n---\nbody\n")
	_, _, err := extractMarkdownFrontmatter(body)
	if !errors.Is(err, ErrUnknownFrontmatterField) {
		t.Errorf("err = %v, want ErrUnknownFrontmatterField", err)
	}
}

func TestExtractMarkdownFrontmatter_XPrefixedFieldAllowed(t *testing.T) {
	t.Parallel()

	body := []byte("---\nrequired: true\nx-experimental: yes\n---\nbody\n")
	fm, _, err := extractMarkdownFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fm.Required {
		t.Errorf("Required = false, want true (x-prefix should not have rejected the block)")
	}
}

func TestExtractMarkdownFrontmatter_MissingClosingDelimiter(t *testing.T) {
	t.Parallel()

	body := []byte("---\nrequired: true\nno closing delimiter follows\n")
	_, _, err := extractMarkdownFrontmatter(body)
	if !errors.Is(err, ErrFrontmatterParse) {
		t.Errorf("err = %v, want ErrFrontmatterParse", err)
	}
}

func TestExtractMarkdownFrontmatter_MalformedYAML(t *testing.T) {
	t.Parallel()

	body := []byte("---\nrequired: [unclosed\n---\nbody\n")
	_, _, err := extractMarkdownFrontmatter(body)
	if !errors.Is(err, ErrFrontmatterParse) {
		t.Errorf("err = %v, want ErrFrontmatterParse", err)
	}
}

func TestExtractMarkdownFrontmatter_CRLF(t *testing.T) {
	t.Parallel()

	body := []byte("---\r\nrequired: true\r\n---\r\nbody\r\n")
	fm, _, err := extractMarkdownFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fm.Required {
		t.Errorf("CRLF parse failed")
	}
}

func TestExtractMarkdownFrontmatter_EmptyBlock(t *testing.T) {
	t.Parallel()

	body := []byte("---\n---\nbody\n")
	fm, _, err := extractMarkdownFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Defaults should apply.
	if fm.Required || fm.Version != 1 {
		t.Errorf("expected defaults on empty block, got %+v", fm)
	}
}

func TestExtractJSONFrontmatter_AllFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{
  "__aienvs_required": true,
  "__aienvs_targets": ["claude"],
  "__aienvs_version": 2,
  "command": "linear-server",
  "url": "https://example.com"
}`)
	fm, stripped, err := extractJSONFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fm.Required || fm.Version != 2 || len(fm.Targets) != 1 || fm.Targets[0] != "claude" {
		t.Errorf("frontmatter wrong: %+v", fm)
	}
	if strings.Contains(string(stripped), "__aienvs_") {
		t.Errorf("stripped body still contains reserved keys: %s", stripped)
	}
	if !strings.Contains(string(stripped), "linear-server") {
		t.Errorf("non-aienvs key dropped: %s", stripped)
	}
}

func TestExtractJSONFrontmatter_NoMetadata(t *testing.T) {
	t.Parallel()

	body := []byte(`{"command": "x"}`)
	fm, stripped, err := extractJSONFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Version != 1 || fm.Required {
		t.Errorf("defaults not applied: %+v", fm)
	}
	if !strings.Contains(string(stripped), "command") {
		t.Errorf("body lost the original key: %s", stripped)
	}
}

func TestExtractJSONFrontmatter_MalformedJSON(t *testing.T) {
	t.Parallel()

	body := []byte(`{this is not json`)
	_, _, err := extractJSONFrontmatter(body)
	if !errors.Is(err, ErrFrontmatterParse) {
		t.Errorf("err = %v, want ErrFrontmatterParse", err)
	}
}

func TestExtractJSONFrontmatter_WrongTypes(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"required not bool": []byte(`{"__aienvs_required": "yes"}`),
		"targets not array": []byte(`{"__aienvs_targets": "claude"}`),
		"version not int":   []byte(`{"__aienvs_version": "two"}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := extractJSONFrontmatter(body)
			if !errors.Is(err, ErrFrontmatterParse) {
				t.Errorf("err = %v, want ErrFrontmatterParse", err)
			}
		})
	}
}

func TestExtractJSONFrontmatter_DeterministicOutput(t *testing.T) {
	t.Parallel()

	body := []byte(`{"b": 2, "a": 1, "__aienvs_version": 5, "z": 26}`)
	first, _, err := extractJSONFrontmatter(body)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_ = first

	// Re-decode 10 times; stripped body must be byte-identical each run.
	var prev []byte
	for i := 0; i < 10; i++ {
		_, stripped, err := extractJSONFrontmatter(body)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if prev != nil && string(prev) != string(stripped) {
			t.Errorf("non-deterministic stripped body across runs:\n run0: %s\n run%d: %s", prev, i, stripped)
		}
		prev = stripped
	}
}

func TestExtractTOMLFrontmatter_StubReturnsDefaults(t *testing.T) {
	t.Parallel()

	body := []byte(`name = "ce"
version = "1.2.3"
`)
	fm, stripped, err := extractTOMLFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Version != 1 || fm.Required {
		t.Errorf("expected default frontmatter, got %+v", fm)
	}
	if string(stripped) != string(body) {
		t.Errorf("stub should return body unchanged")
	}
}

func TestKindForExt(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		want Kind
		ok   bool
	}{
		".md":   {KindRule, true},
		".json": {KindMCPServerEntry, true},
		".toml": {KindPluginReference, true},
		".txt":  {"", false},
		"":      {"", false},
	}
	for ext, tc := range cases {
		got, ok := kindForExt(ext)
		if ok != tc.ok || got != tc.want {
			t.Errorf("kindForExt(%q) = (%q, %v), want (%q, %v)", ext, got, ok, tc.want, tc.ok)
		}
	}
}

func TestValidateTargets(t *testing.T) {
	t.Parallel()

	known := map[string]struct{}{"claude": {}, "cursor": {}}

	if err := validateTargets([]string{"claude"}, known); err != nil {
		t.Errorf("known target rejected: %v", err)
	}
	if err := validateTargets([]string{"claude", "cursor"}, known); err != nil {
		t.Errorf("multiple known targets rejected: %v", err)
	}
	if err := validateTargets([]string{"unknown"}, known); !errors.Is(err, ErrUnknownTarget) {
		t.Errorf("unknown target err = %v, want ErrUnknownTarget", err)
	}
	if err := validateTargets([]string{}, known); err != nil {
		t.Errorf("empty targets should be ok: %v", err)
	}
	// No registry → accept anything (test/early-v1 mode).
	if err := validateTargets([]string{"future-adapter"}, nil); err != nil {
		t.Errorf("nil registry should accept any target: %v", err)
	}
}

func TestExtFunction(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"foo.md":          ".md",
		"path/foo.json":   ".json",
		"a/b/c.toml":      ".toml",
		"noext":           "",
		"foo/bar.no.dots": ".dots",
		"AGENTS.md":       ".md",
	}
	for in, want := range cases {
		if got := ext(in); got != want {
			t.Errorf("ext(%q) = %q, want %q", in, got, want)
		}
	}
}

package skillmeta

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// TestFrontmatter_EscapingVectors is the plan U4 escaping table: every
// vector must round-trip through a real YAML parser to the original
// description, and the block must never gain extra keys (injection
// guard). claude/codex/pi all render through this one helper, so the
// vectors pin cross-adapter byte-identity at the source.
func TestFrontmatter_EscapingVectors(t *testing.T) {
	t.Parallel()

	vectors := []struct {
		name string
		desc string
	}{
		{"plain", "A simple description"},
		{"colon-space", "key: value looking text"},
		{"double-quotes", `it "quotes" things`},
		{"single-quotes", "it 'quotes' things"},
		{"hash", "trailing # comment-like"},
		{"leading-dash", "- looks like a list item"},
		{"leading-gt", "> looks like a block scalar"},
		{"leading-pipe", "| also block-scalar-like"},
		{"leading-amp", "&anchor style"},
		{"newline-injection", "line one\ntargets: [evil]\nname: hijacked"},
		{"crlf", "line one\r\nline two"},
		{"tab", "col1\tcol2"},
		{"control-char", "bell\x07char"},
		{"unicode", "émojis 🎉 and — dashes"},
		{"long", strings.Repeat("long text ", 60)},
		{"empty-after-trim", "   "},
	}

	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			block := Frontmatter("agent-sync-demo", v.desc)
			s := string(block)

			if !strings.HasPrefix(s, "---\n") || !strings.HasSuffix(s, "\n---\n\n") {
				t.Fatalf("frontmatter block malformed:\n%s", s)
			}
			inner := strings.TrimSuffix(strings.TrimPrefix(s, "---\n"), "\n---\n\n")

			var parsed map[string]string
			if err := yaml.Unmarshal([]byte(inner), &parsed); err != nil {
				t.Fatalf("emitted frontmatter does not parse as YAML: %v\n%s", err, s)
			}
			if len(parsed) != 2 {
				t.Errorf("injection guard: expected exactly {name, description}, got %d keys: %v", len(parsed), parsed)
			}
			if parsed["name"] != "agent-sync-demo" {
				t.Errorf("name = %q, want agent-sync-demo (hijack check)", parsed["name"])
			}
			if parsed["description"] != v.desc {
				t.Errorf("description round-trip = %q, want %q", parsed["description"], v.desc)
			}
		})
	}
}

func TestFallbackDescription(t *testing.T) {
	t.Parallel()
	if got := FallbackDescription(".agents"); got != "Synced by agent-sync from .agents (no description authored)." {
		t.Errorf("FallbackDescription(.agents) = %q", got)
	}
	if got := FallbackDescription(""); got != "Synced by agent-sync (no description authored)." {
		t.Errorf("FallbackDescription(empty) = %q", got)
	}
}

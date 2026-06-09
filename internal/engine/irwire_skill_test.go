package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/ir"
)

func TestMarshalIR_SkillWithAssets(t *testing.T) {
	nodes := []ir.Node{{
		ID:   "my-skill",
		Kind: ir.KindSkill,
		Body: []byte("# Skill body"),
	}}
	skills := map[string]ir.Skill{
		"my-skill": {
			Node: nodes[0],
			Assets: []ir.Asset{{
				RelPath:    "templates/foo.txt",
				Provenance: ir.Provenance{Path: "skills/my-skill/templates/foo.txt", BlobSHA: "deadbeef"},
				Content:    []byte("template content"),
			}},
		},
	}

	raw, err := MarshalIR(nodes, skills)
	if err != nil {
		t.Fatalf("MarshalIR: %v", err)
	}

	var doc struct {
		Nodes []struct {
			ID     string `json:"id"`
			Assets []struct {
				RelPath string          `json:"rel_path"`
				Content json.RawMessage `json:"content"`
			} `json:"assets"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, raw)
	}
	if len(doc.Nodes) != 1 || len(doc.Nodes[0].Assets) != 1 {
		t.Fatalf("expected 1 node with 1 asset, got %s", raw)
	}
	if doc.Nodes[0].Assets[0].RelPath != "templates/foo.txt" {
		t.Fatalf("asset rel_path = %q", doc.Nodes[0].Assets[0].RelPath)
	}
}

func TestMarshalIR_SkillKindWithoutAssetsEntry(t *testing.T) {
	// A skill node with no matching skills-map entry ships without assets.
	nodes := []ir.Node{{ID: "lonely", Kind: ir.KindSkill, Body: []byte("x")}}
	raw, err := MarshalIR(nodes, map[string]ir.Skill{})
	if err != nil {
		t.Fatalf("MarshalIR: %v", err)
	}
	if strings.Contains(string(raw), "assets") && strings.Contains(string(raw), "rel_path") {
		t.Fatalf("did not expect asset payload: %s", raw)
	}
}

func TestEncodeBody_StructuredVariants(t *testing.T) {
	// Object with leading whitespace -> passthrough; array -> passthrough;
	// brace-prefixed but invalid JSON -> string-encoded; non-brace -> string.
	tests := []struct {
		name         string
		body         string
		wantPassthru bool
	}{
		{"object leading ws", "  \n{\"k\":1}", true},
		{"array", "[1,2,3]", true},
		{"brace but invalid", "{not valid", false},
		{"plain markdown", "# heading", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeBody([]byte(tt.body))
			// Passthrough preserves the original bytes; string-encoding wraps in quotes.
			isPassthru := string(got) == tt.body
			if isPassthru != tt.wantPassthru {
				t.Fatalf("encodeBody(%q) = %q; passthrough=%v want %v", tt.body, got, isPassthru, tt.wantPassthru)
			}
			if !tt.wantPassthru {
				// String-encoded output must be a valid JSON string.
				var s string
				if err := json.Unmarshal(got, &s); err != nil {
					t.Fatalf("string-encoded body not valid JSON string: %q", got)
				}
			}
		})
	}
}

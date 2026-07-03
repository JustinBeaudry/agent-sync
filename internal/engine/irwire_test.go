package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/ir"
)

func TestMarshalIR_MarkdownBodyEncodedAsJSONString(t *testing.T) {
	nodes := []ir.Node{{ID: "no-fri", Kind: ir.KindRule, Version: 1, Body: []byte("No PRs on Friday.")}}
	raw, err := MarshalIR(nodes, nil)
	if err != nil {
		t.Fatalf("MarshalIR: %v", err)
	}

	var doc struct {
		Nodes []struct {
			ID   string          `json:"id"`
			Kind string          `json:"kind"`
			Body json.RawMessage `json:"body"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(doc.Nodes))
	}
	// Markdown body must be a JSON string so the adapter's
	// decodeBodyOrPassthrough recovers the exact bytes.
	var body string
	if err := json.Unmarshal(doc.Nodes[0].Body, &body); err != nil {
		t.Fatalf("body not a JSON string: %v", err)
	}
	if body != "No PRs on Friday." {
		t.Fatalf("body round-trip mismatch: %q", body)
	}
}

func TestMarshalIR_StructuredBodyPassesThrough(t *testing.T) {
	nodes := []ir.Node{{ID: "echo", Kind: ir.KindMCPServerEntry, Version: 1, Body: []byte(`{"command":"node"}`)}}
	raw, err := MarshalIR(nodes, nil)
	if err != nil {
		t.Fatalf("MarshalIR: %v", err)
	}
	var doc struct {
		Nodes []struct {
			Body json.RawMessage `json:"body"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Structured body passes through as a JSON object, not a string.
	var obj map[string]any
	if err := json.Unmarshal(doc.Nodes[0].Body, &obj); err != nil {
		t.Fatalf("body not a JSON object: %v", err)
	}
	if obj["command"] != "node" {
		t.Fatalf("structured body mismatch: %+v", obj)
	}
}

func TestEncodeBody_Empty(t *testing.T) {
	if got := encodeBody(nil); got != nil {
		t.Fatalf("empty body should encode to nil, got %q", got)
	}
}

// TestMarshalIR_DescriptionAndSourceOverrideRoundTrip pins the U2 additive
// wire fields: a node's description and per-node source override survive
// MarshalIR and decode on the adapter side. The decode struct below uses
// the exact JSON tags of the bundled adapters' irNode mirrors.
func TestMarshalIR_DescriptionAndSourceOverrideRoundTrip(t *testing.T) {
	nodes := []ir.Node{{
		ID:           "described",
		Kind:         ir.KindSkill,
		Version:      1,
		Description:  "Loops compound-capture sessions.",
		SourceURL:    "https://github.com/org/agents.git",
		SourceCommit: "0123456789abcdef0123456789abcdef01234567",
		Body:         []byte("skill body"),
	}}
	raw, err := MarshalIR(nodes, nil)
	if err != nil {
		t.Fatalf("MarshalIR: %v", err)
	}

	var doc struct {
		Nodes []struct {
			ID           string `json:"id"`
			Description  string `json:"description"`
			SourceURL    string `json:"source_url"`
			SourceCommit string `json:"source_commit"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(doc.Nodes))
	}
	got := doc.Nodes[0]
	if got.Description != "Loops compound-capture sessions." {
		t.Errorf("description = %q, want authored value", got.Description)
	}
	if got.SourceURL != "https://github.com/org/agents.git" {
		t.Errorf("source_url = %q, want override value", got.SourceURL)
	}
	if got.SourceCommit != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("source_commit = %q, want override value", got.SourceCommit)
	}
}

// TestMarshalIR_OmitsEmptyDescriptionAndSourceOverride is the additive
// backward-compat proof for the U2 wire fields: a node without them
// marshals to a document that does not contain the new keys at all, so a
// pre-U2 adapter sees byte-compatible input (freeze the frame, grow
// capabilities).
func TestMarshalIR_OmitsEmptyDescriptionAndSourceOverride(t *testing.T) {
	nodes := []ir.Node{{ID: "plain", Kind: ir.KindRule, Version: 1, Body: []byte("x")}}
	raw, err := MarshalIR(nodes, nil)
	if err != nil {
		t.Fatalf("MarshalIR: %v", err)
	}
	for _, key := range []string{`"description"`, `"source_url"`, `"source_commit"`} {
		if strings.Contains(string(raw), key) {
			t.Errorf("wire document must omit %s for a node without it:\n%s", key, raw)
		}
	}
}

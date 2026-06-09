package engine

import (
	"encoding/json"
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

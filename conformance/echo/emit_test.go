package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// emitParamsFor builds EmitParams whose IR is the given emitDocument.
func emitParamsFor(t *testing.T, doc emitDocument) adapterkit.EmitParams {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal emitDocument: %v", err)
	}
	return adapterkit.EmitParams{Target: "echo", IR: raw}
}

func node(id string, body string) emitNode {
	return emitNode{ID: id, Body: json.RawMessage(body)}
}

func TestHandleEmit_ValidNodes(t *testing.T) {
	params := emitParamsFor(t, emitDocument{Nodes: []emitNode{
		node("first", `"hello"`),
		node("second", `"world"`),
	}})

	res, err := handleEmit(context.Background(), params)
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}
	if len(res.OpsPerformed) != 3 {
		t.Fatalf("ops = %d, want 3 (mkdir + 2 writes): %+v", len(res.OpsPerformed), res.OpsPerformed)
	}
	if res.OpsPerformed[0].Op != adapterkit.OpKindMkdir || res.OpsPerformed[0].Path != outputRoot {
		t.Fatalf("first op should be mkdir of %q: %+v", outputRoot, res.OpsPerformed[0])
	}
	// Forward-slash paths regardless of host OS.
	if res.OpsPerformed[1].Path != outputRoot+"/first.md" {
		t.Fatalf("write path = %q, want %q", res.OpsPerformed[1].Path, outputRoot+"/first.md")
	}
	if res.OpsPerformed[2].Path != outputRoot+"/second.md" {
		t.Fatalf("write path = %q, want %q", res.OpsPerformed[2].Path, outputRoot+"/second.md")
	}
}

func TestHandleEmit_ZeroNodesNoMkdir(t *testing.T) {
	params := emitParamsFor(t, emitDocument{Nodes: nil})
	res, err := handleEmit(context.Background(), params)
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}
	if len(res.OpsPerformed) != 0 {
		t.Fatalf("expected no ops for zero nodes, got: %+v", res.OpsPerformed)
	}
}

func TestHandleEmit_InvalidNodeID(t *testing.T) {
	cases := []string{"Bad ID", "-leadinghyphen", "has/slash"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			params := emitParamsFor(t, emitDocument{Nodes: []emitNode{node(id, `"x"`)}})
			_, err := handleEmit(context.Background(), params)
			if err == nil {
				t.Fatalf("expected error for invalid id %q", id)
			}
			var akErr *adapterkit.Error
			if !errors.As(err, &akErr) {
				t.Fatalf("error type = %T, want *adapterkit.Error", err)
			}
			if akErr.Code != adapterkit.CodeInvalidParams {
				t.Fatalf("code = %d, want %d", akErr.Code, adapterkit.CodeInvalidParams)
			}
		})
	}
}

func TestHandleEmit_MalformedIR(t *testing.T) {
	params := adapterkit.EmitParams{Target: "echo", IR: json.RawMessage(`{not json`)}
	_, err := handleEmit(context.Background(), params)
	if err == nil {
		t.Fatal("expected decode error for malformed IR")
	}
}

func TestNodeContent(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantNil bool
		wantErr bool
	}{
		{name: "string body", body: `"hello"`, want: "hello"},
		{name: "object body verbatim", body: `{"k":1}`, want: `{"k":1}`},
		{name: "empty body", body: ``, wantNil: true},
		{name: "null body", body: `null`, wantNil: true},
		{name: "invalid json", body: `{bad`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nodeContent(json.RawMessage(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.body)
				}
				return
			}
			if err != nil {
				t.Fatalf("nodeContent: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil content, got %q", got)
				}
				return
			}
			if string(got) != tt.want {
				t.Fatalf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildCapabilities(t *testing.T) {
	caps := buildCapabilities()
	if !caps.WriteToolOwned {
		t.Fatal("expected WriteToolOwned true")
	}
	for _, kind := range ir.AllKinds() {
		level, ok := caps.ConceptKinds[string(kind)]
		if !ok {
			t.Fatalf("kind %q missing from capabilities", kind)
		}
		if level != adapterkit.CapabilitySupported {
			t.Fatalf("kind %q level = %q, want supported", kind, level)
		}
	}
}

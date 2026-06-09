// Package engine is the sync orchestration core: it composes the merged
// primitives (git materialization, IR decode, adapter runtime, merge,
// ledger, staging, atomic swap, orphan deletion, reporting) into one
// end-to-end pipeline. The CLI command layer (sync/validate/status)
// depends on this package; this package depends on the primitives, never
// the other way around.
//
// Invariant #2 (AGENTS.md) is honored here: adapters emit declarative
// ops over the wire (now carrying content via EmitResult.Ops, plan U0),
// and this package performs the actual writes through internal/fsroot
// and internal/merge.
package engine

import (
	"encoding/json"
	"fmt"

	"github.com/aienvs/aienvs/internal/ir"
)

// wireNode is the on-the-wire IR node shape the adapters decode in their
// emit handlers (mirrors the bundled adapters' irNode). Kept local so
// the engine owns the IR→wire contract in one place.
type wireNode struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Version    int             `json:"version,omitempty"`
	Required   bool            `json:"required,omitempty"`
	Targets    []string        `json:"targets,omitempty"`
	Provenance wireProvenance  `json:"provenance,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Assets     []wireAsset     `json:"assets,omitempty"`
}

type wireProvenance struct {
	Path    string `json:"path,omitempty"`
	BlobSHA string `json:"blob_sha,omitempty"`
}

type wireAsset struct {
	RelPath    string          `json:"rel_path"`
	Provenance wireProvenance  `json:"provenance,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
}

type wireDocument struct {
	Nodes []wireNode `json:"nodes"`
}

// MarshalIR renders decoded IR nodes (plus skill assets) into the wire
// IR document an adapter's Emit decodes. Skills supplies asset bundles
// keyed by skill node ID; a skill node with no entry simply ships
// without assets.
//
// Body encoding is symmetric with the adapters' decodeBodyOrPassthrough:
// a body that is a JSON object or array passes through verbatim
// (mcp-server-entry and other structured kinds); any other body is
// encoded as a JSON string (markdown kinds). This round-trips
// byte-for-byte.
func MarshalIR(nodes []ir.Node, skills map[string]ir.Skill) (json.RawMessage, error) {
	doc := wireDocument{Nodes: make([]wireNode, 0, len(nodes))}
	for _, n := range nodes {
		wn := wireNode{
			ID:       n.ID,
			Kind:     string(n.Kind),
			Version:  n.Version,
			Required: n.Required,
			Targets:  n.Targets,
			Provenance: wireProvenance{
				Path:    n.Provenance.Path,
				BlobSHA: n.Provenance.BlobSHA,
			},
			Body: encodeBody(n.Body),
		}
		if n.Kind == ir.KindSkill {
			if sk, ok := skills[n.ID]; ok {
				for _, a := range sk.Assets {
					wn.Assets = append(wn.Assets, wireAsset{
						RelPath: a.RelPath,
						Provenance: wireProvenance{
							Path:    a.Provenance.Path,
							BlobSHA: a.Provenance.BlobSHA,
						},
						Content: encodeBody(a.Content),
					})
				}
			}
		}
		doc.Nodes = append(doc.Nodes, wn)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("engine: marshal IR: %w", err)
	}
	return out, nil
}

// encodeBody picks the wire encoding for a node/asset body. Object- and
// array-shaped JSON passes through unchanged; everything else (markdown,
// plain text, empty) becomes a JSON string. Mirrors the adapter-side
// decodeBodyOrPassthrough (string-decode first, raw value fallback).
func encodeBody(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	if isJSONObjectOrArray(b) {
		// Valid structured JSON — pass through verbatim.
		return json.RawMessage(b)
	}
	s, err := json.Marshal(string(b))
	if err != nil {
		// string marshal of arbitrary bytes never fails, but stay safe.
		return nil
	}
	return s
}

// isJSONObjectOrArray reports whether b is valid JSON whose first
// non-whitespace byte is '{' or '['.
func isJSONObjectOrArray(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return json.Valid(b)
		default:
			return false
		}
	}
	return false
}

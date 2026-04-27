package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aienvs/aienvs/internal/ir"
	"github.com/aienvs/aienvs/pkg/adapterkit"
)

// irNode is the wire-side shape of one IR node delivered via
// EmitParams.IR. The adapter does not import internal/ir's Node
// directly because that type carries decoder-internal fields the
// wire payload doesn't include.
type irNode struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Version    int             `json:"version,omitempty"`
	Required   bool            `json:"required,omitempty"`
	Targets    []string        `json:"targets,omitempty"`
	Provenance irProvenance    `json:"provenance,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Assets     []irAsset       `json:"assets,omitempty"`
}

type irProvenance struct {
	Path    string `json:"path,omitempty"`
	BlobSHA string `json:"blob_sha,omitempty"`
}

type irAsset struct {
	RelPath    string          `json:"rel_path"`
	Provenance irProvenance    `json:"provenance,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
}

type irDocument struct {
	Nodes []irNode `json:"nodes"`
}

// emittedOps accumulates the ops a single emit call produces. Built
// up across kind-specific helpers; rendered into OpsPerformed at the
// end of handleEmit.
type emittedOps struct {
	ops []adapterkit.Op
}

func (e *emittedOps) add(op adapterkit.Op) {
	e.ops = append(e.ops, op)
}

// records returns the OpRecord summary slice the v1 protocol carries
// in EmitResult.OpsPerformed. Content is built but discarded — the
// wire format only carries summaries.
func (e *emittedOps) records() []adapterkit.OpRecord {
	out := make([]adapterkit.OpRecord, 0, len(e.ops))
	for _, op := range e.ops {
		out = append(out, adapterkit.OpRecord{Op: op.OpKind(), Path: op.OpPath()})
	}
	return out
}

// handleEmit is the OnEmit handler the bundled adapter registers.
// Decodes the IR document, dispatches each node by kind, and returns
// the accumulated OpRecord summary.
//
// One-failed-node-fails-the-emit is the v1 behavior: if any node
// produces an error, the whole call returns that error and partially
// emitted ops are discarded. Per-node skip-with-warning is a Unit 8b
// extension.
func handleEmit(_ context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
	doc, err := decodeIRDocument(params.IR)
	if err != nil {
		return adapterkit.EmitResult{}, &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: %s", err.Error()),
		}
	}

	emitted := &emittedOps{}

	// Track which reserved-subdirectory READMEs have been emitted in
	// this call so we don't write them more than once when an IR
	// contains multiple nodes targeting the same subdir.
	readmeEmitted := map[string]bool{}

	// Iterate in a deterministic order (sorted by kind, then id) so
	// op output is stable across runs and easy to compare against
	// fixtures. The IR decoder upstream already sorts the same way,
	// but we re-sort defensively in case a malformed decoder ships
	// nodes in arbitrary order.
	sort.Slice(doc.Nodes, func(i, j int) bool {
		if doc.Nodes[i].Kind != doc.Nodes[j].Kind {
			return doc.Nodes[i].Kind < doc.Nodes[j].Kind
		}
		return doc.Nodes[i].ID < doc.Nodes[j].ID
	})

	for _, node := range doc.Nodes {
		if !nodeTargetsClaude(node) {
			// Node is targeted at a different adapter — silent skip.
			continue
		}
		if err := dispatchNode(emitted, node, readmeEmitted); err != nil {
			return adapterkit.EmitResult{}, err
		}
	}

	return adapterkit.EmitResult{OpsPerformed: emitted.records()}, nil
}

// dispatchNode routes one IR node to its kind-specific emitter. The
// 9.3 wiring covers rule/command/skill; 9.4 wires mcp-server-entry +
// agents-md; 9.5 wires plugin-reference.
func dispatchNode(emitted *emittedOps, node irNode, readmeEmitted map[string]bool) error {
	if !ir.IsValidID(node.ID) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: invalid node id %q", node.ID),
		}
	}
	switch ir.Kind(node.Kind) {
	case ir.KindRule:
		return emitRule(emitted, node, readmeEmitted)
	case ir.KindCommand:
		return emitCommand(emitted, node, readmeEmitted)
	case ir.KindSkill:
		return emitSkill(emitted, node)
	case ir.KindMCPServerEntry:
		return emitMCPServerEntry(emitted, node)
	case ir.KindAgentsMD:
		return emitAgentsMD(emitted, node)
	case ir.KindPluginReference:
		return emitPluginReferenceWarning(emitted, node)
	default:
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: unknown IR kind %q for node %q", node.Kind, node.ID),
		}
	}
}

// decodeIRDocument unmarshals the EmitParams.IR raw message. Returns
// a wrapped error on parse failure so handleEmit can convert it to a
// JSON-RPC InvalidParams response.
func decodeIRDocument(raw json.RawMessage) (irDocument, error) {
	if len(raw) == 0 {
		return irDocument{}, nil
	}
	var doc irDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return irDocument{}, fmt.Errorf("decode IR document: %w", err)
	}
	return doc, nil
}

// nodeTargetsClaude reports whether the node's targets list either
// is empty (means "all adapters") or includes the claude adapter.
// IR-side validation guarantees the contained values are known
// adapter names; we don't re-validate here.
func nodeTargetsClaude(node irNode) bool {
	if len(node.Targets) == 0 {
		return true
	}
	for _, t := range node.Targets {
		if t == adapterName {
			return true
		}
	}
	return false
}

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
//
// Context is checked between nodes so a runtime cancel during a
// large IR is honored without waiting for the full iteration.
func handleEmit(ctx context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
	doc, err := decodeIRDocument(params.IR)
	if err != nil {
		return adapterkit.EmitResult{}, &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: %s", err.Error()),
		}
	}

	// Detect duplicate (kind, id) pairs in the wire payload before
	// emitting. The IR decoder enforces uniqueness on its own output
	// but the wire shape can carry duplicates from a buggy or
	// adversarial producer; emitting the same path twice would let
	// the sync engine silently last-write-wins.
	if err := rejectDuplicateNodes(doc.Nodes); err != nil {
		return adapterkit.EmitResult{}, err
	}

	emitted := &emittedOps{}

	// readmeEmitted dedups per-subdir mkdir+README pairs.
	// sidecarEmitted dedups the .aienvs-managed sidecar (only ever
	// written next to .mcp.json; one per emit regardless of how many
	// mcp-server-entry nodes are present).
	state := emitState{
		readmeEmitted:   map[string]bool{},
		sidecarEmitted:  false,
		emittedFilePath: map[string]struct{}{},
	}

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
		if err := ctx.Err(); err != nil {
			return adapterkit.EmitResult{}, &adapterkit.Error{
				Code:    adapterkit.CodeInternalError,
				Message: fmt.Sprintf("claude: emit cancelled: %s", err.Error()),
			}
		}
		if !nodeTargetsClaude(node) {
			// Node is targeted at a different adapter — silent skip.
			continue
		}
		if err := dispatchNode(emitted, node, &state); err != nil {
			return adapterkit.EmitResult{}, err
		}
	}

	return adapterkit.EmitResult{OpsPerformed: emitted.records()}, nil
}

// emitState carries per-emit dedup tables. Threading one struct
// instead of multiple maps keeps dispatchNode's signature stable as
// new dedup keys appear.
type emitState struct {
	readmeEmitted   map[string]bool
	sidecarEmitted  bool
	emittedFilePath map[string]struct{}
}

// dispatchNode routes one IR node to its kind-specific emitter.
func dispatchNode(emitted *emittedOps, node irNode, state *emitState) error {
	if !ir.IsValidID(node.ID) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("claude: invalid node id %q", node.ID),
		}
	}
	switch ir.Kind(node.Kind) {
	case ir.KindRule:
		return emitRule(emitted, node, state.readmeEmitted)
	case ir.KindCommand:
		return emitCommand(emitted, node, state.readmeEmitted)
	case ir.KindSkill:
		return emitSkill(emitted, node)
	case ir.KindMCPServerEntry:
		return emitMCPServerEntry(emitted, node, state)
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

// rejectDuplicateNodes returns InvalidParams when the wire payload
// contains two or more nodes with the same (kind, id) pair. The
// fail-closed default avoids the silent last-write-wins behavior the
// sync engine would otherwise hit at write time.
func rejectDuplicateNodes(nodes []irNode) error {
	seen := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		key := n.Kind + "/" + n.ID
		if _, dup := seen[key]; dup {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("claude: duplicate node (kind=%q id=%q) in IR payload", n.Kind, n.ID),
			}
		}
		seen[key] = struct{}{}
	}
	return nil
}

// decodeBodyOrPassthrough turns a node or asset Body field into raw
// bytes. The wire shape allows two body encodings:
//   - JSON string (markdown bodies): `"# heading\n..."`
//   - Raw JSON value (json/toml kinds): `{"command":"node",...}`
//
// String-decode is tried first; if that fails the raw JSON value is
// validated and passed through unchanged.
func decodeBodyOrPassthrough(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []byte(s), nil
	}
	var any any
	if err := json.Unmarshal(raw, &any); err != nil {
		return nil, fmt.Errorf("body must be a JSON string or valid JSON value: %w", err)
	}
	return raw, nil
}

// wrapBodyErr converts a node-body decode error into the structured
// JSON-RPC error the caller returns.
func wrapBodyErr(node irNode, err error) error {
	return &adapterkit.Error{
		Code:    adapterkit.CodeInvalidParams,
		Message: fmt.Sprintf("claude: node %q (%s) body: %s", node.ID, node.Kind, err.Error()),
	}
}

// wrapOpErr converts an op-construction error (typically the
// payload-too-large guard inside NewOpWriteFile) into the structured
// JSON-RPC error the caller returns.
func wrapOpErr(node irNode, err error) error {
	return &adapterkit.Error{
		Code:    adapterkit.CodeInvalidParams,
		Message: fmt.Sprintf("claude: node %q (%s) op: %s", node.ID, node.Kind, err.Error()),
	}
}

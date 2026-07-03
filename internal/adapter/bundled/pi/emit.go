package pi

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// irNode is the wire-side shape of one IR node delivered via EmitParams.IR.
// Identical to the claude/cursor/codex adapters' irNode so the four share one
// auditable wire shape.
type irNode struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Version  int      `json:"version,omitempty"`
	Required bool     `json:"required,omitempty"`
	Targets  []string `json:"targets,omitempty"`
	// Description, SourceURL, SourceCommit are additive wire fields (plan
	// U2): the authored frontmatter description and the per-node source
	// override for composed nodes (empty = inherit the session source).
	Description  string          `json:"description,omitempty"`
	SourceURL    string          `json:"source_url,omitempty"`
	SourceCommit string          `json:"source_commit,omitempty"`
	Provenance   irProvenance    `json:"provenance,omitempty"`
	Body         json.RawMessage `json:"body,omitempty"`
	Assets       []irAsset       `json:"assets,omitempty"`
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

// emittedOps accumulates the ops a single emit call produces.
type emittedOps struct {
	ops []adapterkit.Op
}

func (e *emittedOps) add(op adapterkit.Op) {
	e.ops = append(e.ops, op)
}

// records returns the OpRecord summary slice the v1 protocol carries in
// EmitResult.OpsPerformed.
func (e *emittedOps) records() []adapterkit.OpRecord {
	out := make([]adapterkit.OpRecord, 0, len(e.ops))
	for _, op := range e.ops {
		out = append(out, adapterkit.OpRecord{Op: op.OpKind(), Path: op.OpPath()})
	}
	return out
}

// wireOps marshals each accumulated op to its wire envelope for
// EmitResult.Ops, carrying op content to the CLI core (invariant #2).
func (e *emittedOps) wireOps() ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(e.ops))
	for _, op := range e.ops {
		raw, err := json.Marshal(op)
		if err != nil {
			return nil, fmt.Errorf("pi: marshal op %s %q: %w", op.OpKind(), op.OpPath(), err)
		}
		out = append(out, raw)
	}
	return out, nil
}

// handleEmit is the OnEmit handler the bundled adapter registers.
func handleEmit(ctx context.Context, params adapterkit.EmitParams, scope, sourceURL, sourceCommit string) (adapterkit.EmitResult, error) {
	doc, err := decodeIRDocument(params.IR)
	if err != nil {
		return adapterkit.EmitResult{}, &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("pi: %s", err.Error()),
		}
	}

	if err := rejectDuplicateNodes(doc.Nodes); err != nil {
		return adapterkit.EmitResult{}, err
	}

	emitted := &emittedOps{}
	state := emitState{
		sourceURL:       sourceURL,
		sourceCommit:    sourceCommit,
		emittedFilePath: map[string]struct{}{},
		paths:           resolvePathSet(scope),
	}

	// Deterministic order (sorted by kind, then id) so op output is stable.
	slices.SortFunc(doc.Nodes, func(a, b irNode) int {
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})

	for _, node := range doc.Nodes {
		if err := ctx.Err(); err != nil {
			return adapterkit.EmitResult{}, &adapterkit.Error{
				Code:    adapterkit.CodeInternalError,
				Message: fmt.Sprintf("pi: emit cancelled: %s", err.Error()),
			}
		}
		if !nodeTargetsPi(node) {
			continue
		}
		if err := dispatchNode(emitted, node, &state); err != nil {
			return adapterkit.EmitResult{}, err
		}
	}

	wire, err := emitted.wireOps()
	if err != nil {
		return adapterkit.EmitResult{}, &adapterkit.Error{
			Code:    adapterkit.CodeInternalError,
			Message: err.Error(),
		}
	}
	return adapterkit.EmitResult{OpsPerformed: emitted.records(), Ops: wire}, nil
}

// emitState carries per-emit dedup state and the scope-resolved paths.
// emittedFilePath records every workspace-relative path emitted as a write_file
// so the dispatcher fails closed instead of letting the sync engine
// last-write-wins. paths hold the scope-resolved tool-owned destinations (only
// agents-md is scope-dependent).
type emitState struct {
	// sourceURL / sourceCommit are the session-level source identity from
	// InitializeParams (audit-safe canonical URL or local path + resolved
	// SHA), used to render the managed header. A node-level override
	// (composed nodes) wins over these — see prependHeader.
	sourceURL    string
	sourceCommit string

	emittedFilePath map[string]struct{}
	paths           pathSet
}

// recordWritePath registers a write_file path in the per-emit dedup table.
func (s *emitState) recordWritePath(path string) error {
	if _, exists := s.emittedFilePath[path]; exists {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("pi: duplicate write_file path %q in single emit", path),
		}
	}
	s.emittedFilePath[path] = struct{}{}
	return nil
}

// dispatchNode routes one IR node to its kind-specific emitter.
//
// Supported kinds (agents-md, skill) produce ops; unsupported kinds
// (mcp-server-entry, rule, command, plugin-reference) produce a degradation
// warning and emit no files. The capability matrix in capabilities.go is the
// authoritative source for which kinds are supported; this switch must stay in
// agreement with it. (command is planned — see capabilities.yaml.)
func dispatchNode(emitted *emittedOps, node irNode, state *emitState) error {
	if !ir.IsValidID(node.ID) {
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("pi: invalid node id %q", node.ID),
		}
	}
	switch ir.Kind(node.Kind) {
	case ir.KindAgentsMD:
		return emitAgentsMD(emitted, node, state)
	case ir.KindSkill:
		return emitSkill(emitted, node, state)
	case ir.KindMCPServerEntry, ir.KindRule, ir.KindCommand, ir.KindPluginReference:
		return emitUnsupportedWarning(emitted, node)
	default:
		return &adapterkit.Error{
			Code:    adapterkit.CodeInvalidParams,
			Message: fmt.Sprintf("pi: unknown IR kind %q for node %q", node.Kind, node.ID),
		}
	}
}

// decodeIRDocument unmarshals the EmitParams.IR raw message.
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

// nodeTargetsPi reports whether the node's targets list is empty (means "all
// adapters") or includes the pi adapter.
func nodeTargetsPi(node irNode) bool {
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

// rejectDuplicateNodes returns InvalidParams when the wire payload contains two
// or more nodes with the same (kind, id) pair.
func rejectDuplicateNodes(nodes []irNode) error {
	seen := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		key := n.Kind + "/" + n.ID
		if _, dup := seen[key]; dup {
			return &adapterkit.Error{
				Code:    adapterkit.CodeInvalidParams,
				Message: fmt.Sprintf("pi: duplicate node (kind=%q id=%q) in IR payload", n.Kind, n.ID),
			}
		}
		seen[key] = struct{}{}
	}
	return nil
}

// decodeBodyOrPassthrough turns a node or asset Body field into raw bytes.
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

// wrapBodyErr converts a node-body decode error into a structured JSON-RPC error.
func wrapBodyErr(node irNode, err error) error {
	return &adapterkit.Error{
		Code:    adapterkit.CodeInvalidParams,
		Message: fmt.Sprintf("pi: node %q (%s) body: %s", node.ID, node.Kind, err.Error()),
	}
}

// wrapOpErr converts an op-construction error into a structured JSON-RPC error.
func wrapOpErr(node irNode, err error) error {
	return &adapterkit.Error{
		Code:    adapterkit.CodeInvalidParams,
		Message: fmt.Sprintf("pi: node %q (%s) op: %s", node.ID, node.Kind, err.Error()),
	}
}

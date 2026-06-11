package contract

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Method-name constants. The wire string is part of the contract.
const (
	MethodInitialize  = "initialize"
	MethodInitialized = "initialized"
	MethodEmit        = "emit"
	MethodShutdown    = "shutdown"
)

// MaxOpPayloadBytes caps the decoded byte length of any single op
// payload. The cap matches the parent plan's 8 MiB default. Adapters
// can negotiate higher caps via capabilities in PR 2's adapter.yaml,
// but the type-level constant is the static ceiling and the wire
// floor — a hostile peer cannot slip a larger frame through via base64
// expansion because the cap is applied to *decoded* bytes.
const MaxOpPayloadBytes = 8 * 1024 * 1024

// CapabilityLevel is the supported / partial / unsupported tri-state
// adapters declare per concept kind.
type CapabilityLevel string

const (
	CapabilitySupported   CapabilityLevel = "supported"
	CapabilityPartial     CapabilityLevel = "partial"
	CapabilityUnsupported CapabilityLevel = "unsupported"
)

// OutputMode describes how an adapter declares ownership of an output
// path. owned-subdir means the adapter owns the entire subtree;
// tool-owned-entry means the adapter owns a specific entry inside a
// tool-owned file (an MCP server entry, an AGENTS.md section, etc.);
// shared-subdir means the adapter shares the directory with the user and
// other tools (e.g. .agents/skills) and the engine manages only the
// agent-sync-owned leaf entries within it, never the shared parent.
type OutputMode string

const (
	OutputModeOwnedSubdir    OutputMode = "owned-subdir"
	OutputModeToolOwnedEntry OutputMode = "tool-owned-entry"
	OutputModeSharedSubdir   OutputMode = "shared-subdir"
)

// ToolOwnedKind names the structural locator scheme for write_tool_owned
// ops. Tied to the OutputMode tool-owned-entry mode.
type ToolOwnedKind string

const (
	ToolOwnedKindJSONPointer     ToolOwnedKind = "json-pointer"
	ToolOwnedKindTOMLPath        ToolOwnedKind = "toml-path"
	ToolOwnedKindMarkdownSection ToolOwnedKind = "markdown-section"
)

// WarningStatus classifies an OpWarning as degraded (something the
// adapter could partially handle) or partial (capability-matrix shortfall
// surfaced as data, not as failure).
type WarningStatus string

const (
	WarningStatusDegraded WarningStatus = "degraded"
	WarningStatusPartial  WarningStatus = "partial"
)

// Encoding is the wire-only encoding of an op payload.
// The Go API never exposes this directly — NewOpWriteFile picks the
// encoding automatically based on whether the content is valid UTF-8.
type Encoding string

const (
	EncodingUTF8   Encoding = "utf8"
	EncodingBase64 Encoding = "base64"
)

// OpKind names the op-discriminator value carried in every op envelope.
// The closed v1 set is intentionally small; symlink and any future ops
// land in 8b under capability negotiation.
type OpKind string

const (
	OpKindWriteFile      OpKind = "write_file"
	OpKindWriteToolOwned OpKind = "write_tool_owned"
	OpKindMkdir          OpKind = "mkdir"
	OpKindDelete         OpKind = "delete"
	OpKindWarning        OpKind = "warning"
)

// AllOpKinds returns the closed v1 set in stable order. Mirrors
// internal/ir.AllKinds() — useful for capability iteration and
// schema parity tests.
func AllOpKinds() []OpKind {
	return []OpKind{
		OpKindWriteFile,
		OpKindWriteToolOwned,
		OpKindMkdir,
		OpKindDelete,
		OpKindWarning,
	}
}

// Op-level sentinel errors. Callers branch with errors.Is.
var (
	// ErrPayloadTooLarge is returned by NewOpWriteFile when the content
	// exceeds MaxOpPayloadBytes, and by DecodeOp when a base64-encoded
	// payload's decoded length exceeds the cap.
	ErrPayloadTooLarge = errors.New("contract: op payload exceeds MaxOpPayloadBytes")

	// ErrUnknownOp is returned by DecodeOp when the wire op kind is not
	// in the v1 closed set, or the "op" discriminator is missing.
	ErrUnknownOp = errors.New("contract: unknown or missing op kind")

	// ErrMissingEncoding is returned when a content-bearing op
	// (write_file, write_tool_owned) is decoded without an "encoding"
	// field. The wire schemas mark encoding as required; accepting
	// omission would let schema-invalid ops pass Go-side decoding.
	ErrMissingEncoding = errors.New("contract: op missing required encoding field")
)

// InitializeParams is the params object on the initialize request.
type InitializeParams struct {
	Client           string          `json:"client"`
	ProtocolVersions []string        `json:"protocol_versions"`
	Cookie           string          `json:"cookie"`
	WorkspaceRoot    string          `json:"workspace_root"`
	ReservedPrefix   string          `json:"reserved_prefix"`
	IRVersion        string          `json:"ir_version"`
	Meta             json.RawMessage `json:"_meta,omitempty"`
}

// InitializeResult is the result object the adapter returns from
// initialize.
//
// ProtocolVersion is the version the adapter is committing to speak for
// this session. It must be one of the strings the CLI offered in
// InitializeParams.ProtocolVersions; the runtime in PR 2 enforces this
// and returns ErrorClassAdapterProtocolMismatch on mismatch.
type InitializeResult struct {
	Server          string           `json:"server"`
	ProtocolVersion string           `json:"protocol_version"`
	Capabilities    Capabilities     `json:"capabilities"`
	DeclaredOutputs []DeclaredOutput `json:"declared_outputs"`
	// Echoed magic cookie value; runtime validates against the per-spawn
	// AGENT_SYNC_ADAPTER_COOKIE.
	Cookie string          `json:"cookie,omitempty"`
	Meta   json.RawMessage `json:"_meta,omitempty"`
}

// Capabilities is the additive extension point for v1. New fields land
// here without bumping the protocol version. The parent plan's "freeze
// the wire frame, grow capabilities" rule is the load-bearing invariant.
type Capabilities struct {
	ConceptKinds   map[string]CapabilityLevel `json:"concept_kinds"`
	WriteToolOwned bool                       `json:"write_tool_owned"`
	Progress       bool                       `json:"progress"`
	Meta           json.RawMessage            `json:"_meta,omitempty"`
}

// DeclaredOutput names a path the adapter intends to write. The CLI
// runtime in PR 2 rejects any emit op that names a path outside the
// declared set (Bazel declare_file pattern).
//
// JSONPointer holds an RFC 6901 JSON Pointer (e.g., "/mcpServers/echo")
// when Mode is OutputModeToolOwnedEntry and the tool-owned file is
// JSON-shaped. Aligns with ToolOwnedKindJSONPointer on OpWriteToolOwned
// — both fields use the same locator standard.
type DeclaredOutput struct {
	Path        string          `json:"path"`
	Mode        OutputMode      `json:"mode"`
	JSONPointer *string         `json:"json_pointer,omitempty"`
	SectionID   *string         `json:"section_id,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
}

// EmitParams is the params object on the emit request. The IR is held
// as json.RawMessage to avoid coupling this package up to internal/ir
// — PR 2's runtime is responsible for the typed conversion.
type EmitParams struct {
	Target string          `json:"target"`
	IR     json.RawMessage `json:"ir"`
	Meta   json.RawMessage `json:"_meta,omitempty"`
}

// EmitResult is the final response to an emit request: the ops the
// adapter performed, in the order it streamed them.
//
// Ops carries the full op envelopes (each marshaled via the per-op wire
// form, decodable with DecodeOp) so the CLI core can perform the actual
// writes — invariant #2 ("adapters never write files directly"). It is
// an additive field under the "freeze the wire frame, grow capabilities"
// policy: older adapters omit it and it decodes to nil. OpsPerformed
// remains the {kind, path} summary the declared-outputs and
// capability-lied gates run against, in the same order as Ops.
type EmitResult struct {
	OpsPerformed []OpRecord        `json:"ops_performed"`
	Ops          []json.RawMessage `json:"ops,omitempty"`
	Meta         json.RawMessage   `json:"_meta,omitempty"`
}

// OpRecord is a minimal {kind, path} record of one performed op.
// Lets the CLI summarize work without re-decoding the streamed ops.
type OpRecord struct {
	Op   OpKind `json:"op"`
	Path string `json:"path"`
}

// ShutdownParams is the params object on the shutdown request. Empty in
// MVP; reserved for future structured shutdown reasons.
type ShutdownParams struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// ShutdownResult is the response to shutdown. Empty in MVP.
type ShutdownResult struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// Op is the interface every concrete op satisfies. OpKind names the
// discriminator carried on the wire; OpPath names the target path on
// path-bearing ops (every v1 op except OpWarning, which is concept-
// level and returns the empty string from OpPath()).
type Op interface {
	OpKind() OpKind
	OpPath() string

	isOp() // sentinel to keep external types from satisfying Op
}

// OpWriteFile writes a fully-owned file. Encoding is the wire-only
// concern; callers see Content as raw []byte.
type OpWriteFile struct {
	Path    string
	Mode    uint32
	Content []byte
	Meta    json.RawMessage
}

func (OpWriteFile) OpKind() OpKind   { return OpKindWriteFile }
func (o OpWriteFile) OpPath() string { return o.Path }
func (OpWriteFile) isOp()            {}

// opWriteFileWire is the on-the-wire form. Content is base64 or utf8
// per the encoding field; the Op-level Go type hides the choice.
type opWriteFileWire struct {
	Op       OpKind          `json:"op"`
	Path     string          `json:"path"`
	Mode     uint32          `json:"mode"`
	Encoding Encoding        `json:"encoding"`
	Content  string          `json:"content"`
	Meta     json.RawMessage `json:"_meta,omitempty"`
}

// MarshalJSON picks UTF-8 if the content is valid, base64 otherwise.
// Most adapters write text; the binary path exists for skill assets.
//
// Enforces MaxOpPayloadBytes at encode time so a directly-constructed
// op (bypassing NewOpWriteFile) cannot smuggle an oversized payload onto
// the wire. The receiver also enforces the cap on decode.
func (o OpWriteFile) MarshalJSON() ([]byte, error) {
	if len(o.Content) > MaxOpPayloadBytes {
		return nil, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(o.Content), MaxOpPayloadBytes)
	}
	encoding, content := encodeOpContent(o.Content)
	return json.Marshal(opWriteFileWire{
		Op:       OpKindWriteFile,
		Path:     o.Path,
		Mode:     o.Mode,
		Encoding: encoding,
		Content:  content,
		Meta:     o.Meta,
	})
}

func (o *OpWriteFile) UnmarshalJSON(data []byte) error {
	var w opWriteFileWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("contract: unmarshal write_file: %w", err)
	}
	content, err := decodeOpContent(w.Encoding, w.Content)
	if err != nil {
		return err
	}
	if len(content) > MaxOpPayloadBytes {
		return fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(content), MaxOpPayloadBytes)
	}
	o.Path = w.Path
	o.Mode = w.Mode
	o.Content = content
	o.Meta = w.Meta
	return nil
}

// NewOpWriteFile validates payload size and returns a ready-to-marshal
// op. The encoding is decided at marshal time, not here, so the Go API
// stays bytes-only.
func NewOpWriteFile(path string, mode uint32, content []byte) (OpWriteFile, error) {
	if len(content) > MaxOpPayloadBytes {
		return OpWriteFile{}, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(content), MaxOpPayloadBytes)
	}
	return OpWriteFile{Path: path, Mode: mode, Content: content}, nil
}

// OpWriteToolOwned writes a single entry inside a tool-owned file
// (e.g., an MCP server entry in .mcp.json).
type OpWriteToolOwned struct {
	Path    string
	Kind    ToolOwnedKind
	Locator string
	Content []byte
	Meta    json.RawMessage
}

func (OpWriteToolOwned) OpKind() OpKind   { return OpKindWriteToolOwned }
func (o OpWriteToolOwned) OpPath() string { return o.Path }
func (OpWriteToolOwned) isOp()            {}

type opWriteToolOwnedWire struct {
	Op       OpKind          `json:"op"`
	Path     string          `json:"path"`
	Kind     ToolOwnedKind   `json:"kind"`
	Locator  string          `json:"locator"`
	Encoding Encoding        `json:"encoding"`
	Content  string          `json:"content"`
	Meta     json.RawMessage `json:"_meta,omitempty"`
}

func (o OpWriteToolOwned) MarshalJSON() ([]byte, error) {
	if len(o.Content) > MaxOpPayloadBytes {
		return nil, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(o.Content), MaxOpPayloadBytes)
	}
	encoding, content := encodeOpContent(o.Content)
	return json.Marshal(opWriteToolOwnedWire{
		Op:       OpKindWriteToolOwned,
		Path:     o.Path,
		Kind:     o.Kind,
		Locator:  o.Locator,
		Encoding: encoding,
		Content:  content,
		Meta:     o.Meta,
	})
}

func (o *OpWriteToolOwned) UnmarshalJSON(data []byte) error {
	var w opWriteToolOwnedWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("contract: unmarshal write_tool_owned: %w", err)
	}
	content, err := decodeOpContent(w.Encoding, w.Content)
	if err != nil {
		return err
	}
	if len(content) > MaxOpPayloadBytes {
		return fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(content), MaxOpPayloadBytes)
	}
	o.Path = w.Path
	o.Kind = w.Kind
	o.Locator = w.Locator
	o.Content = content
	o.Meta = w.Meta
	return nil
}

// OpMkdir creates a directory. Mode is interpreted POSIX-style; on
// Windows the file system layer maps it to the closest equivalent.
type OpMkdir struct {
	Path string          `json:"path"`
	Mode uint32          `json:"mode"`
	Meta json.RawMessage `json:"_meta,omitempty"`
}

func (OpMkdir) OpKind() OpKind   { return OpKindMkdir }
func (o OpMkdir) OpPath() string { return o.Path }
func (OpMkdir) isOp()            {}

type opMkdirWire struct {
	Op   OpKind          `json:"op"`
	Path string          `json:"path"`
	Mode uint32          `json:"mode"`
	Meta json.RawMessage `json:"_meta,omitempty"`
}

func (o OpMkdir) MarshalJSON() ([]byte, error) {
	return json.Marshal(opMkdirWire{Op: OpKindMkdir, Path: o.Path, Mode: o.Mode, Meta: o.Meta})
}

func (o *OpMkdir) UnmarshalJSON(data []byte) error {
	var w opMkdirWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("contract: unmarshal mkdir: %w", err)
	}
	o.Path = w.Path
	o.Mode = w.Mode
	o.Meta = w.Meta
	return nil
}

// OpDelete removes a file or directory inside a declared output path.
type OpDelete struct {
	Path string          `json:"path"`
	Meta json.RawMessage `json:"_meta,omitempty"`
}

func (OpDelete) OpKind() OpKind   { return OpKindDelete }
func (o OpDelete) OpPath() string { return o.Path }
func (OpDelete) isOp()            {}

type opDeleteWire struct {
	Op   OpKind          `json:"op"`
	Path string          `json:"path"`
	Meta json.RawMessage `json:"_meta,omitempty"`
}

func (o OpDelete) MarshalJSON() ([]byte, error) {
	return json.Marshal(opDeleteWire{Op: OpKindDelete, Path: o.Path, Meta: o.Meta})
}

func (o *OpDelete) UnmarshalJSON(data []byte) error {
	var w opDeleteWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("contract: unmarshal delete: %w", err)
	}
	o.Path = w.Path
	o.Meta = w.Meta
	return nil
}

// OpWarning surfaces a non-fatal capability/data shortfall that the
// CLI includes in its sync report. Path is the empty string — warnings
// are concept-level, not path-level — but Op satisfies OpPath() with "".
type OpWarning struct {
	ConceptID string          `json:"concept_id"`
	Status    WarningStatus   `json:"status"`
	Note      string          `json:"note"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

func (OpWarning) OpKind() OpKind { return OpKindWarning }
func (OpWarning) OpPath() string { return "" }
func (OpWarning) isOp()          {}

type opWarningWire struct {
	Op        OpKind          `json:"op"`
	ConceptID string          `json:"concept_id"`
	Status    WarningStatus   `json:"status"`
	Note      string          `json:"note"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

func (o OpWarning) MarshalJSON() ([]byte, error) {
	return json.Marshal(opWarningWire{
		Op: OpKindWarning, ConceptID: o.ConceptID, Status: o.Status, Note: o.Note, Meta: o.Meta,
	})
}

func (o *OpWarning) UnmarshalJSON(data []byte) error {
	var w opWarningWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("contract: unmarshal warning: %w", err)
	}
	o.ConceptID = w.ConceptID
	o.Status = w.Status
	o.Note = w.Note
	o.Meta = w.Meta
	return nil
}

// MarshalOp encodes any concrete op to its wire form. Thin convenience
// over json.Marshal, kept symmetric with DecodeOp.
func MarshalOp(op Op) ([]byte, error) {
	return json.Marshal(op)
}

// opDiscriminator is the minimal struct used to peek at the wire op
// kind before dispatching to the concrete type.
type opDiscriminator struct {
	Op OpKind `json:"op"`
}

// DecodeOp reads the "op" discriminator and returns the matching
// concrete op. Returns ErrUnknownOp for unknown kinds (including the
// intentionally-deferred symlink op) and missing discriminators.
func DecodeOp(data []byte) (Op, error) {
	var d opDiscriminator
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("contract: peek op kind: %w", err)
	}
	switch d.Op {
	case OpKindWriteFile:
		var op OpWriteFile
		if err := json.Unmarshal(data, &op); err != nil {
			return nil, err
		}
		return op, nil
	case OpKindWriteToolOwned:
		var op OpWriteToolOwned
		if err := json.Unmarshal(data, &op); err != nil {
			return nil, err
		}
		return op, nil
	case OpKindMkdir:
		var op OpMkdir
		if err := json.Unmarshal(data, &op); err != nil {
			return nil, err
		}
		return op, nil
	case OpKindDelete:
		var op OpDelete
		if err := json.Unmarshal(data, &op); err != nil {
			return nil, err
		}
		return op, nil
	case OpKindWarning:
		var op OpWarning
		if err := json.Unmarshal(data, &op); err != nil {
			return nil, err
		}
		return op, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownOp, d.Op)
	}
}

// encodeOpContent picks the wire encoding for op payload bytes. UTF-8
// for valid text, base64 for everything else. Symmetric with
// decodeOpContent so the encode side and the decode side cannot drift.
func encodeOpContent(content []byte) (Encoding, string) {
	if utf8.Valid(content) {
		return EncodingUTF8, string(content)
	}
	return EncodingBase64, base64.StdEncoding.EncodeToString(content)
}

// decodeOpContent inverts the encoding picked by encodeOpContent.
//
// An empty encoding string is rejected as ErrMissingEncoding rather
// than silently treated as utf8 — the wire schemas mark "encoding" as
// required, and accepting omission would let schema-invalid ops slip
// past Go-side decoding.
func decodeOpContent(encoding Encoding, content string) ([]byte, error) {
	switch encoding {
	case EncodingUTF8:
		return []byte(content), nil
	case EncodingBase64:
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("contract: base64 decode op content: %w", err)
		}
		return decoded, nil
	case "":
		return nil, ErrMissingEncoding
	default:
		return nil, fmt.Errorf("contract: unknown encoding %q", encoding)
	}
}

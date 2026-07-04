package adapterkit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	ContractVersionV1 = "agent-sync/v1"

	MethodInitialize  = "initialize"
	MethodInitialized = "initialized"
	MethodEmit        = "emit"
	MethodShutdown    = "shutdown"

	JSONRPCVersion = "2.0"

	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	CodeServerErrorMin = -32099
	CodeServerErrorMax = -32000

	MaxOpPayloadBytes = 8 * 1024 * 1024
)

type CapabilityLevel string

const (
	CapabilitySupported   CapabilityLevel = "supported"
	CapabilityPartial     CapabilityLevel = "partial"
	CapabilityUnsupported CapabilityLevel = "unsupported"
)

type OutputMode string

const (
	OutputModeOwnedSubdir    OutputMode = "owned-subdir"
	OutputModeToolOwnedEntry OutputMode = "tool-owned-entry"
	// OutputModeSharedSubdir is a directory the adapter shares with the user
	// and other tools (e.g. the cross-tool .agents/skills tree). The engine
	// manages only the agent-sync-owned leaf entries within it (swapped per
	// leaf), never the shared parent — so foreign sibling entries are never
	// touched. Contrast owned-subdir, where the adapter owns the entire subtree.
	OutputModeSharedSubdir OutputMode = "shared-subdir"
	// OutputModeFileLeaf is a flat directory the adapter shares with the user
	// (e.g. .cursor/commands, .pi/prompts), where the adapter owns only the
	// individual direct-child files it emits — not the directory, and not any
	// subtree. The declared Path is the shared parent dir; ownership is per-file,
	// derived from emitted op paths and the ledger. Unlike shared-subdir (whose
	// unit is an agent-sync-<id> leaf DIRECTORY), the file-leaf unit is a single
	// FILE: it is written via the atomic single-file path (no directory swap),
	// drift is checked per-file (the shared parent is never walked), and orphan
	// reclaim deletes only the specific files. Foreign sibling files are never
	// touched; a pre-existing unmanaged file at an exact target path fails closed
	// (adoptable) rather than being clobbered.
	OutputModeFileLeaf OutputMode = "file-leaf"
)

type ToolOwnedKind string

const (
	ToolOwnedKindJSONPointer     ToolOwnedKind = "json-pointer"
	ToolOwnedKindTOMLPath        ToolOwnedKind = "toml-path"
	ToolOwnedKindMarkdownSection ToolOwnedKind = "markdown-section"
)

type WarningStatus string

const (
	WarningStatusDegraded WarningStatus = "degraded"
	WarningStatusPartial  WarningStatus = "partial"
)

type Encoding string

const (
	EncodingUTF8   Encoding = "utf8"
	EncodingBase64 Encoding = "base64"
)

type OpKind string

const (
	OpKindWriteFile      OpKind = "write_file"
	OpKindWriteToolOwned OpKind = "write_tool_owned"
	OpKindMkdir          OpKind = "mkdir"
	OpKindDelete         OpKind = "delete"
	OpKindWarning        OpKind = "warning"
)

func AllOpKinds() []OpKind {
	return []OpKind{
		OpKindWriteFile,
		OpKindWriteToolOwned,
		OpKindMkdir,
		OpKindDelete,
		OpKindWarning,
	}
}

type ErrorClass string

const (
	ErrorClassAdapterProtocolOrder    ErrorClass = "adapter-protocol-order"
	ErrorClassAdapterPanic            ErrorClass = "adapter-panic"
	ErrorClassAdapterTimeout          ErrorClass = "adapter-timeout"
	ErrorClassAdapterProtocolMismatch ErrorClass = "adapter-protocol-mismatch"
	ErrorClassAdapterUndeclaredOutput ErrorClass = "adapter-undeclared-output"
	ErrorClassAdapterExecDenied       ErrorClass = "adapter-exec-denied"
	ErrorClassAdapterCapabilityLied   ErrorClass = "adapter-capability-lied"
)

var (
	ErrPayloadTooLarge = errors.New("adapterkit: op payload exceeds MaxOpPayloadBytes")
	ErrUnknownOp       = errors.New("adapterkit: unknown or missing op kind")
	ErrMissingEncoding = errors.New("adapterkit: op missing required encoding field")
)

type InitializeParams struct {
	Client           string   `json:"client"`
	ProtocolVersions []string `json:"protocol_versions"`
	Cookie           string   `json:"cookie"`
	WorkspaceRoot    string   `json:"workspace_root"`
	ReservedPrefix   string   `json:"reserved_prefix"`
	IRVersion        string   `json:"ir_version"`
	// Scope is the hierarchy level this emit targets: "user", "project",
	// or "directory". It lets an adapter choose scope-appropriate output
	// paths (e.g. the Claude adapter writes ~/.claude/CLAUDE.md at user
	// scope vs ./CLAUDE.md at project scope). Additive and optional under
	// the "freeze the frame, grow capabilities" policy: an adapter that
	// ignores it, or an absent value, MUST be treated as "project".
	Scope string `json:"scope,omitempty"`
	// SourceURL identifies the canonical source of this session's IR:
	// the credential-stripped canonical git URL or the path string for
	// local sources. Additive and optional under the "freeze the frame,
	// grow capabilities" policy: an adapter that ignores it, or an
	// absent value, behaves exactly as before the field existed.
	SourceURL string `json:"source_url,omitempty"`
	// SourceCommit is the resolved canonical commit SHA the IR was
	// decoded at. Additive and optional; absent for working-tree
	// (local_dir) sources.
	SourceCommit string          `json:"source_commit,omitempty"`
	Meta         json.RawMessage `json:"_meta,omitempty"`
}

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

type Capabilities struct {
	ConceptKinds   map[string]CapabilityLevel `json:"concept_kinds"`
	WriteToolOwned bool                       `json:"write_tool_owned"`
	Progress       bool                       `json:"progress"`
	Meta           json.RawMessage            `json:"_meta,omitempty"`
}

type DeclaredOutput struct {
	Path        string          `json:"path"`
	Mode        OutputMode      `json:"mode"`
	JSONPointer *string         `json:"json_pointer,omitempty"`
	SectionID   *string         `json:"section_id,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
}

type EmitParams struct {
	Target string          `json:"target"`
	IR     json.RawMessage `json:"ir"`
	Meta   json.RawMessage `json:"_meta,omitempty"`
}

type EmitResult struct {
	OpsPerformed []OpRecord        `json:"ops_performed"`
	Ops          []json.RawMessage `json:"ops,omitempty"`
	Meta         json.RawMessage   `json:"_meta,omitempty"`
}

type OpRecord struct {
	Op   OpKind `json:"op"`
	Path string `json:"path"`
}

type ShutdownParams struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type ShutdownResult struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type Op interface {
	OpKind() OpKind
	OpPath() string

	isOp()
}

type OpWriteFile struct {
	Path    string
	Mode    uint32
	Content []byte
	Meta    json.RawMessage
}

func (OpWriteFile) OpKind() OpKind   { return OpKindWriteFile }
func (o OpWriteFile) OpPath() string { return o.Path }
func (OpWriteFile) isOp()            {}

type opWriteFileWire struct {
	Op       OpKind          `json:"op"`
	Path     string          `json:"path"`
	Mode     uint32          `json:"mode"`
	Encoding Encoding        `json:"encoding"`
	Content  string          `json:"content"`
	Meta     json.RawMessage `json:"_meta,omitempty"`
}

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
		return fmt.Errorf("adapterkit: unmarshal write_file: %w", err)
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

func NewOpWriteFile(path string, mode uint32, content []byte) (OpWriteFile, error) {
	if len(content) > MaxOpPayloadBytes {
		return OpWriteFile{}, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(content), MaxOpPayloadBytes)
	}
	return OpWriteFile{Path: path, Mode: mode, Content: content}, nil
}

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
		return fmt.Errorf("adapterkit: unmarshal write_tool_owned: %w", err)
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
		return fmt.Errorf("adapterkit: unmarshal mkdir: %w", err)
	}
	o.Path = w.Path
	o.Mode = w.Mode
	o.Meta = w.Meta
	return nil
}

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
		return fmt.Errorf("adapterkit: unmarshal delete: %w", err)
	}
	o.Path = w.Path
	o.Meta = w.Meta
	return nil
}

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
		return fmt.Errorf("adapterkit: unmarshal warning: %w", err)
	}
	o.ConceptID = w.ConceptID
	o.Status = w.Status
	o.Note = w.Note
	o.Meta = w.Meta
	return nil
}

func MarshalOp(op Op) ([]byte, error) {
	return json.Marshal(op)
}

type opDiscriminator struct {
	Op OpKind `json:"op"`
}

func DecodeOp(data []byte) (Op, error) {
	var d opDiscriminator
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("adapterkit: peek op kind: %w", err)
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

type Error struct {
	Code    int
	Message string
	Data    ErrorData
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

type ErrorData struct {
	ErrorClass ErrorClass      `json:"error_class,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

type errorWire struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e Error) MarshalJSON() ([]byte, error) {
	wire := errorWire{
		Code:    e.Code,
		Message: e.Message,
	}
	if e.Data.ErrorClass != "" || len(e.Data.Detail) != 0 {
		data, err := json.Marshal(e.Data)
		if err != nil {
			return nil, fmt.Errorf("adapterkit: marshal error data: %w", err)
		}
		wire.Data = data
	}
	return json.Marshal(wire)
}

func (e *Error) UnmarshalJSON(data []byte) error {
	var wire errorWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("adapterkit: unmarshal error: %w", err)
	}
	e.Code = wire.Code
	e.Message = wire.Message
	e.Data = ErrorData{}
	if len(wire.Data) != 0 && !bytes.Equal(wire.Data, []byte("null")) {
		if err := json.Unmarshal(wire.Data, &e.Data); err != nil {
			return fmt.Errorf("adapterkit: unmarshal error data: %w", err)
		}
	}
	return nil
}

func encodeOpContent(content []byte) (Encoding, string) {
	if utf8.Valid(content) {
		return EncodingUTF8, string(content)
	}
	return EncodingBase64, base64.StdEncoding.EncodeToString(content)
}

func decodeOpContent(encoding Encoding, content string) ([]byte, error) {
	switch encoding {
	case EncodingUTF8:
		return []byte(content), nil
	case EncodingBase64:
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("adapterkit: decode base64 content: %w", err)
		}
		return decoded, nil
	case "":
		return nil, ErrMissingEncoding
	default:
		return nil, fmt.Errorf("adapterkit: unknown encoding %q", encoding)
	}
}

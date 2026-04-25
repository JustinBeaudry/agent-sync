package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

// JSONRPCVersion is the literal string every aienvs/v1 envelope carries
// in its "jsonrpc" field. Validated on parse; emitted on marshal.
const JSONRPCVersion = "2.0"

// JSON-RPC 2.0 standard error codes. We intentionally do not define the
// LSP extension codes (-32800 RequestCancelled, -32801 ContentModified,
// -32803 RequestFailed) — those are deferred to Unit 8b and require
// capability negotiation before they can be used.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// CodeServerErrorMin and CodeServerErrorMax bound the JSON-RPC 2.0
	// "implementation-defined server-error" range. Adapters and the CLI
	// runtime may use any code in [Min, Max] for server-side failures
	// classified by the data.error_class field.
	CodeServerErrorMin = -32099
	CodeServerErrorMax = -32000
)

// ErrorClass is the CLI-side classifier carried in error responses'
// data.error_class field. The wire string is part of the contract;
// renaming any of these breaks every existing adapter.
type ErrorClass string

const (
	ErrorClassAdapterPanic            ErrorClass = "adapter-panic"
	ErrorClassAdapterTimeout          ErrorClass = "adapter-timeout"
	ErrorClassAdapterProtocolMismatch ErrorClass = "adapter-protocol-mismatch"
	ErrorClassAdapterUndeclaredOutput ErrorClass = "adapter-undeclared-output"
	ErrorClassAdapterExecDenied       ErrorClass = "adapter-exec-denied"
	ErrorClassAdapterCapabilityLied   ErrorClass = "adapter-capability-lied"
)

// Envelope-level sentinel errors. Callers branch with errors.Is.
var (
	// ErrInvalidEnvelope is returned by ParseMessage when the inbound
	// frame is missing or has the wrong "jsonrpc" field, has both
	// "result" and "error" set on a response, or has neither method
	// nor result/error.
	ErrInvalidEnvelope = errors.New("contract: invalid JSON-RPC envelope")

	// ErrResponseHasResultAndError is returned by Response.MarshalJSON
	// when both Result and Error are set. JSON-RPC 2.0 forbids a response
	// from carrying both.
	ErrResponseHasResultAndError = errors.New("contract: response has both result and error")

	// ErrResponseEmpty is returned by Response.MarshalJSON when neither
	// Result nor Error is set. JSON-RPC 2.0 requires exactly one.
	ErrResponseEmpty = errors.New("contract: response has neither result nor error")
)

// ID is a JSON-RPC 2.0 message identifier. JSON-RPC permits int, string,
// or null IDs; the CLI emits ints (monotonic counter via IDCorrelator)
// but accepts string IDs from peers and round-trips them verbatim.
//
// The zero value is the null ID — meaningful only on inbound error
// responses to requests whose ID couldn't be determined. Callers that
// want a typed null should compare with id.IsNull().
type ID struct {
	// kind discriminates between the int/string/null forms. Unexported
	// so callers go through the predicate methods.
	kind idKind
	i    int64
	s    string
}

type idKind uint8

const (
	idKindNull idKind = iota
	idKindInt
	idKindString
)

// NewIntID constructs a numeric ID. Used by IDCorrelator and any caller
// that wants to emit a monotonically-increasing identifier.
func NewIntID(i int64) ID { return ID{kind: idKindInt, i: i} }

// NewStringID constructs a string ID. The CLI never produces these; the
// constructor exists so peers' string IDs can be echoed back unchanged.
func NewStringID(s string) ID { return ID{kind: idKindString, s: s} }

// IsNull reports whether the ID is the null/zero value.
func (id ID) IsNull() bool { return id.kind == idKindNull }

// IsInt reports whether the ID carries an int payload.
func (id ID) IsInt() bool { return id.kind == idKindInt }

// IsString reports whether the ID carries a string payload.
func (id ID) IsString() bool { return id.kind == idKindString }

// AsInt returns the int payload. Returns 0 when the ID is null or string.
func (id ID) AsInt() int64 { return id.i }

// AsString returns the string payload. Returns "" when the ID is null
// or int.
func (id ID) AsString() string { return id.s }

// MarshalJSON emits the ID as a JSON number, JSON string, or JSON null
// per its discriminator.
func (id ID) MarshalJSON() ([]byte, error) {
	switch id.kind {
	case idKindNull:
		return []byte("null"), nil
	case idKindInt:
		return []byte(strconv.FormatInt(id.i, 10)), nil
	case idKindString:
		return json.Marshal(id.s)
	default:
		return nil, fmt.Errorf("contract: ID has unknown kind %d", id.kind)
	}
}

// UnmarshalJSON accepts a JSON number, JSON string, or JSON null.
func (id *ID) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		id.kind = idKindNull
		id.i = 0
		id.s = ""
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return fmt.Errorf("contract: ID string: %w", err)
		}
		id.kind = idKindString
		id.s = s
		id.i = 0
		return nil
	}
	// Try int. JSON-RPC permits any number, but ints are the only useful
	// shape for correlators; fractional and scientific-notation values
	// are rejected by json.Number.Int64().
	var n json.Number
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return fmt.Errorf("contract: ID number: %w", err)
	}
	v, err := n.Int64()
	if err != nil {
		return fmt.Errorf("contract: ID non-integer number %q: %w", string(trimmed), err)
	}
	id.kind = idKindInt
	id.i = v
	id.s = ""
	return nil
}

// Request is a JSON-RPC 2.0 request: an ID, a method, and optional
// params. Carries the reserved _meta field per the aienvs/v1 contract.
type Request struct {
	ID     ID
	Method string
	Params json.RawMessage
	Meta   json.RawMessage
}

// MarshalJSON emits the canonical wire form. Field order is fixed for
// byte-stable output (helpful for tests and conformance corpora).
func (r Request) MarshalJSON() ([]byte, error) {
	return marshalEnvelope(envelopeFields{
		hasID:  true,
		id:     r.ID,
		method: r.Method,
		params: r.Params,
		meta:   r.Meta,
	})
}

// Notification is a JSON-RPC 2.0 notification: a method and optional
// params, no ID. The peer must not respond to a notification.
type Notification struct {
	Method string
	Params json.RawMessage
	Meta   json.RawMessage
}

// MarshalJSON emits the canonical wire form.
func (n Notification) MarshalJSON() ([]byte, error) {
	return marshalEnvelope(envelopeFields{
		method: n.Method,
		params: n.Params,
		meta:   n.Meta,
	})
}

// Response is a JSON-RPC 2.0 response: an ID and exactly one of Result
// or Error. MarshalJSON defends the "exactly one" invariant at encode
// time so the wire never carries a malformed response.
type Response struct {
	ID     ID
	Result json.RawMessage
	Error  *Error
	Meta   json.RawMessage
}

// MarshalJSON emits the canonical wire form. Returns ErrResponseHasResultAndError
// or ErrResponseEmpty when the invariant is violated.
//
// Result is treated as set when it carries at least one byte. A non-nil
// zero-length json.RawMessage is treated as absent — emitting "result":
// with no payload would produce malformed JSON, and accepting "null" as
// the explicit-void encoding is the responsibility of the caller (set
// Result = json.RawMessage("null") to emit a void result).
func (r Response) MarshalJSON() ([]byte, error) {
	hasResult := len(r.Result) > 0
	hasError := r.Error != nil
	if hasResult && hasError {
		return nil, ErrResponseHasResultAndError
	}
	if !hasResult && !hasError {
		return nil, ErrResponseEmpty
	}
	return marshalEnvelope(envelopeFields{
		hasID:    true,
		id:       r.ID,
		result:   r.Result,
		errorObj: r.Error,
		meta:     r.Meta,
	})
}

// Error is the JSON-RPC 2.0 error object plus the aienvs-specific Data
// payload carrying error_class and arbitrary detail.
type Error struct {
	Code    int
	Message string
	Data    ErrorData
}

// ErrorData is the structured payload attached to an error response.
// ErrorClass names the CLI-side classifier (adapter-panic,
// adapter-timeout, …). Detail is opaque per-error context.
type ErrorData struct {
	ErrorClass ErrorClass      `json:"error_class,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

// errorWire is the on-the-wire shape of an Error. Kept distinct from
// the Go-side Error so that Data can be elided when both ErrorClass
// and Detail are zero — JSON-RPC clients that don't speak the aienvs
// extension see a vanilla error object.
type errorWire struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// MarshalJSON emits the canonical wire form. If Data is empty, the
// "data" key is omitted.
func (e Error) MarshalJSON() ([]byte, error) {
	w := errorWire{Code: e.Code, Message: e.Message}
	if e.Data.ErrorClass != "" || len(e.Data.Detail) != 0 {
		raw, err := json.Marshal(e.Data)
		if err != nil {
			return nil, fmt.Errorf("contract: marshal error data: %w", err)
		}
		w.Data = raw
	}
	return json.Marshal(w)
}

// envelopeFields collects the canonical envelope keys. marshalEnvelope
// walks them and emits omitempty behavior consistent across
// Request/Response/Notification. The "jsonrpc" field is always emitted
// at the head of the object — there is no envelope shape that omits it.
type envelopeFields struct {
	hasID    bool
	id       ID
	method   string
	params   json.RawMessage
	result   json.RawMessage
	errorObj *Error
	meta     json.RawMessage
}

// marshalEnvelope produces a deterministic byte ordering that mirrors
// the natural JSON-RPC field order: jsonrpc, id, method, params, result,
// error, _meta. Tests rely on byte-stable output for golden comparison.
func marshalEnvelope(f envelopeFields) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	writeKey := func(key string) {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('"')
		buf.WriteString(key)
		buf.WriteString(`":`)
	}

	writeKey("jsonrpc")
	buf.WriteString(`"` + JSONRPCVersion + `"`)
	if f.hasID {
		writeKey("id")
		idBytes, err := f.id.MarshalJSON()
		if err != nil {
			return nil, err
		}
		buf.Write(idBytes)
	}
	if f.method != "" {
		writeKey("method")
		mb, err := json.Marshal(f.method)
		if err != nil {
			return nil, err
		}
		buf.Write(mb)
	}
	if len(f.params) > 0 {
		writeKey("params")
		buf.Write(f.params)
	}
	if len(f.result) > 0 {
		writeKey("result")
		buf.Write(f.result)
	}
	if f.errorObj != nil {
		writeKey("error")
		eb, err := json.Marshal(f.errorObj)
		if err != nil {
			return nil, err
		}
		buf.Write(eb)
	}
	// Suppress empty *and* literal-null _meta — `omitempty` on the Go
	// struct fields does the same, so a json.RawMessage("null") sentinel
	// here would otherwise diverge from the typed-struct behavior.
	if len(f.meta) != 0 && !bytes.Equal(f.meta, []byte("null")) {
		writeKey("_meta")
		buf.Write(f.meta)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// MessageKind classifies an inbound frame.
type MessageKind uint8

const (
	// MessageKindUnknown is the zero value; a parsed Message never has it.
	MessageKindUnknown MessageKind = iota
	MessageKindRequest
	MessageKindNotification
	MessageKindResponse
)

// Message is the result of ParseMessage: a classified envelope with the
// fields relevant to its kind populated. RawParams / RawResult preserve
// the original JSON for downstream typed parsing.
type Message struct {
	Kind   MessageKind
	ID     ID
	Method string
	Params json.RawMessage
	Result json.RawMessage
	Error  *Error
	Meta   json.RawMessage
	HasID  bool
}

// ParseMessage decodes one frame's bytes into a classified Message.
// Returns ErrInvalidEnvelope for shape violations (missing/wrong jsonrpc,
// both result+error on a response, no method/result/error at all).
//
// The envelope is decoded into a generic key map first so ParseMessage
// can distinguish "key absent" from "key present with null value". This
// matters for two JSON-RPC 2.0-mandated wire shapes:
//
//   - {"jsonrpc":"2.0","id":null,"error":{...}} — the spec requires id:null
//     on error responses to requests whose id couldn't be determined
//     (§5.1). A pointer-to-RawMessage decode collapses null and absent
//     to the same nil, breaking parse-error responses.
//   - {"jsonrpc":"2.0","id":1,"result":null} — a void result. Treated
//     as a valid response, not as "no result field present".
func ParseMessage(raw []byte) (Message, error) {
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil {
		return Message{}, fmt.Errorf("contract: parse envelope: %w", err)
	}

	versionRaw, ok := fields["jsonrpc"]
	if !ok {
		return Message{}, fmt.Errorf("%w: jsonrpc field missing", ErrInvalidEnvelope)
	}
	var version string
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return Message{}, fmt.Errorf("%w: jsonrpc=%s", ErrInvalidEnvelope, versionRaw)
	}
	if version != JSONRPCVersion {
		return Message{}, fmt.Errorf("%w: jsonrpc=%q", ErrInvalidEnvelope, version)
	}

	msg := Message{}

	if idRaw, present := fields["id"]; present {
		var id ID
		if err := id.UnmarshalJSON(idRaw); err != nil {
			return Message{}, fmt.Errorf("contract: parse id: %w", err)
		}
		msg.ID = id
		msg.HasID = true
	}
	if methodRaw, present := fields["method"]; present {
		if err := json.Unmarshal(methodRaw, &msg.Method); err != nil {
			return Message{}, fmt.Errorf("contract: parse method: %w", err)
		}
	}
	if paramsRaw, present := fields["params"]; present {
		msg.Params = paramsRaw
	}
	if metaRaw, present := fields["_meta"]; present {
		msg.Meta = metaRaw
	}

	resultRaw, hasResult := fields["result"]
	errorRaw, hasError := fields["error"]
	hasMethod := msg.Method != ""

	if hasResult {
		msg.Result = resultRaw
	}
	if hasError {
		var ew errorWire
		if err := json.Unmarshal(errorRaw, &ew); err != nil {
			return Message{}, fmt.Errorf("contract: parse error: %w", err)
		}
		errData := ErrorData{}
		if len(ew.Data) != 0 && !bytes.Equal(ew.Data, []byte("null")) {
			if err := json.Unmarshal(ew.Data, &errData); err != nil {
				return Message{}, fmt.Errorf("contract: parse error data: %w", err)
			}
		}
		msg.Error = &Error{
			Code:    ew.Code,
			Message: ew.Message,
			Data:    errData,
		}
	}

	switch {
	case hasResult && hasError:
		return Message{}, fmt.Errorf("%w: response has both result and error", ErrInvalidEnvelope)
	case hasResult || hasError:
		if !msg.HasID {
			return Message{}, fmt.Errorf("%w: response missing id", ErrInvalidEnvelope)
		}
		msg.Kind = MessageKindResponse
	case hasMethod && msg.HasID:
		msg.Kind = MessageKindRequest
	case hasMethod:
		msg.Kind = MessageKindNotification
	default:
		return Message{}, fmt.Errorf("%w: no method, result, or error", ErrInvalidEnvelope)
	}

	return msg, nil
}

// IDCorrelator hands out monotonically-increasing IDs and tracks which
// requests are pending their response. Safe for concurrent use.
type IDCorrelator struct {
	mu      sync.Mutex
	next    int64
	pending map[int64]string
}

// NewIDCorrelator returns a correlator with sequence starting at 1.
// The "1" choice mirrors LSP convention; "0" would also work but lower
// values look like uninitialized state to humans reading transcripts.
func NewIDCorrelator() *IDCorrelator {
	return &IDCorrelator{
		pending: make(map[int64]string),
	}
}

// Next allocates the next ID. Safe for concurrent use.
func (c *IDCorrelator) Next() ID {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	return NewIntID(c.next)
}

// MarkPending records that a request with the given ID is awaiting a
// response. Method names what was sent, so the resolver can attach
// context to a late-arriving response. Overwrites any prior entry for
// the same ID; the correlator does not enforce uniqueness because Next()
// already provides it.
func (c *IDCorrelator) MarkPending(id ID, method string) {
	if !id.IsInt() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[id.AsInt()] = method
}

// Resolve looks up the method that was sent for a pending request,
// returning ("", false) if no entry exists. Resolution is one-shot —
// the entry is removed on first lookup so a duplicate response is
// detected as unmatched.
func (c *IDCorrelator) Resolve(id ID) (string, bool) {
	if !id.IsInt() {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	method, ok := c.pending[id.AsInt()]
	if ok {
		delete(c.pending, id.AsInt())
	}
	return method, ok
}

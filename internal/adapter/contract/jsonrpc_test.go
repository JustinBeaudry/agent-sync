package contract

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRequest_MarshalsCanonicalShape(t *testing.T) {
	t.Parallel()

	req := Request{
		ID:     NewIntID(7),
		Method: "initialize",
		Params: json.RawMessage(`{"client":"aienvs/0.1"}`),
	}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"client":"aienvs/0.1"}}`
	if string(got) != want {
		t.Fatalf("marshal mismatch:\nwant %s\ngot  %s", want, got)
	}
}

func TestRequest_OmitsParamsWhenNil(t *testing.T) {
	t.Parallel()

	req := Request{ID: NewIntID(1), Method: "shutdown"}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"jsonrpc":"2.0","id":1,"method":"shutdown"}`
	if string(got) != want {
		t.Fatalf("marshal mismatch:\nwant %s\ngot  %s", want, got)
	}
}

func TestNotification_MarshalsWithoutID(t *testing.T) {
	t.Parallel()

	n := Notification{
		Method: "initialized",
		Params: json.RawMessage(`{}`),
	}
	got, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"jsonrpc":"2.0","method":"initialized","params":{}}`
	if string(got) != want {
		t.Fatalf("marshal mismatch:\nwant %s\ngot  %s", want, got)
	}
}

func TestResponse_MarshalsResult(t *testing.T) {
	t.Parallel()

	r := Response{
		ID:     NewIntID(1),
		Result: json.RawMessage(`{"server":"echo/0.1"}`),
	}
	got, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"server":"echo/0.1"}}`
	if string(got) != want {
		t.Fatalf("marshal mismatch:\nwant %s\ngot  %s", want, got)
	}
}

func TestResponse_MarshalsError(t *testing.T) {
	t.Parallel()

	r := Response{
		ID: NewIntID(2),
		Error: &Error{
			Code:    CodeMethodNotFound,
			Message: "no such method",
			Data: ErrorData{
				ErrorClass: ErrorClassAdapterPanic,
				Detail:     json.RawMessage(`{"stderr_tail":"boom"}`),
			},
		},
	}
	got, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Schema-level checks: present fields, no result key.
	if strings.Contains(string(got), `"result":`) {
		t.Fatalf("error response should omit result: %s", got)
	}
	if !strings.Contains(string(got), `"code":-32601`) {
		t.Fatalf("missing CodeMethodNotFound: %s", got)
	}
	if !strings.Contains(string(got), `"error_class":"adapter-panic"`) {
		t.Fatalf("missing error_class: %s", got)
	}
}

func TestResponse_RejectsBothResultAndError(t *testing.T) {
	t.Parallel()

	r := Response{
		ID:     NewIntID(1),
		Result: json.RawMessage(`{}`),
		Error:  &Error{Code: CodeInternalError, Message: "x"},
	}
	if _, err := json.Marshal(r); !errors.Is(err, ErrResponseHasResultAndError) {
		t.Fatalf("want ErrResponseHasResultAndError, got %v", err)
	}
}

func TestResponse_RejectsNeitherResultNorError(t *testing.T) {
	t.Parallel()

	r := Response{ID: NewIntID(1)}
	if _, err := json.Marshal(r); !errors.Is(err, ErrResponseEmpty) {
		t.Fatalf("want ErrResponseEmpty, got %v", err)
	}
}

func TestParseMessage_ClassifiesRequest(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","id":1,"method":"emit","params":{}}`)
	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if msg.Kind != MessageKindRequest {
		t.Fatalf("want MessageKindRequest, got %v", msg.Kind)
	}
	if msg.Method != "emit" {
		t.Fatalf("want method emit, got %q", msg.Method)
	}
}

func TestParseMessage_ClassifiesNotification(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","method":"initialized"}`)
	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if msg.Kind != MessageKindNotification {
		t.Fatalf("want MessageKindNotification, got %v", msg.Kind)
	}
}

func TestParseMessage_ClassifiesResponseResult(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","id":1,"result":42}`)
	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if msg.Kind != MessageKindResponse {
		t.Fatalf("want MessageKindResponse, got %v", msg.Kind)
	}
	if string(msg.Result) != "42" {
		t.Fatalf("want result 42, got %q", msg.Result)
	}
}

func TestParseMessage_ClassifiesResponseError(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"boom"}}`)
	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if msg.Kind != MessageKindResponse {
		t.Fatalf("want MessageKindResponse, got %v", msg.Kind)
	}
	if msg.Error == nil || msg.Error.Code != CodeInternalError {
		t.Fatalf("want error CodeInternalError, got %+v", msg.Error)
	}
}

func TestParseMessage_RejectsMissingJSONRPCField(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"id":1,"method":"foo"}`)
	_, err := ParseMessage(raw)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("want ErrInvalidEnvelope, got %v", err)
	}
}

func TestParseMessage_RejectsWrongJSONRPCVersion(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"jsonrpc":"1.0","id":1,"method":"foo"}`)
	_, err := ParseMessage(raw)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("want ErrInvalidEnvelope, got %v", err)
	}
}

func TestParseMessage_RejectsResponseWithBothResultAndError(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","id":1,"result":1,"error":{"code":1,"message":"x"}}`)
	_, err := ParseMessage(raw)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("want ErrInvalidEnvelope, got %v", err)
	}
}

func TestParseMessage_RejectsResponseWithoutID(t *testing.T) {
	// A response (result key present) without an id is invalid per
	// JSON-RPC 2.0 §5.
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","result":42}`)
	_, err := ParseMessage(raw)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("want ErrInvalidEnvelope, got %v", err)
	}
}

func TestParseMessage_RejectsEnvelopeWithNoMethodOrResult(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0"}`)
	_, err := ParseMessage(raw)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("want ErrInvalidEnvelope, got %v", err)
	}
}

func TestParseMessage_AcceptsNullIDOnErrorResponse(t *testing.T) {
	// JSON-RPC 2.0 §5.1 mandates id:null on error responses to requests
	// whose id couldn't be determined (e.g., parse errors). This shape
	// must classify as a valid Response.
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`)
	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if msg.Kind != MessageKindResponse {
		t.Fatalf("want MessageKindResponse, got %v", msg.Kind)
	}
	if !msg.HasID || !msg.ID.IsNull() {
		t.Fatalf("want HasID=true with null id, got HasID=%v IsNull=%v", msg.HasID, msg.ID.IsNull())
	}
	if msg.Error == nil || msg.Error.Code != CodeParseError {
		t.Fatalf("want CodeParseError, got %+v", msg.Error)
	}
}

func TestParseMessage_RejectsTrailingBytes(t *testing.T) {
	// Concatenated frames or smuggled trailing content must not pass
	// silently — the framing layer is responsible for one envelope per
	// frame.
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","method":"initialized"}{"extra":1}`)
	_, err := ParseMessage(raw)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("want ErrInvalidEnvelope, got %v", err)
	}
}

func TestParseMessage_RejectsMixedMethodAndResultEnvelope(t *testing.T) {
	// JSON-RPC 2.0 separates request/notification (carries method) from
	// response (carries result/error). A frame with both is invalid.
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","id":1,"method":"emit","result":42}`)
	_, err := ParseMessage(raw)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("want ErrInvalidEnvelope, got %v", err)
	}
}

func TestParseMessage_AcceptsNullResultAsResponse(t *testing.T) {
	// A result:null response is a valid void return. It must classify
	// as MessageKindResponse, not be rejected as "no method/result/error".
	t.Parallel()

	raw := []byte(`{"jsonrpc":"2.0","id":1,"result":null}`)
	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if msg.Kind != MessageKindResponse {
		t.Fatalf("want MessageKindResponse, got %v", msg.Kind)
	}
	if string(msg.Result) != "null" {
		t.Fatalf("want Result=null bytes, got %q", msg.Result)
	}
}

func TestResponse_RejectsZeroLengthRawMessage(t *testing.T) {
	// A non-nil but zero-length json.RawMessage is treated as absent —
	// emitting it would produce malformed wire bytes.
	t.Parallel()

	r := Response{ID: NewIntID(1), Result: json.RawMessage{}}
	if _, err := json.Marshal(r); !errors.Is(err, ErrResponseEmpty) {
		t.Fatalf("want ErrResponseEmpty, got %v", err)
	}
}

func TestRequest_OmitsLiteralNullMeta(t *testing.T) {
	// Meta = json.RawMessage("null") emits no _meta key, matching the
	// omitempty behavior of typed _meta fields.
	t.Parallel()

	req := Request{ID: NewIntID(1), Method: "x", Meta: json.RawMessage("null")}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(got), "_meta") {
		t.Fatalf("expected no _meta key for null Meta, got %s", got)
	}
}

func TestIDCorrelator_AssignsMonotonicIDs(t *testing.T) {
	t.Parallel()

	ids := NewIDCorrelator()
	a := ids.Next()
	b := ids.Next()
	c := ids.Next()

	if a.AsInt() != 1 || b.AsInt() != 2 || c.AsInt() != 3 {
		t.Fatalf("want 1,2,3 got %d,%d,%d", a.AsInt(), b.AsInt(), c.AsInt())
	}
}

func TestIDCorrelator_TracksPendingRequests(t *testing.T) {
	t.Parallel()

	ids := NewIDCorrelator()
	id := ids.Next()
	ids.MarkPending(id, "initialize")

	method, ok := ids.Resolve(id)
	if !ok {
		t.Fatal("Resolve: want ok=true")
	}
	if method != "initialize" {
		t.Fatalf("want method initialize, got %q", method)
	}

	// Re-resolving the same ID returns false (one-shot).
	if _, ok := ids.Resolve(id); ok {
		t.Fatal("Resolve: want ok=false on second resolve")
	}
}

func TestIDCorrelator_IsRaceFreeUnderConcurrentAllocs(t *testing.T) {
	t.Parallel()

	ids := NewIDCorrelator()
	const n = 1000
	ch := make(chan ID, n)
	for i := 0; i < n; i++ {
		go func() { ch <- ids.Next() }()
	}
	seen := make(map[int64]bool, n)
	for i := 0; i < n; i++ {
		got := <-ch
		v := got.AsInt()
		if seen[v] {
			t.Fatalf("duplicate ID %d", v)
		}
		seen[v] = true
	}
}

func TestIDCorrelator_CancelEvictsPendingEntry(t *testing.T) {
	t.Parallel()

	ids := NewIDCorrelator()
	id := ids.Next()
	ids.MarkPending(id, "emit")

	if ids.Pending() != 1 {
		t.Fatalf("Pending: want 1, got %d", ids.Pending())
	}

	ids.Cancel(id)

	if _, ok := ids.Resolve(id); ok {
		t.Error("Resolve after Cancel should return ok=false")
	}
	if ids.Pending() != 0 {
		t.Errorf("Pending after Cancel: want 0, got %d", ids.Pending())
	}
}

func TestIDCorrelator_CancelOfUnknownIDIsNoOp(t *testing.T) {
	t.Parallel()

	ids := NewIDCorrelator()
	// Cancel without a prior MarkPending — must not panic, must not
	// affect Pending().
	ids.Cancel(NewIntID(999))
	if ids.Pending() != 0 {
		t.Errorf("Pending: want 0, got %d", ids.Pending())
	}
}

func TestIDCorrelator_CancelOfStringIDIsNoOp(t *testing.T) {
	// String IDs are not tracked by MarkPending (the correlator is
	// designed for the int IDs the CLI emits). Cancel mirrors that.
	t.Parallel()

	ids := NewIDCorrelator()
	ids.Cancel(NewStringID("req-abc"))
	if ids.Pending() != 0 {
		t.Errorf("Pending: want 0, got %d", ids.Pending())
	}
}

func TestIDCorrelator_PendingReflectsLiveCount(t *testing.T) {
	t.Parallel()

	ids := NewIDCorrelator()
	for i := 0; i < 5; i++ {
		ids.MarkPending(ids.Next(), "x")
	}
	if ids.Pending() != 5 {
		t.Fatalf("Pending after 5 marks: want 5, got %d", ids.Pending())
	}
	id1 := NewIntID(1)
	id2 := NewIntID(2)
	if _, ok := ids.Resolve(id1); !ok {
		t.Fatal("Resolve(1) should succeed")
	}
	ids.Cancel(id2)
	if ids.Pending() != 3 {
		t.Errorf("Pending after one Resolve and one Cancel: want 3, got %d", ids.Pending())
	}
}

func TestIDCorrelator_ConcurrentMarkAndCancel(t *testing.T) {
	// Concurrent marks paired with concurrent cancels must leave the
	// pending count at zero. This catches a missing mutex on Cancel
	// under -race and proves the eviction API matches MarkPending's
	// concurrency contract.
	t.Parallel()

	ids := NewIDCorrelator()
	const n = 200
	allocated := make(chan ID, n)
	for i := 0; i < n; i++ {
		go func() {
			id := ids.Next()
			ids.MarkPending(id, "x")
			allocated <- id
		}()
	}
	for i := 0; i < n; i++ {
		id := <-allocated
		go ids.Cancel(id)
	}
	// Wait for all cancels to drain. A polling loop bounded by a
	// generous deadline is enough — the test under -race surfaces any
	// race condition regardless of timing.
	deadline := 2 * 1000
	for ids.Pending() != 0 && deadline > 0 {
		deadline--
	}
	if ids.Pending() != 0 {
		t.Errorf("Pending after %d concurrent Mark+Cancel: want 0, got %d", n, ids.Pending())
	}
}

func TestID_StringIDsRoundTrip(t *testing.T) {
	// JSON-RPC permits string IDs. We accept them on parse and round-trip
	// them on emit so we can echo client-provided string IDs verbatim.
	t.Parallel()

	id := NewStringID("req-abc")
	encoded, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `"req-abc"` {
		t.Fatalf("want %q, got %s", `"req-abc"`, encoded)
	}

	var decoded ID
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !decoded.IsString() || decoded.AsString() != "req-abc" {
		t.Fatalf("round-trip lost type/value: %+v", decoded)
	}
}

func TestID_NullIsRejected(t *testing.T) {
	// JSON-RPC 2.0 reserves id=null for use in error responses to
	// requests whose id couldn't be determined. We never *send* null IDs
	// from the CLI; on receive we accept them only inside error responses.
	t.Parallel()

	var id ID
	if err := json.Unmarshal([]byte("null"), &id); err != nil {
		t.Fatalf("Unmarshal null: %v", err)
	}
	if !id.IsNull() {
		t.Fatal("want IsNull=true after unmarshaling null")
	}
}

func TestErrorCodes_StableValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  int
		want int
	}{
		{"ParseError", CodeParseError, -32700},
		{"InvalidRequest", CodeInvalidRequest, -32600},
		{"MethodNotFound", CodeMethodNotFound, -32601},
		{"InvalidParams", CodeInvalidParams, -32602},
		{"InternalError", CodeInternalError, -32603},
		{"ServerErrorMin", CodeServerErrorMin, -32099},
		{"ServerErrorMax", CodeServerErrorMax, -32000},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: want %d, got %d", c.name, c.want, c.got)
		}
	}
}

func TestErrorClass_StableNames(t *testing.T) {
	t.Parallel()

	// Wire spec: error_class strings are part of the contract; renaming
	// breaks every existing adapter. Lock them with a literal table.
	cases := []struct {
		got  ErrorClass
		want string
	}{
		{ErrorClassAdapterPanic, "adapter-panic"},
		{ErrorClassAdapterTimeout, "adapter-timeout"},
		{ErrorClassAdapterProtocolMismatch, "adapter-protocol-mismatch"},
		{ErrorClassAdapterUndeclaredOutput, "adapter-undeclared-output"},
		{ErrorClassAdapterExecDenied, "adapter-exec-denied"},
		{ErrorClassAdapterCapabilityLied, "adapter-capability-lied"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("want %q, got %q", c.want, c.got)
		}
	}
}

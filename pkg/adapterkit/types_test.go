package adapterkit

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestTypes_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		value  any
		target any
	}{
		{
			name: "InitializeParams",
			value: InitializeParams{
				Client:           "test",
				ProtocolVersions: []string{ContractVersionV1},
				Cookie:           "cookie",
				WorkspaceRoot:    "/tmp/ws",
				ReservedPrefix:   ".echo",
				IRVersion:        "v1",
			},
			target: &InitializeParams{},
		},
		{
			name: "InitializeResult",
			value: InitializeResult{
				Server:          "echo/0.1",
				ProtocolVersion: ContractVersionV1,
				Capabilities:    NewCapabilities().Supports("rule").Build(),
				DeclaredOutputs: []DeclaredOutput{{Path: ".echo", Mode: OutputModeOwnedSubdir}},
				Cookie:          "0123456789abcdef0123456789abcdef",
			},
			target: &InitializeResult{},
		},
		{
			name:   "EmitParams",
			value:  EmitParams{Target: "test", IR: []byte(`{"nodes":[{"id":"a","kind":"rule"}]}`)},
			target: &EmitParams{},
		},
		{
			name:   "EmitResult",
			value:  EmitResult{OpsPerformed: []OpRecord{{Op: OpKindMkdir, Path: ".echo"}}},
			target: &EmitResult{},
		},
		{
			name:   "ShutdownResult",
			value:  ShutdownResult{},
			target: &ShutdownResult{},
		},
		{
			name:   "OpWriteFile",
			value:  mustOpWriteFile(t, ".echo/a.md", 0o644, []byte("hello")),
			target: &OpWriteFile{},
		},
		{
			name: "OpWriteToolOwned",
			value: OpWriteToolOwned{
				Path:    ".mcp.json",
				Kind:    ToolOwnedKindJSONPointer,
				Locator: "/mcpServers/echo",
				Content: []byte(`{"command":"echo"}`),
			},
			target: &OpWriteToolOwned{},
		},
		{
			name:   "OpMkdir",
			value:  OpMkdir{Path: ".echo", Mode: 0o755},
			target: &OpMkdir{},
		},
		{
			name:   "OpDelete",
			value:  OpDelete{Path: ".echo/a.md"},
			target: &OpDelete{},
		},
		{
			name:   "OpWarning",
			value:  OpWarning{ConceptID: "rule-1", Status: WarningStatusPartial, Note: "not implemented"},
			target: &OpWarning{},
		},
		{
			name:   "Error",
			value:  &Error{Code: CodeInternalError, Message: "boom", Data: ErrorData{ErrorClass: ErrorClassAdapterPanic}},
			target: &Error{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if err := json.Unmarshal(data, tc.target); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}

			got := reflect.ValueOf(tc.target).Elem().Interface()
			want := reflect.ValueOf(tc.value)
			if want.Kind() == reflect.Pointer {
				want = want.Elem()
			}
			if !reflect.DeepEqual(got, want.Interface()) {
				t.Fatalf("roundtrip mismatch:\n got  %#v\n want %#v", got, want.Interface())
			}
		})
	}
}

func TestDecodeOp_RejectsUnknownOp(t *testing.T) {
	t.Parallel()

	_, err := DecodeOp([]byte(`{"op":"wat","path":"x"}`))
	if !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("want ErrUnknownOp, got %v", err)
	}
}

func mustOpWriteFile(t *testing.T, path string, mode uint32, content []byte) OpWriteFile {
	t.Helper()
	op, err := NewOpWriteFile(path, mode, content)
	if err != nil {
		t.Fatalf("NewOpWriteFile: %v", err)
	}
	return op
}

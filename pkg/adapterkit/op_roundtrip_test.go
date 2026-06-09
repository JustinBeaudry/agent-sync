package adapterkit

import (
	"bytes"
	"errors"
	"testing"
)

// These tests exercise the Op-interface paths (MarshalOp, DecodeOp, OpKind,
// OpPath) which the reflection-based TestTypes_RoundTripJSON does not touch.

func mustNewWriteFile(t *testing.T, path string, content []byte) OpWriteFile {
	t.Helper()
	op, err := NewOpWriteFile(path, 0o644, content)
	if err != nil {
		t.Fatalf("NewOpWriteFile: %v", err)
	}
	return op
}

func TestOpInterface_RoundTripAllKinds(t *testing.T) {
	cases := []struct {
		name     string
		op       Op
		wantKind OpKind
		wantPath string
	}{
		{
			name:     "write_file utf8",
			op:       mustNewWriteFile(t, ".echo/a.md", []byte("hello")),
			wantKind: OpKindWriteFile,
			wantPath: ".echo/a.md",
		},
		{
			name:     "write_file binary base64",
			op:       mustNewWriteFile(t, ".echo/b.bin", []byte{0x00, 0x01, 0xff, 0xfe}),
			wantKind: OpKindWriteFile,
			wantPath: ".echo/b.bin",
		},
		{
			name:     "write_tool_owned",
			op:       OpWriteToolOwned{Path: ".mcp.json", Kind: ToolOwnedKindJSONPointer, Locator: "/servers/x", Content: []byte(`{"x":1}`)},
			wantKind: OpKindWriteToolOwned,
			wantPath: ".mcp.json",
		},
		{
			name:     "mkdir",
			op:       OpMkdir{Path: ".echo", Mode: 0o755},
			wantKind: OpKindMkdir,
			wantPath: ".echo",
		},
		{
			name:     "delete",
			op:       OpDelete{Path: ".echo/old.md"},
			wantKind: OpKindDelete,
			wantPath: ".echo/old.md",
		},
		{
			name:     "warning",
			op:       OpWarning{ConceptID: "skill:foo", Status: WarningStatusDegraded, Note: "lossy"},
			wantKind: OpKindWarning,
			wantPath: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Accessor methods.
			if tc.op.OpKind() != tc.wantKind {
				t.Fatalf("OpKind = %q, want %q", tc.op.OpKind(), tc.wantKind)
			}
			if tc.op.OpPath() != tc.wantPath {
				t.Fatalf("OpPath = %q, want %q", tc.op.OpPath(), tc.wantPath)
			}

			// MarshalOp -> DecodeOp -> re-MarshalOp must be byte-stable.
			raw, err := MarshalOp(tc.op)
			if err != nil {
				t.Fatalf("MarshalOp: %v", err)
			}
			decoded, err := DecodeOp(raw)
			if err != nil {
				t.Fatalf("DecodeOp: %v", err)
			}
			if decoded.OpKind() != tc.wantKind {
				t.Fatalf("decoded OpKind = %q, want %q", decoded.OpKind(), tc.wantKind)
			}
			if decoded.OpPath() != tc.wantPath {
				t.Fatalf("decoded OpPath = %q, want %q", decoded.OpPath(), tc.wantPath)
			}
			raw2, err := MarshalOp(decoded)
			if err != nil {
				t.Fatalf("re-MarshalOp: %v", err)
			}
			if !bytes.Equal(raw, raw2) {
				t.Fatalf("round-trip not byte-stable:\n a=%s\n b=%s", raw, raw2)
			}
		})
	}
}

func TestNewOpWriteFile_PayloadTooLarge(t *testing.T) {
	_, err := NewOpWriteFile("x", 0o644, make([]byte, MaxOpPayloadBytes+1))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestOpWriteFile_MarshalRejectsOversizeContent(t *testing.T) {
	// Construct around the NewOpWriteFile guard to exercise MarshalJSON's check.
	op := OpWriteFile{Path: "x", Mode: 0o644, Content: make([]byte, MaxOpPayloadBytes+1)}
	if _, err := MarshalOp(op); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestDecodeOp_Malformed(t *testing.T) {
	if _, err := DecodeOp([]byte(`{`)); err == nil {
		t.Fatal("expected error decoding malformed op")
	}
}

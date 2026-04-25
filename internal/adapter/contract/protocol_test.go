package contract

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMethodNames_StableValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		got, want string
	}{
		{MethodInitialize, "initialize"},
		{MethodInitialized, "initialized"},
		{MethodEmit, "emit"},
		{MethodShutdown, "shutdown"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("want %q, got %q", c.want, c.got)
		}
	}
}

func TestInitializeParams_RoundTrips(t *testing.T) {
	t.Parallel()

	src := InitializeParams{
		Client:           "aienvs/0.1",
		ProtocolVersions: []string{"aienvs/v1"},
		Cookie:           "deadbeef-1234",
		WorkspaceRoot:    "/tmp/ws",
		ReservedPrefix:   ".claude",
		IRVersion:        "v1",
		Meta:             json.RawMessage(`{"trace":"abc"}`),
	}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var dst InitializeParams
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if dst.Client != src.Client || dst.Cookie != src.Cookie || dst.WorkspaceRoot != src.WorkspaceRoot {
		t.Errorf("scalar mismatch: %+v vs %+v", src, dst)
	}
	if len(dst.ProtocolVersions) != 1 || dst.ProtocolVersions[0] != "aienvs/v1" {
		t.Errorf("protocol_versions mismatch: %v", dst.ProtocolVersions)
	}
	if !bytes.Equal(dst.Meta, src.Meta) {
		t.Errorf("_meta mismatch: %s vs %s", src.Meta, dst.Meta)
	}
}

func TestInitializeParams_OmitsEmptyMeta(t *testing.T) {
	t.Parallel()

	src := InitializeParams{Client: "x", ProtocolVersions: []string{"v1"}, Cookie: "c", WorkspaceRoot: "r", ReservedPrefix: "p", IRVersion: "v1"}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(raw), "_meta") {
		t.Errorf("expected _meta omitted when empty, got %s", raw)
	}
}

func TestInitializeResult_CarriesCapabilitiesAndDeclaredOutputs(t *testing.T) {
	t.Parallel()

	src := InitializeResult{
		Server:          "echo/0.1",
		ProtocolVersion: "aienvs/v1",
		Capabilities: Capabilities{
			ConceptKinds: map[string]CapabilityLevel{
				"rule":             CapabilitySupported,
				"skill":            CapabilityPartial,
				"plugin-reference": CapabilityUnsupported,
			},
			WriteToolOwned: true,
			Progress:       false,
		},
		DeclaredOutputs: []DeclaredOutput{
			{Path: ".claude/rules", Mode: OutputModeOwnedSubdir},
			{Path: ".mcp.json", Mode: OutputModeToolOwnedEntry, JSONPath: stringPtr("$.mcpServers.echo")},
		},
	}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var dst InitializeResult
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if dst.Capabilities.ConceptKinds["rule"] != CapabilitySupported {
		t.Errorf("rule capability mismatch: %v", dst.Capabilities.ConceptKinds)
	}
	if !dst.Capabilities.WriteToolOwned {
		t.Errorf("write_tool_owned should be true after round-trip")
	}
	if len(dst.DeclaredOutputs) != 2 {
		t.Fatalf("declared_outputs len: want 2 got %d", len(dst.DeclaredOutputs))
	}
	if dst.DeclaredOutputs[1].JSONPath == nil || *dst.DeclaredOutputs[1].JSONPath != "$.mcpServers.echo" {
		t.Errorf("json_path mismatch: %v", dst.DeclaredOutputs[1].JSONPath)
	}
}

func TestCapabilityLevels_StableValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		got  CapabilityLevel
		want string
	}{
		{CapabilitySupported, "supported"},
		{CapabilityPartial, "partial"},
		{CapabilityUnsupported, "unsupported"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("want %q, got %q", c.want, c.got)
		}
	}
}

func TestOutputModes_StableValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		got  OutputMode
		want string
	}{
		{OutputModeOwnedSubdir, "owned-subdir"},
		{OutputModeToolOwnedEntry, "tool-owned-entry"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("want %q, got %q", c.want, c.got)
		}
	}
}

func TestToolOwnedKinds_StableValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		got  ToolOwnedKind
		want string
	}{
		{ToolOwnedKindJSONPointer, "json-pointer"},
		{ToolOwnedKindTOMLPath, "toml-path"},
		{ToolOwnedKindMarkdownSection, "markdown-section"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("want %q, got %q", c.want, c.got)
		}
	}
}

func TestWarningStatuses_StableValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		got  WarningStatus
		want string
	}{
		{WarningStatusDegraded, "degraded"},
		{WarningStatusPartial, "partial"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("want %q, got %q", c.want, c.got)
		}
	}
}

func TestOpKinds_StableValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		got  OpKind
		want string
	}{
		{OpKindWriteFile, "write_file"},
		{OpKindWriteToolOwned, "write_tool_owned"},
		{OpKindMkdir, "mkdir"},
		{OpKindDelete, "delete"},
		{OpKindWarning, "warning"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("want %q, got %q", c.want, c.got)
		}
	}
}

func TestAllOpKinds_ReturnsClosedSetInStableOrder(t *testing.T) {
	t.Parallel()

	got := AllOpKinds()
	want := []OpKind{
		OpKindWriteFile,
		OpKindWriteToolOwned,
		OpKindMkdir,
		OpKindDelete,
		OpKindWarning,
	}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: want %d got %d", len(want), len(got))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d]: want %q got %q", i, want[i], got[i])
		}
	}
}

func TestNewOpWriteFile_ValidUTF8UsesUTF8Encoding(t *testing.T) {
	t.Parallel()

	op, err := NewOpWriteFile("rules/foo.md", 0o644, []byte("# hello\n"))
	if err != nil {
		t.Fatalf("NewOpWriteFile: %v", err)
	}
	if op.OpKind() != OpKindWriteFile {
		t.Errorf("OpKind: %q", op.OpKind())
	}
	if op.OpPath() != "rules/foo.md" {
		t.Errorf("OpPath: %q", op.OpPath())
	}

	raw, err := MarshalOp(op)
	if err != nil {
		t.Fatalf("MarshalOp: %v", err)
	}
	if !strings.Contains(string(raw), `"encoding":"utf8"`) {
		t.Errorf("expected encoding=utf8 on the wire, got %s", raw)
	}
	if !strings.Contains(string(raw), `"op":"write_file"`) {
		t.Errorf("expected op discriminator, got %s", raw)
	}
}

func TestNewOpWriteFile_NonUTF8UsesBase64Encoding(t *testing.T) {
	t.Parallel()

	// 0xff is invalid UTF-8.
	binary := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0xfe}
	op, err := NewOpWriteFile("assets/icon.png", 0o644, binary)
	if err != nil {
		t.Fatalf("NewOpWriteFile: %v", err)
	}

	raw, err := MarshalOp(op)
	if err != nil {
		t.Fatalf("MarshalOp: %v", err)
	}
	if !strings.Contains(string(raw), `"encoding":"base64"`) {
		t.Errorf("expected encoding=base64 on the wire, got %s", raw)
	}
	want := base64.StdEncoding.EncodeToString(binary)
	if !strings.Contains(string(raw), want) {
		t.Errorf("expected base64 payload %q in wire bytes %s", want, raw)
	}
}

func TestOpWriteFile_RoundTripPreservesContent(t *testing.T) {
	t.Parallel()

	cases := [][]byte{
		[]byte("# utf-8 content"),
		{0x89, 'P', 'N', 'G', 0xff},
		{},
		[]byte("\xe6\x97\xa5\xe6\x9c\xac\xe8\xaa\x9e"), // valid utf-8 (Japanese)
	}
	for i, content := range cases {
		op, err := NewOpWriteFile("p", 0o644, content)
		if err != nil {
			t.Fatalf("[%d] NewOpWriteFile: %v", i, err)
		}
		raw, err := MarshalOp(op)
		if err != nil {
			t.Fatalf("[%d] MarshalOp: %v", i, err)
		}
		decoded, err := DecodeOp(raw)
		if err != nil {
			t.Fatalf("[%d] DecodeOp: %v", i, err)
		}
		got, ok := decoded.(OpWriteFile)
		if !ok {
			t.Fatalf("[%d] DecodeOp returned %T, want OpWriteFile", i, decoded)
		}
		if !bytes.Equal(got.Content, content) {
			t.Errorf("[%d] content mismatch: want %q got %q", i, content, got.Content)
		}
	}
}

func TestNewOpWriteFile_EnforcesPayloadCap(t *testing.T) {
	t.Parallel()

	tooBig := make([]byte, MaxOpPayloadBytes+1)
	_, err := NewOpWriteFile("p", 0o644, tooBig)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("want ErrPayloadTooLarge, got %v", err)
	}
}

func TestOpWriteFile_MarshalEnforcesCapOnDirectConstruction(t *testing.T) {
	// Direct struct construction bypasses NewOpWriteFile's cap check.
	// MarshalJSON is the second line of defense — without it, an
	// oversized payload would land on the wire.
	t.Parallel()

	op := OpWriteFile{Path: "p", Mode: 0o644, Content: make([]byte, MaxOpPayloadBytes+1)}
	_, err := MarshalOp(op)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("want ErrPayloadTooLarge, got %v", err)
	}
}

func TestOpWriteToolOwned_MarshalEnforcesCapOnDirectConstruction(t *testing.T) {
	t.Parallel()

	op := OpWriteToolOwned{
		Path:    ".mcp.json",
		Kind:    ToolOwnedKindJSONPointer,
		Locator: "/mcpServers/echo",
		Content: make([]byte, MaxOpPayloadBytes+1),
	}
	_, err := MarshalOp(op)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("want ErrPayloadTooLarge, got %v", err)
	}
}

func TestNewOpWriteFile_AcceptsExactBoundary(t *testing.T) {
	t.Parallel()

	atCap := make([]byte, MaxOpPayloadBytes)
	for i := range atCap {
		atCap[i] = 'a' // valid UTF-8
	}
	op, err := NewOpWriteFile("p", 0o644, atCap)
	if err != nil {
		t.Fatalf("NewOpWriteFile at cap: %v", err)
	}
	if len(op.Content) != MaxOpPayloadBytes {
		t.Errorf("content len: want %d got %d", MaxOpPayloadBytes, len(op.Content))
	}
}

func TestDecodeOp_RejectsOversizedDecodedPayload(t *testing.T) {
	t.Parallel()

	// Build a wire op that declares more decoded bytes than the cap allows.
	// We do this by hand-crafting the JSON so it bypasses NewOpWriteFile's
	// pre-encode check — defends against a hostile peer slipping past the
	// gate via base64 expansion.
	tooBig := make([]byte, MaxOpPayloadBytes+1)
	encoded := base64.StdEncoding.EncodeToString(tooBig)
	wire := []byte(`{"op":"write_file","path":"p","mode":420,"encoding":"base64","content":"` + encoded + `"}`)

	_, err := DecodeOp(wire)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("want ErrPayloadTooLarge, got %v", err)
	}
}

func TestOpWriteToolOwned_RoundTripJSONPointer(t *testing.T) {
	t.Parallel()

	op := OpWriteToolOwned{
		Path:    ".mcp.json",
		Kind:    ToolOwnedKindJSONPointer,
		Locator: "/mcpServers/echo",
		Content: []byte(`{"command":"echo-server"}`),
	}
	raw, err := MarshalOp(op)
	if err != nil {
		t.Fatalf("MarshalOp: %v", err)
	}
	if !strings.Contains(string(raw), `"op":"write_tool_owned"`) {
		t.Errorf("expected op discriminator, got %s", raw)
	}
	if !strings.Contains(string(raw), `"kind":"json-pointer"`) {
		t.Errorf("expected kind=json-pointer, got %s", raw)
	}

	decoded, err := DecodeOp(raw)
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	got, ok := decoded.(OpWriteToolOwned)
	if !ok {
		t.Fatalf("DecodeOp returned %T, want OpWriteToolOwned", decoded)
	}
	if got.Locator != "/mcpServers/echo" || got.Kind != ToolOwnedKindJSONPointer {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestOpMkdir_RoundTrip(t *testing.T) {
	t.Parallel()

	op := OpMkdir{Path: ".claude/rules", Mode: 0o755}
	raw, err := MarshalOp(op)
	if err != nil {
		t.Fatalf("MarshalOp: %v", err)
	}
	if !strings.Contains(string(raw), `"op":"mkdir"`) {
		t.Errorf("expected op discriminator, got %s", raw)
	}
	decoded, err := DecodeOp(raw)
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	got, ok := decoded.(OpMkdir)
	if !ok || got.Path != ".claude/rules" || got.Mode != 0o755 {
		t.Errorf("round-trip mismatch: %T %+v", decoded, decoded)
	}
}

func TestOpDelete_RoundTrip(t *testing.T) {
	t.Parallel()

	op := OpDelete{Path: ".claude/old-rule.md"}
	raw, err := MarshalOp(op)
	if err != nil {
		t.Fatalf("MarshalOp: %v", err)
	}
	decoded, err := DecodeOp(raw)
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	got, ok := decoded.(OpDelete)
	if !ok || got.Path != ".claude/old-rule.md" {
		t.Errorf("round-trip mismatch: %T %+v", decoded, decoded)
	}
}

func TestOpWarning_RoundTrip(t *testing.T) {
	t.Parallel()

	op := OpWarning{
		ConceptID: "rule:no-pr-friday",
		Status:    WarningStatusPartial,
		Note:      "skill assets unreadable",
	}
	raw, err := MarshalOp(op)
	if err != nil {
		t.Fatalf("MarshalOp: %v", err)
	}
	if !strings.Contains(string(raw), `"status":"partial"`) {
		t.Errorf("expected status=partial, got %s", raw)
	}
	decoded, err := DecodeOp(raw)
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	got, ok := decoded.(OpWarning)
	if !ok || got.ConceptID != "rule:no-pr-friday" || got.Status != WarningStatusPartial {
		t.Errorf("round-trip mismatch: %T %+v", decoded, decoded)
	}
}

func TestDecodeOp_RejectsSymlinkOp(t *testing.T) {
	// symlink is intentionally not part of the v1 op union. A wire op
	// claiming "op": "symlink" must be rejected so adapters can't smuggle
	// symlink emission past the gate.
	t.Parallel()

	wire := []byte(`{"op":"symlink","path":".claude/foo","target":"../bar"}`)
	_, err := DecodeOp(wire)
	if !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("want ErrUnknownOp, got %v", err)
	}
}

func TestDecodeOp_RejectsMissingOpField(t *testing.T) {
	t.Parallel()

	wire := []byte(`{"path":"foo"}`)
	_, err := DecodeOp(wire)
	if !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("want ErrUnknownOp, got %v", err)
	}
}

func TestDecodeOp_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	_, err := DecodeOp([]byte(`{"op":"mkdir",`))
	if err == nil {
		t.Fatal("want error from malformed JSON, got nil")
	}
	// Should wrap a *json.SyntaxError; we just check it's a real error.
}

func TestShutdownParamsAndResult_RoundTrip(t *testing.T) {
	t.Parallel()

	src := ShutdownParams{Meta: json.RawMessage(`{"reason":"sync-complete"}`)}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var dst ShutdownParams
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !bytes.Equal(dst.Meta, src.Meta) {
		t.Errorf("_meta mismatch: %s vs %s", src.Meta, dst.Meta)
	}
}

func TestEmitParams_CarriesIRAsRawMessage(t *testing.T) {
	// EmitParams holds the IR as json.RawMessage to avoid coupling the
	// contract package up to internal/ir. The runtime is responsible for
	// round-tripping the IR into typed form.
	t.Parallel()

	ir := json.RawMessage(`{"nodes":[{"id":"foo","kind":"rule"}]}`)
	src := EmitParams{Target: "claude", IR: ir}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var dst EmitParams
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dst.Target != "claude" {
		t.Errorf("target mismatch: %q", dst.Target)
	}
	if !bytes.Equal(dst.IR, ir) {
		t.Errorf("ir mismatch: %s vs %s", ir, dst.IR)
	}
}

func TestEmitResult_CarriesOpRecords(t *testing.T) {
	t.Parallel()

	src := EmitResult{
		OpsPerformed: []OpRecord{
			{Op: OpKindMkdir, Path: ".claude/rules"},
			{Op: OpKindWriteFile, Path: ".claude/rules/foo.md"},
		},
	}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var dst EmitResult
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(dst.OpsPerformed) != 2 || dst.OpsPerformed[0].Op != OpKindMkdir {
		t.Errorf("ops_performed mismatch: %+v", dst.OpsPerformed)
	}
}

func stringPtr(s string) *string { return &s }

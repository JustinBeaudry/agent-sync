package adapterkit

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestScriptedResponder_RoundTripViaRunInprocServer(t *testing.T) {
	t.Parallel()

	server := (&ScriptedResponder{
		DeclaredOutputs: []DeclaredOutput{{Path: ".echo", Mode: OutputModeOwnedSubdir}},
		ConceptKinds:    map[string]CapabilityLevel{"rule": CapabilitySupported},
		RespondToEmit: func(ctx context.Context, received EmitParams) (EmitResult, *Error) {
			return EmitResult{
				OpsPerformed: []OpRecord{
					{Op: OpKindMkdir, Path: ".echo"},
					{Op: OpKindWriteFile, Path: ".echo/" + received.Target + ".md"},
				},
			}, nil
		},
	}).AsServer("scripted", "0.1")

	client, cleanup := RunInprocServer(t, server)
	t.Cleanup(cleanup)

	initResult, err := client.Initialize(context.Background(), InitializeParams{
		Client:           "test",
		ProtocolVersions: []string{ContractVersionV1},
		Cookie:           "test-cookie",
		WorkspaceRoot:    "/tmp/ws",
		ReservedPrefix:   ".echo",
		IRVersion:        "v1",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if initResult.Capabilities.ConceptKinds["rule"] != CapabilitySupported {
		t.Fatalf("capabilities=%+v", initResult.Capabilities)
	}

	if err := client.Initialized(context.Background()); err != nil {
		t.Fatalf("Initialized: %v", err)
	}

	result, err := client.Emit(context.Background(), EmitParams{
		Target: "rule-test",
		IR:     []byte(`{"nodes":[{"id":"rule-test","kind":"rule"}]}`),
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(result.OpsPerformed) != 2 {
		t.Fatalf("ops=%v", result.OpsPerformed)
	}

	if err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	AssertProtocolShutdownAcked(t, server)
}

func TestSynthesizeInitResult_ProducesValidJSON(t *testing.T) {
	t.Parallel()

	data := SynthesizeInitResult("echo", "0.1", NewCapabilities().Supports("rule").Build(), []DeclaredOutput{
		{Path: ".echo", Mode: OutputModeOwnedSubdir},
	})

	var result InitializeResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.ProtocolVersion != ContractVersionV1 {
		t.Fatalf("protocol_version=%q", result.ProtocolVersion)
	}
}

func TestAssertProtocolShutdownAcked_FailsWhenNoAck(t *testing.T) {
	t.Parallel()

	server := NewServer("echo", "0.1")
	reporter := &stubReporter{}
	AssertProtocolShutdownAcked(reporter, server)
	if reporter.fatalMessage == "" {
		t.Fatal("expected fatal message")
	}
}

type stubReporter struct {
	fatalMessage string
}

func (s *stubReporter) Helper() {}

func (s *stubReporter) Fatal(args ...any) {
	s.fatalMessage = fmt.Sprint(args...)
}

func (s *stubReporter) Fatalf(format string, args ...any) {
	s.fatalMessage = fmt.Sprintf(format, args...)
}

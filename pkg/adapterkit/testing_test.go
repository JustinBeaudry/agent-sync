package adapterkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
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

	data, err := SynthesizeInitResult("echo", "0.1", NewCapabilities().Supports("rule").Build(), []DeclaredOutput{
		{Path: ".echo", Mode: OutputModeOwnedSubdir},
	})
	if err != nil {
		t.Fatalf("SynthesizeInitResult: %v", err)
	}

	var result InitializeResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.ProtocolVersion != ContractVersionV1 {
		t.Fatalf("protocol_version=%q", result.ProtocolVersion)
	}
}

func TestClient_Call_HonorsContextDeadline(t *testing.T) {
	t.Parallel()

	// Wire a Client to pipes whose server side never responds. The
	// write side can drain into adToRtW; the read side blocks on
	// rtToAdR forever because nothing ever writes a frame back.
	rtToAdR, rtToAdW := io.Pipe()
	adToRtR, _ := io.Pipe()
	t.Cleanup(func() {
		_ = rtToAdW.Close()
		_ = rtToAdR.Close()
		_ = adToRtR.Close()
	})

	// Drain anything the client writes so writeFrame doesn't block on
	// a full pipe.
	go func() {
		_, _ = io.Copy(io.Discard, rtToAdR)
	}()

	client := &Client{
		r: newFrameReader(adToRtR),
		w: rtToAdW,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	t.Cleanup(cancel)

	start := time.Now()
	_, err := client.Emit(ctx, EmitParams{Target: "noop"})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	// 150ms gives the goroutine room to schedule on a loaded CI box
	// without making the test sloppy. The point is "returns promptly",
	// not "returns at exactly 100ms".
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Client.call did not honor ctx promptly: elapsed=%v", elapsed)
	}
}

func TestAssertProtocolShutdownAcked_FailsWhenNoAck(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Name: "echo", Version: "0.1"})
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

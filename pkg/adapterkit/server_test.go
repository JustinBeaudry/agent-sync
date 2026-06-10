package adapterkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestServerRun_Lifecycle(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Name: "echo", Version: "0.1"})
	server.OnInitialize(func(ctx context.Context, params InitializeParams) (InitializeResult, error) {
		return InitializeResult{
			Capabilities: NewCapabilities().Supports("rule").Build(),
			DeclaredOutputs: []DeclaredOutput{
				{Path: ".echo", Mode: OutputModeOwnedSubdir},
			},
		}, nil
	})
	server.OnEmit(func(ctx context.Context, params EmitParams) (EmitResult, error) {
		return EmitResult{
			OpsPerformed: []OpRecord{
				{Op: OpKindMkdir, Path: ".echo"},
				{Op: OpKindWriteFile, Path: ".echo/no-fri.md"},
			},
		}, nil
	})

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
	if initResult.ProtocolVersion != ContractVersionV1 {
		t.Fatalf("protocol_version=%q", initResult.ProtocolVersion)
	}

	if err := client.Initialized(context.Background()); err != nil {
		t.Fatalf("Initialized: %v", err)
	}

	emitResult, err := client.Emit(context.Background(), EmitParams{
		Target: "test",
		IR:     []byte(`{"nodes":[{"id":"no-fri","kind":"rule"}]}`),
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(emitResult.OpsPerformed) != 2 {
		t.Fatalf("ops=%v", emitResult.OpsPerformed)
	}

	if err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	AssertProtocolShutdownAcked(t, server)
}

func TestServerRun_MissingCookieReturnsExit7(t *testing.T) {
	t.Parallel()

	stderr := &bytes.Buffer{}
	server := NewServer(ServerOptions{
		Name:    "echo",
		Version: "0.1",
		Stdin:   bytes.NewReader(nil),
		Stdout:  io.Discard,
		Stderr:  stderr,
		Getenv:  func(string) string { return "" },
	})

	err := server.Run(context.Background())
	if ExitCode(err) != MissingCookieExitCode {
		t.Fatalf("ExitCode=%d want %d (err=%v)", ExitCode(err), MissingCookieExitCode, err)
	}
	if !strings.Contains(stderr.String(), "AGENT_SYNC_ADAPTER_COOKIE") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestServerRun_InvalidCookieFormatReturnsExit7(t *testing.T) {
	t.Parallel()

	stderr := &bytes.Buffer{}
	server := NewServer(ServerOptions{
		Name:    "echo",
		Version: "0.1",
		Stdin:   bytes.NewReader(nil),
		Stdout:  io.Discard,
		Stderr:  stderr,
		Getenv:  func(string) string { return "test-cookie" },
	})

	err := server.Run(context.Background())
	if ExitCode(err) != MissingCookieExitCode {
		t.Fatalf("ExitCode=%d want %d (err=%v)", ExitCode(err), MissingCookieExitCode, err)
	}
	if !strings.Contains(stderr.String(), "invalid format") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestServerRun_ProtocolVersionMismatchReturnsTypedError(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Name: "echo", Version: "0.1"})
	client, cleanup := RunInprocServer(t, server)
	t.Cleanup(cleanup)

	err := client.call(context.Background(), MethodInitialize, InitializeParams{
		Client:           "test",
		ProtocolVersions: []string{"agent-sync/v0"},
		Cookie:           "test-cookie",
		WorkspaceRoot:    "/tmp/ws",
		ReservedPrefix:   ".echo",
		IRVersion:        "v1",
	}, &InitializeResult{})
	if err == nil {
		t.Fatal("expected protocol mismatch error")
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if rpcErr.Data.ErrorClass != ErrorClassAdapterProtocolMismatch {
		t.Fatalf("error_class=%q", rpcErr.Data.ErrorClass)
	}
}

func TestServerRun_RecoversHandlerPanics(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Name: "echo", Version: "0.1"})
	server.OnInitialize(func(ctx context.Context, params InitializeParams) (InitializeResult, error) {
		return InitializeResult{
			Capabilities: NewCapabilities().Supports("rule").Build(),
			DeclaredOutputs: []DeclaredOutput{
				{Path: ".echo", Mode: OutputModeOwnedSubdir},
			},
		}, nil
	})
	server.OnEmit(func(ctx context.Context, params EmitParams) (EmitResult, error) {
		panic("boom")
	})

	client, cleanup := RunInprocServer(t, server)
	t.Cleanup(cleanup)

	_, err := client.Initialize(context.Background(), InitializeParams{
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
	if err := client.Initialized(context.Background()); err != nil {
		t.Fatalf("Initialized: %v", err)
	}

	_, err = client.Emit(context.Background(), EmitParams{
		Target: "test",
		IR:     []byte(`{"nodes":[{"id":"panic","kind":"rule"}]}`),
	})
	if err == nil {
		t.Fatal("expected panic error")
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if rpcErr.Data.ErrorClass != ErrorClassAdapterPanic {
		t.Fatalf("error_class=%q", rpcErr.Data.ErrorClass)
	}
}

func TestDispatchRequest_ShutdownWriteFailureDoesNotAckProtocolShutdown(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{
		Name:    "echo",
		Version: "0.1",
		Stdout:  failingWriter{err: errors.New("write failed")},
	})
	server.setState(serverStateReady)

	err := server.dispatchRequest(context.Background(), inboundMessage{
		kind:   inboundKindRequest,
		id:     json.RawMessage("1"),
		method: MethodShutdown,
	})
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("dispatchRequest error=%v", err)
	}
	if server.protocolShutdownAcked() {
		t.Fatal("shutdown should not be acknowledged after write failure")
	}

	reporter := &stubReporter{}
	AssertProtocolShutdownAcked(reporter, server)
	if reporter.fatalMessage == "" {
		t.Fatal("AssertProtocolShutdownAcked should fail when shutdown was not acknowledged")
	}
}

func TestServerRun_InitializedNotificationLogsErrorToStderr(t *testing.T) {
	t.Parallel()

	stderr := &bytes.Buffer{}
	server := NewServer(ServerOptions{
		Name:    "echo",
		Version: "0.1",
		Stdin: bytes.NewReader(mustFrame(t, map[string]any{
			"jsonrpc": JSONRPCVersion,
			"method":  MethodInitialized,
		})),
		Stdout: io.Discard,
		Stderr: stderr,
		Getenv: func(string) string { return "0123456789abcdef0123456789abcdef" },
	})

	err := server.Run(context.Background())
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if !strings.Contains(stderr.String(), "initialized handler error") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestServerRun_TruncatedHeaderReturnsUnexpectedEOF(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{
		Name:    "echo",
		Version: "0.1",
		Stdin:   strings.NewReader("Content-Length: 10"),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		Getenv:  func(string) string { return "0123456789abcdef0123456789abcdef" },
	})

	err := server.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF, got %v", err)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func mustFrame(t *testing.T, payload any) []byte {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var buf bytes.Buffer
	if err := writeFrame(&buf, data); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	return buf.Bytes()
}

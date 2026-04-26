package adapterkit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestServerRun_Lifecycle(t *testing.T) {
	t.Parallel()

	server := NewServer("echo", "0.1")
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

	server := NewServer("echo", "0.1")
	server.stdin = bytes.NewReader(nil)
	server.stdout = io.Discard
	stderr := &bytes.Buffer{}
	server.stderr = stderr
	server.getenv = func(string) string { return "" }

	err := server.Run(context.Background())
	if ExitCode(err) != MissingCookieExitCode {
		t.Fatalf("ExitCode=%d want %d (err=%v)", ExitCode(err), MissingCookieExitCode, err)
	}
	if !strings.Contains(stderr.String(), "AIENVS_ADAPTER_COOKIE") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestServerRun_ProtocolVersionMismatchReturnsTypedError(t *testing.T) {
	t.Parallel()

	server := NewServer("echo", "0.1")
	client, cleanup := RunInprocServer(t, server)
	t.Cleanup(cleanup)

	err := client.call(context.Background(), MethodInitialize, InitializeParams{
		Client:           "test",
		ProtocolVersions: []string{"aienvs/v0"},
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

	server := NewServer("echo", "0.1")
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

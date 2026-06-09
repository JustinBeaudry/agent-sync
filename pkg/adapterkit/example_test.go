package adapterkit_test

import (
	"context"
	"fmt"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

func ExampleNewServer() {
	server := adapterkit.NewServer(adapterkit.ServerOptions{Name: "echo", Version: "0.1"})
	server.OnInitialize(func(ctx context.Context, params adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
		return adapterkit.InitializeResult{
			Capabilities: adapterkit.NewCapabilities().Supports("rule").Build(),
			DeclaredOutputs: []adapterkit.DeclaredOutput{
				{Path: ".echo", Mode: adapterkit.OutputModeOwnedSubdir},
			},
		}, nil
	})
	_ = server
	// Output:
}

func ExampleNewCapabilities() {
	caps := adapterkit.NewCapabilities().
		Supports("rule").
		Partial("skill").
		Unsupported("mcp-server-entry").
		WithWriteToolOwned(true).
		Build()

	fmt.Println(caps.ConceptKinds["rule"], caps.ConceptKinds["skill"], caps.WriteToolOwned)
	// Output:
	// supported partial true
}

func ExampleRunInprocServer() {
	server := adapterkit.NewServer(adapterkit.ServerOptions{Name: "echo", Version: "0.1"})
	server.OnInitialize(func(ctx context.Context, params adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
		return adapterkit.InitializeResult{
			Capabilities: adapterkit.NewCapabilities().Supports("rule").Build(),
			DeclaredOutputs: []adapterkit.DeclaredOutput{
				{Path: ".echo", Mode: adapterkit.OutputModeOwnedSubdir},
			},
		}, nil
	})
	server.OnEmit(func(ctx context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
		return adapterkit.EmitResult{
			OpsPerformed: []adapterkit.OpRecord{
				{Op: adapterkit.OpKindMkdir, Path: ".echo"},
			},
		}, nil
	})

	client, cleanup := adapterkit.RunInprocServer(new(exampleT), server)
	defer cleanup()

	_, _ = client.Initialize(context.Background(), adapterkit.InitializeParams{
		Client:           "example",
		ProtocolVersions: []string{adapterkit.ContractVersionV1},
		Cookie:           "0123456789abcdef0123456789abcdef",
		WorkspaceRoot:    "/tmp/ws",
		ReservedPrefix:   ".echo",
		IRVersion:        "v1",
	})
	_ = client.Initialized(context.Background())
	result, _ := client.Emit(context.Background(), adapterkit.EmitParams{
		Target: "example",
		IR:     []byte(`{"nodes":[{"id":"one","kind":"rule"}]}`),
	})
	fmt.Println(len(result.OpsPerformed))
	// Output:
	// 1
}

type exampleT struct{}

func (*exampleT) Helper() {}

func (*exampleT) Fatal(args ...any) {
	panic(fmt.Sprint(args...))
}

func (*exampleT) Fatalf(format string, args ...any) {
	panic(fmt.Sprintf(format, args...))
}

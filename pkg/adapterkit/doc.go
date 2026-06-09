// Package adapterkit is the supported SDK for writing agent-sync adapters in Go.
// It handles framing, JSON-RPC envelopes, the magic-cookie handshake,
// capability negotiation, and lifecycle dispatch so adapter authors only write
// handlers.
//
// Quickstart:
//
//	server := adapterkit.NewServer(adapterkit.ServerOptions{
//		Name:    "echo",
//		Version: "0.1",
//	})
//	server.OnInitialize(func(context.Context, adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
//		return adapterkit.InitializeResult{
//			Capabilities:    adapterkit.NewCapabilities().Supports("rule").Build(),
//			DeclaredOutputs: []adapterkit.DeclaredOutput{{Path: ".echo", Mode: adapterkit.OutputModeOwnedSubdir}},
//		}, nil
//	})
//	server.OnEmit(func(context.Context, adapterkit.EmitParams) (adapterkit.EmitResult, error) {
//		return adapterkit.EmitResult{}, nil
//	})
//	if err := server.Run(context.Background()); err != nil {
//		os.Exit(adapterkit.ExitCode(err))
//	}
//
// Callers that branch on sentinel errors should see the var block in this
// package, especially ErrPayloadTooLarge, ErrUnknownOp, and ErrMissingEncoding.
//
// conformance/echo/main.go is the canonical end-to-end reference adapter.
// pkg/adapterkit/testing provides in-process helpers for unit tests.
//
// The non-test package does not import internal/ packages; the schema parity
// test in _test.go files imports internal/adapter/contract only to validate
// type-shape parity.
package adapterkit

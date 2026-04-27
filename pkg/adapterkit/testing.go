package adapterkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// ScriptedResponder is a convenience for building a Server from
// typed test hooks.
type ScriptedResponder struct {
	DeclaredOutputs []DeclaredOutput
	ConceptKinds    map[string]CapabilityLevel

	RespondToInitialize func(ctx context.Context, received InitializeParams) (InitializeResult, *Error)
	RespondToEmit       func(ctx context.Context, received EmitParams) (EmitResult, *Error)
	RespondToShutdown   func(ctx context.Context) *Error
}

func (r *ScriptedResponder) AsServer(name, version string) *Server {
	server := NewServer(ServerOptions{Name: name, Version: version})
	server.OnInitialize(func(ctx context.Context, received InitializeParams) (InitializeResult, error) {
		if r.RespondToInitialize != nil {
			result, rpcErr := r.RespondToInitialize(ctx, received)
			if rpcErr != nil {
				return InitializeResult{}, rpcErr
			}
			return result, nil
		}
		conceptKinds := r.ConceptKinds
		if conceptKinds == nil {
			conceptKinds = map[string]CapabilityLevel{}
		}
		declaredOutputs := r.DeclaredOutputs
		if declaredOutputs == nil {
			declaredOutputs = []DeclaredOutput{}
		}
		return InitializeResult{
			Server:          name + "/" + version,
			ProtocolVersion: ContractVersionV1,
			Capabilities: Capabilities{
				ConceptKinds:   conceptKinds,
				WriteToolOwned: true,
			},
			DeclaredOutputs: declaredOutputs,
		}, nil
	})
	server.OnEmit(func(ctx context.Context, received EmitParams) (EmitResult, error) {
		if r.RespondToEmit == nil {
			return EmitResult{OpsPerformed: []OpRecord{}}, nil
		}
		result, rpcErr := r.RespondToEmit(ctx, received)
		if rpcErr != nil {
			return EmitResult{}, rpcErr
		}
		return result, nil
	})
	server.OnShutdown(func(ctx context.Context) error {
		if r.RespondToShutdown == nil {
			return nil
		}
		rpcErr := r.RespondToShutdown(ctx)
		if rpcErr != nil {
			return rpcErr
		}
		return nil
	})
	return server
}

// SynthesizeInitResult marshals an InitializeResult for tests that want a
// canned response payload without standing up a full Server. Returns the
// marshal error so callers see when caps.Meta or outputs[*].Meta contain
// values that round-trip through json.RawMessage but fail json.Marshal
// (e.g. invalid UTF-8 inside a json.RawMessage placeholder).
func SynthesizeInitResult(name, version string, caps Capabilities, outputs []DeclaredOutput) ([]byte, error) {
	result := InitializeResult{
		Server:          name + "/" + version,
		ProtocolVersion: ContractVersionV1,
		Capabilities:    caps,
		DeclaredOutputs: outputs,
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("adapterkit: synthesize init result: %w", err)
	}
	return data, nil
}

// Client drives a Server over the in-memory testing transport.
type Client struct {
	r      *frameReader
	w      io.WriteCloser
	nextID atomic.Int64
}

func (c *Client) Initialize(ctx context.Context, params InitializeParams) (InitializeResult, error) {
	var result InitializeResult
	if err := c.call(ctx, MethodInitialize, params, &result); err != nil {
		return InitializeResult{}, err
	}
	return result, nil
}

func (c *Client) Initialized(ctx context.Context) error {
	// The `initialized` notification has no params per the adapter
	// contract. Passing nil here causes marshalRequestEnvelope to omit
	// the params field entirely, matching what internal/adapter/runtime
	// sends on the wire.
	return c.notify(ctx, MethodInitialized, nil)
}

func (c *Client) Emit(ctx context.Context, params EmitParams) (EmitResult, error) {
	var result EmitResult
	if err := c.call(ctx, MethodEmit, params, &result); err != nil {
		return EmitResult{}, err
	}
	return result, nil
}

func (c *Client) Shutdown(ctx context.Context) error {
	var result ShutdownResult
	return c.call(ctx, MethodShutdown, ShutdownParams{}, &result)
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := marshalRequestEnvelope(nil, method, params)
	if err != nil {
		return err
	}
	// Notifications are fire-and-forget — there is no response to wait
	// for. A pre-write ctx check is sufficient; once the bytes hand off
	// to the pipe the call returns immediately.
	return writeFrame(c.w, payload)
}

// readResult is the value the read goroutine in call delivers back to
// the caller: either a parsed frame or the error that terminated the
// read.
type readResult struct {
	frame []byte
	err   error
}

func (c *Client) call(ctx context.Context, method string, params any, into any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := c.nextID.Add(1)
	rawID := json.RawMessage(fmt.Appendf(nil, "%d", id))
	payload, err := marshalRequestEnvelope(rawID, method, params)
	if err != nil {
		return err
	}
	if err := writeFrame(c.w, payload); err != nil {
		return err
	}

	// Spawn a goroutine to perform the blocking read so we can select
	// on ctx.Done(). Same pattern used by InprocTransport.Close in
	// internal/adapter/inproc.go: a buffered channel guarantees the
	// goroutine can always send (even if the caller has abandoned the
	// wait), so the goroutine never leaks past the next server frame.
	//
	// Note: cancelling here returns ctx.Err() but the abandoned read
	// goroutine continues running in the background. The Client may be
	// in an unusable state until the server eventually responds (the
	// buffered send drains the result silently). Tests should treat a
	// ctx-cancelled call as fatal to the Client.
	resultCh := make(chan readResult, 1)
	go func() {
		frame, err := c.r.Read(DefaultMaxFrameBytes)
		resultCh <- readResult{frame: frame, err: err}
	}()

	var frame []byte
	select {
	case res := <-resultCh:
		if res.err != nil {
			return res.err
		}
		frame = res.frame
	case <-ctx.Done():
		return ctx.Err()
	}

	resp, err := parseResponseEnvelope(frame)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Result, into); err != nil {
		return err
	}
	return nil
}

type responseEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

func marshalRequestEnvelope(id json.RawMessage, method string, params any) ([]byte, error) {
	type envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id,omitempty"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}

	var paramsRaw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		paramsRaw = data
	}
	return json.Marshal(envelope{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  paramsRaw,
	})
}

func parseResponseEnvelope(data []byte) (responseEnvelope, error) {
	var resp responseEnvelope
	if err := json.Unmarshal(data, &resp); err != nil {
		return responseEnvelope{}, err
	}
	return resp, nil
}

type testReporter interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

func RunInprocServer(t testReporter, server *Server) (*Client, func()) {
	t.Helper()

	rtToAdapterR, rtToAdapterW := io.Pipe()
	adapterToRtR, adapterToRtW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	server.stdin = rtToAdapterR
	server.stdout = adapterToRtW
	server.stderr = io.Discard
	server.getenv = func(string) string { return "0123456789abcdef0123456789abcdef" }

	go func() {
		errCh <- server.Run(ctx)
	}()

	client := &Client{
		r: newFrameReader(adapterToRtR),
		w: rtToAdapterW,
	}

	cleanup := func() {
		cancel()
		_ = rtToAdapterW.Close()
		_ = adapterToRtW.Close()
		if err := <-errCh; err != nil &&
			ExitCode(err) == 0 &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("server.Run: %v", err)
		}
	}

	return client, cleanup
}

func AssertProtocolShutdownAcked(t testReporter, server *Server) {
	t.Helper()
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		if server.protocolShutdownAcked() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not acknowledge protocol shutdown")
			return
		}
		time.Sleep(time.Millisecond)
	}
}

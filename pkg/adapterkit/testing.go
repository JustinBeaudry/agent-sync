package adapterkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
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
	server := NewServer(name, version)
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

func SynthesizeInitResult(name, version string, caps Capabilities, outputs []DeclaredOutput) []byte {
	result := InitializeResult{
		Server:          name + "/" + version,
		ProtocolVersion: ContractVersionV1,
		Capabilities:    caps,
		DeclaredOutputs: outputs,
	}
	data, _ := json.Marshal(result)
	return data
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
	return c.notify(ctx, MethodInitialized, ShutdownParams{})
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

func (c *Client) notify(_ context.Context, method string, params any) error {
	payload, err := marshalRequestEnvelope(nil, method, params)
	if err != nil {
		return err
	}
	return writeFrame(c.w, payload)
}

func (c *Client) call(_ context.Context, method string, params any, into any) error {
	id := c.nextID.Add(1)
	rawID := json.RawMessage(fmt.Appendf(nil, "%d", id))
	payload, err := marshalRequestEnvelope(rawID, method, params)
	if err != nil {
		return err
	}
	if err := writeFrame(c.w, payload); err != nil {
		return err
	}
	frame, err := c.r.Read(DefaultMaxFrameBytes)
	if err != nil {
		return err
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
	server.getenv = func(string) string { return "test-cookie" }

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
	if !server.protocolShutdownAcked() {
		t.Fatal("server did not acknowledge protocol shutdown")
	}
}

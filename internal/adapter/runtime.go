package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aienvs/aienvs/internal/adapter/contract"
)

// Initialize sends the initialize request, validates the cookie echo
// and protocol version, and captures the adapter's declared_outputs +
// capabilities for later enforcement. After Initialize returns
// successfully, the session is ready for Initialized + Emit.
func (s *AdapterSession) Initialize(ctx context.Context) (*contract.InitializeResult, error) {
	if s.state != sessionStateNew {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("%w: Initialize called from state %d", ErrAdapterProtocolOrderViolation, s.state),
		}
	}

	timeout := s.timeouts().Handshake
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id := s.ids.Next()
	s.ids.MarkPending(id, contract.MethodInitialize)

	params := contract.InitializeParams{
		Client:           "aienvs",
		ProtocolVersions: []string{ContractVersionV1},
		Cookie:           s.cookie,
		WorkspaceRoot:    s.options.WorkspaceRoot,
		ReservedPrefix:   s.adapter.Manifest.ReservedPrefix,
		IRVersion:        s.options.IRVersion,
	}

	resp, err := s.requestResponse(rctx, id, contract.MethodInitialize, params)
	if err != nil {
		s.ids.Cancel(id)
		return nil, err
	}

	var result contract.InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("decode initialize result: %w", err),
		}
	}

	// Cookie validation: bundled adapters skip (they share process).
	// Subprocess adapters MUST echo the cookie verbatim.
	if s.adapter.Source != SourceBundled {
		if result.Meta != nil {
			// _meta is reserved; we just don't read from it here.
			_ = result.Meta
		}
		// The wire spec stores the echoed cookie either in the result
		// directly (some adapters echo back the entire init params
		// shape) or via _meta. Check both for compatibility.
		echoed := extractCookieFromResult(resp.Result)
		if echoed == "" {
			return nil, &RuntimeError{
				Class: contract.ErrorClassAdapterExecDenied,
				Err:   ErrAdapterCookieMissing,
			}
		}
		if echoed != s.cookie {
			return nil, &RuntimeError{
				Class: contract.ErrorClassAdapterExecDenied,
				Err:   fmt.Errorf("%w: adapter echoed %q (want %q)", ErrAdapterCookieMismatch, echoed, s.cookie),
			}
		}
	}

	if result.ProtocolVersion != ContractVersionV1 {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterProtocolMismatch,
			Err:   fmt.Errorf("%w: adapter speaks %q (want %q)", ErrAdapterProtocolMismatch, result.ProtocolVersion, ContractVersionV1),
		}
	}

	// Record which kinds were declared "supported" — used by
	// capability-lied detection after Emit.
	for kind, level := range result.Capabilities.ConceptKinds {
		if level == contract.CapabilitySupported {
			s.gateChecks[contract.OpKind(kind)] = true
		}
	}

	s.initResult = &result
	s.state = sessionStateInitialized
	return &result, nil
}

// Initialized sends the initialized notification. After this returns,
// the session is ready for Emit. The notification has no response;
// any error here is purely the wire-level send failure.
func (s *AdapterSession) Initialized(ctx context.Context) error {
	if s.state != sessionStateInitialized {
		return &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("%w: Initialized called from state %d", ErrAdapterProtocolOrderViolation, s.state),
		}
	}
	notification := contract.Notification{Method: contract.MethodInitialized}
	payload, err := json.Marshal(notification)
	if err != nil {
		return &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("marshal initialized notification: %w", err),
		}
	}
	if err := s.transport.Send(ctx, payload); err != nil {
		return s.classifySendError(err)
	}
	return nil
}

// Emit sends the emit request for one target with the given IR, then
// reads the streamed ops + final response. Each op is checked against
// the declared_outputs gate; any violation cancels the session.
//
// IR is supplied as json.RawMessage to keep this package decoupled
// from internal/ir.
func (s *AdapterSession) Emit(ctx context.Context, target string, ir json.RawMessage) (*contract.EmitResult, error) {
	if s.state != sessionStateInitialized && s.state != sessionStateEmitting {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("%w: Emit called from state %d", ErrAdapterProtocolOrderViolation, s.state),
		}
	}
	s.state = sessionStateEmitting

	timeout := s.timeouts().Emit
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id := s.ids.Next()
	s.ids.MarkPending(id, contract.MethodEmit)

	params := contract.EmitParams{
		Target: target,
		IR:     ir,
	}

	resp, err := s.requestResponse(rctx, id, contract.MethodEmit, params)
	if err != nil {
		s.ids.Cancel(id)
		return nil, err
	}

	var result contract.EmitResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("decode emit result: %w", err),
		}
	}

	// Declared-outputs integrity gate. Each performed op must target a
	// path inside one of the declared outputs. Warnings are exempt
	// (concept-level, no path).
	for i, op := range result.OpsPerformed {
		if op.Op == contract.OpKindWarning {
			continue
		}
		if !s.pathInDeclaredOutputs(op.Path) {
			return nil, &RuntimeError{
				Class: contract.ErrorClassAdapterUndeclaredOutput,
				Err: fmt.Errorf("%w: op[%d] kind=%s path=%q",
					ErrAdapterUndeclaredOutput, i, op.Op, op.Path),
			}
		}
	}

	return &result, nil
}

// Shutdown sends the shutdown request and tears down the transport.
// Returns the classified termination error (nil for clean exit).
//
// Shutdown is the terminal phase — after it returns, the session is
// finalized and no further methods may be called.
func (s *AdapterSession) Shutdown(ctx context.Context) error {
	switch s.state {
	case sessionStateClosed:
		return nil
	case sessionStateNew:
		// Shutdown without Initialize: just tear down the transport.
		s.state = sessionStateClosed
		return s.transport.Close(ctx)
	}

	timeout := s.timeouts().Shutdown
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id := s.ids.Next()
	s.ids.MarkPending(id, contract.MethodShutdown)

	params := contract.ShutdownParams{}
	_, err := s.requestResponse(rctx, id, contract.MethodShutdown, params)
	if err != nil {
		s.ids.Cancel(id)
		// Don't return here; still tear down the transport so the
		// process exits. Log the error via the closeErr.
	}

	s.state = sessionStateShutdown

	closeErr := s.transport.Close(ctx)

	// Cancel any pending ids that never got resolved; the transport
	// has already torn down so they'd never resolve anyway.
	if leaked := s.ids.Pending(); leaked > 0 {
		// Cancel by walking known ids would require an enumeration
		// API on IDCorrelator we don't have; the leak is bounded by
		// the session lifetime, so we accept this — the runtime is
		// about to drop the IDCorrelator entirely.
		_ = leaked
	}

	s.state = sessionStateClosed

	if err != nil {
		return err
	}
	if closeErr != nil {
		return classifyTransportError(closeErr)
	}
	return nil
}

// requestResponse sends a request, waits for the matching response,
// and returns it. Honors ctx for cancellation. Errors are wrapped in
// RuntimeError with the appropriate ErrorClass.
func (s *AdapterSession) requestResponse(ctx context.Context, id contract.ID, method string, params interface{}) (*contract.Message, error) {
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("marshal %s params: %w", method, err),
		}
	}

	req := contract.Request{
		ID:     id,
		Method: method,
		Params: paramsRaw,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("marshal %s envelope: %w", method, err),
		}
	}

	if err := s.transport.Send(ctx, payload); err != nil {
		return nil, s.classifySendError(err)
	}

	// Read the matching response. v1 doesn't allow adapter→runtime
	// requests or notifications outside the initialized notification
	// (which flows runtime→adapter), so we expect exactly one frame
	// in response. Interleaved progress notifications are an 8b feature.
	raw, err := s.transport.Recv(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &RuntimeError{
				Class: contract.ErrorClassAdapterTimeout,
				Err:   fmt.Errorf("%w: %s exceeded %s", ErrAdapterTimeout, method, s.timeouts().Handshake),
			}
		}
		return nil, classifyTransportError(err)
	}
	msg, err := contract.ParseMessage(raw)
	if err != nil {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("parse message: %w", err),
		}
	}
	if msg.Kind != contract.MessageKindResponse {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("%w: got %v while awaiting %s response", ErrAdapterUnexpectedMessage, msg.Kind, method),
		}
	}
	if !msg.HasID || msg.ID.AsInt() != id.AsInt() {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterPanic,
			Err:   fmt.Errorf("%w: response id mismatch (want %d)", ErrAdapterUnexpectedMessage, id.AsInt()),
		}
	}
	_, _ = s.ids.Resolve(msg.ID)
	if msg.Error != nil {
		return nil, &RuntimeError{
			Class:        msg.Error.Data.ErrorClass,
			Err:          fmt.Errorf("adapter returned error %d: %s", msg.Error.Code, msg.Error.Message),
			AdapterError: msg.Error,
		}
	}
	return &msg, nil
}

// pathInDeclaredOutputs returns true when path is contained within one
// of the adapter's declared outputs. Empty path is rejected (the gate
// is path-based; warnings exempt themselves at the caller).
func (s *AdapterSession) pathInDeclaredOutputs(path string) bool {
	if path == "" {
		return false
	}
	if s.initResult == nil {
		return false
	}
	for _, decl := range s.initResult.DeclaredOutputs {
		if path == decl.Path || strings.HasPrefix(path, decl.Path+"/") {
			return true
		}
	}
	return false
}

// classifySendError wraps a transport.Send error in a RuntimeError.
// Send failures are typically transport-level (process died, pipe
// closed); classify as adapter-panic.
func (s *AdapterSession) classifySendError(err error) *RuntimeError {
	if errors.Is(err, context.DeadlineExceeded) {
		return &RuntimeError{
			Class: contract.ErrorClassAdapterTimeout,
			Err:   fmt.Errorf("%w: send: %s", ErrAdapterTimeout, err.Error()),
		}
	}
	return classifyTransportError(err)
}

// classifyTransportError wraps a transport-layer error in a
// RuntimeError, attaching stderr_tail when available.
func classifyTransportError(err error) *RuntimeError {
	if err == nil {
		return nil
	}
	rt := &RuntimeError{
		Class: contract.ErrorClassAdapterPanic,
		Err:   err,
	}
	var sxerr *SubprocessExitError
	if errors.As(err, &sxerr) {
		rt.ExitCode = sxerr.ExitCode
		rt.StderrTail = sxerr.StderrTail
	}
	return rt
}

// timeouts returns the session's resolved timeouts (option overrides
// applied on top of defaults).
func (s *AdapterSession) timeouts() SubprocessTimeouts {
	t := s.options.Timeouts
	d := DefaultSubprocessTimeouts()
	if t.Handshake == 0 {
		t.Handshake = d.Handshake
	}
	if t.Emit == 0 {
		t.Emit = d.Emit
	}
	if t.Shutdown == 0 {
		t.Shutdown = d.Shutdown
	}
	return t
}

// extractCookieFromResult parses the raw initialize result JSON for a
// "cookie" field. Adapters may echo the cookie at the top level or
// inside _meta; both forms are accepted.
func extractCookieFromResult(raw json.RawMessage) string {
	var loose map[string]json.RawMessage
	if err := json.Unmarshal(raw, &loose); err != nil {
		return ""
	}
	if v, ok := loose["cookie"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			return s
		}
	}
	if v, ok := loose["_meta"]; ok {
		var meta map[string]json.RawMessage
		if err := json.Unmarshal(v, &meta); err == nil {
			if c, ok := meta["cookie"]; ok {
				var s string
				if err := json.Unmarshal(c, &s); err == nil {
					return s
				}
			}
		}
	}
	return ""
}

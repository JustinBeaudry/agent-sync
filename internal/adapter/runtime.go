package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

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

	timeout := resolveTimeouts(s.options.Timeouts).Handshake
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

	resp, err := s.requestResponse(rctx, id, contract.MethodInitialize, params, contract.MethodInitialize, timeout)
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
	// Subprocess adapters MUST echo the cookie verbatim. The cookie
	// itself never appears in the error message — leaking it would
	// undermine the auth boundary it exists to enforce.
	if s.adapter.Source != SourceBundled {
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
				Err:   ErrAdapterCookieMismatch,
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
		return classifySendError(err)
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

	timeout := resolveTimeouts(s.options.Timeouts).Emit
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id := s.ids.Next()
	s.ids.MarkPending(id, contract.MethodEmit)

	params := contract.EmitParams{
		Target: target,
		IR:     ir,
	}

	resp, err := s.requestResponse(rctx, id, contract.MethodEmit, params, contract.MethodEmit, timeout)
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
	nonWarningOps := 0
	for i, op := range result.OpsPerformed {
		if op.Op == contract.OpKindWarning {
			continue
		}
		nonWarningOps++
		if !s.pathInDeclaredOutputs(op.Path) {
			return nil, &RuntimeError{
				Class: contract.ErrorClassAdapterUndeclaredOutput,
				Err: fmt.Errorf("%w: op[%d] kind=%s path=%q",
					ErrAdapterUndeclaredOutput, i, op.Op, op.Path),
			}
		}
	}

	// Capability-lied detection: if the adapter declared any kind as
	// "supported" in initialize and the IR contained nodes of those
	// kinds, then the adapter must have emitted at least one
	// non-warning op. An adapter that says "yes, I handle X" and then
	// silently does nothing is a worse failure mode than a "won't
	// handle X" honest decline — silent nullification breaks the
	// determinism guarantee.
	if nonWarningOps == 0 && irContainsSupportedKind(ir, s.gateChecks) {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterCapabilityLied,
			Err:   ErrAdapterCapabilityLied,
		}
	}

	return &result, nil
}

// irContainsSupportedKind reports whether the IR has at least one node
// whose kind appears in the supported set. Best-effort: a malformed IR
// returns false (no false positives — better to under-report than to
// flag a legitimate adapter for what is really a runtime bug).
func irContainsSupportedKind(ir json.RawMessage, supported map[contract.OpKind]bool) bool {
	if len(supported) == 0 {
		return false
	}
	var doc struct {
		Nodes []struct {
			Kind string `json:"kind"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(ir, &doc); err != nil {
		return false
	}
	for _, n := range doc.Nodes {
		if supported[contract.OpKind(n.Kind)] {
			return true
		}
	}
	return false
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

	timeout := resolveTimeouts(s.options.Timeouts).Shutdown
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id := s.ids.Next()
	s.ids.MarkPending(id, contract.MethodShutdown)

	params := contract.ShutdownParams{}
	_, err := s.requestResponse(rctx, id, contract.MethodShutdown, params, contract.MethodShutdown, timeout)
	if err != nil {
		s.ids.Cancel(id)
		// Don't return here; still tear down the transport so the
		// process exits. Log the error via the closeErr.
	}

	closeErr := s.transport.Close(ctx)

	// Any ids still pending here have been abandoned: the adapter
	// returned an error or the transport was torn down before a
	// response arrived. The IDCorrelator is about to be dropped along
	// with this session, so the leak is bounded by session lifetime.
	// IDCorrelator gained max-pending eviction in PR 2 (ADV-006) so a
	// misbehaving adapter cannot grow this set without bound.

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
//
// phase identifies the lifecycle phase for error messages (initialize,
// emit, shutdown). phaseTimeout is the per-phase deadline so a timeout
// error reports the correct duration — without this, a 5s handshake
// timeout was being reported as "exceeded 30s" because the runtime
// always read its handshake timeout for the message.
func (s *AdapterSession) requestResponse(ctx context.Context, id contract.ID, method string, params interface{}, phase string, phaseTimeout time.Duration) (*contract.Message, error) {
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
		return nil, classifySendError(err)
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
				Err:   fmt.Errorf("%w: %s exceeded %s", ErrAdapterTimeout, phase, phaseTimeout),
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

// pathInDeclaredOutputs reports whether opPath is contained within
// one of the adapter's declared_outputs. Both sides are normalized
// with path.Clean before comparison so an adapter can't smuggle a
// path containing ".." or duplicate "/" segments through the gate.
//
// Returns false for empty paths (warnings exempt themselves at the
// caller), absolute paths (declared_outputs are workspace-relative),
// and paths whose cleaned form starts with ".." (escapes the
// workspace).
func (s *AdapterSession) pathInDeclaredOutputs(opPath string) bool {
	if opPath == "" {
		return false
	}
	if s.initResult == nil {
		return false
	}
	if path.IsAbs(opPath) {
		return false
	}
	clean := path.Clean(opPath)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	for _, decl := range s.initResult.DeclaredOutputs {
		if decl.Path == "" {
			continue
		}
		declClean := path.Clean(decl.Path)
		if path.IsAbs(declClean) {
			continue
		}
		if clean == declClean || strings.HasPrefix(clean, declClean+"/") {
			return true
		}
	}
	return false
}

// classifySendError wraps a transport.Send error in a RuntimeError.
// Send failures are typically transport-level (process died, pipe
// closed); classify as adapter-panic. Free function — does not need
// session state.
func classifySendError(err error) *RuntimeError {
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

// resolveTimeouts fills zero fields in the supplied timeout struct
// from DefaultSubprocessTimeouts. Pure function so it's trivial to
// test in isolation.
func resolveTimeouts(t SubprocessTimeouts) SubprocessTimeouts {
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
// top-level "cookie" field. The contract pins the cookie to a single
// canonical location to avoid ambiguity — adapters that echo the
// cookie elsewhere fail validation the same way as adapters that omit
// it entirely.
func extractCookieFromResult(raw json.RawMessage) string {
	var loose struct {
		Cookie string `json:"cookie"`
	}
	if err := json.Unmarshal(raw, &loose); err != nil {
		return ""
	}
	return loose.Cookie
}

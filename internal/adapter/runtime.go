package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter/contract"
)

// reWindowsVolumePrefix matches a leading drive letter ("C:", "d:", ...).
// Such a prefix is absolute on Windows when interpreted by OS path rules
// even though path.IsAbs (which uses POSIX rules) reports it as relative,
// so we reject it on every OS to keep declared-outputs gating safe
// regardless of host platform. Same shape used in
// manifest.ValidateReservedPrefix.
var reWindowsVolumePrefix = regexp.MustCompile(`\A[A-Za-z]:`)

// Initialize sends the initialize request, validates the cookie echo
// and protocol version, and captures the adapter's declared_outputs +
// capabilities for later enforcement. After Initialize returns
// successfully, the session is ready for Initialized + Emit.
func (s *AdapterSession) Initialize(ctx context.Context) (*contract.InitializeResult, error) {
	if s.state != sessionStateNew {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterProtocolOrder,
			Err:   fmt.Errorf("%w: Initialize called from state %d", ErrAdapterProtocolOrderViolation, s.state),
		}
	}

	timeout := resolveTimeouts(s.options.Timeouts).Handshake
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id := s.ids.Next()
	s.ids.MarkPending(id, contract.MethodInitialize)

	scope := s.options.Scope
	if scope == "" {
		scope = "project" // back-compat default: absent ⇒ project scope
	}
	params := contract.InitializeParams{
		Client:           "agent-sync",
		ProtocolVersions: []string{ContractVersionV1},
		Cookie:           s.cookie,
		WorkspaceRoot:    s.options.WorkspaceRoot,
		ReservedPrefix:   s.adapter.Manifest.ReservedPrefix,
		IRVersion:        s.options.IRVersion,
		Scope:            scope,
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
			Class: contract.ErrorClassAdapterProtocolOrder,
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
			Class: contract.ErrorClassAdapterProtocolOrder,
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
	// kinds *targeted at this adapter*, then the adapter must have
	// emitted at least one non-warning op. An adapter that says "yes,
	// I handle X" and then silently does nothing is a worse failure
	// mode than a "won't handle X" honest decline — silent
	// nullification breaks the determinism guarantee.
	//
	// The per-node `targets` filter matters: an IR node with
	// `targets: [other-adapter]` is intentionally invisible to this
	// adapter, so a zero-op response is correct (not a lie). Without
	// this filter, mixed-target manifests fail at sync time even
	// though every adapter behaves correctly.
	if nonWarningOps == 0 && irContainsSupportedKindForTarget(ir, s.gateChecks, s.adapter.Manifest.Name) {
		return nil, &RuntimeError{
			Class: contract.ErrorClassAdapterCapabilityLied,
			Err:   ErrAdapterCapabilityLied,
		}
	}

	return &result, nil
}

// irContainsSupportedKindForTarget reports whether the IR has at
// least one node whose kind appears in the supported set AND whose
// per-node `targets` filter applies to the given adapter. A node
// with empty `targets` applies to every adapter (matches the IR v1
// spec); a node listing specific targets only applies if `target`
// appears in that list.
//
// Best-effort: a malformed IR returns false (no false positives —
// better to under-report than to flag a legitimate adapter for what
// is really a runtime bug).
//
// The IR is scanned with a streaming decoder so a large IR doesn't
// force a full unmarshal into memory. We walk the top-level object
// looking for a "nodes" array, then decode one element at a time
// into a small struct that captures only "kind" and "targets" —
// short-circuiting on the first supported, in-target match.
func irContainsSupportedKindForTarget(ir json.RawMessage, supported map[contract.OpKind]bool, target string) bool {
	if len(supported) == 0 {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(ir))

	// Expect the document to open with "{".
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return false
	}

	// Walk top-level keys. For "nodes", drop into the array and scan
	// elements one at a time. For everything else, skip the value.
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return false
		}
		key, ok := keyTok.(string)
		if !ok {
			return false
		}
		if key != "nodes" {
			if err := skipJSONValue(dec); err != nil {
				return false
			}
			continue
		}
		// Expect "[".
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return false
		}
		for dec.More() {
			var node struct {
				Kind    string   `json:"kind"`
				Targets []string `json:"targets"`
			}
			if err := dec.Decode(&node); err != nil {
				return false
			}
			if !supported[contract.OpKind(node.Kind)] {
				continue
			}
			if nodeAppliesToTarget(node.Targets, target) {
				return true
			}
		}
		// Consume the closing "]". After "nodes" there's nothing else
		// we need from the IR; done either way.
		if _, err := dec.Token(); err != nil {
			return false
		}
		return false
	}
	return false
}

// nodeAppliesToTarget reports whether a node with the given Targets
// list applies to the supplied adapter target. Empty Targets means
// "all adapters" per the IR v1 spec.
func nodeAppliesToTarget(targets []string, target string) bool {
	if len(targets) == 0 {
		return true
	}
	for _, t := range targets {
		if t == target {
			return true
		}
	}
	return false
}

// skipJSONValue consumes the next JSON value from dec (object, array,
// or scalar) and discards it. Used to skip top-level fields that
// irContainsSupportedKind does not care about without buffering them.
func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		// Scalar — already consumed.
		return nil
	}
	if d != '{' && d != '[' {
		return fmt.Errorf("unexpected delimiter %q", d)
	}
	depth := 1
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if dd, ok := t.(json.Delim); ok {
			switch dd {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
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
	} else {
		// Protocol-level shutdown succeeded. Inform the transport so
		// its Close path knows to treat any non-zero exit as expected.
		// Without this signal, an adapter that returns from main with
		// a non-zero status after responding to shutdown would surface
		// as a SubprocessExitError, which would mis-classify a clean
		// shutdown as a fault.
		s.transport.MarkProtocolShutdownAcked()
	}

	closeErr := s.transport.Close(ctx)

	// Any ids still pending here have been abandoned: the adapter
	// returned an error or the transport was torn down before a
	// response arrived. Failed requests are explicitly removed with
	// Cancel(id), and any ids still pending at this point will be
	// dropped with the IDCorrelator when this session is closed, so
	// retention is bounded by session lifetime rather than by a
	// max-pending eviction policy.

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
		// Class is documented to always be populated. Adapters MAY omit
		// the optional error_class data field; default to adapter-panic
		// so callers never see an empty classifier.
		class := msg.Error.Data.ErrorClass
		if class == "" {
			class = contract.ErrorClassAdapterPanic
		}
		return nil, &RuntimeError{
			Class:        class,
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
// paths whose cleaned form starts with ".." (escapes the workspace),
// and paths containing a backslash or Windows volume prefix — those
// shapes are not absolute under path.IsAbs (which uses the forward-
// slash POSIX rules) but are absolute under OS path rules on Windows
// and would let an op escape the workspace once interpreted there.
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
	// Windows-safety: path.IsAbs ignores both backslash separators and
	// drive-letter volume prefixes, so a path like "C:\\foo" or "..\\..\\etc"
	// would slip past the relative-path gate above. Reject both shapes
	// outright on every OS — they are never valid for a workspace-relative
	// path on the wire, regardless of host platform.
	if strings.ContainsRune(opPath, '\\') {
		return false
	}
	if reWindowsVolumePrefix.MatchString(opPath) {
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
		// path.Clean turns "" or "./" into ".". A declared output of "."
		// is the workspace root: every workspace-relative path is contained
		// within it. Without this case the prefix check below would fail
		// for any normalized path that doesn't literally start with "./".
		if declClean == "." {
			return true
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

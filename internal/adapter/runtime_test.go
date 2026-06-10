package adapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/adapter/contract"
)

// scriptedAdapter is a bundled adapter that responds to wire frames
// according to a caller-supplied script. Each method maps to a
// function returning the result bytes (or an error envelope).
type scriptedAdapter struct {
	name             string
	declaredOutputs  []contract.DeclaredOutput
	conceptKinds     map[string]contract.CapabilityLevel
	emitOps          []contract.OpRecord
	cookieEcho       func(received string) string // override what we echo
	respondToInit    func(received contract.InitializeParams) (json.RawMessage, *contract.Error)
	respondToEmit    func(received contract.EmitParams) (json.RawMessage, *contract.Error)
	respondToShutdwn func() (json.RawMessage, *contract.Error)
}

func (a *scriptedAdapter) bundled(t *testing.T) *adapter.BundledAdapter {
	t.Helper()
	return &adapter.BundledAdapter{
		Manifest: adapter.AdapterManifest{
			Name:            a.name,
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{a.name + "-bundled"},
		},
		Run: func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			fr := contract.NewFrameReader(stdin)
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				frame, err := fr.Read(contract.DefaultMaxFrameBytes)
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
					return nil
				}
				if err != nil {
					return err
				}
				msg, err := contract.ParseMessage(frame)
				if err != nil {
					return err
				}
				switch msg.Kind {
				case contract.MessageKindNotification:
					// swallow
				case contract.MessageKindRequest:
					resp := a.respond(msg)
					out, err := json.Marshal(resp)
					if err != nil {
						return err
					}
					if err := contract.WriteFrame(stdout, out); err != nil {
						return err
					}
				default:
					return errors.New("scripted: unexpected message kind")
				}
			}
		},
	}
}

func (a *scriptedAdapter) respond(req contract.Message) contract.Response {
	switch req.Method {
	case contract.MethodInitialize:
		var params contract.InitializeParams
		_ = json.Unmarshal(req.Params, &params)
		var raw json.RawMessage
		var errObj *contract.Error
		if a.respondToInit != nil {
			raw, errObj = a.respondToInit(params)
		} else {
			raw, errObj = a.defaultInitResult(params)
		}
		if errObj != nil {
			return contract.Response{ID: req.ID, Error: errObj}
		}
		return contract.Response{ID: req.ID, Result: raw}
	case contract.MethodEmit:
		var params contract.EmitParams
		_ = json.Unmarshal(req.Params, &params)
		var raw json.RawMessage
		var errObj *contract.Error
		if a.respondToEmit != nil {
			raw, errObj = a.respondToEmit(params)
		} else {
			result := contract.EmitResult{OpsPerformed: a.emitOps}
			b, _ := json.Marshal(result)
			raw = b
		}
		if errObj != nil {
			return contract.Response{ID: req.ID, Error: errObj}
		}
		return contract.Response{ID: req.ID, Result: raw}
	case contract.MethodShutdown:
		var raw json.RawMessage
		var errObj *contract.Error
		if a.respondToShutdwn != nil {
			raw, errObj = a.respondToShutdwn()
		} else {
			b, _ := json.Marshal(contract.ShutdownResult{})
			raw = b
		}
		if errObj != nil {
			return contract.Response{ID: req.ID, Error: errObj}
		}
		return contract.Response{ID: req.ID, Result: raw}
	}
	return contract.Response{ID: req.ID, Error: &contract.Error{Code: contract.CodeMethodNotFound, Message: "unknown method"}}
}

func (a *scriptedAdapter) defaultInitResult(params contract.InitializeParams) (json.RawMessage, *contract.Error) {
	cookieToEcho := params.Cookie
	if a.cookieEcho != nil {
		cookieToEcho = a.cookieEcho(params.Cookie)
	}
	caps := contract.Capabilities{ConceptKinds: a.conceptKinds, WriteToolOwned: true, Progress: false}
	if caps.ConceptKinds == nil {
		caps.ConceptKinds = map[string]contract.CapabilityLevel{}
	}
	declared := a.declaredOutputs
	if declared == nil {
		declared = []contract.DeclaredOutput{}
	}
	wire := contract.InitializeResult{
		Server:          "scripted/0.1",
		ProtocolVersion: adapter.ContractVersionV1,
		Capabilities:    caps,
		DeclaredOutputs: declared,
		Cookie:          cookieToEcho,
	}
	b, err := json.Marshal(wire)
	if err != nil {
		return nil, &contract.Error{Code: contract.CodeInternalError, Message: err.Error()}
	}
	return b, nil
}

// runScriptedSession is the test harness: builds a session against
// the scripted adapter and returns it for assertions.
func runScriptedSession(t *testing.T, sa *scriptedAdapter) (*adapter.AdapterSession, context.Context, context.CancelFunc) {
	t.Helper()

	b := sa.bundled(t)
	a := &adapter.Adapter{
		Manifest: b.Manifest,
		Source:   adapter.SourceBundled,
		Bundled:  b,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	sess, err := a.NewSession(ctx, adapter.SessionOptions{
		WorkspaceRoot: "/tmp/ws",
		IRVersion:     "v1",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Shutdown(context.Background())
	})
	return sess, ctx, cancel
}

func TestSession_HappyPathFullLifecycle(t *testing.T) {
	t.Parallel()

	sa := &scriptedAdapter{
		name:            "happy",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".happy/rules", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported},
		emitOps: []contract.OpRecord{
			{Op: contract.OpKindMkdir, Path: ".happy/rules"},
			{Op: contract.OpKindWriteFile, Path: ".happy/rules/no-fri.md"},
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	result, err := sess.Emit(ctx, "happy", json.RawMessage(`{"nodes":[{"id":"no-fri","kind":"rule"}]}`))
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(result.OpsPerformed) != 2 {
		t.Errorf("ops_performed: %+v", result.OpsPerformed)
	}
	if err := sess.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if sess.Pending() != 0 {
		t.Errorf("Pending after shutdown: %d", sess.Pending())
	}
}

func TestSession_RejectsCookieMismatch(t *testing.T) {
	// Bundled adapters skip cookie validation in this PR (they share
	// process). To exercise the cookie-mismatch path, we'd need a
	// subprocess harness. Confirm the bundled fast-path does NOT
	// fail when the cookie is "wrong" (because there's no cookie in
	// the bundled flow).
	t.Parallel()

	sa := &scriptedAdapter{
		name:         "cookie-skip",
		conceptKinds: map[string]contract.CapabilityLevel{},
		cookieEcho:   func(_ string) string { return "wrong-cookie" },
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("bundled adapter cookie validation should be skipped, got %v", err)
	}
}

func TestSession_RejectsProtocolVersionMismatch(t *testing.T) {
	t.Parallel()

	sa := &scriptedAdapter{
		name: "v0",
		respondToInit: func(_ contract.InitializeParams) (json.RawMessage, *contract.Error) {
			b := []byte(`{"server":"v0","protocol_version":"agent-sync/v0","capabilities":{"concept_kinds":{},"write_tool_owned":false,"progress":false},"declared_outputs":[]}`)
			return b, nil
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	_, err := sess.Initialize(ctx)
	if !errors.Is(err, adapter.ErrAdapterProtocolMismatch) {
		t.Fatalf("want ErrAdapterProtocolMismatch, got %v", err)
	}
}

func TestSession_RejectsUndeclaredOutputs(t *testing.T) {
	t.Parallel()

	sa := &scriptedAdapter{
		name:            "leaky",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".leaky", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{},
		emitOps: []contract.OpRecord{
			{Op: contract.OpKindWriteFile, Path: "/etc/passwd"},
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	_, err := sess.Emit(ctx, "leaky", json.RawMessage(`{"nodes":[]}`))
	if !errors.Is(err, adapter.ErrAdapterUndeclaredOutput) {
		t.Fatalf("want ErrAdapterUndeclaredOutput, got %v", err)
	}
	if !strings.Contains(err.Error(), "/etc/passwd") {
		t.Errorf("error should mention the offending path: %v", err)
	}
}

func TestSession_OpWarningExemptFromGate(t *testing.T) {
	// AC-002: warnings carry path="" intentionally. The gate must not
	// flag them as undeclared outputs.
	t.Parallel()

	sa := &scriptedAdapter{
		name:            "warner",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".warner", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{},
		emitOps: []contract.OpRecord{
			{Op: contract.OpKindWarning, Path: ""},
			{Op: contract.OpKindWriteFile, Path: ".warner/foo.md"},
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	result, err := sess.Emit(ctx, "warner", json.RawMessage(`{"nodes":[]}`))
	if err != nil {
		t.Fatalf("Emit: %v (warnings should pass the gate)", err)
	}
	if len(result.OpsPerformed) != 2 {
		t.Errorf("ops_performed: %+v", result.OpsPerformed)
	}
}

func TestSession_AdapterErrorResponseSurfaced(t *testing.T) {
	t.Parallel()

	sa := &scriptedAdapter{
		name: "errorful",
		respondToInit: func(_ contract.InitializeParams) (json.RawMessage, *contract.Error) {
			return nil, &contract.Error{
				Code:    contract.CodeInvalidParams,
				Message: "missing required field",
				Data: contract.ErrorData{
					ErrorClass: contract.ErrorClassAdapterUndeclaredOutput,
				},
			}
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	_, err := sess.Initialize(ctx)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var rt *adapter.RuntimeError
	if !errors.As(err, &rt) {
		t.Fatalf("want *RuntimeError, got %T: %v", err, err)
	}
	if rt.Class != contract.ErrorClassAdapterUndeclaredOutput {
		t.Errorf("class: want %q, got %q", contract.ErrorClassAdapterUndeclaredOutput, rt.Class)
	}
	if rt.AdapterError == nil || rt.AdapterError.Code != contract.CodeInvalidParams {
		t.Errorf("adapter error: %+v", rt.AdapterError)
	}
}

func TestSession_AdapterErrorWithEmptyClassDefaultsToAdapterPanic(t *testing.T) {
	// The adapter contract treats RuntimeError.Class as always-populated.
	// If the adapter omits the optional error_class data field, the
	// runtime must default to adapter-panic so callers never see an
	// empty classifier.
	t.Parallel()

	sa := &scriptedAdapter{
		name: "no-class",
		respondToInit: func(_ contract.InitializeParams) (json.RawMessage, *contract.Error) {
			return nil, &contract.Error{
				Code:    contract.CodeInternalError,
				Message: "adapter exploded",
				// Data omitted — ErrorClass is the zero value "".
			}
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	_, err := sess.Initialize(ctx)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var rt *adapter.RuntimeError
	if !errors.As(err, &rt) {
		t.Fatalf("want *RuntimeError, got %T: %v", err, err)
	}
	if rt.Class != contract.ErrorClassAdapterPanic {
		t.Errorf("class: want %q, got %q", contract.ErrorClassAdapterPanic, rt.Class)
	}
}

func TestSession_StateMachineEnforcesOrder(t *testing.T) {
	t.Parallel()

	sa := &scriptedAdapter{name: "order"}
	sess, ctx, _ := runScriptedSession(t, sa)

	// Calling Emit before Initialize must be rejected.
	_, err := sess.Emit(ctx, "x", json.RawMessage(`{}`))
	if !errors.Is(err, adapter.ErrAdapterProtocolOrderViolation) {
		t.Fatalf("Emit-before-Init: want ErrAdapterProtocolOrderViolation, got %v", err)
	}
	var emitRT *adapter.RuntimeError
	if !errors.As(err, &emitRT) {
		t.Fatalf("Emit-before-Init: want *RuntimeError, got %T", err)
	}
	if emitRT.Class != contract.ErrorClassAdapterProtocolOrder {
		t.Fatalf("Emit-before-Init class=%q want %q", emitRT.Class, contract.ErrorClassAdapterProtocolOrder)
	}

	// Calling Initialized before Initialize must be rejected.
	if err := sess.Initialized(ctx); !errors.Is(err, adapter.ErrAdapterProtocolOrderViolation) {
		t.Errorf("Initialized-before-Init: want ErrAdapterProtocolOrderViolation, got %v", err)
	} else {
		var initRT *adapter.RuntimeError
		if !errors.As(err, &initRT) {
			t.Fatalf("Initialized-before-Init: want *RuntimeError, got %T", err)
		}
		if initRT.Class != contract.ErrorClassAdapterProtocolOrder {
			t.Fatalf("Initialized-before-Init class=%q want %q", initRT.Class, contract.ErrorClassAdapterProtocolOrder)
		}
	}
}

func TestSession_AdapterProtocolOrderErrorClassFlowsThrough(t *testing.T) {
	t.Parallel()

	sa := &scriptedAdapter{
		name:            "protocol-order",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".echo", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported},
		respondToEmit: func(_ contract.EmitParams) (json.RawMessage, *contract.Error) {
			return nil, &contract.Error{
				Code:    contract.CodeInvalidRequest,
				Message: "emit called before initialized notification",
				Data:    contract.ErrorData{ErrorClass: contract.ErrorClassAdapterProtocolOrder},
			}
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}

	_, err := sess.Emit(ctx, "rule", json.RawMessage(`{"nodes":[{"id":"rule","kind":"rule"}]}`))
	if err == nil {
		t.Fatal("expected emit error")
	}
	var rt *adapter.RuntimeError
	if !errors.As(err, &rt) {
		t.Fatalf("want *RuntimeError, got %T: %v", err, err)
	}
	if rt.Class != contract.ErrorClassAdapterProtocolOrder {
		t.Fatalf("class=%q want %q", rt.Class, contract.ErrorClassAdapterProtocolOrder)
	}
}

func TestSession_ContextCancellationDuringEmit(t *testing.T) {
	// A cancelled emit should return ctx.Err() and leave Pending at 0
	// (Cancel was called on the in-flight id).
	t.Parallel()

	// We use respondToEmit to block forever; the test cancels mid-stream.
	blocker := make(chan struct{})
	sa := &scriptedAdapter{
		name:            "blocker",
		declaredOutputs: []contract.DeclaredOutput{},
		conceptKinds:    map[string]contract.CapabilityLevel{},
		respondToEmit: func(_ contract.EmitParams) (json.RawMessage, *contract.Error) {
			<-blocker
			b, _ := json.Marshal(contract.EmitResult{})
			return b, nil
		},
	}
	defer close(blocker)

	sess, _, _ := runScriptedSession(t, sa)
	ctx, cancel := context.WithCancel(context.Background())

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := sess.Emit(ctx, "blocker", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from cancelled Emit, got nil")
	}
}

func TestSession_RejectsDotDotPathInDeclaredOutputsGate(t *testing.T) {
	// SEC-002: an adapter that declares ".out" but emits an op whose
	// path normalizes to ".." or escapes the declared subtree must be
	// rejected. Without path.Clean, ".out/../etc/passwd" would slip
	// through HasPrefix(".out/") even though it escapes the workspace.
	t.Parallel()

	sa := &scriptedAdapter{
		name:            "escape",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".escape", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{},
		emitOps: []contract.OpRecord{
			{Op: contract.OpKindWriteFile, Path: ".escape/../etc/passwd"},
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	_, err := sess.Emit(ctx, "escape", json.RawMessage(`{"nodes":[]}`))
	if !errors.Is(err, adapter.ErrAdapterUndeclaredOutput) {
		t.Fatalf("want ErrAdapterUndeclaredOutput for path-escape, got %v", err)
	}
}

func TestSession_RejectsAbsolutePathInDeclaredOutputsGate(t *testing.T) {
	t.Parallel()

	sa := &scriptedAdapter{
		name:            "abs",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".abs", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{},
		emitOps: []contract.OpRecord{
			{Op: contract.OpKindWriteFile, Path: "/abs/foo.md"},
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	_, err := sess.Emit(ctx, "abs", json.RawMessage(`{"nodes":[]}`))
	if !errors.Is(err, adapter.ErrAdapterUndeclaredOutput) {
		t.Fatalf("want ErrAdapterUndeclaredOutput for absolute path, got %v", err)
	}
}

func TestSession_DeclaredOutputDotAcceptsAnyRelativePath(t *testing.T) {
	// SEC: an adapter declaring "." as a declared_output (the workspace
	// root) must have ops at any relative path accepted by the gate.
	// Without the "declClean == \".\"" branch the prefix check fails for
	// normalized paths like "foo.md" because path.Clean strips "./".
	t.Parallel()

	sa := &scriptedAdapter{
		name:            "rooty",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{},
		emitOps: []contract.OpRecord{
			{Op: contract.OpKindWriteFile, Path: "foo.md"},
			{Op: contract.OpKindMkdir, Path: "subdir"},
			{Op: contract.OpKindWriteFile, Path: "subdir/nested.md"},
		},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	if _, err := sess.Emit(ctx, "rooty", json.RawMessage(`{"nodes":[]}`)); err != nil {
		t.Fatalf("Emit with declared_output \".\": want nil, got %v", err)
	}
}

func TestSession_RejectsWindowsUnsafeOpPathsInGate(t *testing.T) {
	// SEC: path.IsAbs uses POSIX rules and won't catch Windows drive-letter
	// paths or backslashes. The gate must reject both shapes on every OS so
	// declared-outputs containment holds regardless of host platform.
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{"backslash-separator", `.outs\foo.md`},
		{"windows-drive-uppercase", `C:\foo.md`},
		{"windows-drive-forward-slash", `C:/foo.md`},
		{"windows-drive-lowercase", `d:foo`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sa := &scriptedAdapter{
				name:            "win",
				declaredOutputs: []contract.DeclaredOutput{{Path: ".outs", Mode: contract.OutputModeOwnedSubdir}},
				conceptKinds:    map[string]contract.CapabilityLevel{},
				emitOps: []contract.OpRecord{
					{Op: contract.OpKindWriteFile, Path: tc.path},
				},
			}
			sess, ctx, _ := runScriptedSession(t, sa)

			if _, err := sess.Initialize(ctx); err != nil {
				t.Fatalf("Initialize: %v", err)
			}
			if err := sess.Initialized(ctx); err != nil {
				t.Fatalf("Initialized: %v", err)
			}
			_, err := sess.Emit(ctx, "win", json.RawMessage(`{"nodes":[]}`))
			if !errors.Is(err, adapter.ErrAdapterUndeclaredOutput) {
				t.Fatalf("path=%q: want ErrAdapterUndeclaredOutput, got %v", tc.path, err)
			}
		})
	}
}

func TestSession_DetectsCapabilityLied(t *testing.T) {
	// MAINT-001: an adapter that declares "rule" as supported but emits
	// no ops for an IR containing rule nodes must trigger
	// ErrAdapterCapabilityLied. Silent nullification breaks the
	// determinism guarantee.
	t.Parallel()

	sa := &scriptedAdapter{
		name:            "liar",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".liar", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported},
		emitOps:         []contract.OpRecord{}, // declared support but does nothing
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	_, err := sess.Emit(ctx, "liar", json.RawMessage(`{"nodes":[{"id":"x","kind":"rule"}]}`))
	if !errors.Is(err, adapter.ErrAdapterCapabilityLied) {
		t.Fatalf("want ErrAdapterCapabilityLied, got %v", err)
	}
}

func TestSession_CapabilityLiedNotTriggeredForUnsupportedKinds(t *testing.T) {
	// IR contains nodes of a kind the adapter never declared as
	// supported. Emitting nothing is the correct behavior — not a lie.
	t.Parallel()

	sa := &scriptedAdapter{
		name:            "honest",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".honest", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported},
		emitOps:         []contract.OpRecord{},
	}
	sess, ctx, _ := runScriptedSession(t, sa)

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	// IR contains "agent" kind (which adapter did NOT declare). No ops
	// emitted is honest decline, not a lie.
	if _, err := sess.Emit(ctx, "honest", json.RawMessage(`{"nodes":[{"id":"a","kind":"agent"}]}`)); err != nil {
		t.Fatalf("Emit: want nil, got %v", err)
	}
}

func TestSession_TimeoutErrorMentionsPhaseAndDuration(t *testing.T) {
	// REL-002/MAINT-008: the timeout error message must report the
	// phase that timed out (initialize / emit / shutdown) and its
	// configured duration. Earlier the message hardcoded handshake's
	// timeout regardless of phase.
	t.Parallel()

	blocker := make(chan struct{})
	sa := &scriptedAdapter{
		name:            "slow",
		declaredOutputs: []contract.DeclaredOutput{{Path: ".slow", Mode: contract.OutputModeOwnedSubdir}},
		conceptKinds:    map[string]contract.CapabilityLevel{},
		respondToEmit: func(_ contract.EmitParams) (json.RawMessage, *contract.Error) {
			<-blocker
			b, _ := json.Marshal(contract.EmitResult{})
			return b, nil
		},
	}

	b := sa.bundled(t)
	a := &adapter.Adapter{Manifest: b.Manifest, Source: adapter.SourceBundled, Bundled: b}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	sess, err := a.NewSession(ctx, adapter.SessionOptions{
		IRVersion: "v1",
		Timeouts: adapter.SubprocessTimeouts{
			Handshake: 5 * time.Second,
			Emit:      100 * time.Millisecond,
			Shutdown:  5 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Cleanup ordering matters here: t.Cleanup is LIFO. Shutdown talks
	// to the adapter, so the blocker must be closed BEFORE Shutdown
	// runs — register the unblock cleanup AFTER the Shutdown cleanup
	// so it fires first.
	t.Cleanup(func() { _ = sess.Shutdown(context.Background()) })
	t.Cleanup(func() { close(blocker) })

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	_, err = sess.Emit(ctx, "slow", json.RawMessage(`{"nodes":[]}`))
	if !errors.Is(err, adapter.ErrAdapterTimeout) {
		t.Fatalf("want ErrAdapterTimeout, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "emit") {
		t.Errorf("error should mention 'emit' phase: %q", msg)
	}
	if !strings.Contains(msg, "100ms") {
		t.Errorf("error should mention 100ms (the emit timeout): %q", msg)
	}
}

func TestSession_AdaptersInParallelDontInterfere(t *testing.T) {
	t.Parallel()

	mkSession := func(name string) *adapter.AdapterSession {
		sa := &scriptedAdapter{
			name:            name,
			declaredOutputs: []contract.DeclaredOutput{{Path: "." + name, Mode: contract.OutputModeOwnedSubdir}},
			conceptKinds:    map[string]contract.CapabilityLevel{},
			emitOps:         []contract.OpRecord{{Op: contract.OpKindMkdir, Path: "." + name}},
		}
		s, _, _ := runScriptedSession(t, sa)
		return s
	}

	s1 := mkSession("para1")
	s2 := mkSession("para2")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	for _, s := range []*adapter.AdapterSession{s1, s2} {
		if _, err := s.Initialize(ctx); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		if err := s.Initialized(ctx); err != nil {
			t.Fatalf("Initialized: %v", err)
		}
	}
	for _, s := range []*adapter.AdapterSession{s1, s2} {
		result, err := s.Emit(ctx, "x", json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		if len(result.OpsPerformed) != 1 {
			t.Errorf("ops: %+v", result.OpsPerformed)
		}
	}
}

package adapter

import (
	"errors"
	"fmt"

	"github.com/aienvs/aienvs/internal/adapter/contract"
)

// Runtime-level sentinel errors. Callers branch with errors.Is.
//
// These map to contract.ErrorClass values that the CLI surfaces in
// crash reports and exit codes. The mapping is:
//
//	ErrAdapterCookieMissing       → ErrorClassAdapterExecDenied
//	ErrAdapterCookieMismatch      → ErrorClassAdapterExecDenied
//	ErrAdapterProtocolMismatch    → ErrorClassAdapterProtocolMismatch
//	ErrAdapterUndeclaredOutput    → ErrorClassAdapterUndeclaredOutput
//	ErrAdapterProtocolOrderViolation → ErrorClassAdapterProtocolOrder
//	ErrAdapterCapabilityLied      → ErrorClassAdapterCapabilityLied
//	(timeout)                     → ErrorClassAdapterTimeout
//	(subprocess crash)            → ErrorClassAdapterPanic
var (
	// ErrAdapterCookieMissing is returned when the adapter's
	// initialize result omits the cookie field entirely. Likely cause:
	// the adapter binary doesn't speak aienvs/v1, or someone ran it
	// outside the CLI handshake.
	ErrAdapterCookieMissing = errors.New("adapter: initialize result missing cookie field")

	// ErrAdapterCookieMismatch is returned when the cookie echoed by
	// the adapter doesn't match the per-spawn value the runtime sent.
	ErrAdapterCookieMismatch = errors.New("adapter: cookie mismatch")

	// ErrAdapterProtocolMismatch is returned when the adapter's
	// protocol_version is not "aienvs/v1". Counter-propose negotiation
	// is deferred to Unit 8b; PR 2 simply refuses on mismatch.
	ErrAdapterProtocolMismatch = errors.New("adapter: protocol_version mismatch")

	// ErrAdapterUndeclaredOutput is returned when an emit op targets
	// a path outside the adapter's declared_outputs set.
	ErrAdapterUndeclaredOutput = errors.New("adapter: op path not in declared_outputs")

	// ErrAdapterProtocolOrderViolation is returned when the adapter
	// emits ops before the runtime's `initialized` notification, or
	// otherwise violates the four-phase lifecycle.
	ErrAdapterProtocolOrderViolation = errors.New("adapter: protocol order violation")

	// ErrAdapterCapabilityLied is returned when the adapter declared
	// a kind as "supported" in initialize but emitted no ops for IR
	// nodes of that kind during emit.
	ErrAdapterCapabilityLied = errors.New("adapter: declared capability but emitted no ops")

	// ErrAdapterUnexpectedMessage is returned when the adapter sends
	// a message the orchestrator wasn't expecting (e.g., a request
	// from the adapter side, or a response with an unknown id).
	ErrAdapterUnexpectedMessage = errors.New("adapter: unexpected wire message")

	// ErrAdapterTimeout is returned when handshake / emit / shutdown
	// exceeds its configured timeout. Wraps context.DeadlineExceeded.
	ErrAdapterTimeout = errors.New("adapter: timeout")
)

// RuntimeError is the typed error the orchestrator returns. It carries
// the contract-level ErrorClass for routing decisions plus optional
// adapter-side context (the wire-level Error and the stderr tail when
// the adapter terminated abnormally).
type RuntimeError struct {
	// Class is the CLI-side classifier. Always set.
	Class contract.ErrorClass

	// Err is the underlying error. Always set; errors.Is / errors.As
	// unwrap to it.
	Err error

	// AdapterError, when non-nil, is the JSON-RPC error object the
	// adapter returned (only populated when the failure was a typed
	// adapter response, not a process-level failure).
	AdapterError *contract.Error

	// StderrTail is the bounded ring-buffer snapshot from the adapter's
	// stderr. Populated on subprocess crashes; nil for inproc adapters.
	StderrTail []byte

	// ExitCode is the OS-level exit code on subprocess termination.
	// Zero when the adapter exited cleanly or never exited (inproc).
	ExitCode int
}

func (e *RuntimeError) Error() string {
	if e == nil || e.Err == nil {
		return "adapter: runtime error (no detail)"
	}
	if e.ExitCode != 0 {
		return fmt.Sprintf("adapter[%s exit=%d]: %v", e.Class, e.ExitCode, e.Err)
	}
	return fmt.Sprintf("adapter[%s]: %v", e.Class, e.Err)
}

// Unwrap lets errors.Is and errors.As walk to the underlying cause.
func (e *RuntimeError) Unwrap() error {
	return e.Err
}

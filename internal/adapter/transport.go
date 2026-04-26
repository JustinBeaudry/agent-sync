package adapter

import (
	"context"
)

// Transport is the byte-level layer between the runtime and an adapter.
// Two implementations: subprocess (for adapters that ship as binaries)
// and inproc (for bundled adapters that run in the CLI's own process).
// The runtime cannot tell them apart by behavior — same wire format,
// same lifecycle, same error surface.
//
// Send and Recv each carry a context.Context. The context is honored
// at the I/O boundary: if it cancels mid-write or mid-read, the
// transport tears down. Close returns once the underlying process /
// goroutine has exited.
type Transport interface {
	// Send writes one LSP-framed JSON-RPC envelope to the adapter.
	// Returns when the bytes are accepted by the OS pipe (or, for
	// inproc, by the adapter's reader goroutine). Honors ctx for
	// cancellation.
	Send(ctx context.Context, payload []byte) error

	// Recv reads one LSP-framed envelope from the adapter. Honors ctx
	// for cancellation. Returns io.EOF when the adapter exits cleanly.
	Recv(ctx context.Context) ([]byte, error)

	// Close terminates the adapter. For subprocess on Unix: SIGTERM,
	// then wait up to the configured shutdown timeout, then SIGKILL.
	// For subprocess on Windows: wait up to the configured shutdown
	// timeout for the process to exit on its own, then Kill(). The
	// graceful-signal step is intentionally skipped on Windows because
	// Go's os/exec stdlib cannot reliably deliver os.Interrupt to a
	// child process via Process.Signal — Console Ctrl events require
	// the child to share the parent's console and explicit
	// CREATE_NEW_PROCESS_GROUP / GenerateConsoleCtrlEvent handling that
	// the stdlib does not expose. For inproc: closes the pipes and
	// waits for the bundled adapter's Run to return.
	//
	// Returns the classified error describing how the adapter exited
	// (nil for clean exit). After Close, no further Send / Recv may
	// be called.
	Close(ctx context.Context) error

	// StderrTail returns a snapshot of the most recent stderr bytes
	// from the adapter, capped at the transport's configured ring
	// buffer size. For inproc, returns nil. Used by the runtime to
	// attach stderr context to abnormal-termination error reports.
	StderrTail() []byte

	// MarkProtocolShutdownAcked signals that the runtime received a
	// successful response to the protocol-level `shutdown` request.
	// The transport may use this to suppress non-zero exit-code
	// classification during the subsequent Close (a clean shutdown
	// round-trip is the authoritative signal that the adapter exited
	// on purpose, regardless of what exit code main returned).
	// For inproc, this is a no-op — there is no exit code to classify.
	MarkProtocolShutdownAcked()
}

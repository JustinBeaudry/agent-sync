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

	// Close terminates the adapter. For subprocess: graceful stop
	// (SIGTERM on Unix, os.Interrupt on Windows) bounded by the
	// configured shutdown timeout, then SIGKILL / Kill(). For inproc:
	// closes the pipes and waits for the bundled adapter's Run to
	// return.
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
}

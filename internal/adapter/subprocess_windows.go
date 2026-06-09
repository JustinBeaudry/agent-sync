//go:build windows

package adapter

// gracefulStop is intentionally a no-op on Windows.
//
// Windows lacks Unix signal semantics, and Go's standard library
// cannot reliably deliver os.Interrupt to an arbitrary child process
// via (*os.Process).Signal. Per the os package documentation, sending
// any signal other than os.Kill on Windows returns an error along the
// lines of "not supported by windows" for processes that are not
// attached to the caller's console group — which is the common case
// for adapters spawned by the agent-sync CLI (no shared console, often
// started with a hidden window or detached). Even when delivery does
// succeed (CTRL_BREAK_EVENT to a console-group process), many child
// processes ignore it.
//
// Rather than pretend to perform a graceful step that almost always
// silently fails, we skip the graceful path entirely on Windows and
// rely on Subprocess.Close's shutdown-timeout-then-Kill fallback as
// the sole termination guarantee. This keeps observable behavior
// predictable across Windows configurations and matches what the
// caller already has to handle.
func (s *Subprocess) gracefulStop() error {
	if s.cmd.Process == nil {
		return nil
	}
	return nil
}

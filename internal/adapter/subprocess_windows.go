//go:build windows

package adapter

// gracefulStop attempts a Windows-friendly graceful stop. Windows
// lacks Unix signal semantics: os.Interrupt is supported by Go's
// runtime as a CTRL_BREAK_EVENT for processes attached to the same
// console group, but in practice many child processes (especially
// detached ones) ignore it.
//
// We send the interrupt as a best effort; the Close caller's timeout-
// then-Kill loop is the actual termination guarantee. Any error from
// Signal is non-fatal — the worst case is that we wait the full
// shutdown timeout and then Kill, which is the expected Windows
// behavior.
func (s *Subprocess) gracefulStop() error {
	if s.cmd.Process == nil {
		return nil
	}
	// On Windows, os.Process.Signal accepts os.Interrupt for
	// well-behaved console processes. For detached or service-mode
	// processes, Kill() is the reliable termination path; the timeout
	// in Close drives that fallback.
	//
	// We deliberately do NOT call cmd.Process.Signal(os.Interrupt)
	// here because Go's Windows implementation returns an error
	// ("not supported by windows") for many process attach patterns
	// — relying on the timeout-then-Kill path is more predictable
	// across Windows configurations.
	return nil
}

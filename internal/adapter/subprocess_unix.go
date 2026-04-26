//go:build !windows

package adapter

import "syscall"

// gracefulStop sends SIGTERM to the child process. The Close caller
// then waits up to the shutdown timeout before escalating to Kill().
//
// SIGTERM is the conventional Unix "please exit" signal — handlers
// can drain in-flight work before the process exits. SIGKILL is the
// fallback when the adapter ignores SIGTERM or hangs.
func (s *Subprocess) gracefulStop() error {
	if s.cmd.Process == nil {
		return nil
	}
	return s.cmd.Process.Signal(syscall.SIGTERM)
}

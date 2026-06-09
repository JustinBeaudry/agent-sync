package conformance

import (
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"
)

// TestMatchError_SubprocessExitAcceptsBrokenPipe pins the timing-
// independent contract: a subprocess that dies before/while the harness
// writes the init frame surfaces as a broken/closed pipe on the write
// side rather than EOF on the read side. Both must satisfy the
// "SubprocessExitError" expectation, so the conformance assertion does
// not flake under CI load.
func TestMatchError_SubprocessExitAcceptsBrokenPipe(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"eof", io.EOF, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"epipe", syscall.EPIPE, true},
		{"closed pipe", io.ErrClosedPipe, true},
		{"wrapped broken pipe", fmt.Errorf("write frame to subprocess: %w", syscall.EPIPE), true},
		{"windows pipe-closed string", errors.New("write |1: The pipe is being closed."), true},
		{"unrelated error", errors.New("some other failure"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchError("SubprocessExitError", tc.err); got != tc.want {
				t.Errorf("MatchError(SubprocessExitError, %v) = %v want %v", tc.err, got, tc.want)
			}
		})
	}
}

package adapterkit

import (
	"errors"
	"testing"
)

func TestExitError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("boom")
	e := &ExitError{Code: 7, Err: inner}

	if e.Error() != "boom" {
		t.Fatalf("Error() = %q, want wrapped message", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Fatal("errors.Is should unwrap to inner")
	}

	// No wrapped error: Error() reports the code.
	bare := &ExitError{Code: 3}
	if bare.Error() == "" {
		t.Fatal("bare ExitError.Error() should be non-empty")
	}
	if bare.Unwrap() != nil {
		t.Fatal("bare ExitError.Unwrap() should be nil")
	}

	// nil receiver is safe.
	var nilErr *ExitError
	_ = nilErr.Error()
	if nilErr.Unwrap() != nil {
		t.Fatal("nil ExitError.Unwrap() should be nil")
	}
}

func TestServerState_String(t *testing.T) {
	cases := map[serverState]string{
		serverStateNew:         "new",
		serverStateInitialized: "initialized",
		serverStateReady:       "ready",
		serverStateClosed:      "closed",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("serverState(%d).String() = %q, want %q", s, got, want)
		}
	}
}

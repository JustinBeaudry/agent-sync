package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter/contract"
)

func TestClassifySendError_Timeout(t *testing.T) {
	rt := classifySendError(context.DeadlineExceeded)
	if rt == nil {
		t.Fatal("expected a RuntimeError")
	}
	if rt.Class != contract.ErrorClassAdapterTimeout {
		t.Fatalf("class = %q, want adapter-timeout", rt.Class)
	}
	if !errors.Is(rt.Err, ErrAdapterTimeout) {
		t.Fatalf("err should wrap ErrAdapterTimeout, got %v", rt.Err)
	}
}

func TestClassifySendError_GenericIsPanic(t *testing.T) {
	rt := classifySendError(errors.New("pipe closed"))
	if rt == nil {
		t.Fatal("expected a RuntimeError")
	}
	if rt.Class != contract.ErrorClassAdapterPanic {
		t.Fatalf("class = %q, want adapter-panic", rt.Class)
	}
}

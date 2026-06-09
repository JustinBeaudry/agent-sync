package adapter_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/adapter/contract"
)

// echoBundled is a tiny in-process adapter that echoes every frame
// it receives back as a Notification with method="echo". Used to
// exercise the inproc transport without depending on a subprocess.
func echoBundled(t *testing.T) *adapter.BundledAdapter {
	t.Helper()
	return &adapter.BundledAdapter{
		Manifest: adapter.AdapterManifest{
			Name:            "echo",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{"echo-bundled"},
		},
		Run: func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			fr := contract.NewFrameReader(stdin)
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				payload, err := fr.Read(contract.DefaultMaxFrameBytes)
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				// Echo back as a notification with the original payload as params.
				out := []byte(`{"jsonrpc":"2.0","method":"echo","params":` + string(payload) + `}`)
				if err := contract.WriteFrame(stdout, out); err != nil {
					return err
				}
			}
		},
	}
}

func TestInproc_RoundTrip(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	tr, err := adapter.NewInprocTransport(ctx, echoBundled(t))
	if err != nil {
		t.Fatalf("NewInprocTransport: %v", err)
	}
	t.Cleanup(func() {
		_ = tr.Close(context.Background())
	})

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"hello"}`)
	if err := tr.Send(ctx, payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !strings.Contains(string(got), `"method":"echo"`) {
		t.Errorf("expected echo notification, got %s", got)
	}
}

func TestInproc_CloseTerminatesAdapter(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	tr, err := adapter.NewInprocTransport(ctx, echoBundled(t))
	if err != nil {
		t.Fatalf("NewInprocTransport: %v", err)
	}

	// Adapter should exit cleanly when stdin closes (Close closes the pipes).
	if err := tr.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestInproc_StderrTailIsNil(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	tr, err := adapter.NewInprocTransport(ctx, echoBundled(t))
	if err != nil {
		t.Fatalf("NewInprocTransport: %v", err)
	}
	t.Cleanup(func() {
		_ = tr.Close(context.Background())
	})

	if got := tr.StderrTail(); got != nil {
		t.Errorf("inproc transport has no stderr; want nil, got %d bytes", len(got))
	}
}

func TestInproc_BundledRunPanicSurfaced(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	panicker := &adapter.BundledAdapter{
		Manifest: adapter.AdapterManifest{
			Name:            "panicker",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{"panicker"},
		},
		Run: func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			panic("boom")
		},
	}

	tr, err := adapter.NewInprocTransport(ctx, panicker)
	if err != nil {
		t.Fatalf("NewInprocTransport: %v", err)
	}

	// First Recv should return a panic-surfaced error since the
	// adapter died before writing anything.
	_, recvErr := tr.Recv(ctx)
	if recvErr == nil {
		t.Fatal("expected error from Recv when adapter panicked")
	}

	closeErr := tr.Close(context.Background())
	if closeErr == nil {
		t.Fatal("expected error from Close when adapter panicked")
	}
	if !strings.Contains(closeErr.Error(), "panic") {
		t.Errorf("close error should mention panic, got %v", closeErr)
	}
}

func TestInproc_TwoAdaptersInParallel(t *testing.T) {
	// Two bundled adapters running side-by-side in their own goroutines
	// must not interfere — independent pipes, independent state.
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	t1, err := adapter.NewInprocTransport(ctx, echoBundled(t))
	if err != nil {
		t.Fatalf("transport 1: %v", err)
	}
	t.Cleanup(func() { _ = t1.Close(context.Background()) })

	t2, err := adapter.NewInprocTransport(ctx, echoBundled(t))
	if err != nil {
		t.Fatalf("transport 2: %v", err)
	}
	t.Cleanup(func() { _ = t2.Close(context.Background()) })

	for _, tr := range []adapter.Transport{t1, t2} {
		if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"x"}`)); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if _, err := tr.Recv(ctx); err != nil {
			t.Fatalf("Recv: %v", err)
		}
	}
}

// TestInproc_CloseDoesNotDeadlockWithUndrainedFrame regresses REL-001:
// without the closedCh signal, the reader goroutine blocks on
// `frames <- ...` when the channel is full and no Recv is consuming,
// so Close (which joins readerDone) hangs forever. With the fix,
// closing the channel signal breaks the blocking send.
func TestInproc_CloseDoesNotDeadlockWithUndrainedFrame(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	tr, err := adapter.NewInprocTransport(ctx, echoBundled(t))
	if err != nil {
		t.Fatalf("NewInprocTransport: %v", err)
	}

	// Send three frames. The echo adapter responds with a notification
	// per frame, which queues into the bounded frames channel. We
	// never call Recv — by the time Close runs, the channel is full
	// and the reader goroutine is blocked on its next send.
	for i := 0; i < 3; i++ {
		if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"x"}`)); err != nil {
			t.Fatalf("Send #%d: %v", i, err)
		}
	}
	// Tiny sleep to let the echo adapter actually produce frames.
	time.Sleep(50 * time.Millisecond)

	// Close must return within the test timeout (without the fix, this
	// deadlocks indefinitely and the t.Cleanup ctx cancel won't help).
	// The Close result itself is allowed to surface a "pipe closed"
	// error from the bundled adapter — what we're regressing here is
	// the deadlock, not the result.
	closed := make(chan error, 1)
	go func() { closed <- tr.Close(context.Background()) }()
	select {
	case <-closed:
		// Returned within the timeout — the deadlock is fixed.
	case <-time.After(2 * time.Second):
		t.Fatal("Close deadlocked with undrained frames in channel")
	}
}

// TestInproc_CloseHonorsContextDeadline regresses copilot review feedback:
// if a bundled adapter ignores stdin EOF and refuses to exit (e.g. an
// adapter that's broken or stuck), Close must not block past the
// caller's ctx deadline. Without honoring ctx, Close hangs forever
// waiting for runDone/readerDone — leaving callers vulnerable to
// hangs from misbehaving bundled adapters.
func TestInproc_CloseHonorsContextDeadline(t *testing.T) {
	t.Parallel()

	// Adapter that ignores stdin EOF entirely. It only exits when its
	// own ctx fires — which Close cannot trigger without leaking the
	// adapter's ctx, so this simulates a bundled adapter that doesn't
	// honor pipe close. The outer ctx (from NewInprocTransport) keeps
	// the goroutine bounded so the test itself doesn't leak.
	stuck := &adapter.BundledAdapter{
		Manifest: adapter.AdapterManifest{
			Name:            "stuck",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{"stuck"},
		},
		Run: func(ctx context.Context, _ io.Reader, _ io.Writer) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}

	// Outer ctx with a longer lifetime — bounds the adapter goroutine
	// so it eventually exits and we don't leak. Doesn't drive Close.
	outerCtx, outerCancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(outerCancel)

	tr, err := adapter.NewInprocTransport(outerCtx, stuck)
	if err != nil {
		t.Fatalf("NewInprocTransport: %v", err)
	}

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	t.Cleanup(closeCancel)

	start := time.Now()
	err = tr.Close(closeCtx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected Close to return ctx error when adapter ignores pipe close")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected Close error to wrap context.DeadlineExceeded, got %v", err)
	}
	// Should return shortly after the deadline; allow generous slack
	// for scheduler noise but ensure we're nowhere near a true hang.
	if elapsed > 2*time.Second {
		t.Errorf("Close took %v; expected to honor ~200ms ctx deadline", elapsed)
	}
}

// TestInproc_ConcurrentCloseSafe regresses REL-005: a second concurrent
// Close call must not race the first one; both must observe the same
// settled closeErr value.
func TestInproc_ConcurrentCloseSafe(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	tr, err := adapter.NewInprocTransport(ctx, echoBundled(t))
	if err != nil {
		t.Fatalf("NewInprocTransport: %v", err)
	}

	const N = 8
	results := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() { results <- tr.Close(context.Background()) }()
	}
	for i := 0; i < N; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("close #%d: %v", i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent Close hung")
		}
	}
}

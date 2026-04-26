package adapter_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aienvs/aienvs/internal/adapter"
	"github.com/aienvs/aienvs/internal/adapter/contract"
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

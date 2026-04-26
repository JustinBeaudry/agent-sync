package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/aienvs/aienvs/internal/adapter/contract"
)

// InprocTransport runs a BundledAdapter in a goroutine, plumbing its
// stdin / stdout via io.Pipe pairs. The adapter speaks the same wire
// protocol as a subprocess — same FrameReader, same JSON-RPC envelope.
// The runtime cannot tell them apart by behavior.
type InprocTransport struct {
	bundled *BundledAdapter

	// runtime → adapter
	rtToAd *io.PipeWriter // runtime writes here
	adIn   *io.PipeReader // adapter reads here

	// adapter → runtime
	adToRt *io.PipeWriter // adapter writes here
	rtIn   *io.PipeReader // runtime reads here

	frameRead *contract.FrameReader
	frameMax  int64

	// runDone is closed when the bundled adapter's Run returns.
	// runErr captures the return value (or recovered panic) for Close.
	runDone chan struct{}
	runErr  error
	runOnce sync.Once

	// closeOnce / closeErr — Close is idempotent; subsequent calls
	// return the first call's classified error.
	closeOnce sync.Once
	closeErr  error

	// shutdownRequested is set by Close before tearing down the pipes.
	shutdownRequested bool
}

// NewInprocTransport launches the bundled adapter in a goroutine and
// returns the connected transport. The bundled adapter's Run is
// invoked with stdin/stdout pipes; it speaks the protocol over them.
//
// ctx bounds the lifetime of the bundled adapter goroutine. Returning
// from Run (or panicking) closes the runDone channel; Close joins it.
func NewInprocTransport(ctx context.Context, bundled *BundledAdapter) (*InprocTransport, error) {
	if bundled == nil {
		return nil, errors.New("adapter: NewInprocTransport: bundled is nil")
	}
	if bundled.Run == nil {
		return nil, errors.New("adapter: NewInprocTransport: bundled.Run is nil")
	}

	adIn, rtToAd := io.Pipe()
	rtIn, adToRt := io.Pipe()

	tr := &InprocTransport{
		bundled:   bundled,
		rtToAd:    rtToAd,
		adIn:      adIn,
		adToRt:    adToRt,
		rtIn:      rtIn,
		frameRead: contract.NewFrameReader(rtIn),
		frameMax:  contract.DefaultMaxFrameBytes,
		runDone:   make(chan struct{}),
	}

	go tr.run(ctx)

	return tr, nil
}

// run invokes the bundled adapter's Run function and captures its
// return value. Recovers panics so they surface as Close errors rather
// than crashing the runtime.
func (t *InprocTransport) run(ctx context.Context) {
	defer close(t.runDone)
	defer func() {
		if r := recover(); r != nil {
			t.runErr = fmt.Errorf("adapter: bundled %q panic: %v", t.bundled.Manifest.Name, r)
		}
		// Close the writer end of the runtime-side pipe so any
		// pending Recv sees EOF instead of hanging.
		_ = t.adToRt.Close()
	}()
	t.runErr = t.bundled.Run(ctx, t.adIn, t.adToRt)
}

// Send implements Transport.
func (t *InprocTransport) Send(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return contract.WriteFrame(t.rtToAd, payload)
}

// Recv implements Transport. Honors ctx by surfacing the context
// error; the underlying io.PipeReader.Read does not natively support
// context, so a goroutine is used to allow cancellation.
func (t *InprocTransport) Recv(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		payload []byte
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		payload, err := t.frameRead.Read(t.frameMax)
		ch <- result{payload: payload, err: err}
	}()
	select {
	case r := <-ch:
		// If the adapter died (panic or Run returned an error) while
		// we were reading, surface the typed adapter error rather than
		// the cryptic "io: read/write on closed pipe".
		if r.err != nil {
			t.runOnce.Do(func() {})
			select {
			case <-t.runDone:
				if t.runErr != nil {
					return nil, t.runErr
				}
			default:
			}
		}
		return r.payload, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close implements Transport. Closes the pipes (signaling EOF to the
// bundled adapter), joins the Run goroutine, and returns the bundled
// adapter's return value or recovered panic.
func (t *InprocTransport) Close(_ context.Context) error {
	t.closeOnce.Do(func() {
		t.shutdownRequested = true
		// Closing the runtime → adapter writer signals EOF on the
		// adapter's stdin. A well-behaved bundled adapter exits its
		// read loop and returns from Run.
		_ = t.rtToAd.Close()
		// Closing the adapter → runtime reader unblocks any pending
		// Recv with io.ErrClosedPipe.
		_ = t.rtIn.Close()
		// Wait for Run to finish.
		<-t.runDone
		t.closeErr = t.runErr
	})
	return t.closeErr
}

// StderrTail implements Transport. Inproc adapters have no stderr —
// they share the runtime's process and log via the runtime's logger.
// Returns nil so the runtime knows there's nothing to attach to crash
// reports.
func (t *InprocTransport) StderrTail() []byte {
	return nil
}

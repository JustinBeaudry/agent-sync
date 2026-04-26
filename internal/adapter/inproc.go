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
//
// Concurrency: a single dedicated reader goroutine pumps inbound
// frames into a bounded channel. Recv just selects on that channel +
// ctx. There is never more than one goroutine touching the
// FrameReader's bufio.Reader, so ctx cancellation in one Recv call
// does not race with a subsequent Recv (which is the common case
// during teardown — Emit cancels, then Shutdown's Recv runs).
type InprocTransport struct {
	bundled *BundledAdapter

	// runtime → adapter
	rtToAd *io.PipeWriter
	adIn   *io.PipeReader

	// adapter → runtime
	adToRt *io.PipeWriter
	rtIn   *io.PipeReader

	frameRead *contract.FrameReader
	frameMax  int64

	// frames is the channel the dedicated reader goroutine pushes
	// inbound frames into. Capacity 1 — the protocol is request-
	// response, so buffering more than one frame doesn't help and
	// could mask a runtime that's not draining.
	frames chan frameOrError

	// runDone is closed when the bundled adapter's Run returns.
	// readerDone is closed when the reader goroutine exits.
	runDone    chan struct{}
	readerDone chan struct{}
	runErr     error

	// closeOnce / closeErr — Close is idempotent.
	closeOnce sync.Once
	closeErr  error
}

type frameOrError struct {
	payload []byte
	err     error
}

// NewInprocTransport launches the bundled adapter in a goroutine and
// returns the connected transport.
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
		bundled:    bundled,
		rtToAd:     rtToAd,
		adIn:       adIn,
		adToRt:     adToRt,
		rtIn:       rtIn,
		frameRead:  contract.NewFrameReader(rtIn),
		frameMax:   contract.DefaultMaxFrameBytes,
		frames:     make(chan frameOrError, 1),
		runDone:    make(chan struct{}),
		readerDone: make(chan struct{}),
	}

	go tr.runBundled(ctx)
	go tr.readLoop()

	return tr, nil
}

// runBundled invokes the bundled adapter's Run function and captures
// its return value. Recovers panics so they surface as Close errors.
func (t *InprocTransport) runBundled(ctx context.Context) {
	defer close(t.runDone)
	defer func() {
		if r := recover(); r != nil {
			t.runErr = fmt.Errorf("adapter: bundled %q panic: %v", t.bundled.Manifest.Name, r)
		}
		// Close the writer end of the runtime-side pipe so the
		// reader loop sees EOF instead of hanging.
		_ = t.adToRt.Close()
	}()
	t.runErr = t.bundled.Run(ctx, t.adIn, t.adToRt)
}

// readLoop is the dedicated reader goroutine. Pumps frames from the
// FrameReader into the frames channel until the pipe closes or an
// unrecoverable read error occurs. Closes the channel on exit so
// Recv can detect end-of-stream.
func (t *InprocTransport) readLoop() {
	defer close(t.readerDone)
	defer close(t.frames)
	for {
		payload, err := t.frameRead.Read(t.frameMax)
		t.frames <- frameOrError{payload: payload, err: err}
		if err != nil {
			return
		}
	}
}

// Send implements Transport.
func (t *InprocTransport) Send(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return contract.WriteFrame(t.rtToAd, payload)
}

// Recv implements Transport. Pulls the next frame from the dedicated
// reader goroutine's channel, honoring ctx for cancellation.
func (t *InprocTransport) Recv(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case fr, ok := <-t.frames:
		if !ok {
			// Channel closed — reader goroutine exited. If the
			// bundled adapter died with a panic / Run error,
			// surface that error rather than the cryptic closed-
			// pipe message.
			select {
			case <-t.runDone:
				if t.runErr != nil {
					return nil, t.runErr
				}
			default:
			}
			return nil, io.EOF
		}
		if fr.err != nil {
			// Reader returned an error; check whether the bundled
			// adapter died for a more useful error message.
			select {
			case <-t.runDone:
				if t.runErr != nil {
					return nil, t.runErr
				}
			default:
			}
			return nil, fr.err
		}
		return fr.payload, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close implements Transport. Closes the pipes (signaling EOF to the
// bundled adapter), joins both background goroutines, and returns
// the bundled adapter's return value or recovered panic.
func (t *InprocTransport) Close(_ context.Context) error {
	t.closeOnce.Do(func() {
		// Closing the runtime → adapter writer signals EOF on the
		// adapter's stdin. A well-behaved bundled adapter exits its
		// read loop and returns from Run.
		_ = t.rtToAd.Close()
		// Closing the runtime-side reader unblocks the reader loop.
		_ = t.rtIn.Close()
		// Wait for both goroutines to finish.
		<-t.runDone
		<-t.readerDone
		t.closeErr = t.runErr
	})
	return t.closeErr
}

// StderrTail implements Transport. Inproc adapters have no stderr.
func (t *InprocTransport) StderrTail() []byte {
	return nil
}

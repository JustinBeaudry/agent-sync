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
	//
	// closedCh is closed by Close (under closeOnce) before joining
	// readerDone, so the reader goroutine can break out of a blocking
	// channel send when nobody is consuming. Without it, Close would
	// deadlock: readerDone only closes when readLoop returns, and
	// readLoop is blocked on `frames <- ...` when no Recv is listening.
	frames   chan frameOrError
	closedCh chan struct{}

	// runDone is closed when the bundled adapter's Run returns.
	// readerDone is closed when the reader goroutine exits.
	runDone    chan struct{}
	readerDone chan struct{}
	runErr     error

	// closeOnce guards Close so concurrent callers don't both try to
	// reap the bundled adapter goroutine. closeDone is closed at the
	// end of Close's Do body so concurrent callers wait for the first
	// to finish populating closeErr before reading it.
	closeOnce sync.Once
	closeErr  error
	closeDone chan struct{}
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
		closedCh:   make(chan struct{}),
		runDone:    make(chan struct{}),
		readerDone: make(chan struct{}),
		closeDone:  make(chan struct{}),
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
// FrameReader into the frames channel until the pipe closes, an
// unrecoverable read error occurs, or Close signals via closedCh.
// Closes the channel on exit so Recv can detect end-of-stream.
//
// The send is in a select against closedCh so a Close that races with
// a frame-arrival doesn't deadlock: without the select, the goroutine
// would block forever on `frames <- ...` when no Recv is consuming,
// and Close (which waits on readerDone) would block forever waiting
// for the goroutine to return.
func (t *InprocTransport) readLoop() {
	defer close(t.readerDone)
	defer close(t.frames)
	for {
		payload, err := t.frameRead.Read(t.frameMax)
		select {
		case t.frames <- frameOrError{payload: payload, err: err}:
		case <-t.closedCh:
			return
		}
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
//
// Close honors ctx: if a bundled adapter ignores its ctx and refuses
// to exit on pipe close, Close will not block past ctx's deadline.
// In that case it returns a wrapped ctx error. The reaping goroutine
// continues running so the bundled adapter is not abandoned silently
// — but the caller is no longer held hostage to it.
//
// Close is safe to call multiple times; subsequent callers wait for
// the first call to finish populating closeErr (or for ctx to fire)
// before returning.
func (t *InprocTransport) Close(ctx context.Context) error {
	t.closeOnce.Do(func() {
		// The reaping work runs in its own goroutine so the caller can
		// abandon the wait if ctx fires before a misbehaving bundled
		// adapter exits. closeDone is closed when the reap finishes;
		// closeErr is settled before that close.
		go func() {
			defer close(t.closeDone)

			// Signal the reader goroutine to exit even if it's blocked
			// on a frame-send into a channel nobody is consuming. Must
			// come BEFORE the join below.
			close(t.closedCh)

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
		}()
	})
	// Concurrent callers wait on the same closeDone channel, so they
	// see consistent behavior. Honor ctx so a stuck bundled adapter
	// can't block the caller indefinitely.
	select {
	case <-t.closeDone:
		return t.closeErr
	case <-ctx.Done():
		return fmt.Errorf("adapter: inproc close: %w", ctx.Err())
	}
}

// StderrTail implements Transport. Inproc adapters have no stderr.
func (t *InprocTransport) StderrTail() []byte {
	return nil
}

// MarkProtocolShutdownAcked implements Transport. No-op for inproc:
// there is no exit code to classify, so a successful protocol shutdown
// has no impact on what Close returns.
func (t *InprocTransport) MarkProtocolShutdownAcked() {}

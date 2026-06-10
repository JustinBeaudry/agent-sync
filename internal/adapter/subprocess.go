package adapter

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter/contract"
)

// CookieEnvVar is the env var name the runtime uses to deliver the
// magic cookie to a spawned adapter. Mirrors hashicorp/go-plugin's
// pattern: a per-spawn random value the adapter must echo on
// `initialize`. Lets the adapter detect when it was invoked outside
// the CLI handshake (e.g., a human running it in a shell).
const CookieEnvVar = "AIENVS_ADAPTER_COOKIE"

// stderrRingBytes is the bounded size of the in-memory stderr
// ring buffer attached to a subprocess. 64 KiB is generous for the
// crash-report use case (only the tail matters for diagnosis); larger
// values cost memory per-session without much added value.
const stderrRingBytes = 64 * 1024

// SubprocessTimeouts bounds the runtime's interaction with a subprocess
// adapter. Defaults follow the k8s mutating-webhook convention.
type SubprocessTimeouts struct {
	Handshake time.Duration // initialize → result
	Emit      time.Duration // emit → final result (per emit)
	Shutdown  time.Duration // shutdown → process exit
}

// DefaultSubprocessTimeouts returns the runtime's default bounds.
// Per-method overrides via adapter.yaml are deferred to Unit 8b.
func DefaultSubprocessTimeouts() SubprocessTimeouts {
	return SubprocessTimeouts{
		Handshake: 5 * time.Second,
		Emit:      30 * time.Second,
		Shutdown:  5 * time.Second,
	}
}

// Subprocess runs an adapter in a child process. The runtime drives
// the lifecycle through the Transport interface; Subprocess owns the
// bytes and the process handle.
type Subprocess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	frameRead  *contract.FrameReader
	frameMax   int64
	stderrRing *ringBuffer
	stderrDone chan struct{}

	// procCancel cancels the internal context bound to exec.CommandContext.
	// We deliberately decouple the subprocess lifetime from the caller's
	// spawn ctx so a short-lived setup ctx (typical for handshake) does
	// not kill the adapter process while a long-running emit is in
	// flight. Close calls procCancel as part of teardown to release
	// the os/exec watcher goroutine.
	procCancel context.CancelFunc

	// frames is the channel a dedicated reader goroutine pumps inbound
	// frames into. Same pattern as InprocTransport — guarantees only
	// one goroutine touches the FrameReader's bufio.Reader so ctx
	// cancellation in one Recv doesn't race with a subsequent Recv.
	//
	// closedCh is closed by Close (under closeOnce) before joining
	// readerDone, so the reader goroutine can break out of a blocking
	// channel send when nobody is consuming. Without it, Close would
	// deadlock: readerDone only closes when readLoop returns, and
	// readLoop is blocked on `s.frames <- ...` when no Recv is
	// listening.
	frames     chan frameOrError
	readerDone chan struct{}
	closedCh   chan struct{}

	timeouts SubprocessTimeouts

	// closeOnce guards Close so a concurrent Recv-error and explicit
	// Close don't both try to reap the process.
	closeOnce sync.Once
	closeErr  error
	// closeDone is closed at the end of Close's Do body so concurrent
	// Close callers wait for the first one to finish populating
	// closeErr before reading it.
	closeDone chan struct{}

	// waitOnce ensures cmd.Wait is called exactly once, and waitErr
	// captures the result for any later observer (Close, classifier).
	waitOnce sync.Once
	waitErr  error

	// protocolShutdownAcked records whether the runtime received a
	// successful response to the protocol-level `shutdown` request. It
	// is set ONLY by MarkProtocolShutdownAcked, which the runtime calls
	// after a successful shutdown round-trip — never by Close itself.
	// classifyExit suppresses non-zero exits only when this flag is
	// true, so an adapter that crashes during teardown is not silently
	// masked by Close's force-kill path.
	protocolShutdownAcked bool
}

// SpawnOptions configures Subprocess construction.
type SpawnOptions struct {
	// Manifest is the validated AdapterManifest. Command + ReservedPrefix
	// are read; the runtime treats the manifest as immutable.
	Manifest AdapterManifest

	// ExtraEnv appends to the child's environment. The runtime auto-sets
	// AIENVS_ADAPTER_COOKIE; ExtraEnv values do not override it.
	ExtraEnv []string

	// Timeouts controls handshake / emit / shutdown bounds. Zero values
	// are filled from DefaultSubprocessTimeouts.
	Timeouts SubprocessTimeouts

	// FrameMaxBytes caps each inbound frame. Zero defaults to
	// contract.DefaultMaxFrameBytes.
	FrameMaxBytes int64
}

// Spawn launches a new subprocess for the given adapter. Returns the
// Subprocess and the magic cookie the runtime must use when echoing
// cookie validation on initialize.
//
// ctx is honored for the spawn operation only — Spawn returns ctx.Err()
// without starting the child if ctx is already cancelled before
// cmd.Start runs. Once running, the subprocess's lifetime is bound to
// an internal context that lives until Close, regardless of the state
// of the caller's ctx. This is deliberate: callers commonly pass a
// short setup deadline (e.g. the handshake timeout) to NewSession,
// and we cannot let that deadline kill the child while a later
// long-running emit or shutdown is in flight.
func Spawn(ctx context.Context, opts SpawnOptions) (sp *Subprocess, cookie string, err error) {
	if len(opts.Manifest.Command) == 0 {
		return nil, "", ErrAdapterManifestEmptyCommand
	}
	cookie, err = newCookie()
	if err != nil {
		return nil, "", fmt.Errorf("adapter: cookie generation: %w", err)
	}

	timeouts := opts.Timeouts
	defaults := DefaultSubprocessTimeouts()
	if timeouts.Handshake == 0 {
		timeouts.Handshake = defaults.Handshake
	}
	if timeouts.Emit == 0 {
		timeouts.Emit = defaults.Emit
	}
	if timeouts.Shutdown == 0 {
		timeouts.Shutdown = defaults.Shutdown
	}

	frameMax := opts.FrameMaxBytes
	if frameMax == 0 {
		frameMax = contract.DefaultMaxFrameBytes
	}

	// Honor the caller's ctx for the spawn operation: fail fast if it
	// is already cancelled before we touch the OS. After cmd.Start the
	// child is bound to procCtx (below), not ctx — see the doc comment
	// above.
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	// Subprocess lifetime context. Deliberately detached from the
	// caller's ctx (via context.Background) so a short setup deadline
	// cannot terminate the child mid-session. Cancelled by Close as
	// part of teardown to release the os/exec watcher goroutine.
	// contextcheck flags this as a non-inherited context — that's the
	// design intent here, not a bug.
	procCtx, procCancel := context.WithCancel(context.Background()) //nolint:contextcheck // intentional lifetime detachment; see doc above

	// Command is sourced from the adapter manifest, which is validated
	// upstream (LoadAdapterManifestBytes / discover.go). The runtime's
	// security boundary is "the workspace manifest declares trusted
	// adapters" — see docs/plans/2026-04-21-001-feat-agent-sync-workspace-cli-plan.md
	// Unit 6 (trust). gosec's G204 here would block the runtime from
	// doing what it exists to do. contextcheck flags procCtx as
	// non-inherited; that's the deliberate detachment documented above.
	cmd := exec.CommandContext(procCtx, opts.Manifest.Command[0], opts.Manifest.Command[1:]...) //nolint:gosec,contextcheck // see comments above
	cmd.Env = append(os.Environ(), CookieEnvVar+"="+cookie)
	cmd.Env = append(cmd.Env, opts.ExtraEnv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		procCancel()
		return nil, "", fmt.Errorf("adapter: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		procCancel()
		return nil, "", fmt.Errorf("adapter: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		procCancel()
		return nil, "", fmt.Errorf("adapter: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderrPipe.Close()
		procCancel()
		return nil, "", fmt.Errorf("adapter: start subprocess: %w", err)
	}

	sp = &Subprocess{
		cmd:        cmd,
		stdin:      stdin,
		procCancel: procCancel,
		frameRead:  contract.NewFrameReader(bufio.NewReader(stdout)),
		frameMax:   frameMax,
		stderrRing: newRingBuffer(stderrRingBytes),
		stderrDone: make(chan struct{}),
		frames:     make(chan frameOrError, 1),
		readerDone: make(chan struct{}),
		closedCh:   make(chan struct{}),
		closeDone:  make(chan struct{}),
		timeouts:   timeouts,
	}

	go sp.drainStderr(stderrPipe)
	go sp.readLoop()

	return sp, cookie, nil
}

// readLoop is the dedicated reader goroutine for the subprocess's
// stdout. Pumps frames into sp.frames until EOF, an unrecoverable
// read error, or Close signals via closedCh.
//
// The send is in a select against closedCh so a Close that races with
// a frame-arrival doesn't deadlock: without the select, the goroutine
// would block forever on `frames <- ...` when no Recv is consuming,
// and Close (which waits on readerDone) would block forever waiting
// for the goroutine to return.
func (s *Subprocess) readLoop() {
	defer close(s.readerDone)
	defer close(s.frames)
	for {
		payload, err := s.frameRead.Read(s.frameMax)
		select {
		case s.frames <- frameOrError{payload: payload, err: err}:
		case <-s.closedCh:
			return
		}
		if err != nil {
			return
		}
	}
}

// drainStderr reads stderr into the ring buffer until the pipe closes.
// Goroutine exits when EOF or any read error occurs; signaling the
// stderrDone channel so Close can join the goroutine before returning.
func (s *Subprocess) drainStderr(pipe io.ReadCloser) {
	defer close(s.stderrDone)
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			_, _ = s.stderrRing.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// Send implements Transport.
func (s *Subprocess) Send(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// stdin.Write is blocking; no native context support. The
	// subprocess is bound to the internal procCtx (not this ctx), so a
	// caller-side ctx cancel does not directly kill the child — only
	// Close does. The pre-write ctx.Err() check above is the
	// fast-fail; mid-write cancellation cannot be honored without
	// per-Send timeouts on a separate goroutine, which v1 does not do.
	if err := contract.WriteFrame(s.stdin, payload); err != nil {
		return fmt.Errorf("adapter: write frame to subprocess: %w", err)
	}
	return nil
}

// Recv implements Transport. Pulls from the dedicated reader
// goroutine's channel, honoring ctx for cancellation.
func (s *Subprocess) Recv(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case fr, ok := <-s.frames:
		if !ok {
			return nil, io.EOF
		}
		return fr.payload, fr.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// MarkProtocolShutdownAcked records that the runtime received a
// successful response to the protocol-level `shutdown` request. Called
// by the runtime ONLY after a clean shutdown round-trip; never called
// on protocol error, transport error, or timeout. classifyExit reads
// this flag to decide whether to suppress a non-zero exit (a clean
// shutdown can still produce a non-zero exit if the adapter returns
// from main with an error after responding — that's expected and
// non-fatal). Without this flag, Close's force-kill path would
// silently mask any abnormal exit, including legitimate adapter
// crashes during teardown.
func (s *Subprocess) MarkProtocolShutdownAcked() {
	s.protocolShutdownAcked = true
}

// Close implements Transport. Drives the graceful-stop sequence
// (OS-specific via gracefulStop) bounded by the shutdown timeout,
// then escalates to Kill if the process doesn't exit. Joins the
// stderr drainer goroutine before returning.
//
// Close is safe to call multiple times; subsequent calls return the
// classified error from the first call.
func (s *Subprocess) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		// Signal the reader goroutine to exit even if it's blocked on
		// a frame-send into a channel nobody is consuming. Must come
		// BEFORE the join below.
		close(s.closedCh)

		// Closing stdin gives the adapter the EOF signal on its read
		// loop, which is the cleanest way for it to know we're done.
		_ = s.stdin.Close()

		// Apply graceful-stop signal (OS-specific). gracefulStop is
		// best-effort: an error from the signal call is logged via
		// the classifier, not returned here, because the timeout +
		// kill path below is the actual termination guarantee.
		_ = s.gracefulStop()

		// Wait for the process with a timeout. If exceeded, force-kill.
		exitCh := make(chan error, 1)
		go func() {
			s.waitOnce.Do(func() {
				s.waitErr = s.cmd.Wait()
			})
			exitCh <- s.waitErr
		}()
		select {
		case <-exitCh:
			// process exited within the timeout
		case <-time.After(s.timeouts.Shutdown):
			// graceful stop didn't take. Force-kill.
			_ = s.cmd.Process.Kill()
			<-exitCh
		case <-ctx.Done():
			// caller's context cancelled — force-kill.
			_ = s.cmd.Process.Kill()
			<-exitCh
		}

		// Cancel the internal proc context to release the os/exec
		// watcher goroutine. Safe after Wait has returned.
		if s.procCancel != nil {
			s.procCancel()
		}

		// Wait for the stderr drainer + reader goroutines to finish
		// so their last writes are visible to StderrTail callers and
		// the frames channel is fully drained.
		<-s.stderrDone
		<-s.readerDone

		s.closeErr = s.classifyExit()
		close(s.closeDone)
	})
	// Concurrent callers wait until the first Close has finished
	// populating closeErr before returning.
	<-s.closeDone
	return s.closeErr
}

// StderrTail returns a snapshot of the bounded stderr ring buffer.
func (s *Subprocess) StderrTail() []byte {
	return s.stderrRing.Bytes()
}

// classifyExit maps the process's exit state to a runtime error or nil.
// Returns nil when the process exited 0 OR when the runtime received
// a successful protocol-level shutdown response and the process exited
// non-zero (the adapter may have set a non-zero exit code in main even
// after a clean shutdown round-trip; we trust the protocol-level ack
// as the authoritative outcome). A non-zero exit WITHOUT a successful
// protocol shutdown — for example, OS-level signal-and-kill driven by
// Close after a transport failure — is surfaced as a SubprocessExitError
// so the runtime can attach exit code + stderr context for diagnosis.
func (s *Subprocess) classifyExit() error {
	if s.waitErr == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(s.waitErr, &exitErr) {
		// Suppress the non-zero exit only when the runtime confirmed a
		// clean protocol-level shutdown. Without this evidence, the
		// non-zero exit is a real fault: an adapter that crashed during
		// teardown, or a misbehaving adapter that ignored SIGTERM and
		// was force-killed.
		if s.protocolShutdownAcked {
			return nil
		}
		return &SubprocessExitError{
			ExitCode:   exitErr.ExitCode(),
			StderrTail: s.stderrRing.Bytes(),
			Underlying: s.waitErr,
		}
	}
	return fmt.Errorf("adapter: subprocess wait: %w", s.waitErr)
}

// SubprocessExitError is the typed error returned when an adapter
// subprocess exits abnormally (non-zero exit, no shutdown requested).
// The runtime maps this to ErrorClassAdapterPanic in its error-class
// classification.
type SubprocessExitError struct {
	ExitCode   int
	StderrTail []byte
	Underlying error
}

func (e *SubprocessExitError) Error() string {
	return fmt.Sprintf("adapter: subprocess exited %d (stderr_tail=%d bytes): %v",
		e.ExitCode, len(e.StderrTail), e.Underlying)
}

func (e *SubprocessExitError) Unwrap() error {
	return e.Underlying
}

// newCookie returns 32 hex chars from crypto/rand. 128 bits of entropy
// is plenty to defeat a sibling process guessing the value.
func newCookie() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ringBuffer is a fixed-size byte ring. Writes that exceed the cap
// drop the oldest bytes. Bytes() returns a snapshot in chronological
// order. Safe for concurrent Write + Bytes; the locks are coarse but
// the workload is bounded (one drainer goroutine writing, occasional
// reader).
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	full bool
	pos  int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{data: make([]byte, size)}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	written := len(p)
	cap := len(r.data)
	// If the input alone is larger than the ring's capacity, only the
	// last `cap` bytes can survive — drop the leading prefix that would
	// be overwritten anyway. The buffer is fully populated after this
	// write, so the result is the trailing window of the input.
	if len(p) >= cap {
		copy(r.data, p[len(p)-cap:])
		r.pos = 0
		r.full = true
		return written, nil
	}
	// Standard case: write p into the ring starting at r.pos. If the
	// write fits between r.pos and the end, one copy. Otherwise wrap
	// with two copies (tail of ring, then head).
	remain := cap - r.pos
	if len(p) <= remain {
		copy(r.data[r.pos:], p)
		r.pos += len(p)
		if r.pos == cap {
			r.pos = 0
			r.full = true
		}
	} else {
		copy(r.data[r.pos:], p[:remain])
		copy(r.data, p[remain:])
		r.pos = len(p) - remain
		r.full = true
	}
	return written, nil
}

// Bytes returns the buffer contents in chronological order. For an
// unfilled buffer, returns the prefix; for a filled buffer, returns
// the data unwrapped from the ring.
func (r *ringBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		out := make([]byte, r.pos)
		copy(out, r.data[:r.pos])
		return out
	}
	out := make([]byte, len(r.data))
	n := copy(out, r.data[r.pos:])
	copy(out[n:], r.data[:r.pos])
	return out
}

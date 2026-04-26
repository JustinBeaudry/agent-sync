package adapter

import (
	"context"
	"fmt"

	"github.com/aienvs/aienvs/internal/adapter/contract"
)

// SessionOptions configures an AdapterSession at construction. All
// fields are optional; zero values yield runtime defaults.
type SessionOptions struct {
	// WorkspaceRoot is the absolute path the adapter is operating
	// within. Sent in InitializeParams; the adapter uses it to scope
	// declared_outputs.
	WorkspaceRoot string

	// IRVersion is the IR version the runtime is using. v1 only in
	// PR 2; future versions ride additively under capabilities.
	IRVersion string

	// Timeouts overrides DefaultSubprocessTimeouts on a per-session
	// basis. Zero fields fall back to defaults.
	Timeouts SubprocessTimeouts
}

// NewSession constructs a new AdapterSession from a discovered Adapter.
// Spawns the transport (subprocess or inproc) and prepares it for the
// initialize handshake. Does NOT call Initialize — the caller drives
// the four-phase lifecycle.
func (a *Adapter) NewSession(ctx context.Context, opts SessionOptions) (*AdapterSession, error) {
	if opts.IRVersion == "" {
		opts.IRVersion = "v1"
	}

	var tr Transport
	var cookie string
	switch a.Source {
	case SourceBundled:
		if a.Bundled == nil {
			return nil, fmt.Errorf("adapter: bundled adapter %q has no Run callback", a.Manifest.Name)
		}
		ipt, err := NewInprocTransport(ctx, a.Bundled)
		if err != nil {
			return nil, fmt.Errorf("adapter: bundled %q: %w", a.Manifest.Name, err)
		}
		tr = ipt
		cookie = "" // bundled adapters share process; no cookie required
	default:
		sp, c, err := Spawn(ctx, SpawnOptions{
			Manifest: a.Manifest,
			Timeouts: opts.Timeouts,
		})
		if err != nil {
			return nil, err
		}
		tr = sp
		cookie = c
	}

	return &AdapterSession{
		adapter:    a,
		transport:  tr,
		cookie:     cookie,
		ids:        contract.NewIDCorrelator(),
		options:    opts,
		state:      sessionStateNew,
		gateChecks: map[contract.OpKind]bool{},
	}, nil
}

// AdapterSession is one initialize→initialized→emit→shutdown lifecycle
// against a single adapter. Single-goroutine usage; the orchestrator
// drives all method calls from the same goroutine. The transport may
// use its own goroutines internally (subprocess stderr drainer, inproc
// Run goroutine), but those don't race with the session's state.
type AdapterSession struct {
	adapter   *Adapter
	transport Transport
	cookie    string
	ids       *contract.IDCorrelator
	options   SessionOptions

	// state tracks where we are in the four-phase lifecycle. Methods
	// that don't match the current state return
	// ErrAdapterProtocolOrderViolation.
	state sessionState

	// initResult is captured from the adapter's initialize response;
	// declared_outputs feeds the integrity gate.
	initResult *contract.InitializeResult

	// gateChecks records which kinds were declared "supported" — used
	// by capability-lied detection after Emit.
	gateChecks map[contract.OpKind]bool
}

type sessionState uint8

const (
	sessionStateNew sessionState = iota
	sessionStateInitialized
	sessionStateEmitting
	sessionStateShutdown
	sessionStateClosed
)

// Manifest returns the adapter's manifest. Read-only.
func (s *AdapterSession) Manifest() AdapterManifest {
	return s.adapter.Manifest
}

// StderrTail returns a snapshot of the adapter's stderr ring buffer.
// nil for inproc adapters.
func (s *AdapterSession) StderrTail() []byte {
	return s.transport.StderrTail()
}

// Pending returns the IDCorrelator's in-flight count. Useful for
// observability and for asserting no leaks at session teardown.
func (s *AdapterSession) Pending() int {
	return s.ids.Pending()
}

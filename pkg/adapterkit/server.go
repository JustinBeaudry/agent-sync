package adapterkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sync"
)

const (
	CookieEnvVar          = "AIENVS_ADAPTER_COOKIE"
	MissingCookieExitCode = 7
	DefaultMaxFrameBytes  = 16 * 1024 * 1024
)

var magicCookiePattern = regexp.MustCompile(`\A[0-9a-f]{32}\z`)

type InitializeFunc func(ctx context.Context, params InitializeParams) (InitializeResult, error)
type EmitFunc func(ctx context.Context, params EmitParams) (EmitResult, error)
type ShutdownFunc func(ctx context.Context) error

// ExitError reports that the adapter process should exit with a
// specific code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e == nil || e.Err == nil {
		return fmt.Sprintf("adapterkit: exit %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ExitCode returns the process exit code encoded in err, or zero when
// err does not request a process exit.
func ExitCode(err error) int {
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return 0
}

type Server struct {
	name    string
	version string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string

	onInitialize InitializeFunc
	onEmit       EmitFunc
	onShutdown   ShutdownFunc

	mu            sync.Mutex
	state         serverState
	cookie        string
	shutdownAcked bool
}

type ServerOptions struct {
	Name    string
	Version string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string
}

type serverState uint8

const (
	serverStateNew serverState = iota
	serverStateInitialized
	serverStateReady
	serverStateClosed
)

func NewServer(opts ServerOptions) *Server {
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Server{
		name:    opts.Name,
		version: opts.Version,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		getenv:  getenv,
	}
}

// OnInitialize registers the initialize handler. Handler registration is safe
// at any time including after Run(); calls during Run() are serialized but the
// registered handler is invoked outside the lock.
func (s *Server) OnInitialize(fn InitializeFunc) {
	s.mu.Lock()
	s.onInitialize = fn
	s.mu.Unlock()
}

// OnEmit registers the emit handler.
func (s *Server) OnEmit(fn EmitFunc) {
	s.mu.Lock()
	s.onEmit = fn
	s.mu.Unlock()
}

// OnShutdown registers the shutdown handler.
func (s *Server) OnShutdown(fn ShutdownFunc) {
	s.mu.Lock()
	s.onShutdown = fn
	s.mu.Unlock()
}

func (s *Server) Run(ctx context.Context) error {
	cookie := s.getenv(CookieEnvVar)
	if cookie == "" {
		_, _ = fmt.Fprintln(s.stderr, "adapterkit: AIENVS_ADAPTER_COOKIE not set")
		return &ExitError{Code: MissingCookieExitCode, Err: errors.New("adapterkit: missing AIENVS_ADAPTER_COOKIE")}
	}
	if !magicCookiePattern.MatchString(cookie) {
		_, _ = fmt.Fprintln(s.stderr, "adapterkit: AIENVS_ADAPTER_COOKIE has invalid format")
		return &ExitError{Code: MissingCookieExitCode, Err: errors.New("adapterkit: invalid AIENVS_ADAPTER_COOKIE format")}
	}
	s.mu.Lock()
	s.cookie = cookie
	s.mu.Unlock()

	if s.name != "" {
		_, _ = fmt.Fprintf(s.stderr, "%s: started\n", s.name)
	} else {
		_, _ = fmt.Fprintln(s.stderr, "started")
	}

	return s.serve(ctx)
}

func (s *Server) protocolShutdownAcked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdownAcked
}

func (s *Server) setProtocolShutdownAcked() {
	s.mu.Lock()
	s.shutdownAcked = true
	s.mu.Unlock()
}

func (s *Server) currentState() serverState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Server) setState(state serverState) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

func (s *Server) initializeHandler() InitializeFunc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.onInitialize
}

func (s *Server) emitHandler() EmitFunc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.onEmit
}

func (s *Server) shutdownHandler() ShutdownFunc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.onShutdown
}

func (s *Server) handleInitialize(ctx context.Context, params InitializeParams, handler InitializeFunc) (InitializeResult, *Error) {
	if s.currentState() != serverStateNew {
		return InitializeResult{}, newProtocolOrderError("initialize called more than once")
	}
	if !containsProtocolVersion(params.ProtocolVersions, ContractVersionV1) {
		return InitializeResult{}, &Error{
			Code:    CodeInvalidParams,
			Message: "adapter does not support any offered protocol version",
			Data:    ErrorData{ErrorClass: ErrorClassAdapterProtocolMismatch},
		}
	}

	result := InitializeResult{
		Server:          s.serverIdentity(),
		ProtocolVersion: ContractVersionV1,
		Capabilities: Capabilities{
			ConceptKinds: map[string]CapabilityLevel{},
		},
		DeclaredOutputs: []DeclaredOutput{},
	}

	if handler != nil {
		var err error
		result, err = safelyCallInitialize(ctx, handler, params)
		if err != nil {
			return InitializeResult{}, toRPCError(err)
		}
	}
	if result.Server == "" {
		result.Server = s.serverIdentity()
	}
	if result.ProtocolVersion == "" {
		result.ProtocolVersion = ContractVersionV1
	}
	if result.Capabilities.ConceptKinds == nil {
		result.Capabilities.ConceptKinds = map[string]CapabilityLevel{}
	}
	if result.DeclaredOutputs == nil {
		result.DeclaredOutputs = []DeclaredOutput{}
	}
	s.setState(serverStateInitialized)
	return result, nil
}

func (s *Server) handleInitialized() *Error {
	if s.currentState() != serverStateInitialized {
		return newProtocolOrderError("initialized notification received before initialize")
	}
	s.setState(serverStateReady)
	return nil
}

func (s *Server) handleEmit(ctx context.Context, params EmitParams, handler EmitFunc) (EmitResult, *Error) {
	state := s.currentState()
	if state != serverStateReady {
		return EmitResult{}, newProtocolOrderError(fmt.Sprintf("emit called from invalid state %s", state.String()))
	}

	result := EmitResult{OpsPerformed: []OpRecord{}}
	if handler == nil {
		return result, nil
	}
	got, err := safelyCallEmit(ctx, handler, params)
	if err != nil {
		return EmitResult{}, toRPCError(err)
	}
	if got.OpsPerformed == nil {
		got.OpsPerformed = []OpRecord{}
	}
	return got, nil
}

func (s *Server) handleShutdown(ctx context.Context, handler ShutdownFunc) *Error {
	state := s.currentState()
	if state != serverStateInitialized && state != serverStateReady {
		return newProtocolOrderError(fmt.Sprintf("shutdown called from invalid state %s", state.String()))
	}
	if handler != nil {
		if err := safelyCallShutdown(ctx, handler); err != nil {
			return toRPCError(err)
		}
	}
	s.setState(serverStateClosed)
	return nil
}

func (s *Server) serverIdentity() string {
	if s.version == "" {
		return s.name
	}
	if s.name == "" {
		return s.version
	}
	return s.name + "/" + s.version
}

func containsProtocolVersion(versions []string, want string) bool {
	for _, version := range versions {
		if version == want {
			return true
		}
	}
	return false
}

func newProtocolOrderError(message string) *Error {
	return &Error{
		Code:    CodeInvalidRequest,
		Message: message,
		Data:    ErrorData{ErrorClass: ErrorClassAdapterProtocolOrder},
	}
}

func toRPCError(err error) *Error {
	var rpcErr *Error
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return &Error{
		Code:    CodeInternalError,
		Message: err.Error(),
		Data:    ErrorData{ErrorClass: ErrorClassAdapterPanic},
	}
}

func safelyCallInitialize(ctx context.Context, fn InitializeFunc, params InitializeParams) (result InitializeResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicAsError(recovered)
		}
	}()
	return fn(ctx, params)
}

func safelyCallEmit(ctx context.Context, fn EmitFunc, params EmitParams) (result EmitResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicAsError(recovered)
		}
	}()
	return fn(ctx, params)
}

func safelyCallShutdown(ctx context.Context, fn ShutdownFunc) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicAsError(recovered)
		}
	}()
	return fn(ctx)
}

func panicAsError(recovered any) error {
	detail, _ := json.Marshal(map[string]string{"panic": fmt.Sprint(recovered)})
	return &Error{
		Code:    CodeInternalError,
		Message: "adapter handler panicked",
		Data: ErrorData{
			ErrorClass: ErrorClassAdapterPanic,
			Detail:     detail,
		},
	}
}

func (s serverState) String() string {
	switch s {
	case serverStateNew:
		return "new"
	case serverStateInitialized:
		return "initialized"
	case serverStateReady:
		return "ready"
	case serverStateClosed:
		return "closed"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

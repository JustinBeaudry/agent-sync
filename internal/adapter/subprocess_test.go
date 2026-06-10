package adapter_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/adapter/contract"
)

// echoBinaryPath is the cached path to the compiled testdata echo
// binary. Built once per test run via buildEchoBinary; tests that
// need it call ensureEchoBinary(t).
var (
	echoBinaryOnce sync.Once
	echoBinaryPath string
	echoBinaryErr  error
)

// crashyBinaryPath is the cached path to the compiled testdata crashy
// binary. Built once per test run; tests that need it call
// ensureCrashyBinary(t).
var (
	crashyBinaryOnce sync.Once
	crashyBinaryPath string
	crashyBinaryErr  error
)

// ensureEchoBinary builds (or reuses) the testdata echo binary and
// returns its absolute path. Build happens at most once per test run,
// even when tests run in parallel.
func ensureEchoBinary(t *testing.T) string {
	t.Helper()
	echoBinaryOnce.Do(func() {
		echoBinaryPath, echoBinaryErr = buildTestdataBinary("echo", "echo")
	})
	if echoBinaryErr != nil {
		if errors.Is(echoBinaryErr, exec.ErrNotFound) {
			t.Skipf("echo binary build skipped: go tool not found: %v", echoBinaryErr)
		}
		t.Fatalf("echo binary build failed: %v", echoBinaryErr)
	}
	return echoBinaryPath
}

// ensureCrashyBinary builds (or reuses) the testdata crashy binary and
// returns its absolute path. crashy is a non-protocol fixture that
// exits with code 13 immediately on startup; used by tests that need
// to confirm Subprocess.Close surfaces *SubprocessExitError when no
// protocol shutdown was acked.
func ensureCrashyBinary(t *testing.T) string {
	t.Helper()
	crashyBinaryOnce.Do(func() {
		crashyBinaryPath, crashyBinaryErr = buildTestdataBinary("crashy", "crashy")
	})
	if crashyBinaryErr != nil {
		if errors.Is(crashyBinaryErr, exec.ErrNotFound) {
			t.Skipf("crashy binary build skipped: go tool not found: %v", crashyBinaryErr)
		}
		t.Fatalf("crashy binary build failed: %v", crashyBinaryErr)
	}
	return crashyBinaryPath
}

// testBinaryDir is a per-process directory used to cache compiled
// testdata binaries across all tests in the run. Created lazily by
// buildTestdataBinary on first call; cleaned up by TestMain.
// testBinaryDirOnce + testBinaryDirMu coordinate the lazy init so
// concurrent ensure*Binary callers (each guarded by their own
// sync.Once) do not race on the shared dir name.
var (
	testBinaryDir     string
	testBinaryDirOnce sync.Once
	testBinaryDirErr  error
)

// buildTestdataBinary compiles internal/adapter/testdata/<srcSubdir>/main.go
// into a per-process temp directory. The output is content-hashed so
// re-invocations within the same process reuse the cache. The directory
// itself is a randomized path that a co-tenant on the host cannot
// predict — avoiding the well-known-location pitfall of os.TempDir +
// a deterministic filename.
func buildTestdataBinary(srcSubdir, binPrefix string) (string, error) {
	srcDir := filepath.Join("testdata", srcSubdir)
	srcFile := filepath.Join(srcDir, "main.go")
	src, err := os.ReadFile(srcFile)
	if err != nil {
		return "", err
	}
	testBinaryDirOnce.Do(func() {
		testBinaryDir, testBinaryDirErr = os.MkdirTemp("", "agent-sync-test-bin-")
	})
	if testBinaryDirErr != nil {
		return "", testBinaryDirErr
	}
	hash := sha256.Sum256(append(src, []byte(runtime.Version())...))
	suffix := hex.EncodeToString(hash[:8])
	binName := binPrefix + "-" + suffix
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	out := filepath.Join(testBinaryDir, binName)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	cmd := exec.Command("go", "build", "-o", out, "./"+srcDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out, nil
}

// TestMain cleans up the per-process testdata binary cache directory.
func TestMain(m *testing.M) {
	code := m.Run()
	if testBinaryDir != "" {
		_ = os.RemoveAll(testBinaryDir)
	}
	os.Exit(code)
}

func TestSubprocess_HappyPath(t *testing.T) {
	t.Parallel()

	bin := ensureEchoBinary(t)

	a := &adapter.Adapter{
		Manifest: adapter.AdapterManifest{
			Name:            "echo",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{bin},
			ReservedPrefix:  ".echo",
		},
		Source: adapter.SourcePATH,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	sess, err := a.NewSession(ctx, adapter.SessionOptions{
		WorkspaceRoot: "/tmp/ws",
		IRVersion:     "v1",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Shutdown(context.Background()) })

	initResult, err := sess.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if initResult.ProtocolVersion != adapter.ContractVersionV1 {
		t.Errorf("protocol_version: %q", initResult.ProtocolVersion)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}

	emitResult, err := sess.Emit(ctx, "echo", json.RawMessage(`{"nodes":[{"id":"foo","kind":"rule"},{"id":"bar","kind":"rule"}]}`))
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(emitResult.OpsPerformed) != 3 {
		t.Errorf("ops_performed: %+v", emitResult.OpsPerformed)
	}

	if err := sess.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if sess.Pending() != 0 {
		t.Errorf("Pending after shutdown: %d", sess.Pending())
	}
}

func TestSubprocess_StderrCaptured(t *testing.T) {
	t.Parallel()

	bin := ensureEchoBinary(t)

	a := &adapter.Adapter{
		Manifest: adapter.AdapterManifest{
			Name:            "echo",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{bin},
		},
		Source: adapter.SourcePATH,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	sess, err := a.NewSession(ctx, adapter.SessionOptions{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	tail := sess.StderrTail()
	if !strings.Contains(string(tail), "echo: started") {
		t.Errorf("expected 'echo: started' in stderr tail, got %q", tail)
	}
}

func TestSubprocess_NoCookieEnvCausesAdapterExit(t *testing.T) {
	// The echo binary exits 7 if AIENVS_ADAPTER_COOKIE is missing.
	// This test confirms the binary's behavior — but the runtime always
	// sets the env var, so the path is normally unreachable. We invoke
	// the binary directly with a clean env to verify.
	t.Parallel()

	bin := ensureEchoBinary(t)

	cmd := exec.Command(bin)
	cmd.Env = []string{} // no AIENVS_ADAPTER_COOKIE
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit when cookie env is missing")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 7 {
		t.Errorf("exit code: want 7, got %d", exitErr.ExitCode())
	}
}

func TestSubprocess_DeclaredOutputsGateRespected(t *testing.T) {
	// The echo binary always emits paths under .echo/ — the runtime's
	// declared-outputs gate accepts them.
	t.Parallel()

	bin := ensureEchoBinary(t)

	a := &adapter.Adapter{
		Manifest: adapter.AdapterManifest{
			Name:            "echo",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{bin},
		},
		Source: adapter.SourcePATH,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	sess, err := a.NewSession(ctx, adapter.SessionOptions{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Shutdown(context.Background()) })

	if _, err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(ctx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}

	result, err := sess.Emit(ctx, "echo", json.RawMessage(`{"nodes":[{"id":"x","kind":"rule"}]}`))
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	for _, op := range result.OpsPerformed {
		if op.Op != contract.OpKindMkdir && op.Op != contract.OpKindWriteFile {
			t.Errorf("unexpected op kind: %s", op.Op)
		}
		if !strings.HasPrefix(op.Path, ".echo") {
			t.Errorf("op path outside declared output: %s", op.Path)
		}
	}
}

func TestSubprocess_NonexistentBinary(t *testing.T) {
	t.Parallel()

	a := &adapter.Adapter{
		Manifest: adapter.AdapterManifest{
			Name:            "ghost",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{filepath.Join(t.TempDir(), "does-not-exist")},
		},
		Source: adapter.SourcePATH,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	_, err := a.NewSession(ctx, adapter.SessionOptions{})
	if err == nil {
		t.Fatal("expected error from spawn of nonexistent binary")
	}
}

// TestSubprocess_LifetimeOutlivesSpawnCtx confirms the subprocess survives
// expiry of the context that was passed to NewSession. Callers commonly
// pass a short setup deadline (e.g. the handshake timeout) when spawning;
// without lifetime detachment, that deadline would also kill the child
// mid-emit, contradicting the documented "once running, the process
// lives until Close" promise.
func TestSubprocess_LifetimeOutlivesSpawnCtx(t *testing.T) {
	t.Parallel()

	bin := ensureEchoBinary(t)

	a := &adapter.Adapter{
		Manifest: adapter.AdapterManifest{
			Name:            "echo",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{bin},
			ReservedPrefix:  ".echo",
		},
		Source: adapter.SourcePATH,
	}

	// Spawn with a short-lived ctx. After this ctx expires, the child
	// MUST still be alive and able to handle an emit. We give it
	// generous setup time (so Initialize doesn't race the deadline)
	// but still short enough that we can wait for it to expire before
	// emitting.
	spawnCtx, spawnCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	t.Cleanup(spawnCancel)

	// Use a separate, long-lived ctx for Initialize / Initialized so
	// the handshake has time to complete. The spawnCtx is what we're
	// testing detachment from — it gets passed into NewSession only.
	sess, err := a.NewSession(spawnCtx, adapter.SessionOptions{
		WorkspaceRoot: "/tmp/ws",
		IRVersion:     "v1",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Shutdown(context.Background()) })

	initCtx, initCancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(initCancel)
	if _, err := sess.Initialize(initCtx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Initialized(initCtx); err != nil {
		t.Fatalf("Initialized: %v", err)
	}

	// Wait for the spawn ctx to be definitely expired.
	<-spawnCtx.Done()
	// Sleep a touch beyond expiry to give the OS time to deliver any
	// signal that exec.CommandContext would have queued. If the child
	// were tied to spawnCtx, it would be dead by now.
	time.Sleep(50 * time.Millisecond)

	// Emit on a fresh ctx. This MUST succeed — the child should still
	// be alive and responsive.
	emitCtx, emitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(emitCancel)
	result, err := sess.Emit(emitCtx, "echo", json.RawMessage(`{"nodes":[{"id":"foo","kind":"rule"}]}`))
	if err != nil {
		t.Fatalf("Emit after spawn ctx expiry: %v", err)
	}
	if len(result.OpsPerformed) == 0 {
		t.Errorf("expected ops_performed, got %+v", result)
	}
}

// TestSubprocess_Close_NoProtocolAck_SurfacesExitError confirms that an
// adapter that exits non-zero without a successful protocol shutdown
// round-trip surfaces as *SubprocessExitError. This is the regression
// guard for the bug where Close unconditionally suppressed non-zero
// exit codes (masking adapter crashes during teardown).
func TestSubprocess_Close_NoProtocolAck_SurfacesExitError(t *testing.T) {
	t.Parallel()

	bin := ensureCrashyBinary(t)

	// Spawn the crashy fixture directly via Spawn — we do NOT go
	// through a session. crashy exits with code 13 immediately.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	sp, _, err := adapter.Spawn(ctx, adapter.SpawnOptions{
		Manifest: adapter.AdapterManifest{
			Name:            "crashy",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{bin},
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Wait for the child to actually exit before calling Close, so
	// classifyExit observes the natural exit-13 instead of a race
	// against Close's gracefulStop SIGTERM (which would produce exit
	// code -1 from the signal). Recv returning EOF is a reliable
	// signal that the stdout pipe has closed, i.e. the process has
	// finished.
	for {
		if _, err := sp.Recv(ctx); err != nil {
			break
		}
	}

	// Close before any MarkProtocolShutdownAcked is called. The child
	// has already exited 13. classifyExit must surface
	// *SubprocessExitError.
	closeErr := sp.Close(context.Background())
	if closeErr == nil {
		t.Fatal("expected non-nil error from Close when adapter exited non-zero without protocol ack")
	}
	var sxerr *adapter.SubprocessExitError
	if !errors.As(closeErr, &sxerr) {
		t.Fatalf("want *SubprocessExitError, got %T: %v", closeErr, closeErr)
	}
	if sxerr.ExitCode != 13 {
		t.Errorf("ExitCode: want 13, got %d", sxerr.ExitCode)
	}
}

// TestSubprocess_Close_AfterProtocolAck_ReturnsNil confirms the
// inverse: when MarkProtocolShutdownAcked has been called (the runtime
// got a successful shutdown response), Close returns nil even if the
// child's exit was technically non-zero. The protocol-level ack is
// authoritative for clean-shutdown classification.
func TestSubprocess_Close_AfterProtocolAck_ReturnsNil(t *testing.T) {
	t.Parallel()

	bin := ensureCrashyBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	sp, _, err := adapter.Spawn(ctx, adapter.SpawnOptions{
		Manifest: adapter.AdapterManifest{
			Name:            "crashy",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{bin},
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Pretend the runtime saw a successful shutdown response.
	sp.MarkProtocolShutdownAcked()

	// crashy still exits 13, but because we've ack'd a clean protocol
	// shutdown, the subprocess transport treats the non-zero exit as
	// expected and returns nil.
	closeErr := sp.Close(context.Background())
	if closeErr != nil {
		t.Errorf("expected nil from Close after protocol ack, got %v", closeErr)
	}
}

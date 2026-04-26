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

	"github.com/aienvs/aienvs/internal/adapter"
	"github.com/aienvs/aienvs/internal/adapter/contract"
)

// echoBinaryPath is the cached path to the compiled testdata echo
// binary. Built once per test run via buildEchoBinary; tests that
// need it call ensureEchoBinary(t).
var (
	echoBinaryOnce sync.Once
	echoBinaryPath string
	echoBinaryErr  error
)

// ensureEchoBinary builds (or reuses) the testdata echo binary and
// returns its absolute path. Build happens at most once per test run,
// even when tests run in parallel.
func ensureEchoBinary(t *testing.T) string {
	t.Helper()
	echoBinaryOnce.Do(func() {
		echoBinaryPath, echoBinaryErr = buildEchoBinary()
	})
	if echoBinaryErr != nil {
		t.Skipf("echo binary build failed (likely environment-specific): %v", echoBinaryErr)
	}
	return echoBinaryPath
}

// buildEchoBinary compiles internal/adapter/testdata/echo/main.go
// into a temp file. The output path is keyed on the source's content
// hash + Go version, so reruns within a development session reuse
// the cache.
func buildEchoBinary() (string, error) {
	srcDir := "testdata/echo"
	srcFile := filepath.Join(srcDir, "main.go")
	src, err := os.ReadFile(srcFile)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(append(src, []byte(runtime.Version())...))
	suffix := hex.EncodeToString(hash[:8])
	binName := "aienvs-test-echo-" + suffix
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	out := filepath.Join(os.TempDir(), binName)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	cmd := exec.Command("go", "build", "-o", out, "./testdata/echo")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out, nil
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

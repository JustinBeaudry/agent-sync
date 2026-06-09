package cli

import (
	"bytes"
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

	"github.com/agent-sync/agent-sync/internal/adapter/conformance"
)

type adapterTestEnv struct {
	t   *testing.T
	out bytes.Buffer
	err bytes.Buffer
}

var (
	adapterBinaryCacheDir     string
	adapterBinaryCacheDirErr  error
	adapterBinaryCacheDirOnce sync.Once

	referenceEchoOnce sync.Once
	referenceEchoPath string
	referenceEchoErr  error

	crashyOnce sync.Once
	crashyPath string
	crashyErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if adapterBinaryCacheDir != "" {
		_ = os.RemoveAll(adapterBinaryCacheDir)
	}
	os.Exit(code)
}

func newAdapterTestEnv(t *testing.T) *adapterTestEnv {
	t.Helper()
	return &adapterTestEnv{t: t}
}

func (e *adapterTestEnv) deps() AdapterDeps {
	return AdapterDeps{
		Out: &e.out,
		Err: &e.err,
		Now: func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) },
	}
}

func (e *adapterTestEnv) run(args ...string) error {
	cmd := NewAdapterCommand(e.deps())
	cmd.SetArgs(args)
	cmd.SetOut(&e.out)
	cmd.SetErr(&e.err)
	return cmd.ExecuteContext(context.Background())
}

func TestNewAdapterCommandHasConformanceSubcommand(t *testing.T) {
	t.Parallel()

	cmd := NewAdapterCommand(AdapterDeps{})
	if len(cmd.Commands()) != 1 || cmd.Commands()[0].Name() != "conformance-test" {
		t.Fatalf("commands=%v", cmd.Commands())
	}
}

func TestAdapterConformanceTextHappyPath(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", ensureReferenceEchoBinaryForCLI(t))
	if err != nil {
		t.Fatalf("Execute: %v stderr=%q stdout=%q", err, e.err.String(), e.out.String())
	}
	if !strings.Contains(e.out.String(), "pass happy-rule") {
		t.Fatalf("stdout missing happy-rule pass line: %q", e.out.String())
	}
	if !strings.Contains(e.out.String(), "summary total=") {
		t.Fatalf("stdout missing summary: %q", e.out.String())
	}
}

func TestAdapterConformanceJSONOutput(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", ensureReferenceEchoBinaryForCLI(t), "--format=json")
	if err != nil {
		t.Fatalf("Execute: %v stderr=%q stdout=%q", err, e.err.String(), e.out.String())
	}

	var got struct {
		Cases   []conformance.CaseResult `json:"cases"`
		Summary conformance.Summary      `json:"summary"`
		Version string                   `json:"version"`
	}
	if decodeErr := json.Unmarshal(e.out.Bytes(), &got); decodeErr != nil {
		t.Fatalf("unmarshal json output: %v\noutput=%q", decodeErr, e.out.String())
	}
	if got.Version != conformanceVersion {
		t.Fatalf("version=%q want %q", got.Version, conformanceVersion)
	}
	if got.Summary.Total < 1 || got.Summary.Passed < 1 {
		t.Fatalf("summary=%+v", got.Summary)
	}
	if len(got.Cases) < 1 {
		t.Fatalf("cases=%+v", got.Cases)
	}
	if got.Summary.Failed != 0 {
		t.Fatalf("summary=%+v", got.Summary)
	}
	foundHappyRule := false
	for _, result := range got.Cases {
		if result.Name == "happy-rule" && result.Status == conformance.StatusPass {
			foundHappyRule = true
			break
		}
	}
	if !foundHappyRule {
		t.Fatalf("cases=%+v", got.Cases)
	}
}

func TestAdapterConformanceSpawnErrorMissingBinary(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", "/does/not/exist")
	exitErr := mustExitError(t, err)
	if exitErr.ExitCode() != exitCodeConformanceSpawn {
		t.Fatalf("exit code=%d want %d", exitErr.ExitCode(), exitCodeConformanceSpawn)
	}
	if !strings.Contains(e.err.String(), "spawn error") {
		t.Fatalf("stderr=%q", e.err.String())
	}
}

func TestAdapterConformanceCrashyBinaryFailsCases(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", ensureCrashyBinaryForCLI(t), "--filter=^(happy|spec-example)-")
	exitErr := mustExitError(t, err)
	if exitErr.ExitCode() != exitCodeConformanceFail {
		t.Fatalf("exit code=%d want %d", exitErr.ExitCode(), exitCodeConformanceFail)
	}
	if !strings.Contains(e.out.String(), "fail happy-rule") {
		t.Fatalf("stdout=%q", e.out.String())
	}
}

func TestAdapterConformanceFilterRunsOnlyMatchingCases(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", ensureReferenceEchoBinaryForCLI(t), "--filter=^happy-")
	if err != nil {
		t.Fatalf("Execute: %v stderr=%q stdout=%q", err, e.err.String(), e.out.String())
	}
	if strings.Contains(e.out.String(), "spec-example-") {
		t.Fatalf("stdout should not include spec-example cases: %q", e.out.String())
	}
}

func TestAdapterConformanceTimeoutFailsCases(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", ensureReferenceEchoBinaryForCLI(t), "--timeout=1ns")
	exitErr := mustExitError(t, err)
	if exitErr.ExitCode() != exitCodeConformanceFail {
		t.Fatalf("exit code=%d want %d", exitErr.ExitCode(), exitCodeConformanceFail)
	}
	if !strings.Contains(e.out.String(), "adapter: timeout") {
		t.Fatalf("stdout=%q", e.out.String())
	}
}

func TestAdapterConformanceInvalidFilterRegex(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", ensureReferenceEchoBinaryForCLI(t), "--filter=[")
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *exitError
	if errors.As(err, &exitErr) {
		t.Fatalf("want plain error, got exit code %d", exitErr.ExitCode())
	}
	if !strings.Contains(err.Error(), "compile filter") {
		t.Fatalf("err=%v", err)
	}
}

func TestAdapterConformanceBadFormat(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", ensureReferenceEchoBinaryForCLI(t), "--format=xml")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("err=%v", err)
	}
}

func TestAdapterConformanceVerboseOutput(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", ensureCrashyBinaryForCLI(t), "--filter=^happy-rule$", "--verbose")
	exitErr := mustExitError(t, err)
	if exitErr.ExitCode() != exitCodeConformanceFail {
		t.Fatalf("exit code=%d want %d", exitErr.ExitCode(), exitCodeConformanceFail)
	}
	if !strings.Contains(e.out.String(), "actual_ops=") {
		t.Fatalf("stdout=%q", e.out.String())
	}
}

func TestAdapterConformanceDirectoryPath(t *testing.T) {
	t.Parallel()

	e := newAdapterTestEnv(t)
	err := e.run("conformance-test", t.TempDir())
	exitErr := mustExitError(t, err)
	if exitErr.ExitCode() != exitCodeConformanceSpawn {
		t.Fatalf("exit code=%d want %d", exitErr.ExitCode(), exitCodeConformanceSpawn)
	}
	if !strings.Contains(e.err.String(), "spawn error") {
		t.Fatalf("stderr=%q", e.err.String())
	}
}

func mustExitError(t *testing.T, err error) *exitError {
	t.Helper()
	if err == nil {
		t.Fatal("expected exit error")
	}
	var exitErr *exitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err=%T want *exitError", err)
	}
	return exitErr
}

func ensureReferenceEchoBinaryForCLI(t *testing.T) string {
	t.Helper()
	referenceEchoOnce.Do(func() {
		referenceEchoPath, referenceEchoErr = buildRepoBinary("conformance/echo", "conformance-echo")
	})
	if referenceEchoErr != nil {
		t.Fatalf("build reference echo binary: %v", referenceEchoErr)
	}
	return referenceEchoPath
}

func ensureCrashyBinaryForCLI(t *testing.T) string {
	t.Helper()
	crashyOnce.Do(func() {
		crashyPath, crashyErr = buildRepoBinary("internal/adapter/testdata/crashy", "crashy")
	})
	if crashyErr != nil {
		t.Fatalf("build crashy binary: %v", crashyErr)
	}
	return crashyPath
}

func buildRepoBinary(srcDir, prefix string) (string, error) {
	rootDir := filepath.Join("..", "..")
	src, err := os.ReadFile(filepath.Join(rootDir, srcDir, "main.go"))
	if err != nil {
		return "", err
	}
	adapterBinaryCacheDirOnce.Do(func() {
		adapterBinaryCacheDir, adapterBinaryCacheDirErr = os.MkdirTemp("", "aienvs-cli-test-bin-")
	})
	if adapterBinaryCacheDirErr != nil {
		return "", adapterBinaryCacheDirErr
	}

	hash := sha256.Sum256(append(src, []byte(runtime.Version())...))
	name := prefix + "-" + hex.EncodeToString(hash[:8])
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(adapterBinaryCacheDir, name)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}

	cmd := exec.Command("go", "build", "-o", out, "./"+filepath.ToSlash(srcDir))
	cmd.Dir = rootDir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out, nil
}

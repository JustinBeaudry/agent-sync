package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aienvs/aienvs/internal/adapter/conformance"
	"github.com/aienvs/aienvs/pkg/adapterkit"
)

var (
	echoBinaryCacheDir     string
	echoBinaryCacheDirErr  error
	echoBinaryCacheDirOnce sync.Once

	echoBinaryOnce sync.Once
	echoBinaryPath string
	echoBinaryErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if echoBinaryCacheDir != "" {
		_ = os.RemoveAll(echoBinaryCacheDir)
	}
	os.Exit(code)
}

func TestReferenceEchoBuildsAndPassesPositiveCorpus(t *testing.T) {
	t.Parallel()

	report, err := conformance.Run(context.Background(), ensureReferenceEchoBinary(t), conformance.RunOptions{
		Cases: mustPositiveCases(t),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.Failed != 0 {
		t.Fatalf("summary=%+v cases=%+v", report.Summary, report.Cases)
	}
	if report.Summary.Passed != len(report.Cases) {
		t.Fatalf("summary=%+v cases=%+v", report.Summary, report.Cases)
	}
}

func TestReferenceEchoRequiresCookie(t *testing.T) {
	t.Parallel()

	cmd := exec.Command(ensureReferenceEchoBinary(t))
	cmd.Env = []string{}
	err := cmd.Run()
	if err == nil {
		t.Fatal("Run succeeded without AIENVS_ADAPTER_COOKIE")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err=%T want *exec.ExitError", err)
	}
	if exitErr.ExitCode() != adapterkit.MissingCookieExitCode {
		t.Fatalf("exit code=%d want %d", exitErr.ExitCode(), adapterkit.MissingCookieExitCode)
	}
}

func mustPositiveCases(t *testing.T) []conformance.Case {
	t.Helper()

	all, err := conformance.LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	out := make([]conformance.Case, 0, len(all))
	for _, tc := range all {
		if strings.HasPrefix(tc.Name, "happy-") || strings.HasPrefix(tc.Name, "spec-example-") {
			out = append(out, tc)
		}
	}
	if len(out) == 0 {
		t.Fatal("positive conformance corpus is empty")
	}
	return out
}

func ensureReferenceEchoBinary(t *testing.T) string {
	t.Helper()
	echoBinaryOnce.Do(func() {
		echoBinaryPath, echoBinaryErr = buildReferenceEchoBinary()
	})
	if echoBinaryErr != nil {
		t.Fatalf("build reference echo binary: %v", echoBinaryErr)
	}
	return echoBinaryPath
}

func buildReferenceEchoBinary() (string, error) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		return "", err
	}
	echoBinaryCacheDirOnce.Do(func() {
		echoBinaryCacheDir, echoBinaryCacheDirErr = os.MkdirTemp("", "aienvs-conformance-echo-bin-")
	})
	if echoBinaryCacheDirErr != nil {
		return "", echoBinaryCacheDirErr
	}

	hash := sha256.Sum256(append(src, []byte(runtime.Version())...))
	name := "conformance-echo-" + hex.EncodeToString(hash[:8])
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(echoBinaryCacheDir, name)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}

	cmd := exec.Command("go", "build", "-o", out, "./conformance/echo")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out, nil
}
